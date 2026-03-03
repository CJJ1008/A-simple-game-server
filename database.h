#pragma once
// ============================================================
// database.h  —  轻量级文件型持久化数据库
//
// 无需 SQLite / 第三方库，使用两个纯文本文件：
//   data/users.db   — 账号信息（用户名|密码哈希|创建时间）
//   data/stats.db   — 战绩信息（用户名|胜|负|击杀|死亡|局数|最后游戏时间）
//
// 并发安全：读写均持 db_mutex，保证多线程下的文件完整性。
// 写策略：先写临时文件，成功后原子重命名，避免写到一半崩溃导致损坏。
// ============================================================

#include <string>
#include <vector>
#include <mutex>
#include <fstream>
#include <sstream>
#include <cstring>
#include <cstdint>
#include <ctime>
#include <sys/stat.h>
#include <algorithm>

// ──────────────────────────────────────────────
// 用户账号记录
// ──────────────────────────────────────────────
struct UserRecord {
    std::string username;
    std::string password_hash;  // FNV-1a(用户名+密码+pepper) hex 字符串
    std::string created_at;     // "YYYY-MM-DD HH:MM:SS"
};

// ──────────────────────────────────────────────
// 战绩记录
// ──────────────────────────────────────────────
struct StatsRecord {
    std::string username;
    int32_t     wins        = 0;
    int32_t     losses      = 0;
    int32_t     kills       = 0;
    int32_t     deaths      = 0;
    int32_t     games       = 0;
    std::string last_played;    // "YYYY-MM-DD HH:MM:SS"
};

// ──────────────────────────────────────────────
// FNV-1a 64-bit 哈希（密码存储用，加盐混淆）
// ──────────────────────────────────────────────
inline uint64_t fnv1a_64(const std::string& s) {
    uint64_t h = 14695981039346656037ULL;
    for (unsigned char c : s) {
        h ^= c;
        h *= 1099511628211ULL;
    }
    return h;
}

// pepper：固定密钥，增加彩虹表攻击难度（生产环境应从配置文件读取）
constexpr const char* DB_PEPPER = "BattleGame2024!";

inline std::string hash_password(const std::string& username,
                                 const std::string& password) {
    std::string salted = username + ":" + password + ":" + DB_PEPPER;
    uint64_t h = fnv1a_64(salted);
    char buf[20];
    snprintf(buf, sizeof(buf), "%016llx", (unsigned long long)h);
    return std::string(buf);
}

// ──────────────────────────────────────────────
// 获取当前本地时间字符串
// ──────────────────────────────────────────────
inline std::string now_str() {
    time_t t = time(nullptr);
    struct tm tm_info;
    localtime_r(&t, &tm_info);
    char buf[24];
    strftime(buf, sizeof(buf), "%Y-%m-%d %H:%M:%S", &tm_info);
    return buf;
}

// ──────────────────────────────────────────────
// 字符串分割（按 sep 字符）
// ──────────────────────────────────────────────
inline std::vector<std::string> split(const std::string& s, char sep) {
    std::vector<std::string> parts;
    std::stringstream ss(s);
    std::string token;
    while (std::getline(ss, token, sep))
        parts.push_back(token);
    return parts;
}

// ──────────────────────────────────────────────
// Database 类
// ──────────────────────────────────────────────
class Database {
public:
    // dir：数据目录，初始化时创建
    explicit Database(const std::string& dir = "data") : dir_(dir) {
        mkdir(dir.c_str(), 0755);
        users_file_ = dir + "/users.db";
        stats_file_ = dir + "/stats.db";
    }

    // ── 注册新用户 ──
    // 返回 true=成功，false=用户名已存在
    bool register_user(const std::string& username,
                       const std::string& password,
                       std::string& err_msg) {
        std::lock_guard<std::mutex> lk(mutex_);

        // 非空检查
        if (username.empty() || username.size() > 30) {
            err_msg = "用户名长度须为 1~30 个字符"; return false;
        }
        if (password.size() < 4) {
            err_msg = "密码至少 4 个字符"; return false;
        }
        // 非法字符（| 是字段分隔符）
        if (username.find('|') != std::string::npos ||
            username.find('\n') != std::string::npos) {
            err_msg = "用户名含非法字符"; return false;
        }

        auto users = load_users_locked();
        for (auto& u : users)
            if (u.username == username) { err_msg = "用户名已被注册"; return false; }

        UserRecord ur;
        ur.username      = username;
        ur.password_hash = hash_password(username, password);
        ur.created_at    = now_str();
        users.push_back(ur);
        save_users_locked(users);

        // 同时创建空战绩记录
        auto stats = load_stats_locked();
        StatsRecord sr;
        sr.username    = username;
        sr.last_played = ur.created_at;
        stats.push_back(sr);
        save_stats_locked(stats);

        err_msg = "注册成功";
        return true;
    }

    // ── 登录验证 ──
    // 返回 true=成功，false=用户名或密码错误
    bool login(const std::string& username,
               const std::string& password,
               std::string& err_msg) {
        std::lock_guard<std::mutex> lk(mutex_);
        auto users = load_users_locked();
        for (auto& u : users) {
            if (u.username == username) {
                if (u.password_hash == hash_password(username, password)) {
                    err_msg = "登录成功";
                    return true;
                } else {
                    err_msg = "密码错误";
                    return false;
                }
            }
        }
        err_msg = "用户名不存在";
        return false;
    }

    // ── 查询战绩 ──
    bool get_stats(const std::string& username, StatsRecord& out) {
        std::lock_guard<std::mutex> lk(mutex_);
        auto stats = load_stats_locked();
        for (auto& s : stats) {
            if (s.username == username) { out = s; return true; }
        }
        return false;
    }

    // ── 更新战绩（游戏结束后调用）──
    // is_winner：该玩家是否胜利
    // kills_this_game：本局击杀数
    // died：本局是否死亡
    void update_stats(const std::string& username,
                      bool is_winner,
                      int  kills_this_game,
                      bool died) {
        std::lock_guard<std::mutex> lk(mutex_);
        auto stats = load_stats_locked();
        bool found = false;
        for (auto& s : stats) {
            if (s.username == username) {
                s.games++;
                s.kills  += kills_this_game;
                if (died)       s.deaths++;
                if (is_winner)  s.wins++;
                else            s.losses++;
                s.last_played = now_str();
                found = true;
                break;
            }
        }
        if (!found) {
            // 理论上不会发生（注册时已创建），兜底处理
            StatsRecord sr;
            sr.username    = username;
            sr.games       = 1;
            sr.kills       = kills_this_game;
            sr.wins        = is_winner ? 1 : 0;
            sr.losses      = is_winner ? 0 : 1;
            sr.deaths      = died ? 1 : 0;
            sr.last_played = now_str();
            stats.push_back(sr);
        }
        save_stats_locked(stats);
    }

    // ── 获取排行榜（按胜场降序，返回前 N 名）──
    std::vector<StatsRecord> leaderboard(int top_n = 10) {
        std::lock_guard<std::mutex> lk(mutex_);
        auto stats = load_stats_locked();
        // 按胜场降序排序
        std::sort(stats.begin(), stats.end(),
                  [](const StatsRecord& a, const StatsRecord& b){
                      return a.wins > b.wins;
                  });
        if ((int)stats.size() > top_n) stats.resize(top_n);
        return stats;
    }

private:
    std::string dir_;
    std::string users_file_;
    std::string stats_file_;
    std::mutex  mutex_;

    // ── 从文件读取全部用户（须持锁调用）──
    std::vector<UserRecord> load_users_locked() {
        std::vector<UserRecord> list;
        std::ifstream f(users_file_);
        if (!f.is_open()) return list;
        std::string line;
        while (std::getline(f, line)) {
            if (line.empty() || line[0] == '#') continue;
            auto p = split(line, '|');
            if (p.size() < 3) continue;
            UserRecord ur;
            ur.username      = p[0];
            ur.password_hash = p[1];
            ur.created_at    = p[2];
            list.push_back(ur);
        }
        return list;
    }

    // ── 安全写入用户文件（须持锁调用）──
    void save_users_locked(const std::vector<UserRecord>& list) {
        std::string tmp = users_file_ + ".tmp";
        std::ofstream f(tmp);
        f << "# username|password_hash|created_at\n";
        for (auto& u : list)
            f << u.username << "|" << u.password_hash << "|" << u.created_at << "\n";
        f.close();
        rename(tmp.c_str(), users_file_.c_str());
    }

    // ── 从文件读取全部战绩（须持锁调用）──
    std::vector<StatsRecord> load_stats_locked() {
        std::vector<StatsRecord> list;
        std::ifstream f(stats_file_);
        if (!f.is_open()) return list;
        std::string line;
        while (std::getline(f, line)) {
            if (line.empty() || line[0] == '#') continue;
            auto p = split(line, '|');
            if (p.size() < 7) continue;
            StatsRecord sr;
            sr.username    = p[0];
            sr.wins        = std::stoi(p[1]);
            sr.losses      = std::stoi(p[2]);
            sr.kills       = std::stoi(p[3]);
            sr.deaths      = std::stoi(p[4]);
            sr.games       = std::stoi(p[5]);
            sr.last_played = p[6];
            list.push_back(sr);
        }
        return list;
    }

    // ── 安全写入战绩文件（须持锁调用）──
    void save_stats_locked(const std::vector<StatsRecord>& list) {
        std::string tmp = stats_file_ + ".tmp";
        std::ofstream f(tmp);
        f << "# username|wins|losses|kills|deaths|games|last_played\n";
        for (auto& s : list)
            f << s.username << "|" << s.wins << "|" << s.losses
              << "|" << s.kills << "|" << s.deaths << "|" << s.games
              << "|" << s.last_played << "\n";
        f.close();
        rename(tmp.c_str(), stats_file_.c_str());
    }
};
