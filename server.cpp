// ============================================================
// server.cpp  —  多人对战游戏服务器 v3.1
//
// 架构：两层分离
//   连接层  MAX_CONN=20  —  每个 TCP 连接占一个 conn_slot
//           auth/lobby 阶段在此停留，心跳在此维护
//   游戏层  MAX_PLAYERS=5 — 只有 JOIN 后才占用 game_slot
//
// Bug 修复：
//   1. 心跳超时：auth/lobby 阶段正确处理 HEARTBEAT 包
//   2. 第 6 个玩家无响应：accept() 始终执行，不再卡在槽位检查
//   3. 出生点：动态按 MAP_W/MAP_H 和人数计算，永不越界
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

#include <sys/socket.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <arpa/inet.h>
#include <unistd.h>

// ──────────────────────────────────────────────
// 容量常量
// ──────────────────────────────────────────────
constexpr int MAX_CONN    = 20;   // 最多同时 20 个 TCP 连接（含认证中/大厅中的玩家）
constexpr int DEFAULT_PORT = 9000;

// ──────────────────────────────────────────────
// 连接层：每个 TCP 连接的信息（MAX_CONN 个）
// ──────────────────────────────────────────────
struct ConnInfo {
    int         fd          = -1;
    bool        active      = false;
    bool        authed      = false;   // 已通过 REGISTER/LOGIN
    std::string username;
    int         game_slot   = -1;      // -1 = 未进入游戏；0..4 = 游戏槽编号
    time_t      last_hb     = 0;       // 最后心跳时间
    int         kills_game  = 0;       // 本局击杀（结算用）
    bool        died_game   = false;   // 本局是否死亡
};

// 连接层全局数组（须持 g_conn_mutex 读写，除 active/fd 外的原子操作）
static ConnInfo         g_conns[MAX_CONN];
static std::mutex       g_conn_mutex;           // 保护 g_conns 整体

// ──────────────────────────────────────────────
// 游戏层：5 个游戏槽（须持 g_state_mutex）
// ──────────────────────────────────────────────
struct ServerState {
    PlayerState players[MAX_PLAYERS];
    WeaponItem  weapons[MAX_WEAPONS];
    bool        game_started;
    bool        game_over;
    int         winner_conn;   // 胜利者的 conn_slot（-1=无）
    char        last_event[64];
};

static std::mutex  g_state_mutex;
static ServerState g_state;
static int         g_slot_conn[MAX_PLAYERS];   // game_slot → conn_slot (-1=空)

// ──────────────────────────────────────────────
// 其他全局
// ──────────────────────────────────────────────
static Database          g_db("data");
static std::atomic<bool> g_running{true};
static int               g_server_fd = -1;

// ──────────────────────────────────────────────
// TCP_NODELAY
// ──────────────────────────────────────────────
static void set_nodelay(int fd) {
    int f = 1;
    setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &f, sizeof(f));
}

// ──────────────────────────────────────────────
// 通过 conn_slot 找到对应的 game_slot（须持 g_state_mutex）
// ──────────────────────────────────────────────
static int conn_to_game_locked(int cid) {
    for (int g = 0; g < MAX_PLAYERS; g++)
        if (g_slot_conn[g] == cid) return g;
    return -1;
}

// ──────────────────────────────────────────────
// 统计游戏中在线/准备人数（须持 g_state_mutex）
// ──────────────────────────────────────────────
static int game_online_locked() {
    int n = 0;
    for (int g = 0; g < MAX_PLAYERS; g++)
        if (g_slot_conn[g] >= 0) n++;
    return n;
}
static int game_ready_locked() {
    int n = 0;
    for (int g = 0; g < MAX_PLAYERS; g++)
        if (g_slot_conn[g] >= 0 && g_state.players[g].ready) n++;
    return n;
}

// ──────────────────────────────────────────────
// 广播游戏状态给所有在游戏中的连接（须持 g_state_mutex）
// ──────────────────────────────────────────────
static void broadcast_state_locked() {
    int pc = game_online_locked();
    int rc = game_ready_locked();
    for (int g = 0; g < MAX_PLAYERS; g++) {
        int cid = g_slot_conn[g];
        if (cid < 0) continue;
        int fd = g_conns[cid].fd;
        if (fd < 0) continue;

        StateUpdatePayload pkt{};
        for (int j = 0; j < MAX_PLAYERS; j++) pkt.players[j] = g_state.players[j];
        for (int j = 0; j < MAX_WEAPONS; j++) pkt.weapons[j] = g_state.weapons[j];
        pkt.your_id      = (uint8_t)g;
        pkt.player_count = (uint8_t)pc;
        pkt.ready_count  = (uint8_t)rc;
        pkt.game_started = g_state.game_started ? 1 : 0;
        pkt.game_over    = g_state.game_over    ? 1 : 0;
        pkt.winner_id    = (g_state.winner_conn >= 0)
                           ? (uint8_t)conn_to_game_locked(g_state.winner_conn)
                           : 0xFF;
        snprintf(pkt.last_event, sizeof(pkt.last_event), "%s", g_state.last_event);
        send_packet(fd, PacketType::STATE_UPDATE, &pkt, sizeof(pkt));
    }
}

// ──────────────────────────────────────────────
// 动态计算出生点（须持 g_state_mutex）
// 根据实际在线人数在地图内均匀分布，保证永不越界
// ──────────────────────────────────────────────
static void reset_game_locked() {
    // 收集在线游戏槽列表（按槽号排序）
    int online_gs[MAX_PLAYERS]; int oc = 0;
    for (int g = 0; g < MAX_PLAYERS; g++)
        if (g_slot_conn[g] >= 0) online_gs[oc++] = g;

    // 有效坐标边界（留 2 格边距）
    const int X0 = 2,        X1 = MAP_W - 3;  // [2, MAP_W-3]
    const int Y0 = 2,        Y1 = MAP_H - 3;  // [2, MAP_H-3]
    const int CX = (X0+X1)/2, CY = (Y0+Y1)/2;

    // 5 个候选出生点：四角 + 中心（基于动态边界，永不越界）
    struct Pos { int x, y; } const SP[5] = {
        {X0, Y0},   // 0: 左上
        {X1, Y0},   // 1: 右上
        {X0, Y1},   // 2: 左下
        {X1, Y1},   // 3: 右下
        {CX, CY},   // 4: 中心
    };

    // 按人数从候选点中选最分散的子集
    // order[oc-1][rank] = SP 下标
    const int ORDER[5][5] = {
        {4, 0, 0, 0, 0},   // 1人：中心
        {0, 3, 0, 0, 0},   // 2人：左上+右下（对角最远）
        {0, 3, 4, 0, 0},   // 3人：左上+右下+中心
        {0, 1, 2, 3, 0},   // 4人：四角
        {0, 1, 2, 3, 4},   // 5人：四角+中心
    };

    for (int rank = 0; rank < oc; rank++) {
        int gs  = online_gs[rank];
        int cid = g_slot_conn[gs];
        int idx = ORDER[oc-1][rank];
        auto& p = g_state.players[gs];
        p.x = (int8_t)SP[idx].x;
        p.y = (int8_t)SP[idx].y;
        p.health     = MAX_HEALTH;
        p.alive      = 1;
        p.connected  = 1;
        p.ready      = 0;
        p.has_weapon = 0;
        g_conns[cid].kills_game = 0;
        g_conns[cid].died_game  = false;
    }
    // 清空离线槽
    for (int g = 0; g < MAX_PLAYERS; g++)
        if (g_slot_conn[g] < 0) { g_state.players[g].connected=0; g_state.players[g].alive=0; }
    // 清空武器
    for (int i = 0; i < MAX_WEAPONS; i++) g_state.weapons[i].active = 0;

    g_state.game_started = true;
    g_state.game_over    = false;
    g_state.winner_conn  = -1;
    snprintf(g_state.last_event, sizeof(g_state.last_event),
             "🎮 游戏开始！%d 名玩家", oc);

    std::cout << "[服务器] 出生点（" << oc << "人）：";
    for (int r=0;r<oc;r++) {
        int gs=online_gs[r];
        std::cout << g_state.players[gs].name
                  << "(" << (int)g_state.players[gs].x
                  << "," << (int)g_state.players[gs].y << ") ";
    }
    std::cout << "\n";
}

// ──────────────────────────────────────────────
// 检测并拾取武器（须持 g_state_mutex）
// ──────────────────────────────────────────────
static void check_pickup_locked(int gs) {
    auto& p = g_state.players[gs];
    for (auto& w : g_state.weapons) {
        if (w.active && w.x==p.x && w.y==p.y) {
            w.active=0; p.has_weapon=1;
            snprintf(g_state.last_event, sizeof(g_state.last_event),
                     "⚡ %s 拾取武器！下次攻击 ×2", p.name);
            return;
        }
    }
}

// ──────────────────────────────────────────────
// 在空格随机生成一个武器（须持 g_state_mutex）
// ──────────────────────────────────────────────
static void spawn_weapon_locked() {
    int slot = -1;
    for (int i=0;i<MAX_WEAPONS;i++) if(!g_state.weapons[i].active){slot=i;break;}
    if (slot<0) return;
    for (int t=0;t<50;t++) {
        int x = 2+rand()%(MAP_W-4), y = 2+rand()%(MAP_H-4);
        bool busy=false;
        for (int g=0;g<MAX_PLAYERS;g++)
            if(g_slot_conn[g]>=0&&g_state.players[g].alive&&
               g_state.players[g].x==x&&g_state.players[g].y==y){busy=true;break;}
        for (int i=0;i<MAX_WEAPONS&&!busy;i++)
            if(g_state.weapons[i].active&&g_state.weapons[i].x==x&&g_state.weapons[i].y==y)
                busy=true;
        if (!busy) {
            g_state.weapons[slot]={(int8_t)x,(int8_t)y,1};
            snprintf(g_state.last_event,sizeof(g_state.last_event),
                     "⚔  强力武器出现在 (%d,%d)！",x,y);
            return;
        }
    }
}

// ──────────────────────────────────────────────
// 结算战绩（须持 g_state_mutex；db 有自己的锁）
// ──────────────────────────────────────────────
static void save_stats_locked() {
    for (int g=0;g<MAX_PLAYERS;g++) {
        int cid=g_slot_conn[g];
        if (cid<0||g_conns[cid].username.empty()) continue;
        bool win=(g_state.winner_conn==cid);
        g_db.update_stats(g_conns[cid].username, win,
                          g_conns[cid].kills_game,
                          g_conns[cid].died_game);
    }
    std::cout << "[服务器] 战绩已写入数据库\n";
}

// ──────────────────────────────────────────────
// 处理玩家操作（须持 g_state_mutex）
// ──────────────────────────────────────────────
static void apply_action_locked(int cid, ActionType action) {
    if (!g_state.game_started||g_state.game_over) return;
    int gs=conn_to_game_locked(cid);
    if (gs<0) return;
    auto& me=g_state.players[gs];
    if (!me.alive) return;

    switch(action) {
        case ActionType::MOVE_UP:    if(me.y>0)       me.y--; break;
        case ActionType::MOVE_DOWN:  if(me.y<MAP_H-1) me.y++; break;
        case ActionType::MOVE_LEFT:  if(me.x>0)       me.x--; break;
        case ActionType::MOVE_RIGHT: if(me.x<MAP_W-1) me.x++; break;
        case ActionType::ATTACK: {
            int bd=-1,bj=-1;
            for (int g=0;g<MAX_PLAYERS;g++) {
                if(g==gs||g_slot_conn[g]<0||!g_state.players[g].alive) continue;
                int d=abs((int)me.x-g_state.players[g].x)+abs((int)me.y-g_state.players[g].y);
                if(bj<0||d<bd){bd=d;bj=g;}
            }
            if (bj<0) {
                snprintf(g_state.last_event,sizeof(g_state.last_event),
                         "%s 攻击！场上无存活对手",me.name); break;
            }
            if (bd>ATTACK_RANGE) {
                snprintf(g_state.last_event,sizeof(g_state.last_event),
                         "%s 攻击 %s，距离太远（%d格）",me.name,g_state.players[bj].name,bd); break;
            }
            int dmg=me.has_weapon?POWER_DAMAGE:ATTACK_DAMAGE;
            bool pw=me.has_weapon; if(pw) me.has_weapon=0;
            auto& tgt=g_state.players[bj];
            tgt.health-=(int16_t)dmg;
            if(pw)
                snprintf(g_state.last_event,sizeof(g_state.last_event),
                         "⚡ %s 强力攻击 %s！-%d HP",me.name,tgt.name,dmg);
            else
                snprintf(g_state.last_event,sizeof(g_state.last_event),
                         "%s 攻击 %s，-%d HP",me.name,tgt.name,dmg);
            if (tgt.health<=0) {
                tgt.health=0; tgt.alive=0;
                g_conns[cid].kills_game++;
                int tcid=g_slot_conn[bj]; if(tcid>=0) g_conns[tcid].died_game=true;
                snprintf(g_state.last_event,sizeof(g_state.last_event),
                         "💀 %s 击败了 %s！",me.name,tgt.name);
                int alive=0;
                for(int g=0;g<MAX_PLAYERS;g++)
                    if(g_slot_conn[g]>=0&&g_state.players[g].alive) alive++;
                if(alive<=1){
                    g_state.game_over=true; g_state.winner_conn=cid;
                    snprintf(g_state.last_event,sizeof(g_state.last_event),
                             "🏆 %s 是最后的幸存者！游戏结束",me.name);
                    save_stats_locked();
                }
            }
            return; // 攻击不检测拾取
        }
    }
    if(action!=ActionType::ATTACK) check_pickup_locked(gs);
}

// ──────────────────────────────────────────────
// 将玩家从游戏槽移除（须持 g_state_mutex）
// ──────────────────────────────────────────────
static void remove_from_game_locked(int cid) {
    for (int g=0;g<MAX_PLAYERS;g++) {
        if (g_slot_conn[g]==cid) {
            g_state.players[g].connected=0;
            g_state.players[g].alive=0;
            g_slot_conn[g]=-1;
            if (cid>=0) g_conns[cid].game_slot=-1;

            // 如果游戏进行中，检查是否只剩一名玩家
            if (g_state.game_started&&!g_state.game_over) {
                int alive=0,last=-1;
                for(int j=0;j<MAX_PLAYERS;j++)
                    if(g_slot_conn[j]>=0&&g_state.players[j].alive){alive++;last=j;}
                if(alive<=1&&last>=0){
                    g_state.game_over=true;
                    g_state.winner_conn=g_slot_conn[last];
                    snprintf(g_state.last_event,sizeof(g_state.last_event),
                             "🏆 %s 是最后的幸存者！",g_state.players[last].name);
                    save_stats_locked();
                }
                if(game_online_locked()==0){
                    g_state.game_started=false;
                    g_state.game_over=false;
                }
            }
            return;
        }
    }
}

// ──────────────────────────────────────────────
// 每条 TCP 连接的 I/O 处理线程
// ──────────────────────────────────────────────
static void conn_thread(int cid) {
    int fd = g_conns[cid].fd;
    std::cout << "[服务器] 连接" << cid << " 已建立 fd=" << fd << "\n";

    // ── 辅助：更新心跳时间戳（任意阶段均可调用，不需要持锁）──
    auto touch_hb = [&]() {
        g_conns[cid].last_hb = time(nullptr);
    };

    // ══════════════════════════════════════════
    // 阶段一：认证循环（最多 10 次尝试）
    // 关键修复：正确处理 HEARTBEAT 包，不关闭连接
    // ══════════════════════════════════════════
    for (int attempt=0; attempt<10; ) {
        PacketHeader hdr{};
        if (!recv_all(fd,&hdr,HEADER_SIZE)) goto cleanup;

        if (hdr.type==PacketType::DISCONNECT) goto cleanup;

        // ← 修复1：auth 阶段必须响应心跳，否则 8 秒超时被踢
        if (hdr.type==PacketType::HEARTBEAT) {
            touch_hb();
            send_packet(fd, PacketType::HEARTBEAT_ACK);
            continue;
        }
        if (hdr.type==PacketType::HEARTBEAT_ACK) {
            touch_hb();
            continue;
        }

        if (hdr.type!=PacketType::REGISTER && hdr.type!=PacketType::LOGIN) {
            if(hdr.length>0){char s[256]{};recv_all(fd,s,std::min((int)hdr.length,256));}
            continue;
        }
        ++attempt;

        AuthPayload ap{};
        if(hdr.length>0) recv_all(fd,&ap,std::min((int)hdr.length,(int)sizeof(ap)));
        ap.username[sizeof(ap.username)-1]='\0';
        ap.password[sizeof(ap.password)-1]='\0';
        std::string user(ap.username), pass(ap.password), msg;
        bool ok = (hdr.type==PacketType::REGISTER)
                  ? g_db.register_user(user,pass,msg)
                  : g_db.login(user,pass,msg);

        std::cout << "[服务器] " << (hdr.type==PacketType::REGISTER?"注册 ":"登录 ")
                  << user << ": " << msg << "\n";

        AuthResultPayload ar{};
        ar.success=ok?1:0;
        snprintf(ar.message,sizeof(ar.message),"%s",msg.c_str());
        snprintf(ar.username,sizeof(ar.username),"%s",user.c_str());
        if(send_packet(fd,PacketType::AUTH_RESULT,&ar,sizeof(ar))<0) goto cleanup;

        if (ok) {
            g_conns[cid].authed   = true;
            g_conns[cid].username = user;
            break;  // 进入阶段二
        }
        // 失败 → 继续循环等重试
    }
    if (!g_conns[cid].authed) {
        std::cout << "[服务器] 连接" << cid << " 认证失败超过上限\n";
        goto cleanup;
    }

    // ══════════════════════════════════════════
    // 阶段二：大厅等待（JOIN / STATS_REQUEST / 心跳）
    // ══════════════════════════════════════════
    while (g_running && g_conns[cid].active) {
        PacketHeader hdr{};
        if (!recv_all(fd,&hdr,HEADER_SIZE)) goto cleanup;

        if (hdr.type==PacketType::DISCONNECT) goto cleanup;

        // ← 修复1：lobby 阶段也必须响应心跳
        if (hdr.type==PacketType::HEARTBEAT) {
            touch_hb();
            send_packet(fd, PacketType::HEARTBEAT_ACK);
            continue;
        }
        if (hdr.type==PacketType::HEARTBEAT_ACK) {
            touch_hb(); continue;
        }

        if (hdr.type==PacketType::JOIN) {
            // ← 修复2：在此处分配游戏槽，而不是在 accept 时
            int gs=-1;
            {
                std::lock_guard<std::mutex> lk(g_state_mutex);
                // 检查是否已有空闲游戏槽
                for(int g=0;g<MAX_PLAYERS;g++)
                    if(g_slot_conn[g]<0){gs=g;break;}

                if (gs<0) {
                    // 游戏已满，发消息告知
                    snprintf(g_state.last_event,sizeof(g_state.last_event),
                             "游戏已满（%d人），稍后再试",MAX_PLAYERS);
                    // 发一个临时 STATE_UPDATE 告知已满
                    StateUpdatePayload tmp{};
                    tmp.your_id=0xFF; tmp.player_count=MAX_PLAYERS;
                    snprintf(tmp.last_event,sizeof(tmp.last_event),"游戏房间已满，请稍后再试");
                    send_packet(fd,PacketType::STATE_UPDATE,&tmp,sizeof(tmp));
                    // 继续等待（不断开），下次有人离开可再 JOIN
                    continue;
                }

                // 分配游戏槽
                g_slot_conn[gs]       = cid;
                g_conns[cid].game_slot = gs;

                strncpy(g_state.players[gs].name,
                        g_conns[cid].username.c_str(),15);
                g_state.players[gs].name[15]   = '\0';
                g_state.players[gs].connected  = 1;
                g_state.players[gs].alive      = 0;
                g_state.players[gs].ready      = 0;
                g_state.players[gs].has_weapon = 0;
                g_state.players[gs].health     = 0;
                g_state.players[gs].x = g_state.players[gs].y = 0;

                int cnt=game_online_locked();
                snprintf(g_state.last_event,sizeof(g_state.last_event),
                         "%s 进入房间（%d/%d人）按 R 准备",
                         g_state.players[gs].name,cnt,MAX_PLAYERS);
                std::cout << "[服务器] " << g_state.players[gs].name
                          << " 进入房间（游戏槽" << gs << "，当前" << cnt << "人）\n";
                broadcast_state_locked();
            }
            break;  // 进入阶段三
        }

        if (hdr.type==PacketType::STATS_REQUEST) {
            StatsRequestPayload srp{};
            if(hdr.length>0) recv_all(fd,&srp,std::min((int)hdr.length,(int)sizeof(srp)));
            std::string target=srp.username[0]?std::string(srp.username):g_conns[cid].username;
            StatsRecord rec{};
            bool found=g_db.get_stats(target,rec);
            StatsResponsePayload resp{};
            snprintf(resp.username,sizeof(resp.username),"%s",target.c_str());
            resp.found=found?1:0;
            if(found){
                resp.games=rec.games;resp.wins=rec.wins;resp.losses=rec.losses;
                resp.kills=rec.kills;resp.deaths=rec.deaths;
                snprintf(resp.last_played,sizeof(resp.last_played),"%s",rec.last_played.c_str());
            }
            send_packet(fd,PacketType::STATS_RESPONSE,&resp,sizeof(resp));
            continue;
        }

        // 跳过其他包
        if(hdr.length>0){char s[512]{};recv_all(fd,s,std::min((int)hdr.length,512));}
    }

    // ══════════════════════════════════════════
    // 阶段三：游戏主循环
    // ══════════════════════════════════════════
    while (g_running && g_conns[cid].active) {
        PacketHeader hdr{};
        if (!recv_all(fd,&hdr,HEADER_SIZE)) {
            std::cerr << "[服务器] 连接" << cid << " 断开\n"; break;
        }

        switch(hdr.type) {
            case PacketType::READY: {
                std::lock_guard<std::mutex> lk(g_state_mutex);
                if (!g_state.game_started) {
                    int gs=conn_to_game_locked(cid);
                    if(gs>=0) {
                        g_state.players[gs].ready=1;
                        int rc=game_ready_locked(),oc=game_online_locked();
                        snprintf(g_state.last_event,sizeof(g_state.last_event),
                                 "%s 已准备 (%d/%d)",g_state.players[gs].name,rc,oc);
                        std::cout << "[服务器] " << g_state.players[gs].name
                                  << " 准备，" << rc << "/" << oc << "\n";
                        if(rc==oc&&oc>=2){
                            reset_game_locked();
                            std::cout << "[服务器] 游戏开始！" << oc << " 名玩家\n";
                        }
                        broadcast_state_locked();
                    }
                }
                break;
            }
            case PacketType::ACTION: {
                ActionPayload ap{};
                if(hdr.length>0) recv_all(fd,&ap,std::min((int)hdr.length,(int)sizeof(ap)));
                std::lock_guard<std::mutex> lk(g_state_mutex);
                apply_action_locked(cid,ap.action);
                broadcast_state_locked();
                break;
            }
            case PacketType::STATS_REQUEST: {
                StatsRequestPayload srp{};
                if(hdr.length>0) recv_all(fd,&srp,std::min((int)hdr.length,(int)sizeof(srp)));
                std::string target=srp.username[0]?std::string(srp.username):g_conns[cid].username;
                StatsRecord rec{};
                bool found=g_db.get_stats(target,rec);
                StatsResponsePayload resp{};
                snprintf(resp.username,sizeof(resp.username),"%s",target.c_str());
                resp.found=found?1:0;
                if(found){
                    resp.games=rec.games;resp.wins=rec.wins;resp.losses=rec.losses;
                    resp.kills=rec.kills;resp.deaths=rec.deaths;
                    snprintf(resp.last_played,sizeof(resp.last_played),"%s",rec.last_played.c_str());
                }
                send_packet(fd,PacketType::STATS_RESPONSE,&resp,sizeof(resp));
                break;
            }
            // ← 修复1：游戏中心跳也要正确处理
            case PacketType::HEARTBEAT:
                touch_hb();
                send_packet(fd,PacketType::HEARTBEAT_ACK);
                break;
            case PacketType::HEARTBEAT_ACK:
                touch_hb();
                break;
            case PacketType::DISCONNECT:
                std::cout << "[服务器] 连接" << cid << " 主动断开\n";
                goto cleanup;
            default:
                if(hdr.length>0){char s[512]{};recv_all(fd,s,std::min((int)hdr.length,512));}
                break;
        }
    }

cleanup:
    // 从游戏中移除并广播
    {
        std::lock_guard<std::mutex> lk(g_state_mutex);
        int gs=conn_to_game_locked(cid);
        if(gs>=0) {
            snprintf(g_state.last_event,sizeof(g_state.last_event),
                     "%s 断线了",g_state.players[gs].name);
            remove_from_game_locked(cid);
            broadcast_state_locked();
        }
    }
    // 释放连接槽
    close(fd);
    {
        std::lock_guard<std::mutex> lk(g_conn_mutex);
        g_conns[cid].fd        = -1;
        g_conns[cid].active    = false;
        g_conns[cid].authed    = false;
        g_conns[cid].username  = "";
        g_conns[cid].game_slot = -1;
    }
    std::cout << "[服务器] 连接" << cid << " 线程退出\n";
}

// ──────────────────────────────────────────────
// 武器刷新线程
// ──────────────────────────────────────────────
static void weapon_thread() {
    while (g_running) {
        std::this_thread::sleep_for(std::chrono::seconds(WEAPON_INTERVAL));
        if (!g_running) break;
        std::lock_guard<std::mutex> lk(g_state_mutex);
        if (g_state.game_started&&!g_state.game_over) {
            spawn_weapon_locked();
            broadcast_state_locked();
        }
    }
}

// ──────────────────────────────────────────────
// 心跳监控线程（监控所有 MAX_CONN 个连接）
// ──────────────────────────────────────────────
static void hb_watch_thread() {
    while (g_running) {
        std::this_thread::sleep_for(std::chrono::seconds(2));
        time_t now=time(nullptr);
        for (int i=0;i<MAX_CONN;i++) {
            // 快速读 active（不持锁，可能有 TOCTOU 但无害）
            if (!g_conns[i].active||g_conns[i].fd<0) continue;
            if (now-g_conns[i].last_hb > HEARTBEAT_TIMEOUT) {
                std::cerr << "[服务器] 连接" << i << " 心跳超时，踢出\n";
                shutdown(g_conns[i].fd, SHUT_RDWR);
                // conn_thread 会在 recv_all 返回 false 后进行清理
            }
        }
    }
}

static void sig_handler(int) {
    g_running=false;
    if(g_server_fd>=0) close(g_server_fd);
    std::cout << "\n[服务器] 正在关闭…\n";
}

// ──────────────────────────────────────────────
// main
// ──────────────────────────────────────────────
int main(int argc, char* argv[]) {
    srand((unsigned)time(nullptr));
    int port = argc>=2 ? atoi(argv[1]) : DEFAULT_PORT;

    signal(SIGINT,  sig_handler);
    signal(SIGTERM, sig_handler);
    signal(SIGPIPE, SIG_IGN);

    // 初始化连接层
    for (int i=0;i<MAX_CONN;i++) {
        g_conns[i].fd         = -1;
        g_conns[i].active     = false;
        g_conns[i].last_hb    = time(nullptr);
        g_conns[i].game_slot  = -1;
    }
    // 初始化游戏层
    for (int g=0;g<MAX_PLAYERS;g++) {
        g_slot_conn[g]=  -1;
        memset(&g_state.players[g],0,sizeof(PlayerState));
    }
    for (int i=0;i<MAX_WEAPONS;i++) g_state.weapons[i].active=0;
    g_state.game_started=false; g_state.game_over=false; g_state.winner_conn=-1;
    snprintf(g_state.last_event,sizeof(g_state.last_event),"服务器已启动");

    g_server_fd=socket(AF_INET,SOCK_STREAM,0);
    if(g_server_fd<0){perror("socket");return 1;}
    int opt=1;
    setsockopt(g_server_fd,SOL_SOCKET,SO_REUSEADDR,&opt,sizeof(opt));
    set_nodelay(g_server_fd);

    sockaddr_in addr{};
    addr.sin_family=AF_INET;
    addr.sin_addr.s_addr=INADDR_ANY;
    addr.sin_port=htons(port);
    if(bind(g_server_fd,(sockaddr*)&addr,sizeof(addr))<0){perror("bind");return 1;}
    if(listen(g_server_fd,32)<0){perror("listen");return 1;}  // backlog=32，充足

    std::cout << "╔══════════════════════════════════════════════╗\n";
    std::cout << "║  多人对战服务器 v3.1  游戏:" << MAX_PLAYERS << "人  连接:" << MAX_CONN << "人  ║\n";
    std::cout << "╚══════════════════════════════════════════════╝\n";
    std::cout << "[服务器] 监听 0.0.0.0:" << port << "\n";
    std::cout << "[服务器] 数据目录：./data/\n";
    std::cout << "[服务器] 地图：" << MAP_W << "×" << MAP_H << "\n\n";

    std::thread(weapon_thread).detach();
    std::thread(hb_watch_thread).detach();

    // ── accept 主循环：始终接受连接，不受游戏槽限制 ──
    while (g_running) {
        sockaddr_in cli{};
        socklen_t clen=sizeof(cli);
        int cfd=accept(g_server_fd,(sockaddr*)&cli,&clen);
        if(cfd<0){if(g_running)perror("accept");continue;}

        set_nodelay(cfd);

        // 找空连接槽
        int cid=-1;
        {
            std::lock_guard<std::mutex> lk(g_conn_mutex);
            for(int i=0;i<MAX_CONN;i++)
                if(!g_conns[i].active&&g_conns[i].fd<0){cid=i;break;}
            if(cid>=0){
                g_conns[cid].fd       = cfd;
                g_conns[cid].active   = true;
                g_conns[cid].authed   = false;
                g_conns[cid].username = "";
                g_conns[cid].game_slot= -1;
                g_conns[cid].last_hb  = time(nullptr);
            }
        }

        if(cid<0) {
            // 连接数已达 MAX_CONN（极少发生）
            std::cerr << "[服务器] 连接池已满，拒绝连接\n";
            close(cfd);
            continue;
        }

        std::cout << "[服务器] 新连接 " << inet_ntoa(cli.sin_addr)
                  << ":" << ntohs(cli.sin_port) << " → 连接" << cid << "\n";
        std::thread(conn_thread,cid).detach();
    }

    close(g_server_fd);
    return 0;
}