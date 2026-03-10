# Lab 2 · 并发编程：多人开放世界对战（Goroutine + 锁）

> **前置要求**：已完成 Lab 1，理解基本的 TCP 通信和 JSON 消息收发。
> Lab 2 在 Lab 1 的协议基础上扩展，重点新增**并发控制**。

---

## 1. 实验目的

| 目标 | 说明 |
|------|------|
| 理解 Goroutine | Go 的轻量级并发单元，`go func()` 即可启动 |
| 掌握 sync.RWMutex | 读写锁：多读者并发读，写者独占写 |
| 识别数据竞争 | 多 Goroutine 并发访问共享变量的问题及修复方式 |
| 设计并发安全 API | 在函数内部加锁，对外暴露无需调用方关心并发的接口 |

---

## 2. 游戏规则（相比 Lab 1 的变化）

```
地图：30 × 20（更大，支持更多玩家）
玩家数量：不限（随时可加入/离开）
游戏模式：实时开放世界（非回合制）—— 每位玩家可随时行动
攻击目标：自动攻击攻击范围内血量最低的敌人
死亡：HP 降至 0 后 5 秒自动复活（随机位置，满血）
状态推送：服务器每 500ms 向所有玩家广播全量世界状态
```

---

## 3. 并发架构图

```
┌─────────────────────────────────────────────────────────┐
│                    Server 进程                          │
│                                                         │
│  main Goroutine                                         │
│  ├─ net.Listen → net.Accept() 循环                      │
│  │   ├─ 新连接 → go handleClient(world, conn)  ←─ 任务D │
│  │   ├─ 新连接 → go handleClient(world, conn)           │
│  │   └─ ...（无限接受，不阻塞）                          │
│  │                                                      │
│  └─ go broadcastLoop()  ←─────────────────────── 任务D  │
│       每 500ms：world.GetSnapshot() → BroadcastAll()    │
│                                                         │
│  handleClient Goroutine（每个玩家一个）                   │
│  └─ 循环读指令 → world.MovePlayer / AttackPlayer / ...   │
│                           ↕ 加锁访问共享状态              │
│  ┌─────────────────────────────────────────────────┐    │
│  │  world.World（共享状态）                         │    │
│  │  mu sync.RWMutex   ← 保护以下字段               │    │
│  │  players map[int]*Player                        │    │
│  │  nextID int                                     │    │
│  └─────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────┘
```

---

## 4. 文件结构

```
Lab2/
├── student/           ← 学生填写目录
│   ├── go.mod
│   ├── protocol/message.go   已提供，无需修改
│   ├── world/world.go        ★ 需填写 C-1~C-5 五个函数
│   └── cmd/                  
│       ├── server/main.go    ★ 需填写 D-1、D-2 两处 Goroutine 启动
│       └── client/main.go    ★ 需填写 E：接收 Goroutine
│
└── test/
    ├── autotest.go    自动测试程序
    └── run_test.sh    一键测试脚本（含 -race 数据竞争检测）
```

---

## 5. 实验任务

### 任务 C：并发安全的世界状态（`student/world/world.go`）

> **核心规则**：任何对 `w.players` 的读写，都必须在持有锁的情况下进行。

#### C-1  `AddPlayer`（写锁）

向世界加入新玩家，返回其 ID 和对象指针。

```
必须使用：w.mu.Lock() / defer w.mu.Unlock()
步骤：取 nextID → nextID++ → newPlayer → 存入 map → 返回
```

#### C-2  `RemovePlayer`（写锁）

从世界移除指定 ID 的玩家。

```
必须使用：w.mu.Lock() / defer w.mu.Unlock()
步骤：delete(w.players, id)
```

#### C-3  `MovePlayer`（写锁）

向指定方向移动玩家，越界时保持原位（逻辑与 Lab1 相同）。

```
必须使用写锁（修改了坐标）
先从 map 取玩家，再做边界检查，再修改坐标
```

#### C-4  `AttackPlayer`（写锁 + 死亡复活 Goroutine）

攻击范围内血量最低的存活对手，死亡后启动复活 Goroutine。

```
必须使用写锁（修改 HP、Alive、Kills）
遍历 players，用 math.Abs 计算曼哈顿距离，找最弱目标
死亡判断：HP ≤ 0 → Alive=false → go func(){ time.Sleep(5s); respawn() }()
```

> ⚠️ **死锁警告**：复活 Goroutine 内部会重新加锁（`respawn` 会调用 `w.mu.Lock()`）。
> 因此主函数必须先**释放锁**，复活 Goroutine 才能获取锁。
> 用 `defer w.mu.Unlock()` 可以确保主函数返回时自动释放，不会死锁。

#### C-5  `GetSnapshot`（**读锁**）

返回所有玩家状态的快照切片，用于广播。

```
必须使用：w.mu.RLock() / defer w.mu.RUnlock()
（读锁允许多个 Goroutine 同时调用 GetSnapshot，比写锁性能更好）
```

---

### 任务 D：Goroutine 启动（`student/cmd/server/main.go`）

#### D-1  启动广播 Goroutine

```go
// 在 main 函数中，Accept 循环之前添加：
go func() {
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()
    for range ticker.C {
        snapshot := w.GetSnapshot()
        if len(snapshot) == 0 { continue }
        w.BroadcastAll(protocol.Message{
            Type:    protocol.TypeBroadcast,
            Players: snapshot,
        })
    }
}()
```

**关键**：必须用 `go func(){}()` 形式，否则会阻塞 `Accept` 循环。

#### D-2  为每个连接启动独立 Goroutine

```go
// Accept 循环内，将：
handleClient(w, raw)     // ← 阻塞，服务器卡住
// 改为：
go handleClient(w, raw)  // ← 并发，可同时服务多人
```

**原因**：`handleClient` 内有阻塞 I/O（等待客户端消息）。不用 `go`，第一个玩家未断开前，第二个玩家无法连接。

---

### 任务 E：客户端接收 Goroutine（`student/cmd/client/main.go`）

在 `main` 函数中，键盘读取循环之前，启动一个后台 Goroutine 专门接收服务器消息：

```go
go func() {
    defer close(done)
    for {
        msg, err := conn.Receive()
        if err != nil {
            addEvent("与服务器的连接已断开。")
            drawUI()
            return
        }
        switch msg.Type {
        case protocol.TypeInit:
            myPlayerID = msg.YourID
            addEvent(fmt.Sprintf("🎮 %s（你的ID: %d）", msg.Text, myPlayerID))
            drawUI()
        case protocol.TypeBroadcast:
            updateSnapshot(msg)
            drawUI()
        case protocol.TypeEvent:
            addEvent(msg.Text)
            drawUI()
        case protocol.TypeGameOver:
            addEvent(fmt.Sprintf("💀 游戏通知: %s", msg.Winner))
            drawUI()
        }
    }
}()
```

**为什么需要两个 Goroutine？**
- main Goroutine 阻塞在 `reader.ReadString('\n')` 等待键盘输入
- 若没有第二个 Goroutine，就无法同时接收服务器推送的状态更新
- 两个 Goroutine 并发运行，实现"边听键盘边收网络"

---

## 6. 运行方法

### 6.1 手动运行

```bash
# 终端 1：启动服务器（支持任意多玩家）
cd student
go run ./cmd/server

# 终端 2、3、4...：多个客户端同时连接
go run ./cmd/client
```

### 6.2 数据竞争检测（重要！）

Go 提供内置的 race detector，可以自动检测多 Goroutine 并发访问共享变量但未加锁的情况：

```bash
cd student
go run -race ./cmd/server
```

如果你的锁实现有问题，运行时会看到类似输出：
```
WARNING: DATA RACE
Write at 0x... by goroutine 7:
  battleworld/world.(*World).MovePlayer(...)
...
```

正确实现后，`-race` 模式下不应有任何 `DATA RACE` 警告。

---

## 7. 测试方法

### 7.1 一键测试（推荐）

```bash
cd test
bash run_test.sh            # 测试 student 目录（含 -race 检测）
```

### 7.2 单项测试

```bash
# 先启动服务器
cd student && go run ./cmd/server &
# 可能端口会被占用 运行下面命令
PID=$(lsof -ti :9001)
kill -9 $PID

# 运行指定测试
cd test
go run autotest.go 1   # 多客户端并发连接
go run autotest.go 4   # 移动边界
go run autotest.go 7   # 广播 Goroutine
```

### 7.3 测试用例说明

| 编号 | 测试内容 | 对应任务 | 关键验证点 |
|------|----------|----------|-----------|
| Test 1 | 5 个客户端并发连接 | D-2 | 不使用 `go` 时第 2 个客户端无法连接 |
| Test 2 | AddPlayer ID 唯一 | C-1 | 并发写 map 不加锁会 panic 或 ID 重复 |
| Test 3 | RemovePlayer | C-2 | 断线后玩家从快照消失 |
| Test 4 | MovePlayer 边界 | C-3 | X/Y 不超出 [0, MapWidth/Height-1] |
| Test 5 | GetSnapshot 并发读 | C-5 | 10 个客户端同时触发广播，不崩溃 |
| Test 6 | AttackPlayer 扣血 | C-4 | 命中后 HP 减少 AttackDmg |
| Test 7 | 广播 Goroutine | D-1 | 2 秒内收到 ≥ 2 次 TypeBroadcast |

---

## 8. 常见错误与排查

### 错误：第 2 个客户端无法连接

**原因**：`handleClient` 未以 Goroutine 方式调用（任务 D-2 未完成）。
**修复**：`go handleClient(w, raw)`

### 错误：`fatal error: concurrent map writes`

**原因**：`AddPlayer` 或 `RemovePlayer` 未加写锁，多个 Goroutine 同时写 `players` map。
**修复**：添加 `w.mu.Lock() / defer w.mu.Unlock()`

### 错误：`DATA RACE` 警告（`-race` 模式）

**原因**：某个函数读写 `players` 或其字段时未持有锁。
**定位**：查看 race detector 输出的函数名，对该函数加对应类型的锁。

### 错误：死锁（程序卡死）

**原因**：写锁还未释放时，复活 Goroutine 尝试再次加写锁。
**修复**：使用 `defer w.mu.Unlock()` 确保主函数返回前自动释放锁。

### 错误：客户端不显示其他玩家移动

**原因**：任务 E 的接收 Goroutine 未实现，键盘读取阻塞了接收循环。
**修复**：在 `go func(){...}()` 中处理 `TypeBroadcast`，调用 `renderState`。

