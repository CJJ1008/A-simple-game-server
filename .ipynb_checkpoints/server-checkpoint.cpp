// ============================================================
// server.cpp  —  多人对战游戏服务器 v3.0
//
// 新增功能：
//   · 账号注册 / 登录（database.h 文件型持久化）
//   · 每局结束后自动更新战绩（胜/负/击杀/死亡）
//   · STATS_REQUEST：玩家可查询自己或任意用户的战绩
//   · 0.0.0.0 监听，支持局域网多机连接
//
// 并发设计：
//   · g_state_mutex  保护游戏状态（移动/攻击/武器/ready）
//   · Database 内置 mutex 保护文件读写
//   · g_connected[] 使用 atomic<bool> 无锁判断
// ============================================================

#include "protocol.h"
#include "database.h"

#include <iostream>
#include <cstring>
#include <cstdlib>
#include <ctime>
#include <csignal>
#include <algorithm>
#include <thread>
#include <mutex>
#include <atomic>
#include <chrono>
#include <string>
#include <map>

#include <sys/socket.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <arpa/inet.h>
#include <unistd.h>

constexpr int DEFAULT_PORT = 9000;

// ──────────────────────────────────────────────
// 全局数据库（单例，线程安全）
// ──────────────────────────────────────────────
static Database g_db("data");

// ──────────────────────────────────────────────
// 玩家会话信息（认证后记录）
// ──────────────────────────────────────────────
struct PlayerSession {
    std::string username;           // 账号名
    int         kills_this_game = 0;// 本局击杀数（用于结算）
    bool        died_this_game  = false;
    bool        authenticated   = false;
};

// ──────────────────────────────────────────────
// 服务器游戏状态
// ──────────────────────────────────────────────
struct ServerState {
    PlayerState players[MAX_PLAYERS];
    WeaponItem  weapons[MAX_WEAPONS];
    bool        game_started;
    bool        game_over;
    int         winner_id;
    char        last_event[64];
};

// ──────────────────────────────────────────────
// 全局变量
// ──────────────────────────────────────────────
static std::mutex    g_state_mutex;
static ServerState   g_state;
static PlayerSession g_sessions[MAX_PLAYERS];

static int                g_client_fd[MAX_PLAYERS];
static time_t             g_last_heartbeat[MAX_PLAYERS];
static std::atomic<bool>  g_connected[MAX_PLAYERS];
static std::atomic<bool>  g_running{true};
static int                g_server_fd = -1;

// ──────────────────────────────────────────────
// TCP_NODELAY 禁用 Nagle 算法（消除延迟关键）
// ──────────────────────────────────────────────
static void set_nodelay(int fd) {
    int flag = 1;
    setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &flag, sizeof(flag));
}

// ──────────────────────────────────────────────
// 统计在线/准备人数（须持锁调用）
// ──────────────────────────────────────────────
static int online_count_locked() {
    int n = 0;
    for (int i = 0; i < MAX_PLAYERS; i++)
        if (g_connected[i]) n++;
    return n;
}
static int ready_count_locked() {
    int n = 0;
    for (int i = 0; i < MAX_PLAYERS; i++)
        if (g_connected[i] && g_state.players[i].ready) n++;
    return n;
}

// ──────────────────────────────────────────────
// 广播当前状态到所有在线玩家（须持锁调用）
// ──────────────────────────────────────────────
static void broadcast_state_locked() {
    int pc = online_count_locked();
    int rc = ready_count_locked();
    for (int i = 0; i < MAX_PLAYERS; i++) {
        if (!g_connected[i] || g_client_fd[i] < 0) continue;
        StateUpdatePayload pkt{};
        for (int j = 0; j < MAX_PLAYERS; j++) pkt.players[j] = g_state.players[j];
        for (int j = 0; j < MAX_WEAPONS; j++) pkt.weapons[j] = g_state.weapons[j];
        pkt.your_id      = (uint8_t)i;
        pkt.player_count = (uint8_t)pc;
        pkt.ready_count  = (uint8_t)rc;
        pkt.game_started = g_state.game_started ? 1 : 0;
        pkt.game_over    = g_state.game_over    ? 1 : 0;
        pkt.winner_id    = (uint8_t)(g_state.winner_id >= 0 ? g_state.winner_id : 0xFF);
        snprintf(pkt.last_event, sizeof(pkt.last_event), "%s", g_state.last_event);
        send_packet(g_client_fd[i], PacketType::STATE_UPDATE, &pkt, sizeof(pkt));
    }
}

// ──────────────────────────────────────────────
// 重置游戏状态准备开始新局（须持锁调用）
// ──────────────────────────────────────────────
static void reset_game_locked() {
    // 5 个出生点分散在地图各角
    static const int8_t SX[MAX_PLAYERS] = {2,  47, 2,  47, 25};
    static const int8_t SY[MAX_PLAYERS] = {2,  2,  17, 17, 10};
    int oc = online_count_locked();

    for (int i = 0; i < MAX_PLAYERS; i++) {
        auto& p = g_state.players[i];
        if (!g_connected[i]) { p.connected=0; p.alive=0; continue; }
        p.x          = SX[i]; p.y = SY[i];
        p.health     = MAX_HEALTH;
        p.alive      = 1;
        p.connected  = 1;
        p.ready      = 0;
        p.has_weapon = 0;
        // 重置本局统计
        g_sessions[i].kills_this_game = 0;
        g_sessions[i].died_this_game  = false;
    }
    for (int i = 0; i < MAX_WEAPONS; i++) g_state.weapons[i].active = 0;
    g_state.game_started = true;
    g_state.game_over    = false;
    g_state.winner_id    = -1;
    snprintf(g_state.last_event, sizeof(g_state.last_event),
             "🎮 游戏开始！%d 名玩家", oc);
}

// ──────────────────────────────────────────────
// 判断地图格子是否被占（须持锁调用）
// ──────────────────────────────────────────────
static bool cell_busy_locked(int x, int y) {
    for (int i=0;i<MAX_PLAYERS;i++)
        if (g_connected[i]&&g_state.players[i].alive&&
            g_state.players[i].x==x&&g_state.players[i].y==y) return true;
    for (int i=0;i<MAX_WEAPONS;i++)
        if (g_state.weapons[i].active&&
            g_state.weapons[i].x==x&&g_state.weapons[i].y==y) return true;
    return false;
}

// ──────────────────────────────────────────────
// 在空格随机生成一个武器（须持锁调用）
// ──────────────────────────────────────────────
static void spawn_weapon_locked() {
    int slot = -1;
    for (int i=0;i<MAX_WEAPONS;i++) if(!g_state.weapons[i].active){slot=i;break;}
    if (slot<0) return;
    for (int t=0;t<40;t++) {
        int x = 2 + rand()%(MAP_W-4);
        int y = 2 + rand()%(MAP_H-4);
        if (!cell_busy_locked(x,y)) {
            g_state.weapons[slot] = {(int8_t)x,(int8_t)y,1};
            snprintf(g_state.last_event, sizeof(g_state.last_event),
                     "⚔  强力武器出现在 (%d,%d)！", x, y);
            return;
        }
    }
}

// ──────────────────────────────────────────────
// 检测并拾取武器（须持锁调用）
// ──────────────────────────────────────────────
static void check_pickup_locked(int pid) {
    auto& p = g_state.players[pid];
    for (auto& w : g_state.weapons) {
        if (w.active && w.x==p.x && w.y==p.y) {
            w.active     = 0;
            p.has_weapon = 1;
            snprintf(g_state.last_event, sizeof(g_state.last_event),
                     "⚡  %s 拾取武器！下次攻击 ×2", p.name);
            return;
        }
    }
}

// ──────────────────────────────────────────────
// 结算战绩并写入数据库（须持锁调用，在 game_over 后调用）
// ──────────────────────────────────────────────
static void save_stats_locked() {
    for (int i=0;i<MAX_PLAYERS;i++) {
        if (!g_connected[i] || g_sessions[i].username.empty()) continue;
        bool is_winner = (g_state.winner_id == i);
        g_db.update_stats(g_sessions[i].username,
                          is_winner,
                          g_sessions[i].kills_this_game,
                          g_sessions[i].died_this_game);
    }
    std::cout << "[服务器] 战绩已写入数据库\n";
}

// ──────────────────────────────────────────────
// 处理玩家操作（须持锁调用）
// ──────────────────────────────────────────────
static void apply_action_locked(int pid, ActionType action) {
    if (!g_state.game_started || g_state.game_over) return;
    auto& me = g_state.players[pid];
    if (!me.alive) return;

    switch (action) {
        case ActionType::MOVE_UP:    if(me.y>0)        me.y--; break;
        case ActionType::MOVE_DOWN:  if(me.y<MAP_H-1)  me.y++; break;
        case ActionType::MOVE_LEFT:  if(me.x>0)        me.x--; break;
        case ActionType::MOVE_RIGHT: if(me.x<MAP_W-1)  me.x++; break;
        case ActionType::ATTACK: {
            // 找最近存活敌人
            int best_d=-1, best_j=-1;
            for (int j=0;j<MAX_PLAYERS;j++) {
                if (j==pid||!g_connected[j]||!g_state.players[j].alive) continue;
                int d=abs((int)me.x-g_state.players[j].x)+
                      abs((int)me.y-g_state.players[j].y);
                if (best_j<0||d<best_d){best_d=d;best_j=j;}
            }
            if (best_j<0) {
                snprintf(g_state.last_event,sizeof(g_state.last_event),
                         "%s 攻击！但场上无存活对手",me.name);
                break;
            }
            if (best_d>ATTACK_RANGE) {
                snprintf(g_state.last_event,sizeof(g_state.last_event),
                         "%s 攻击 %s，但距离太远（%d格）",
                         me.name,g_state.players[best_j].name,best_d);
                break;
            }
            int dmg = me.has_weapon ? POWER_DAMAGE : ATTACK_DAMAGE;
            bool used_weapon = me.has_weapon;
            if (used_weapon) me.has_weapon = 0;

            auto& tgt = g_state.players[best_j];
            tgt.health -= (int16_t)dmg;

            if (used_weapon)
                snprintf(g_state.last_event,sizeof(g_state.last_event),
                         "⚡ %s 强力攻击 %s！-%d HP",me.name,tgt.name,dmg);
            else
                snprintf(g_state.last_event,sizeof(g_state.last_event),
                         "%s 攻击 %s，-%d HP",me.name,tgt.name,dmg);

            if (tgt.health<=0) {
                tgt.health=0; tgt.alive=0;
                g_sessions[pid].kills_this_game++;
                g_sessions[best_j].died_this_game = true;
                snprintf(g_state.last_event,sizeof(g_state.last_event),
                         "💀 %s 击败了 %s！",me.name,tgt.name);

                // 检查存活人数
                int alive=0;
                for(int j=0;j<MAX_PLAYERS;j++)
                    if(g_connected[j]&&g_state.players[j].alive) alive++;
                if(alive<=1) {
                    g_state.game_over = true;
                    g_state.winner_id = pid;
                    snprintf(g_state.last_event,sizeof(g_state.last_event),
                             "🏆 %s 是最后的幸存者！游戏结束",me.name);
                    // 保存战绩（不持 db 的锁，database 内部有自己的锁）
                    save_stats_locked();
                }
            }
            return; // 攻击不检测拾取
        }
    }
    // 移动后检测拾取
    if (action != ActionType::ATTACK)
        check_pickup_locked(pid);
}

// ──────────────────────────────────────────────
// 每名玩家的 I/O 处理线程
// ──────────────────────────────────────────────
static void client_thread(int pid) {
    int fd = g_client_fd[pid];
    std::cout << "[服务器] 槽" << pid << " 新连接，fd=" << fd << "\n";

    // ══════════════════════════════════════════
    // 阶段一：认证（REGISTER 或 LOGIN）
    // ══════════════════════════════════════════
    {
        PacketHeader hdr{};
        if (!recv_all(fd, &hdr, HEADER_SIZE)) goto cleanup;

        if (hdr.type != PacketType::REGISTER && hdr.type != PacketType::LOGIN) {
            std::cerr << "[服务器] 槽" << pid << " 未发送认证包\n";
            goto cleanup;
        }

        AuthPayload ap{};
        if (hdr.length>0) recv_all(fd, &ap, std::min((int)hdr.length,(int)sizeof(ap)));

        std::string username(ap.username);
        std::string password(ap.password);
        std::string msg;
        bool ok = false;

        if (hdr.type == PacketType::REGISTER) {
            ok = g_db.register_user(username, password, msg);
            std::cout << "[服务器] 注册 " << username << ": " << msg << "\n";
        } else {
            ok = g_db.login(username, password, msg);
            std::cout << "[服务器] 登录 " << username << ": " << msg << "\n";
        }

        // 发送认证结果
        AuthResultPayload ar{};
        ar.success = ok ? 1 : 0;
        snprintf(ar.message,   sizeof(ar.message),   "%s", msg.c_str());
        snprintf(ar.username,  sizeof(ar.username),  "%s", username.c_str());
        send_packet(fd, PacketType::AUTH_RESULT, &ar, sizeof(ar));

        if (!ok) goto cleanup;

        g_sessions[pid].username      = username;
        g_sessions[pid].authenticated = true;
    }

    // ══════════════════════════════════════════
    // 阶段二：等待 JOIN（进入房间）
    // ══════════════════════════════════════════
    {
        PacketHeader hdr{};
        // 等待 JOIN 或 STATS_REQUEST（允许认证后但未进房间时查询战绩）
        while (g_running && g_connected[pid]) {
            if (!recv_all(fd, &hdr, HEADER_SIZE)) goto cleanup;

            if (hdr.type == PacketType::JOIN) {
                // 把账号名截短作为游戏内昵称（最长 15 字节）
                std::lock_guard<std::mutex> lk(g_state_mutex);
                strncpy(g_state.players[pid].name,
                        g_sessions[pid].username.c_str(), 15);
                g_state.players[pid].name[15]   = '\0';
                g_state.players[pid].connected  = 1;
                g_state.players[pid].alive      = 0;
                g_state.players[pid].ready      = 0;
                g_state.players[pid].has_weapon = 0;
                int cnt = online_count_locked();
                snprintf(g_state.last_event, sizeof(g_state.last_event),
                         "%s 进入房间（%d/%d人）按 R 准备",
                         g_state.players[pid].name, cnt, MAX_PLAYERS);
                std::cout << "[服务器] " << g_state.players[pid].name
                          << " 进入房间，当前 " << cnt << " 人\n";
                broadcast_state_locked();
                break;  // 进入游戏主循环
            }

            if (hdr.type == PacketType::STATS_REQUEST) {
                StatsRequestPayload srp{};
                if (hdr.length>0) recv_all(fd, &srp, std::min((int)hdr.length,(int)sizeof(srp)));
                std::string target = srp.username[0] ? std::string(srp.username)
                                                     : g_sessions[pid].username;
                StatsRecord rec{};
                bool found = g_db.get_stats(target, rec);
                StatsResponsePayload resp{};
                snprintf(resp.username, sizeof(resp.username), "%s", target.c_str());
                resp.found = found ? 1 : 0;
                if (found) {
                    resp.games  = rec.games;
                    resp.wins   = rec.wins;
                    resp.losses = rec.losses;
                    resp.kills  = rec.kills;
                    resp.deaths = rec.deaths;
                    snprintf(resp.last_played, sizeof(resp.last_played),
                             "%s", rec.last_played.c_str());
                }
                send_packet(fd, PacketType::STATS_RESPONSE, &resp, sizeof(resp));
                continue;
            }

            // 跳过其余包
            if (hdr.length>0) { char skip[512]{}; recv_all(fd,skip,std::min((int)hdr.length,512)); }
        }
    }

    // ══════════════════════════════════════════
    // 阶段三：游戏主循环
    // ══════════════════════════════════════════
    while (g_running && g_connected[pid]) {
        PacketHeader hdr{};
        if (!recv_all(fd, &hdr, HEADER_SIZE)) {
            std::cerr << "[服务器] 槽" << pid << " 断开\n";
            break;
        }

        switch (hdr.type) {
            case PacketType::READY: {
                std::lock_guard<std::mutex> lk(g_state_mutex);
                if (!g_state.game_started) {
                    g_state.players[pid].ready = 1;
                    int rc = ready_count_locked();
                    int oc = online_count_locked();
                    snprintf(g_state.last_event,sizeof(g_state.last_event),
                             "%s 已准备 (%d/%d)",g_state.players[pid].name,rc,oc);
                    std::cout << "[服务器] " << g_state.players[pid].name
                              << " 准备，" << rc << "/" << oc << "\n";
                    if (rc==oc && oc>=2) {
                        reset_game_locked();
                        std::cout << "[服务器] 游戏开始！" << oc << " 名玩家\n";
                    }
                    broadcast_state_locked();
                }
                break;
            }

            case PacketType::ACTION: {
                ActionPayload ap{};
                if (hdr.length>0) recv_all(fd,&ap,std::min((int)hdr.length,(int)sizeof(ap)));
                std::lock_guard<std::mutex> lk(g_state_mutex);
                apply_action_locked(pid, ap.action);
                broadcast_state_locked();
                break;
            }

            case PacketType::STATS_REQUEST: {
                StatsRequestPayload srp{};
                if (hdr.length>0) recv_all(fd,&srp,std::min((int)hdr.length,(int)sizeof(srp)));
                std::string target = srp.username[0] ? std::string(srp.username)
                                                     : g_sessions[pid].username;
                StatsRecord rec{};
                bool found = g_db.get_stats(target, rec);
                StatsResponsePayload resp{};
                snprintf(resp.username,sizeof(resp.username),"%s",target.c_str());
                resp.found = found ? 1 : 0;
                if (found) {
                    resp.games=rec.games; resp.wins=rec.wins;
                    resp.losses=rec.losses; resp.kills=rec.kills;
                    resp.deaths=rec.deaths;
                    snprintf(resp.last_played,sizeof(resp.last_played),
                             "%s",rec.last_played.c_str());
                }
                send_packet(fd, PacketType::STATS_RESPONSE, &resp, sizeof(resp));
                break;
            }

            case PacketType::HEARTBEAT:
                g_last_heartbeat[pid] = time(nullptr);
                send_packet(fd, PacketType::HEARTBEAT_ACK);
                break;

            case PacketType::HEARTBEAT_ACK:
                g_last_heartbeat[pid] = time(nullptr);
                break;

            case PacketType::DISCONNECT:
                std::cout << "[服务器] 槽" << pid << " 主动断开\n";
                goto cleanup;

            default:
                if (hdr.length>0){char s[512]{};recv_all(fd,s,std::min((int)hdr.length,512));}
                break;
        }
    }

cleanup:
    g_connected[pid] = false;
    close(fd);
    g_client_fd[pid] = -1;

    {
        std::lock_guard<std::mutex> lk(g_state_mutex);
        g_state.players[pid].connected = 0;
        g_state.players[pid].alive     = 0;

        if (g_state.game_started && !g_state.game_over) {
            snprintf(g_state.last_event,sizeof(g_state.last_event),
                     "%s 断线了",g_state.players[pid].name);
            // 检查是否只剩一人存活
            int alive=0, last=-1;
            for(int j=0;j<MAX_PLAYERS;j++)
                if(g_connected[j]&&g_state.players[j].alive){alive++;last=j;}
            if(alive<=1&&last>=0){
                g_state.game_over=true; g_state.winner_id=last;
                snprintf(g_state.last_event,sizeof(g_state.last_event),
                         "🏆 %s 是最后的幸存者！",g_state.players[last].name);
                save_stats_locked();
            }
            // 所有人离开 → 重置
            if (online_count_locked()==0) {
                g_state.game_started=false; g_state.game_over=false;
            }
        }
        broadcast_state_locked();
    }
    std::cout << "[服务器] 槽" << pid << " 线程退出\n";
}

// ──────────────────────────────────────────────
// 武器刷新线程
// ──────────────────────────────────────────────
static void weapon_thread() {
    while (g_running) {
        std::this_thread::sleep_for(std::chrono::seconds(WEAPON_INTERVAL));
        if (!g_running) break;
        std::lock_guard<std::mutex> lk(g_state_mutex);
        if (g_state.game_started && !g_state.game_over) {
            spawn_weapon_locked();
            broadcast_state_locked();
        }
    }
}

// ──────────────────────────────────────────────
// 心跳监控线程
// ──────────────────────────────────────────────
static void heartbeat_watch() {
    while (g_running) {
        std::this_thread::sleep_for(std::chrono::seconds(2));
        time_t now = time(nullptr);
        for (int i=0;i<MAX_PLAYERS;i++) {
            if (!g_connected[i]) continue;
            if (now-g_last_heartbeat[i] > HEARTBEAT_TIMEOUT) {
                std::cerr << "[服务器] 槽" << i << " 心跳超时，踢出\n";
                shutdown(g_client_fd[i], SHUT_RDWR);
                g_connected[i] = false;
            }
        }
    }
}

static void sig_handler(int) {
    g_running = false;
    if (g_server_fd>=0) close(g_server_fd);
    std::cout << "\n[服务器] 正在关闭…\n";
}

int main(int argc, char* argv[]) {
    srand((unsigned)time(nullptr));
    int port = argc>=2 ? atoi(argv[1]) : DEFAULT_PORT;

    signal(SIGINT,  sig_handler);
    signal(SIGTERM, sig_handler);
    signal(SIGPIPE, SIG_IGN);

    for (int i=0;i<MAX_PLAYERS;i++) {
        g_connected[i]      = false;
        g_client_fd[i]      = -1;
        g_last_heartbeat[i] = time(nullptr);
        memset(&g_state.players[i], 0, sizeof(PlayerState));
    }
    for (int i=0;i<MAX_WEAPONS;i++) g_state.weapons[i].active=0;
    g_state.game_started=false; g_state.game_over=false; g_state.winner_id=-1;
    snprintf(g_state.last_event,sizeof(g_state.last_event),"服务器已启动");

    g_server_fd = socket(AF_INET, SOCK_STREAM, 0);
    if (g_server_fd<0){perror("socket");return 1;}
    int opt=1;
    setsockopt(g_server_fd, SOL_SOCKET, SO_REUSEADDR, &opt, sizeof(opt));
    set_nodelay(g_server_fd);

    sockaddr_in addr{};
    addr.sin_family      = AF_INET;
    addr.sin_addr.s_addr = INADDR_ANY;
    addr.sin_port        = htons(port);
    if (bind(g_server_fd,(sockaddr*)&addr,sizeof(addr))<0){perror("bind");return 1;}
    if (listen(g_server_fd,MAX_PLAYERS)<0){perror("listen");return 1;}

    std::cout << "╔══════════════════════════════════════════╗\n";
    std::cout << "║  多人对战服务器 v3.0  最多 " << MAX_PLAYERS << " 人        ║\n";
    std::cout << "╚══════════════════════════════════════════╝\n";
    std::cout << "[服务器] 监听 0.0.0.0:" << port
              << "\n[服务器] 数据目录：./data/\n\n";

    std::thread(weapon_thread).detach();
    std::thread(heartbeat_watch).detach();

    while (g_running) {
        int slot=-1;
        for (int i=0;i<MAX_PLAYERS;i++)
            if (!g_connected[i]&&g_client_fd[i]<0){slot=i;break;}
        if (slot<0){
            std::this_thread::sleep_for(std::chrono::milliseconds(200));
            continue;
        }

        sockaddr_in cli{};
        socklen_t clen=sizeof(cli);
        int cfd = accept(g_server_fd,(sockaddr*)&cli,&clen);
        if (cfd<0){if(g_running)perror("accept");continue;}

        set_nodelay(cfd);   // ← 每个客户端连接都要设置！
        std::cout << "[服务器] 新连接 " << inet_ntoa(cli.sin_addr)
                  << ":" << ntohs(cli.sin_port) << " → 槽" << slot << "\n";

        // 如果上一局已结束，重置 ready 状态
        {
            std::lock_guard<std::mutex> lk(g_state_mutex);
            if (g_state.game_over) {
                for(int i=0;i<MAX_PLAYERS;i++){
                    g_state.players[i].ready=0;
                    g_state.players[i].alive=0;
                }
                for(int i=0;i<MAX_WEAPONS;i++) g_state.weapons[i].active=0;
                g_state.game_started=false;
                g_state.game_over=false;
                g_state.winner_id=-1;
            }
        }

        g_client_fd[slot]      = cfd;
        g_connected[slot]      = true;
        g_last_heartbeat[slot] = time(nullptr);
        std::thread(client_thread, slot).detach();
    }

    close(g_server_fd);
    return 0;
}