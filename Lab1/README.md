# 实验一：双雄对决

## 1. 教学目的

本实验旨在让学生从底层的 **Socket 通信** 开始，构建一个能够跨越网络边界进行交互的分布式系统原型。

- **掌握 TCP 编程**：理解并实现服务器的监听（Listen）、接收（Accept）以及客户端的拨号（Dial）逻辑。
    
- **理解协议序列化**：学习如何利用 JSON 格式将内存中的结构体对象序列化为可在网络上传输的字节流。
    
- **构建异步 I/O 循环**：理解长连接（Long-lived Connection）模式下的读取循环（readLoop）设计，处理异步到达的网络事件。
    
- **初步接触并发分发**：学习如何为每一个进入的连接开启独立的协程（Goroutine）进行处理，避免阻塞主流程。
    

---

## 2. 实验内容

本实验采用“挖空填代码”的形式。你将获得一个基本架构完整但网络功能缺失的系统，你需要补全以下核心函数：

### 任务 A：客户端连接与心跳

在 `internal/client/net_backend.go` 中：

1. **实现 `Connect` 函数**：使用 `net.Dial` 建立 TCP 连接，并初始化 JSON 编码器（Encoder）和解码器（Decoder）。
    
2. **实现 `readLoop` 函数**：在一个无限循环中，利用 `json.Decoder` 不断读取服务器下发的 `proto.Message` 对象，并调用 `handleMessage` 进行处理。
    

### 任务 B：服务端监听与握手

在 `internal/server/server.go` 中：

 **实现 `Start` 函数**：使用 `net.Listen` 在指定地址开启监听，并通过 `Accept` 循环接收新的连接请求。每当有一个新连接进入，必须通过 `go s.handleConn(conn)` 开启独立协程处理。
    

---

## 3. 实验步骤

1. **环境检查**：确保你的环境中已安装 Go 。
    
2. **代码补全**：根据 `internal/client/net_backend.go` 和 `internal/server/server.go` 中的 注释补全代码。
    
3. **协议阅读**：仔细阅读 `internal/proto/proto.go`。所有网络通信都必须封装在 `Message` 结构体中，否则对端将无法解析。
    

---

## 4. 测试与评测

### 4.1 自动化单元测试

我们提供了一个评测脚本 `internal/client/lab1_test.go`。该脚本会启动一个模拟服务器并尝试让你的客户端进行连接。

运行命令：

Bash

```
go test -v ./internal/client/lab1_test.go ./internal/client/net_backend.go ./internal/client/backend.go
```

**合格标准**：输出结果显示 `PASS` 且看到 `client 1 connected` 的日志。


### 4.2 手动集成测试

在两个不同的终端窗口中分别运行服务端和客户端，观察是否能进入游戏地图：

- **启动服务端**：
    
    Bash
    
    ```
    go run ./cmd/server/main.go -addr :7777
    ```
    
- **启动客户端**：
    
    Bash
    
    ```
    go run ./cmd/client/main.go -addr 127.0.0.1:7777 -name YourName
    ```
    

**成功标志**：客户端出现 TUI 地图界面，右侧显示你的玩家 ID 和坐标（不再卡在 "Connecting..." 界面）。

---

## 5. 常见问题提示

- **端口占用**：如果提示 `address already in use`，请使用 `lsof -i :7777` 查找并杀掉残留进程，或更换端口。
    
- **阻塞问题**：如果在 `Start` 函数中忘记使用 `go` 关键字启动 `handleConn` 或 `readLoop`，你的程序将只能处理一个玩家。
    
- **序列化错误**：请确保发送的消息类型 `Type` 与 `proto.go` 中定义的常量完全一致。
    

---

