// ============================================================
// client.cpp  —  多人对战客户端 v3.0
//
// 核心优化：
//   ① TCP_NODELAY：操作包零延迟发出
//   ② 差异渲染（Differential Rendering）：
//      - 维护 prev_frame[] / curr_frame[] 行缓冲
//      - 每帧只对变化的行用 \033[row;1H 定点覆盖
//      - 不再 \033[2J 清屏，彻底消除闪烁
//   ③ 登录/注册 UI（正常 cooked 模式下的菜单）
//   ④ 战绩查询页面
// ============================================================

#include "protocol.h"

#include <iostream>
#include <sstream>
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

#include <termios.h>
#include <fcntl.h>

// ──────────────────────────────────────────────
// 连接默认参数
// ──────────────────────────────────────────────
constexpr const char* DEFAULT_HOST = "127.0.0.1";
constexpr int         DEFAULT_PORT = 9000;

// ──────────────────────────────────────────────
// ANSI 色彩宏
// ──────────────────────────────────────────────
#define R     "\033[0m"          // Reset
#define B     "\033[1m"          // Bold
#define DIM   "\033[2m"
#define BLINK "\033[5m"

#define FC    "\033[96m"         // Cyan
#define FY    "\033[93m"         // Yellow
#define FG    "\033[92m"         // Green
#define FR    "\033[91m"         // Red
#define FM    "\033[95m"         // Magenta
#define FB    "\033[94m"         // Blue
#define FW    "\033[97m"         // White

#define BGb   "\033[44m"         // BG Blue
#define BGk   "\033[40m"         // BG Black

#define CLS   "\033[2J\033[H"    // 清屏（仅切换页面时用一次）
#define HIDE  "\033[?25l"
#define SHOW  "\033[?25h"

// 定位光标到第 r 行第 1 列（r 从 1 开始）
#define GOTO_ROW(r) "\033[" + std::to_string(r) + ";1H"
// 清除从当前位置到行尾
#define EL    "\033[K"

// 5 种玩家颜色
static const char* PCOLORS[MAX_PLAYERS] = {FC, FY, FG, FM, FR};
static const char* PSYMS[MAX_PLAYERS]   = {"A","B","C","D","E"};

// ──────────────────────────────────────────────
// 终端原始模式
// ──────────────────────────────────────────────
static struct termios g_orig_term;

static void enter_raw() {
    tcgetattr(STDIN_FILENO, &g_orig_term);
    struct termios raw = g_orig_term;
    raw.c_lflag &= ~(tcflag_t)(ICANON | ECHO);
    raw.c_cc[VMIN]  = 0;
    raw.c_cc[VTIME] = 0;
    tcsetattr(STDIN_FILENO, TCSAFLUSH, &raw);
    fcntl(STDIN_FILENO, F_SETFL,
          fcntl(STDIN_FILENO, F_GETFL, 0) | O_NONBLOCK);
}

static void leave_raw() {
    fcntl(STDIN_FILENO, F_SETFL,
          fcntl(STDIN_FILENO, F_GETFL, 0) & ~O_NONBLOCK);
    tcsetattr(STDIN_FILENO, TCSAFLUSH, &g_orig_term);
    std::cout << SHOW << std::flush;
}

// ──────────────────────────────────────────────
// 屏幕帧缓冲（差异渲染引擎）
// ──────────────────────────────────────────────
struct FrameBuffer {
    static constexpr int ROWS = 60;
    std::string lines[ROWS];  // 每行渲染内容（不含 \n）

    void clear() {
        for (auto& l : lines) l.clear();
    }

    // 将 buf 差异写到终端：只刷新与 prev 不同的行
    // first_paint=true 时强制全部重绘（切换场景时）
    void flush_diff(FrameBuffer& prev, bool first_paint = false) const {
        std::string out;
        out.reserve(4096);
        for (int i = 0; i < ROWS; i++) {
            if (!first_paint && lines[i] == prev.lines[i]) continue;
            // 定位到行 i+1，第1列
            out += "\033[";
            out += std::to_string(i + 1);
            out += ";1H";
            out += lines[i];
            out += "\033[K";  // 清除行尾残留字符
        }
        if (!out.empty()) {
            std::cout << out << std::flush;
            prev = *this;  // 更新前帧缓冲
        }
    }
};

static FrameBuffer g_curr, g_prev;
static bool        g_first_paint = true;  // 切换场景后强制全刷

// ──────────────────────────────────────────────
// 全局客户端状态
// ──────────────────────────────────────────────
static std::mutex         g_state_mutex;
static StateUpdatePayload g_game_state{};   // 最新游戏状态
static StatsResponsePayload g_stats_resp{}; // 最新战绩查询结果
static std::atomic<bool>  g_game_dirty{false};
static std::atomic<bool>  g_stats_dirty{false};
static std::atomic<bool>  g_running{true};
static int                g_sockfd   = -1;
static int                g_my_id    = 0;
static std::string        g_username;       // 登录后的账号名

// ──────────────────────────────────────────────
// 视图模式
// ──────────────────────────────────────────────
enum class View { GAME, STATS };
static std::atomic<View> g_view{View::GAME};

// ──────────────────────────────────────────────
// 辅助：血条字符串
// ──────────────────────────────────────────────
static std::string hp_bar(int hp, int max, int w = 16) {
    int n = hp > 0 ? (hp * w) / max : 0;
    n = std::max(0, std::min(n, w));
    std::string s;
    s += (hp > max / 3) ? FG : FR;
    s += B;
    s += std::string((size_t)n, '|');
    s += R;
    s += DIM;
    s += std::string((size_t)(w - n), '.');
    s += R;
    return s;
}

// ──────────────────────────────────────────────
// 辅助：在 FrameBuffer 的指定行写入字符串
// ──────────────────────────────────────────────
static inline void set_line(FrameBuffer& fb, int row, const std::string& s) {
    if (row >= 0 && row < FrameBuffer::ROWS)
        fb.lines[row] = s;
}

// ──────────────────────────────────────────────
// 渲染：游戏主界面 → FrameBuffer
// ──────────────────────────────────────────────
static void build_game_frame(FrameBuffer& fb,
                              const StateUpdatePayload& s) {
    fb.clear();
    int row = 0;

    // ── 标题栏 ──
    {
        std::string t;
        t += B; t += BGb; t += FW;
        char buf[128];
        snprintf(buf, sizeof(buf),
                 "  %-12s  玩家:%d  准备:%d/%d  %s",
                 g_username.c_str(),
                 (int)s.player_count, (int)s.ready_count, (int)s.player_count,
                 s.game_started ? (s.game_over ? "[游戏结束]" : "[游戏中]")
                                : "[等待准备]");
        t += buf; t += R;
        set_line(fb, row++, t);
    }
    row++; // 空行

    // ── 地图（上边框）──
    set_line(fb, row++, std::string("  +") + std::string(MAP_W*2, '-') + "+");

    // 预建玩家/武器位置表
    int  pat[MAP_H][MAP_W]; memset(pat, -1, sizeof(pat));
    bool wat[MAP_H][MAP_W]; memset(wat,  0, sizeof(wat));
    for (int i = MAX_PLAYERS-1; i >= 0; i--) {
        const auto& p = s.players[i];
        if (p.connected && p.alive &&
            p.x>=0 && p.x<MAP_W && p.y>=0 && p.y<MAP_H)
            pat[(int)p.y][(int)p.x] = i;
    }
    for (int i = 0; i < MAX_WEAPONS; i++)
        if (s.weapons[i].active &&
            s.weapons[i].x>=0 && s.weapons[i].x<MAP_W &&
            s.weapons[i].y>=0 && s.weapons[i].y<MAP_H)
            wat[(int)s.weapons[i].y][(int)s.weapons[i].x] = true;

    for (int y = 0; y < MAP_H; y++) {
        std::string line = "  |";
        for (int x = 0; x < MAP_W; x++) {
            int pid = pat[y][x];
            if (pid >= 0) {
                bool is_me = (pid == g_my_id);
                line += B; line += PCOLORS[pid];
                line += (is_me ? "@" : PSYMS[pid]);
                // 持有武器时在右侧加 * 标识
                if (s.players[pid].has_weapon) { line += FY; line += "*"; }
                else                           { line += " "; }
                line += R;
            } else if (wat[y][x]) {
                line += B; line += FY; line += "W "; line += R;
            } else {
                line += DIM; line += ". "; line += R;
            }
        }
        line += "|";
        set_line(fb, row++, line);
    }

    // 下边框
    set_line(fb, row++, std::string("  +") + std::string(MAP_W*2, '-') + "+");
    row++; // 空行

    // ── 玩家状态面板 ──
    {
        std::string title = std::string(B) + "  玩家状态：" + R;
        set_line(fb, row++, title);
    }
    for (int i = 0; i < MAX_PLAYERS; i++) {
        const auto& p = s.players[i];
        if (!p.connected) continue;
        bool is_me = (i == g_my_id);
        std::string ln = "  ";
        ln += PCOLORS[i]; ln += B;
        ln += (is_me ? "▶ " : "  ");
        char nbuf[20]; snprintf(nbuf, sizeof(nbuf), "%-12s", p.name);
        ln += nbuf; ln += R;

        if (!p.alive) {
            ln += FR; ln += "  【阵亡】"; ln += R;
        } else {
            char info[80];
            snprintf(info, sizeof(info), "  (%2d,%2d)  HP:%3d/%-3d  [",
                     p.x, p.y, p.health, MAX_HEALTH);
            ln += info;
            ln += hp_bar(p.health, MAX_HEALTH);
            ln += "]";
            if (p.has_weapon) { ln += "  "; ln += FY; ln += B; ln += "⚡×2"; ln += R; }
        }
        if (!s.game_started) {
            ln += (p.ready ? (std::string("  ") + FG + "✓准备" + R)
                           : (std::string("  ") + DIM + "未准备" + R));
        }
        set_line(fb, row++, ln);
    }
    row++; // 空行

    // ── 事件日志 ──
    {
        std::string ev = std::string("  ") + FM + "► " + s.last_event + R;
        set_line(fb, row++, ev);
    }

    // ── 特殊提示 ──
    if (s.game_over) {
        bool i_win = (s.winner_id == g_my_id);
        std::string msg = "  ";
        msg += B;
        msg += (i_win ? FG : FR);
        msg += (i_win ? "★  恭喜你获胜！" : "✗  你已落败。");
        msg += "  按 Q 退出  按 T 查战绩";
        msg += R;
        set_line(fb, row++, msg);
    } else if (!s.game_started) {
        int need = (int)s.player_count - (int)s.ready_count;
        if (need > 0) {
            std::string msg = "  ";
            msg += FB; msg += B;
            char buf[64]; snprintf(buf,sizeof(buf),"⏳  还需 %d 名玩家按 R 准备…",need);
            msg += buf; msg += R;
            set_line(fb, row++, msg);
        }
    }
    row++; // 空行

    // ── 操作提示 ──
    {
        char buf[128];
        snprintf(buf, sizeof(buf),
                 "  " B "操作：" R
                 "  WASD/方向键=移动  空格/F=攻击(范围≤%d)  R=准备  T=战绩  Q=退出",
                 ATTACK_RANGE);
        set_line(fb, row++, buf);
    }
    {
        std::string legend = "  图例：";
        for (int i = 0; i < MAX_PLAYERS; i++) {
            legend += PCOLORS[i]; legend += B; legend += PSYMS[i]; legend += R;
            char buf[16]; snprintf(buf,sizeof(buf),"=玩家%d  ",i);
            legend += buf;
        }
        legend += FY; legend += B; legend += "W"; legend += R;
        legend += "=武器  ";
        legend += B; legend += "@"; legend += R; legend += "=自己";
        set_line(fb, row++, legend);
    }
}

// ──────────────────────────────────────────────
// 渲染：战绩查询页面 → FrameBuffer
// ──────────────────────────────────────────────
static void build_stats_frame(FrameBuffer& fb,
                               const StatsResponsePayload& sr) {
    fb.clear();
    int row = 0;

    // 标题
    {
        std::string t;
        t += B; t += BGb; t += FW;
        t += "  战绩查询  ";
        t += R;
        set_line(fb, row++, t);
    }
    row++;

    if (!sr.found) {
        set_line(fb, row++, std::string("  ") + FR + "用户不存在" + R);
    } else {
        // 用户名
        {
            std::string ln = "  ";
            ln += B; ln += FC;
            char buf[64]; snprintf(buf,sizeof(buf),"用户名：%s",sr.username);
            ln += buf; ln += R;
            set_line(fb, row++, ln);
        }
        row++;

        // 数据格子
        auto stat_line = [&](const char* label, const char* color, const char* val) {
            std::string ln = "    ";
            ln += DIM; ln += label; ln += R;
            ln += "  "; ln += color; ln += B; ln += val; ln += R;
            set_line(fb, row++, ln);
        };
        char buf[32];
        snprintf(buf,sizeof(buf),"%d", sr.games);  stat_line("总 局 数：",FW,buf);
        snprintf(buf,sizeof(buf),"%d", sr.wins);   stat_line("胜    场：",FG,buf);
        snprintf(buf,sizeof(buf),"%d", sr.losses); stat_line("败    场：",FR,buf);

        // 胜率
        float wr = sr.games > 0 ? (sr.wins * 100.0f / sr.games) : 0.f;
        snprintf(buf,sizeof(buf),"%.1f%%", wr);    stat_line("胜    率：",FY,buf);

        snprintf(buf,sizeof(buf),"%d", sr.kills);  stat_line("总 击 杀：",FC,buf);
        snprintf(buf,sizeof(buf),"%d", sr.deaths); stat_line("总 死 亡：",FM,buf);

        float kd = sr.deaths > 0 ? (sr.kills * 1.0f / sr.deaths) : (float)sr.kills;
        snprintf(buf,sizeof(buf),"%.2f", kd);      stat_line("K/D  比：",FY,buf);

        row++;
        {
            std::string ln = "    ";
            ln += DIM; ln += "最后游戏："; ln += R;
            ln += "  "; ln += FW; ln += sr.last_played; ln += R;
            set_line(fb, row++, ln);
        }
    }

    row++;
    set_line(fb, row++,
             std::string("  ") + DIM + "按 Q 返回游戏  按 S 查询其他玩家" + R);
}

// ──────────────────────────────────────────────
// 接收线程
// ──────────────────────────────────────────────
static void recv_thread_fn() {
    while (g_running) {
        PacketHeader hdr{};
        if (!recv_all(g_sockfd, &hdr, HEADER_SIZE)) {
            g_running = false; break;
        }
        switch (hdr.type) {
            case PacketType::STATE_UPDATE: {
                StateUpdatePayload pkt{};
                size_t to_read = std::min((size_t)hdr.length, sizeof(pkt));
                if (hdr.length>0 && !recv_all(g_sockfd, &pkt, to_read)) {
                    g_running=false; break;
                }
                std::lock_guard<std::mutex> lk(g_state_mutex);
                g_game_state = pkt;
                g_my_id      = pkt.your_id;
                g_game_dirty = true;
                break;
            }
            case PacketType::STATS_RESPONSE: {
                StatsResponsePayload resp{};
                size_t to_read = std::min((size_t)hdr.length, sizeof(resp));
                if (hdr.length>0) recv_all(g_sockfd, &resp, to_read);
                std::lock_guard<std::mutex> lk(g_state_mutex);
                g_stats_resp  = resp;
                g_stats_dirty = true;
                break;
            }
            case PacketType::HEARTBEAT:
                send_packet(g_sockfd, PacketType::HEARTBEAT_ACK);
                break;
            case PacketType::HEARTBEAT_ACK:
                break;
            case PacketType::DISCONNECT:
                g_running = false; break;
            default:
                if (hdr.length>0){
                    char skip[512]{};
                    recv_all(g_sockfd, skip, std::min((int)hdr.length, 512));
                }
                break;
        }
    }
}

// ──────────────────────────────────────────────
// 心跳线程
// ──────────────────────────────────────────────
static void hb_thread_fn() {
    while (g_running) {
        std::this_thread::sleep_for(std::chrono::seconds(HEARTBEAT_INTERVAL));
        if (!g_running) break;
        if (send_packet(g_sockfd, PacketType::HEARTBEAT) < 0)
            g_running = false;
    }
}

static void send_action(ActionType a) {
    ActionPayload ap; ap.action = a;
    send_packet(g_sockfd, PacketType::ACTION, &ap, sizeof(ap));
}

static int read_arrow() {
    char c=0;
    if (read(STDIN_FILENO,&c,1)<=0||c!='[') return 0;
    if (read(STDIN_FILENO,&c,1)<=0) return 0;
    return (unsigned char)c;
}

static void sig_handler(int) { g_running = false; }

// ──────────────────────────────────────────────
// 登录/注册 UI（在 cooked 终端模式下运行）
// 返回 true = 已成功认证，false = 用户主动退出
// ──────────────────────────────────────────────
static bool login_ui() {
    // 辅助：读一行并去除末尾 \r\n 以及重置 cin 状态
    auto read_line = [](const char* prompt) -> std::string {
        std::cout << prompt << std::flush;
        std::string s;
        if (!std::getline(std::cin, s)) {
            std::cin.clear();   // 重置 eof/fail 状态，允许继续读
            return "";
        }
        // 去除 Windows 风格 \r
        if (!s.empty() && s.back() == '\r') s.pop_back();
        return s;
    };

    // 辅助：关闭回显读密码，使用 TCSADRAIN（不丢弃已读缓冲）
    auto read_pass = [](const char* prompt) -> std::string {
        std::cout << prompt << std::flush;
        struct termios t, t2;
        tcgetattr(STDIN_FILENO, &t);
        t2 = t;
        t2.c_lflag &= ~(tcflag_t)ECHO;
        tcsetattr(STDIN_FILENO, TCSADRAIN, &t2);   // TCSADRAIN：等待输出完成后切换
        std::string s;
        if (!std::getline(std::cin, s)) { std::cin.clear(); s = ""; }
        tcsetattr(STDIN_FILENO, TCSADRAIN, &t);    // 恢复 echo
        if (!s.empty() && s.back() == '\r') s.pop_back();
        std::cout << "\n";
        return s;
    };

    // 清屏、标题
    std::cout << CLS << SHOW;   // 登录阶段显示光标，方便用户看到输入位置
    std::cout << B << BGb << FW
              << "  多人对战游戏 v3.0  ——  账号登录  "
              << R << "\n";
    std::cout << DIM << "  (建议终端宽度 ≥ 80 列，高度 ≥ 40 行)" << R << "\n\n";

    while (true) {
        std::cout << FB << B
                  << "  [1]  登录\n"
                  << "  [2]  注册新账号\n"
                  << "  [3]  退出\n\n"
                  << R;
        std::string choice = read_line("  请选择 > ");
        // 去除空白
        while (!choice.empty() && (choice.back()==' '||choice.back()=='\t'))
            choice.pop_back();

        if (choice == "3" || choice == "q" || choice == "Q") return false;
        if (choice != "1" && choice != "2") {
            std::cout << FR << "  请输入 1、2 或 3\n\n" << R;
            continue;
        }

        bool is_register = (choice == "2");

        std::string username = read_line("  用户名 > ");
        while (!username.empty() && (username.back()==' '||username.back()=='\t'))
            username.pop_back();
        if (username.empty()) { std::cout << FR << "  用户名不能为空\n\n" << R; continue; }

        std::string password = read_pass("  密  码 > ");
        if (is_register) {
            std::string confirm = read_pass("  确认密码 > ");
            if (password != confirm) {
                std::cout << FR << "  两次密码不一致\n\n" << R; continue;
            }
        }

        // 发包给服务器
        AuthPayload ap{};
        strncpy(ap.username, username.c_str(), 31);
        ap.username[31] = '\0';
        strncpy(ap.password, password.c_str(), 63);
        ap.password[63] = '\0';

        PacketType ptype = is_register ? PacketType::REGISTER : PacketType::LOGIN;
        if (send_packet(g_sockfd, ptype, &ap, sizeof(ap)) < 0) {
            std::cout << FR << "  网络错误：发包失败，请检查服务器连接\n\n" << R;
            return false;   // 真正的网络错误才退出
        }

        // 等待 AUTH_RESULT。
        // 关键修复：心跳线程与 login_ui 共享同一 socket；服务器对
        // HEARTBEAT 的回应 HEARTBEAT_ACK(11) 可能在 AUTH_RESULT
        // 之前抵达，必须在这里循环跳过心跳包，否则 recv_pkt 读错类型
        // 就会打印"意外响应"并 continue，导致用户以为出了问题。
        AuthResultPayload ar{};
        bool got_result = false;
        for (int wait_loop = 0; wait_loop < 32 && !got_result; ++wait_loop) {
            PacketHeader hdr{};
            if (!recv_all(g_sockfd, &hdr, HEADER_SIZE)) {
                std::cout << FR << "  网络错误：连接断开\n\n" << R;
                return false;
            }
            if (hdr.type == PacketType::HEARTBEAT) {
                // 服务器主动心跳：回应它，然后继续等
                send_packet(g_sockfd, PacketType::HEARTBEAT_ACK);
                continue;
            }
            if (hdr.type == PacketType::HEARTBEAT_ACK) {
                // 我们发出的心跳的回包：静默忽略，继续等
                continue;
            }
            if (hdr.type != PacketType::AUTH_RESULT) {
                if (hdr.length>0){char s[256]{};recv_all(g_sockfd,s,std::min((int)hdr.length,256));}
                continue;
            }
            if (hdr.length>0) recv_all(g_sockfd, &ar, std::min((int)hdr.length,(int)sizeof(ar)));
            got_result = true;
        }
        if (!got_result) {
            std::cout << FR << "  未收到服务器响应，请重试\n\n" << R;
            continue;
        }

        if (ar.success) {
            g_username = ar.username;
            std::cout << FG << B << "\n  ✓ " << ar.message
                      << "，欢迎 " << ar.username << "！\n" << R;
            std::this_thread::sleep_for(std::chrono::milliseconds(600));
            return true;
        } else {
            // 认证失败：显示原因，循环让用户重试（服务器保持连接）
            std::cout << FR << "\n  ✗ " << ar.message << "\n\n" << R;
        }
    }
}

// ──────────────────────────────────────────────
// 查询战绩对话框（在 raw 模式中调用，临时切回 cooked）
// ──────────────────────────────────────────────
static void query_stats_dialog() {
    // 暂时离开 raw 模式，恢复 cooked + echo + 阻塞，让用户正常输入
    leave_raw();
    // 清除 cin 可能的 eof/fail 状态
    std::cin.clear();

    std::cout << "\n" << B << "  查询战绩 — 输入用户名（直接回车=查自己）> " << R;
    std::cout.flush();

    std::string target;
    std::getline(std::cin, target);
    std::cin.clear();
    // 去除 \r 和前后空白
    if (!target.empty() && target.back() == '\r') target.pop_back();
    while (!target.empty() && target.back() == ' ') target.pop_back();

    StatsRequestPayload srp{};
    strncpy(srp.username, target.c_str(), 31);
    srp.username[31] = '\0';
    send_packet(g_sockfd, PacketType::STATS_REQUEST, &srp, sizeof(srp));

    // 切换到战绩视图，清屏确保干净起点
    std::cout << CLS << HIDE << std::flush;
    g_prev.clear();          // 清除旧帧缓冲，强制全量重绘
    enter_raw();
    g_view        = View::STATS;
    g_first_paint = true;
    // 等 recv_thread 收到 STATS_RESPONSE 后触发渲染
}

// ──────────────────────────────────────────────
// main
// ──────────────────────────────────────────────
int main(int argc, char* argv[]) {
    const char* host = DEFAULT_HOST;
    int         port = DEFAULT_PORT;

    if (argc >= 2) host = argv[1];
    if (argc >= 3) port = atoi(argv[2]);

    signal(SIGINT,  sig_handler);
    signal(SIGTERM, sig_handler);
    signal(SIGPIPE, SIG_IGN);

    // ── 建立 TCP 连接 ──
    g_sockfd = socket(AF_INET, SOCK_STREAM, 0);
    if (g_sockfd < 0) { perror("socket"); return 1; }
    {
        int f=1;
        setsockopt(g_sockfd, IPPROTO_TCP, TCP_NODELAY, &f, sizeof(f));
    }

    sockaddr_in sa{};
    sa.sin_family = AF_INET;
    sa.sin_port   = htons(port);
    if (inet_pton(AF_INET, host, &sa.sin_addr) <= 0) {
        std::cerr << "无效地址: " << host << "\n"; return 1;
    }
    if (connect(g_sockfd,(sockaddr*)&sa,sizeof(sa)) < 0) {
        perror("connect");
        std::cerr << "\n  提示：先启动服务器，再连接\n"
                  << "  同机：./client 127.0.0.1 " << port << "\n"
                  << "  局域网：./client <服务器IP> " << port << "\n";
        return 1;
    }

    // ── 登录前立即启动心跳线程 ──
    // 关键修复：login_ui() 可能耗时数十秒（用户慢慢输入），
    // 必须在此之前保持心跳，否则服务器 8 秒后踢出连接。
    // send（心跳线程）和 recv（login_ui 主线程）方向相反，
    // TCP 全双工，两者并发访问同一 socket 是安全的。
    std::thread hb_t(hb_thread_fn);

    // ── 登录/注册 UI（cooked 模式，主线程负责收发认证包）──
    if (!login_ui()) {
        g_running = false;
        if (hb_t.joinable()) hb_t.join();
        close(g_sockfd);
        std::cout << CLS << "再见！\n" << SHOW;
        return 0;
    }

    // 通知服务器进入房间
    send_packet(g_sockfd, PacketType::JOIN);

    // ── 进入 raw 模式，启动接收线程 ──
    std::cout << CLS << HIDE << std::flush;
    enter_raw();

    std::thread recv_t(recv_thread_fn);
    // hb_t 已在 connect 后启动，此处不重复创建

    g_first_paint = true;
    g_prev.clear();

    // ── 主循环：10ms 键盘轮询 + 按需差异渲染 ──
    while (g_running) {
        // 读取键盘
        char ch = 0;
        if (read(STDIN_FILENO, &ch, 1) > 0) {
            View cur_view = g_view.load();

            if (cur_view == View::GAME) {
                switch (ch) {
                    case 'w': case 'W': send_action(ActionType::MOVE_UP);    break;
                    case 's': case 'S': send_action(ActionType::MOVE_DOWN);  break;
                    case 'a': case 'A': send_action(ActionType::MOVE_LEFT);  break;
                    case 'd': case 'D': send_action(ActionType::MOVE_RIGHT); break;
                    case ' ': case 'f': case 'F': send_action(ActionType::ATTACK); break;
                    case 'r': case 'R':
                        send_packet(g_sockfd, PacketType::READY);
                        break;
                    case 't': case 'T':
                        query_stats_dialog();
                        break;
                    case 'q': case 'Q':
                        g_running = false; break;
                    case '\033': {
                        int arr = read_arrow();
                        if      (arr=='A') send_action(ActionType::MOVE_UP);
                        else if (arr=='B') send_action(ActionType::MOVE_DOWN);
                        else if (arr=='C') send_action(ActionType::MOVE_RIGHT);
                        else if (arr=='D') send_action(ActionType::MOVE_LEFT);
                        break;
                    }
                    default: break;
                }
            } else { // STATS view
                switch (ch) {
                    case 'q': case 'Q':
                        // 清屏后回到游戏界面，彻底擦除战绩页残留内容
                        std::cout << CLS << HIDE << std::flush;
                        g_prev.clear();         // 强制下次全量重绘
                        g_view        = View::GAME;
                        g_first_paint = true;
                        g_game_dirty  = true;
                        break;
                    case 's': case 'S':
                        query_stats_dialog();  // 再查一次
                        break;
                    default: break;
                }
            }
        }

        // ── 差异渲染 ──
        View cur_view = g_view.load();

        if (cur_view == View::GAME && g_game_dirty) {
            StateUpdatePayload snap;
            {
                std::lock_guard<std::mutex> lk(g_state_mutex);
                snap = g_game_state;
                g_game_dirty = false;
            }
            build_game_frame(g_curr, snap);
            g_curr.flush_diff(g_prev, g_first_paint);
            g_first_paint = false;

        } else if (cur_view == View::STATS && g_stats_dirty) {
            StatsResponsePayload snap;
            {
                std::lock_guard<std::mutex> lk(g_state_mutex);
                snap = g_stats_resp;
                g_stats_dirty = false;
            }
            build_stats_frame(g_curr, snap);
            g_curr.flush_diff(g_prev, g_first_paint);
            g_first_paint = false;
        }

        std::this_thread::sleep_for(std::chrono::milliseconds(10));
    }

    // ── 清理 ──
    leave_raw();
    g_running = false;  // 通知所有线程退出
    send_packet(g_sockfd, PacketType::DISCONNECT);
    close(g_sockfd);
    if (recv_t.joinable()) recv_t.join();
    if (hb_t.joinable())   hb_t.join();

    std::cout << CLS << SHOW << "已退出游戏。再见！\n";
    return 0;
}