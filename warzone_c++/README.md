# 多人对战游戏 v3.0 — 单体原型

---

## 文件说明

```
protocol.h   共用网络协议（包结构、常量、收发辅助）
database.h   文件型持久化数据库（账号 + 战绩，线程安全）
server.cpp   游戏服务器（认证、游戏逻辑、战绩入库）
client.cpp   游戏客户端（登录UI、差异渲染、战绩查询）
Makefile
data/        运行时自动创建
  users.db   账号文件（用户名|密码哈希|注册时间）
  stats.db   战绩文件（用户名|胜|负|击杀|死亡|局数|最后游戏时间）
```

---

## 编译

```bash
make          # 编译 server + client（仅需 g++ ≥ C++17）
make clean
```

---

## 启动

### 服务器（一台机器）

```bash
./server          # 监听 0.0.0.0:9000
./server 8080     # 指定端口

# 查本机局域网 IP（告知其他玩家）
ip addr show      # Linux
ipconfig          # Windows/WSL
```

### 客户端（每台玩家机器）

```bash
./client                        # 连 127.0.0.1:9000（同机测试）
./client 192.168.1.100 9000     # 局域网
./client <服务器IP> <端口>
```

启动后会出现登录菜单，选择登录或注册即可进入游戏。

> **防火墙提示**：`sudo ufw allow 9000/tcp`（Ubuntu）

---

## 游戏流程

```
连接 → 注册/登录 → 进入房间 → 按 R 准备
→（所有人准备且 ≥ 2 人）→ 游戏开始
→（每 8 秒刷新武器）→ 最后存活者获胜
→ 战绩自动写入 data/stats.db → 按 Q 退出
```

---

## 操作按键

| 按键 | 动作 |
|------|------|
| W / S / A / D 或方向键 | 移动 |
| 空格 / F | 攻击（自动瞄准最近存活玩家，范围 ≤ 3） |
| R | 准备（游戏开始前） |
| T | 查看战绩（游戏中也可使用） |
| Q | 退出 / 返回游戏（在战绩页） |
| S | 查询其他玩家战绩（在战绩页） |

---

## 地图图例

| 符号 | 含义 |
|------|------|
| `@` | 自己 |
| `A B C D E` | 其他玩家（5种颜色区分） |
| `*` | 该玩家持有武器（下次攻击 ×2） |
| `W` | 地图上的强力武器（走上去自动拾取） |

---

## 延迟优化详解

### 问题根源

旧版卡顿的根本是 **TCP Nagle 算法**：操作系统将小包（4 字节操作包）
缓存 200ms 后合并发送，导致每次按键都有明显延迟。

### 三项修复

**① TCP_NODELAY**（最关键）
```cpp
// 服务器对每个 accept 的套接字、客户端套接字都必须设置
int flag = 1;
setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &flag, sizeof(flag));
```

**② 差异渲染（消除闪烁）**

旧版每次状态更新都 `\033[2J`（清屏），导致屏幕先变黑再重绘，肉眼可见闪烁。

新版维护双缓冲 `curr_frame[]` / `prev_frame[]`，每次只刷新变化的行：
```cpp
// 只更新第 i 行（用绝对定位覆盖，不清屏）
"\033[{i+1};1H" + new_line + "\033[K"
```
结果：无闪烁，按键只触发必要的行更新。

**③ 输入轮询 10ms**（100Hz），去除原始阻塞等待。

---

## 账号与战绩系统

### 数据库设计

```
data/users.db  （文本格式）
username | password_hash | created_at

data/stats.db  （文本格式）
username | wins | losses | kills | deaths | games | last_played
```

- 密码使用 FNV-1a 64-bit 哈希 + 用户名盐 + 固定 pepper，防止彩虹表攻击
- 写入采用 **先写临时文件再原子重命名** 策略，防止崩溃导致数据损坏
- `Database` 类内置 `std::mutex`，所有读写均加锁，多线程安全

### 战绩更新时机

每局游戏结束（最后一人存活或所有人断线）时，服务器自动调用 `save_stats_locked()`，
为每名参与玩家更新：胜/负、击杀数、死亡数、局数、最后游戏时间。

---

## 并发与锁设计

```
g_state_mutex（std::mutex）
  保护：PlayerState[5]、WeaponItem[6]、game_started/over/winner、last_event

Database::mutex_（std::mutex，内部）
  保护：users.db 和 stats.db 的文件读写

原子变量（无锁）：
  g_connected[i]（atomic<bool>）— 快速判断槽位
  g_running（atomic<bool>）— 全局退出标志
```

### 加锁路径

```
client_thread  ← ACTION/READY  → lock(g_state) → 修改/广播 → unlock
weapon_thread  ← 定时8s        → lock(g_state) → 刷新武器/广播 → unlock
game_over      → save_stats    → db.update_stats → lock(db) → 写文件 → unlock
```

---

## 网络协议

| 包类型 | 方向 | 说明 |
|--------|------|------|
| REGISTER(1) | 客→服 | 注册（用户名+密码） |
| LOGIN(2) | 客→服 | 登录（用户名+密码） |
| AUTH_RESULT(3) | 服→客 | 认证结果（ok + 消息） |
| JOIN(4) | 客→服 | 进入游戏房间 |
| ACTION(5) | 客→服 | 操作（移动/攻击） |
| STATE_UPDATE(6) | 服→客 | 权威状态广播（208B） |
| READY(7) | 客→服 | 准备确认 |
| STATS_REQUEST(8) | 客→服 | 查询战绩（用户名） |
| STATS_RESPONSE(9) | 服→客 | 战绩数据 |
| HEARTBEAT(10) | 双向 | 心跳探活 |
| HEARTBEAT_ACK(11) | 双向 | 心跳回应 |
| DISCONNECT(12) | 任意 | 主动断开 |

包格式：`[type:1B][length:2B][payload:N B]`（小端序）

---

## 可调参数（protocol.h）

| 常量 | 默认 | 说明 |
|------|------|------|
| MAX_PLAYERS | 5 | 最大玩家数 |
| MAP_W / MAP_H | 50 / 20 | 地图尺寸 |
| ATTACK_DAMAGE | 15 | 普通攻击 |
| POWER_DAMAGE | 30 | 武器攻击 |
| ATTACK_RANGE | 3 | 攻击范围 |
| WEAPON_INTERVAL | 8s | 武器刷新周期 |
| HEARTBEAT_TIMEOUT | 8s | 掉线踢出 |