# 多人对战游戏  — 单体原型（go语言版）

---

## 文件结构

```
go/
├── go.mod / Makefile
├── cmd/
│   ├── server/main.go       # 服务器入口（端口参数）
│   └── client/main.go       # 客户端入口（host:port 参数）
└── internal/
    ├── protocol/protocol.go  # 协议常量、二进制结构体、收发辅助
    ├── database/database.go  # 文件型数据库（账号+战绩）
    ├── server/
    │   ├── server.go         # 监听循环、武器/心跳后台 goroutine
    │   ├── conn.go           # 每条连接的处理 goroutine（认证→大厅→游戏三阶段）
    │   └── game.go           # 游戏状态、动作处理、广播
    └── client/
        ├── client.go         # 登录UI、游戏主循环
        ├── render.go         # FrameBuffer 差异渲染、buildGame、buildStats
        ├── input.go          # 终端 raw 模式、readline（含 Delete 修复）
        └── network.go        # RecvWorker、HeartbeatWorker
```

---

## 环境准备 & 首次构建步骤

```bash
cd go          # 进入项目根目录
go mod init game-server  # 初始化模块（自定义模块名，如 game-server）
go mod tidy    # 下载 golang.org/x/term 和 golang.org/x/sys 等依赖
make           # （可选）编译 server + client 可执行文件
```

---

## 启动方式

### 方式1：直接运行
无需先执行 `make`，直接通过 `go run` 启动，支持参数传递。

#### 服务器（一台机器）
```shell
cd go  # 务必先进入项目根目录

# 默认监听 0.0.0.0:9000
go run ./cmd/server

# 指定端口
go run ./cmd/server 8080

# 查本机局域网 IP（告知其他玩家）
ip addr show      # Linux
ipconfig          # Windows/WSL
```

#### 客户端（每台玩家机器）
```shell
cd go  # 务必先进入项目根目录

# 默认连接 127.0.0.1:9000（同机测试）
go run ./cmd/client

# 连接局域网服务器（指定 IP+端口）
go run ./cmd/client <服务器IP> <端口>

```

### 方式2：编译后运行
```shell
cd go  # 务必先进入项目根目录

# 编译
make

# 启动服务器
./server          # 默认 0.0.0.0:9000
./server 8080     # 指定端口

# 启动客户端
./client                        # 默认 127.0.0.1:9000
./client <服务器IP> <端口>        # 连接局域网服务器（指定 IP+端口）

```

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