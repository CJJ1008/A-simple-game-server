// ============================================================
// client.cpp  —  多人对战客户端 v3.2  (macOS/Linux 兼容)
//
// macOS 兼容修复：
//   1. select() 代替 O_NONBLOCK：彻底消除与 std::cin 冲突
//   2. TIOCGWINSZ + SIGWINCH：终端尺寸检测，小终端等待调整
//   3. read_line_raw()：原始 read() 行编辑，不碰 std::cin
//   4. tcdrain+tcflush+TCSADRAIN：正确切换 cooked/raw
//   5. 渲染截断到实际终端高度，不写越界行
// ============================================================

#include "protocol.h"

#include <iostream>
#include <string>
#include <vector>
#include <thread>
#include <mutex>
#include <atomic>
#include <chrono>
#include <cstring>
#include <cstdlib>
#include <csignal>
#include <algorithm>

#include <sys/socket.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <arpa/inet.h>
#include <unistd.h>
#include <sys/ioctl.h>
#include <sys/select.h>
#include <termios.h>
#include <fcntl.h>

constexpr const char* DEFAULT_HOST = "127.0.0.1";
constexpr int         DEFAULT_PORT = 9000;
constexpr int         MIN_COLS     = 80;
constexpr int         MIN_ROWS     = 32;

// ANSI
#define R    "\033[0m"
#define B    "\033[1m"
#define DIM  "\033[2m"
#define FC   "\033[96m"
#define FY   "\033[93m"
#define FG   "\033[92m"
#define FR   "\033[91m"
#define FM   "\033[95m"
#define FBL  "\033[94m"
#define FW   "\033[97m"
#define BGb  "\033[44m"
#define CLS  "\033[2J\033[H"
#define HIDE "\033[?25l"
#define SHOW "\033[?25h"

static const char* PCOLORS[MAX_PLAYERS] = {FC, FY, FG, FM, FR};
static const char* PSYMS[MAX_PLAYERS]   = {"A","B","C","D","E"};

// ── 终端尺寸 ──────────────────────────────────
static std::atomic<int> g_rows{24}, g_cols{80};
static void update_winsize() {
    struct winsize ws{};
    if (ioctl(STDOUT_FILENO, TIOCGWINSZ, &ws) == 0 && ws.ws_row > 0) {
        g_rows = ws.ws_row; g_cols = ws.ws_col;
    }
}
static void on_sigwinch(int) { update_winsize(); }

// ── 终端 raw 模式（无 O_NONBLOCK）────────────
static struct termios g_orig_term;
static bool g_raw = false;
static void enter_raw() {
    if (g_raw) return;
    tcgetattr(STDIN_FILENO, &g_orig_term);
    struct termios t = g_orig_term;
    t.c_lflag &= ~(tcflag_t)(ICANON|ECHO|ISIG);
    t.c_iflag &= ~(tcflag_t)(IXON|ICRNL);
    t.c_cc[VMIN]=1; t.c_cc[VTIME]=0;
    tcsetattr(STDIN_FILENO, TCSAFLUSH, &t);
    g_raw = true;
}
static void leave_raw() {
    if (!g_raw) return;
    tcdrain(STDIN_FILENO);
    tcflush(STDIN_FILENO, TCIFLUSH);
    tcsetattr(STDIN_FILENO, TCSAFLUSH, &g_orig_term);
    g_raw = false;
}

// ── select() 非阻塞检测 stdin ────────────────
static bool stdin_ready(int ms = 10) {
    fd_set fds; FD_ZERO(&fds); FD_SET(STDIN_FILENO, &fds);
    struct timeval tv{ms/1000, (ms%1000)*1000};
    return select(STDIN_FILENO+1, &fds, nullptr, nullptr, &tv) > 0;
}

// ── 原始行编辑器（不碰 std::cin）─────────────
// echo=true 显示字符，echo=false 显示 * （密码）
static std::string read_line_raw(const char* prompt, bool echo = true) {
    if (prompt && prompt[0]) { std::cout << prompt << std::flush; }
    // 临时切到逐字符模式（保留 echo 设置）
    struct termios t, t2;
    tcgetattr(STDIN_FILENO, &t);
    t2 = t;
    t2.c_lflag &= ~(tcflag_t)(ICANON | ECHO);  // 始终关闭终端自带 echo，由代码手动控制
    t2.c_cc[VMIN]=1; t2.c_cc[VTIME]=0;
    tcsetattr(STDIN_FILENO, TCSADRAIN, &t2);

    std::string s;
    while (true) {
        char c=0;
        if (::read(STDIN_FILENO, &c, 1) <= 0) break;
        if (c=='\n'||c=='\r') {
            if (echo) std::cout << "\n" << std::flush;
            break;
        }
        if (c==3) { s="\x03"; break; }  // Ctrl+C
        if ((c==127||c==8) && !s.empty()) {
            s.pop_back();
            if (echo) std::cout << "\b \b" << std::flush;
            continue;
        }
        if ((unsigned char)c < 32) continue;
        s += c;
        if (echo) std::cout << c << std::flush;
        else      std::cout << '*' << std::flush;
    }
    tcsetattr(STDIN_FILENO, TCSADRAIN, &t);
    if (!echo) std::cout << "\n" << std::flush;
    return s;
}

// ── 帧缓冲（差异渲染）───────────────────────
struct FrameBuffer {
    static constexpr int MAX_R = 80;
    std::string lines[MAX_R];
    void clear() { for (auto& l:lines) l.clear(); }
    void flush_diff(FrameBuffer& prev, bool full=false) const {
        int lim = std::min(g_rows.load(), MAX_R);
        std::string out; out.reserve(8192);
        for (int i=0;i<lim;i++) {
            if (!full && lines[i]==prev.lines[i]) continue;
            out += "\033["; out += std::to_string(i+1); out += ";1H";
            out += lines[i];
            out += "\033[K";
        }
        if (!out.empty()) { std::cout << out << std::flush; prev = *this; }
    }
};
static FrameBuffer g_curr, g_prev;
static bool g_first_paint = true;

// ── 全局状态 ─────────────────────────────────
static std::mutex           g_mx;
static StateUpdatePayload   g_gs{};
static StatsResponsePayload g_sr{};
static std::atomic<bool>    g_gdirty{false}, g_sdirty{false};
static std::atomic<bool>    g_running{true};
static int                  g_fd = -1;
static int                  g_myid = 0;
static std::string          g_user;

enum class View { GAME, STATS };
static std::atomic<View> g_view{View::GAME};

// ── 辅助：血条 ───────────────────────────────
static std::string hpbar(int hp, int max, int w=14) {
    int n = hp>0 ? hp*w/max : 0;
    n = std::max(0, std::min(n, w));
    return std::string(hp>max/3?FG:FR) + B
           + std::string((size_t)n, '|') + R DIM
           + std::string((size_t)(w-n), '.') + R;
}
static void setline(FrameBuffer& fb, int r, const std::string& s) {
    if (r>=0 && r<FrameBuffer::MAX_R && r<g_rows.load()) fb.lines[r]=s;
}

// ── 构建游戏帧 ───────────────────────────────
static void build_game(FrameBuffer& fb, const StateUpdatePayload& s) {
    fb.clear(); int row=0;
    {
        char b[128];
        snprintf(b,sizeof(b),"  %-12s  玩家:%d  准备:%d/%d  %s",
                 g_user.c_str(), (int)s.player_count, (int)s.ready_count,
                 (int)s.player_count,
                 s.game_started?(s.game_over?"[游戏结束]":"[游戏中]"):"[等待准备]");
        setline(fb,row++,std::string(B BGb FW)+b+R);
    }
    row++;
    setline(fb,row++,std::string("  +")+std::string(MAP_W*2,'-')+"+");

    int  pat[MAP_H][MAP_W]; memset(pat,-1,sizeof(pat));
    bool wat[MAP_H][MAP_W]; memset(wat, 0,sizeof(wat));
    for (int i=MAX_PLAYERS-1;i>=0;i--) {
        const auto& p=s.players[i];
        if (p.connected&&p.alive&&p.x>=0&&p.x<MAP_W&&p.y>=0&&p.y<MAP_H)
            pat[(int)p.y][(int)p.x]=i;
    }
    for (int i=0;i<MAX_WEAPONS;i++)
        if (s.weapons[i].active&&s.weapons[i].x>=0&&s.weapons[i].x<MAP_W
            &&s.weapons[i].y>=0&&s.weapons[i].y<MAP_H)
            wat[(int)s.weapons[i].y][(int)s.weapons[i].x]=true;

    for (int y=0;y<MAP_H;y++) {
        std::string ln="  |";
        for (int x=0;x<MAP_W;x++) {
            int pid=pat[y][x];
            if (pid>=0) {
                bool me=(pid==g_myid);
                ln+=B; ln+=PCOLORS[pid];
                ln+=(me?"@":PSYMS[pid]);
                ln+=s.players[pid].has_weapon?(FY "*"):" ";
                ln+=R;
            } else if (wat[y][x]) {
                ln+=B FY "W " R;
            } else {
                ln+=DIM ". " R;
            }
        }
        ln+="|"; setline(fb,row++,ln);
    }
    setline(fb,row++,std::string("  +")+std::string(MAP_W*2,'-')+"+");
    row++;

    setline(fb,row++,B "  玩家状态：" R);
    for (int i=0;i<MAX_PLAYERS;i++) {
        const auto& p=s.players[i]; if (!p.connected) continue;
        bool me=(i==g_myid);
        std::string ln="  "; ln+=PCOLORS[i]; ln+=B;
        ln+=(me?"▶ ":"  ");
        char nb[20]; snprintf(nb,sizeof(nb),"%-12s",p.name); ln+=nb; ln+=R;
        if (!p.alive) { ln+=FR "  【阵亡】" R; }
        else {
            char info[80]; snprintf(info,sizeof(info),"  (%2d,%2d) HP:%3d [",p.x,p.y,p.health);
            ln+=info; ln+=hpbar(p.health,MAX_HEALTH); ln+="]";
            if (p.has_weapon) ln+=FY B " ⚡×2" R;
        }
        if (!s.game_started)
            ln+=p.ready?(std::string("  " FG "✓" R)):(std::string("  " DIM "未准备" R));
        setline(fb,row++,ln);
    }
    row++;
    setline(fb,row++,std::string("  " FM "► ")+s.last_event+R);

    if (s.game_over) {
        bool iw=(s.winner_id==g_myid);
        setline(fb,row++,std::string("  " B)+(iw?FG "★ 恭喜你获胜！":FR "✗ 你已落败。")+"  Q=退出  T=战绩" R);
    } else if (!s.game_started) {
        int need=(int)s.player_count-(int)s.ready_count;
        if (need>0) {
            char b[64]; snprintf(b,sizeof(b),"  " FBL B "⏳ 还需%d名玩家按R准备…" R,need);
            setline(fb,row++,b);
        }
    }
    row++;
    char ctrl[160];
    snprintf(ctrl,sizeof(ctrl),
             "  " B "操作：" R " WASD/方向键=移动  空格/F=攻击(范围≤%d)  R=准备  T=战绩  Q=退出",
             ATTACK_RANGE);
    setline(fb,row++,ctrl);
    std::string leg="  ";
    for (int i=0;i<MAX_PLAYERS;i++){
        leg+=PCOLORS[i]; leg+=B; leg+=PSYMS[i]; leg+=R;
        char b[10]; snprintf(b,sizeof(b),"=P%d  ",i); leg+=b;
    }
    leg+=FY B "W" R "=武器  " B "@" R "=自己";
    setline(fb,row,leg);
}

// ── 构建战绩帧 ───────────────────────────────
static void build_stats(FrameBuffer& fb, const StatsResponsePayload& sr) {
    fb.clear(); int row=0;
    setline(fb,row++,B BGb FW "  战绩查询  " R);
    row++;
    if (!sr.found) { setline(fb,row++,FR "  用户不存在" R); }
    else {
        char b[64]; snprintf(b,sizeof(b),"  " B FC "用户名：%s" R,sr.username);
        setline(fb,row++,b); row++;
        auto sr_=sr;
        auto srow=[&](const char* lbl,const char* col,const char* v){
            setline(fb,row++,std::string("    " DIM)+lbl+R "  "+col+B+v+R);
        };
        char v[32];
        snprintf(v,sizeof(v),"%d",sr_.games);  srow("总 局 数：",FW,v);
        snprintf(v,sizeof(v),"%d",sr_.wins);   srow("胜    场：",FG,v);
        snprintf(v,sizeof(v),"%d",sr_.losses); srow("败    场：",FR,v);
        float wr=sr_.games>0?sr_.wins*100.f/sr_.games:0;
        snprintf(v,sizeof(v),"%.1f%%",wr);     srow("胜    率：",FY,v);
        snprintf(v,sizeof(v),"%d",sr_.kills);  srow("总 击 杀：",FC,v);
        snprintf(v,sizeof(v),"%d",sr_.deaths); srow("总 死 亡：",FM,v);
        float kd=sr_.deaths>0?sr_.kills*1.f/sr_.deaths:(float)sr_.kills;
        snprintf(v,sizeof(v),"%.2f",kd);       srow("K / D ：",FY,v);
        row++;
        setline(fb,row++,std::string("    " DIM "最后游戏：" R "  " FW)+sr_.last_played+R);
    }
    row++;
    setline(fb,row,DIM "  Q=返回游戏  S=查询其他玩家" R);
}

// ── 终端尺寸等待 ─────────────────────────────
static void wait_resize() {
    update_winsize();
    while (g_rows<MIN_ROWS || g_cols<MIN_COLS) {
        std::cout << CLS SHOW
                  << "\n\n  " FR B "终端太小！" R "\n\n"
                  << "  当前：" << g_cols.load() << "列 × " << g_rows.load() << "行\n"
                  << "  需要：" << MIN_COLS << "列 × " << MIN_ROWS << "行（建议 ≥ 80×40）\n\n"
                  << "  请拖大终端窗口，程序自动继续…\n" << std::flush;
        std::this_thread::sleep_for(std::chrono::milliseconds(400));
        update_winsize();
    }
    std::cout << CLS SHOW << std::flush;
}

// ── 接收线程 ─────────────────────────────────
static void recv_fn() {
    while (g_running) {
        PacketHeader hdr{};
        if (!recv_all(g_fd,&hdr,HEADER_SIZE)){g_running=false;break;}
        switch(hdr.type) {
            case PacketType::STATE_UPDATE: {
                StateUpdatePayload p{};
                size_t to=std::min((size_t)hdr.length,sizeof(p));
                if (hdr.length>0&&!recv_all(g_fd,&p,to)){g_running=false;break;}
                std::lock_guard<std::mutex> lk(g_mx);
                g_gs=p; g_myid=p.your_id; g_gdirty=true; break;
            }
            case PacketType::STATS_RESPONSE: {
                StatsResponsePayload p{};
                size_t to=std::min((size_t)hdr.length,sizeof(p));
                if (hdr.length>0) recv_all(g_fd,&p,to);
                std::lock_guard<std::mutex> lk(g_mx);
                g_sr=p; g_sdirty=true; break;
            }
            case PacketType::HEARTBEAT:
                send_packet(g_fd,PacketType::HEARTBEAT_ACK); break;
            case PacketType::HEARTBEAT_ACK: break;
            case PacketType::DISCONNECT: g_running=false; break;
            default:
                if (hdr.length>0){char s[512]{};recv_all(g_fd,s,std::min((int)hdr.length,512));}
        }
    }
}

// ── 心跳线程 ─────────────────────────────────
static void hb_fn() {
    while (g_running) {
        std::this_thread::sleep_for(std::chrono::seconds(HEARTBEAT_INTERVAL));
        if (!g_running) break;
        if (send_packet(g_fd,PacketType::HEARTBEAT)<0) g_running=false;
    }
}

static void send_action(ActionType a){
    ActionPayload ap; ap.action=a;
    send_packet(g_fd,PacketType::ACTION,&ap,sizeof(ap));
}

// ── 登录/注册 UI ─────────────────────────────
static bool login_ui() {
    wait_resize();
    std::cout << CLS SHOW
              << B BGb FW "  多人对战游戏 v3.2  ──  账号登录  " R "\n"
              << DIM "  建议终端：" << MIN_COLS << "列 × " << MIN_ROWS << "行以上\n" R
              << std::flush;

    while (true) {
        std::cout << "\n" FBL B "  [1] 登录\n  [2] 注册\n  [3] 退出\n\n" R;
        std::string ch = read_line_raw("  请选择 > ");
        if (ch=="\x03"||ch=="3"||ch=="q"||ch=="Q") return false;
        if (ch!="1"&&ch!="2"){ std::cout<<FR "  请输入 1、2 或 3\n" R; continue; }
        bool is_reg=(ch=="2");

        std::string user = read_line_raw("  用户名 > ");
        if (user=="\x03") return false;
        while (!user.empty()&&(user.back()==' '||user.back()=='\t')) user.pop_back();
        if (user.empty()){ std::cout<<FR "  用户名不能为空\n" R; continue; }

        std::string pass = read_line_raw("  密  码 > ", false);
        if (pass=="\x03") return false;

        if (is_reg) {
            std::string conf = read_line_raw("  确认密码 > ", false);
            if (conf=="\x03") return false;
            if (pass!=conf){ std::cout<<FR "  两次密码不一致\n" R; continue; }
        }

        AuthPayload ap{};
        strncpy(ap.username,user.c_str(),31); ap.username[31]='\0';
        strncpy(ap.password,pass.c_str(),63); ap.password[63]='\0';
        if (send_packet(g_fd, is_reg?PacketType::REGISTER:PacketType::LOGIN, &ap,sizeof(ap))<0){
            std::cout<<FR "  网络错误\n" R; return false;
        }

        // 等 AUTH_RESULT，跳过 HEARTBEAT/ACK
        AuthResultPayload ar{};
        bool got=false;
        for (int i=0;i<64&&!got;i++) {
            PacketHeader hdr{};
            if (!recv_all(g_fd,&hdr,HEADER_SIZE)){ std::cout<<FR "  连接断开\n" R; return false; }
            if (hdr.type==PacketType::HEARTBEAT){send_packet(g_fd,PacketType::HEARTBEAT_ACK);continue;}
            if (hdr.type==PacketType::HEARTBEAT_ACK) continue;
            if (hdr.type==PacketType::AUTH_RESULT) {
                size_t to=std::min((size_t)hdr.length,sizeof(ar));
                if (hdr.length>0) recv_all(g_fd,&ar,to);
                got=true;
            } else {
                if (hdr.length>0){char s[256]{};recv_all(g_fd,s,std::min((int)hdr.length,256));}
            }
        }
        if (!got){ std::cout<<FR "  服务器未响应，请重试\n" R; continue; }

        if (ar.success) {
            g_user=ar.username;
            std::cout<<FG B "\n  ✓ "<<ar.message<<"，欢迎 "<<ar.username<<"！\n" R;
            std::this_thread::sleep_for(std::chrono::milliseconds(500));
            return true;
        } else {
            std::cout<<FR "\n  ✗ "<<ar.message<<"\n" R;
        }
    }
}

// ── 战绩对话框 ───────────────────────────────
static void query_stats_dialog() {
    leave_raw();
    std::cout << SHOW "\n" B "  查询战绩（回车=查自己）> " R;
    std::cout.flush();
    std::string target = read_line_raw("", true);
    if (target=="\x03") target="";
    while (!target.empty()&&target.back()==' ') target.pop_back();
    StatsRequestPayload srp{};
    strncpy(srp.username,target.c_str(),31); srp.username[31]='\0';
    send_packet(g_fd,PacketType::STATS_REQUEST,&srp,sizeof(srp));
    std::cout << CLS HIDE << std::flush;
    g_prev.clear();
    enter_raw();
    g_view=View::STATS; g_first_paint=true;
}

static void sig_int(int){ g_running=false; }

static int read_arrow_seq() {
    char c=0;
    if (!stdin_ready(20)) return 0;
    if (::read(STDIN_FILENO,&c,1)<=0||c!='[') return 0;
    if (!stdin_ready(20)) return 0;
    if (::read(STDIN_FILENO,&c,1)<=0) return 0;
    return (unsigned char)c;
}

// ── main ─────────────────────────────────────
int main(int argc, char* argv[]) {
    const char* host=DEFAULT_HOST; int port=DEFAULT_PORT;
    if (argc>=2) host=argv[1];
    if (argc>=3) port=atoi(argv[2]);

    signal(SIGINT,  sig_int);
    signal(SIGTERM, sig_int);
    signal(SIGPIPE, SIG_IGN);
    signal(SIGWINCH,on_sigwinch);
    update_winsize();

    g_fd=socket(AF_INET,SOCK_STREAM,0);
    if (g_fd<0){perror("socket");return 1;}
    {int f=1;setsockopt(g_fd,IPPROTO_TCP,TCP_NODELAY,&f,sizeof(f));}
    sockaddr_in sa{};
    sa.sin_family=AF_INET; sa.sin_port=htons(port);
    if (inet_pton(AF_INET,host,&sa.sin_addr)<=0){
        std::cerr<<"无效地址: "<<host<<"\n"; return 1;
    }
    if (connect(g_fd,(sockaddr*)&sa,sizeof(sa))<0){
        perror("connect");
        std::cerr<<"提示：先启动服务器\n  同机: ./client 127.0.0.1 "<<port<<"\n";
        return 1;
    }

    std::thread hb(hb_fn);

    if (!login_ui()) {
        g_running=false;
        if (hb.joinable()) hb.join();
        close(g_fd);
        std::cout<<CLS SHOW "再见！\n";
        return 0;
    }
    send_packet(g_fd,PacketType::JOIN);

    std::cout<<CLS HIDE<<std::flush;
    enter_raw();
    std::thread rv(recv_fn);

    g_first_paint=true; g_prev.clear();

    while (g_running) {
        // 终端变小时暂停渲染
        if (g_rows<MIN_ROWS||g_cols<MIN_COLS){
            std::this_thread::sleep_for(std::chrono::milliseconds(200));
            update_winsize(); continue;
        }

        if (stdin_ready(10)) {
            char ch=0;
            if (::read(STDIN_FILENO,&ch,1)>0) {
                View cur=g_view.load();
                if (cur==View::GAME) {
                    switch(ch) {
                        case 'w':case 'W': send_action(ActionType::MOVE_UP);    break;
                        case 's':case 'S': send_action(ActionType::MOVE_DOWN);  break;
                        case 'a':case 'A': send_action(ActionType::MOVE_LEFT);  break;
                        case 'd':case 'D': send_action(ActionType::MOVE_RIGHT); break;
                        case ' ':case 'f':case 'F': send_action(ActionType::ATTACK); break;
                        case 'r':case 'R': send_packet(g_fd,PacketType::READY); break;
                        case 't':case 'T': query_stats_dialog(); break;
                        case 'q':case 'Q': g_running=false; break;
                        case '\033': {
                            int a=read_arrow_seq();
                            if      (a=='A') send_action(ActionType::MOVE_UP);
                            else if (a=='B') send_action(ActionType::MOVE_DOWN);
                            else if (a=='C') send_action(ActionType::MOVE_RIGHT);
                            else if (a=='D') send_action(ActionType::MOVE_LEFT);
                            break;
                        }
                        default: break;
                    }
                } else {
                    switch(ch) {
                        case 'q':case 'Q':
                            std::cout<<CLS HIDE<<std::flush;
                            g_prev.clear(); g_view=View::GAME;
                            g_first_paint=true; g_gdirty=true; break;
                        case 's':case 'S': query_stats_dialog(); break;
                        default: break;
                    }
                }
            }
        }

        View cur=g_view.load();
        if (cur==View::GAME&&g_gdirty) {
            StateUpdatePayload snap;
            {std::lock_guard<std::mutex> lk(g_mx); snap=g_gs; g_gdirty=false;}
            build_game(g_curr,snap);
            g_curr.flush_diff(g_prev,g_first_paint);
            g_first_paint=false;
        } else if (cur==View::STATS&&g_sdirty) {
            StatsResponsePayload snap;
            {std::lock_guard<std::mutex> lk(g_mx); snap=g_sr; g_sdirty=false;}
            build_stats(g_curr,snap);
            g_curr.flush_diff(g_prev,g_first_paint);
            g_first_paint=false;
        }
    }

    leave_raw();
    send_packet(g_fd,PacketType::DISCONNECT);
    close(g_fd);
    if (rv.joinable()) rv.join();
    if (hb.joinable()) hb.join();
    std::cout<<CLS SHOW "已退出游戏。再见！\n";
    return 0;
}
