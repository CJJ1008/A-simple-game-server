#pragma once
// ============================================================
// protocol.h  —  多人对战游戏 v3.0  客户端/服务器共用协议
// ============================================================

#include <cstdint>
#include <cstring>
#include <sys/socket.h>
#include <unistd.h>

// ──────────────────────────────────────────────
// 游戏常量
// ──────────────────────────────────────────────
constexpr int MAX_PLAYERS     = 5;    // 最多 5 人同时在线
constexpr int MAP_W           = 36;   // 地图宽（格）36格×2字符=72 + 边框4 = 76列，80列终端可容纳
constexpr int MAP_H           = 16;   // 地图高（格）
constexpr int MAX_HEALTH      = 100;  // 初始血量
constexpr int ATTACK_DAMAGE   = 15;   // 普通攻击伤害
constexpr int POWER_DAMAGE    = 30;   // 武器攻击伤害（×2）
constexpr int ATTACK_RANGE    = 3;    // 攻击范围（曼哈顿距离）
constexpr int MAX_WEAPONS     = 6;    // 地图武器上限
constexpr int WEAPON_INTERVAL = 8;    // 武器刷新周期（秒）

// ──────────────────────────────────────────────
// 心跳
// ──────────────────────────────────────────────
constexpr int HEARTBEAT_INTERVAL = 2;
constexpr int HEARTBEAT_TIMEOUT  = 8;

// ──────────────────────────────────────────────
// 包类型枚举
// ──────────────────────────────────────────────
enum class PacketType : uint8_t {
    // ── 认证阶段 ──
    REGISTER     = 1,   // 客→服：注册账号
    LOGIN        = 2,   // 客→服：登录
    AUTH_RESULT  = 3,   // 服→客：认证结果

    // ── 游戏阶段 ──
    JOIN         = 4,   // 客→服：进入房间（已认证）
    ACTION       = 5,   // 客→服：操作（移动/攻击）
    STATE_UPDATE = 6,   // 服→客：权威状态广播
    READY        = 7,   // 客→服：准备确认

    // ── 战绩查询 ──
    STATS_REQUEST  = 8, // 客→服：查询战绩（可查自己或他人）
    STATS_RESPONSE = 9, // 服→客：战绩数据

    // ── 通用 ──
    HEARTBEAT     = 10,
    HEARTBEAT_ACK = 11,
    DISCONNECT    = 12,
};

// ──────────────────────────────────────────────
// 操作类型
// ──────────────────────────────────────────────
enum class ActionType : uint8_t {
    MOVE_UP    = 1,
    MOVE_DOWN  = 2,
    MOVE_LEFT  = 3,
    MOVE_RIGHT = 4,
    ATTACK     = 5,
};

// ──────────────────────────────────────────────
// 包头（3 字节）
// ──────────────────────────────────────────────
struct PacketHeader {
    PacketType type;
    uint16_t   length;
} __attribute__((packed));

constexpr int HEADER_SIZE = sizeof(PacketHeader); // = 3

// ──────────────────────────────────────────────
// 认证 payload
// ──────────────────────────────────────────────
struct AuthPayload {        // 用于 REGISTER 和 LOGIN
    char username[32];
    char password[64];      // 明文（传输层简化；生产环境应用 TLS）
} __attribute__((packed));

struct AuthResultPayload {
    uint8_t success;        // 1=成功，0=失败
    char    message[64];    // 结果描述（"登录成功" / "用户名已存在" 等）
    char    username[32];   // 成功时回填用户名（供客户端显示）
} __attribute__((packed));

// ──────────────────────────────────────────────
// 战绩 payload
// ──────────────────────────────────────────────
struct StatsRequestPayload {
    char username[32];      // 要查询的用户名；空字符串 = 查自己
} __attribute__((packed));

struct StatsResponsePayload {
    char    username[32];
    uint8_t found;          // 1=找到用户，0=不存在
    int32_t games;          // 总局数
    int32_t wins;           // 胜场
    int32_t losses;         // 败场
    int32_t kills;          // 总击杀
    int32_t deaths;         // 总死亡
    char    last_played[24];// 最后游戏时间（"YYYY-MM-DD HH:MM:SS"）
} __attribute__((packed));

// ──────────────────────────────────────────────
// 游戏包 payload（与 v2 相同）
// ──────────────────────────────────────────────
struct ActionPayload {
    ActionType action;
} __attribute__((packed));

struct PlayerState {
    int8_t  x, y;
    int16_t health;
    uint8_t alive;
    uint8_t connected;
    uint8_t ready;
    uint8_t has_weapon;
    char    name[16];       // 游戏内显示名（昵称，可与账号名不同）
} __attribute__((packed));  // 24 bytes

struct WeaponItem {
    int8_t  x, y;
    uint8_t active;
} __attribute__((packed));  // 3 bytes

struct StateUpdatePayload {
    PlayerState players[MAX_PLAYERS];   // 5×24=120
    WeaponItem  weapons[MAX_WEAPONS];   // 6×3=18
    uint8_t     your_id;
    uint8_t     player_count;
    uint8_t     ready_count;
    uint8_t     game_started;
    uint8_t     game_over;
    uint8_t     winner_id;
    char        last_event[64];
} __attribute__((packed));              // 120+18+6+64=208 bytes

// ──────────────────────────────────────────────
// 辅助：可靠发送（MSG_NOSIGNAL 防止 SIGPIPE）
// ──────────────────────────────────────────────
inline int send_packet(int fd, PacketType type,
                       const void* payload = nullptr,
                       uint16_t payload_len = 0) {
    PacketHeader hdr;
    hdr.type   = type;
    hdr.length = payload_len;
    if (::send(fd, &hdr, HEADER_SIZE, MSG_NOSIGNAL) != HEADER_SIZE)
        return -1;
    if (payload_len > 0 && payload)
        if (::send(fd, payload, payload_len, MSG_NOSIGNAL) != (ssize_t)payload_len)
            return -1;
    return HEADER_SIZE + payload_len;
}

// ──────────────────────────────────────────────
// 辅助：循环接收直到收满 len 字节
// ──────────────────────────────────────────────
inline bool recv_all(int fd, void* buf, size_t len) {
    size_t done = 0;
    char*  ptr  = static_cast<char*>(buf);
    while (done < len) {
        ssize_t n = ::recv(fd, ptr + done, len - done, 0);
        if (n <= 0) return false;
        done += (size_t)n;
    }
    return true;
}