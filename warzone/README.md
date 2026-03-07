## 文件结构

```
battlegame/
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


## 首次构建步骤

```bash
go mod tidy          # 下载 golang.org/x/term 和 golang.org/x/sys
make                 # 编译 server + client
```

## 编译

```shell
make          # 编译 server + client（仅需 g++ ≥ C++17）
make clean
```

## 启动

### 服务器（一台机器）

```shell
./server          # 监听 0.0.0.0:9000
./server 8080     # 指定端口

# 查本机局域网 IP（告知其他玩家）
ip addr show      # Linux
ipconfig          # Windows/WSL
```

### 客户端（每台玩家机器）

```shell
./client                        # 连 127.0.0.1:9000（同机测试）
./client 192.168.1.100 9000     # 局域网
./client <服务器IP> <端口>
```

启动后会出现登录菜单，选择登录或注册即可进入游戏。

> **防火墙提示**：`sudo ufw allow 9000/tcp`（Ubuntu）
