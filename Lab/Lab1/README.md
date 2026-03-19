# Lab 1 · 网络编程：双人对战游戏（C/S 架构）

## 1. 实验目的

| 目标 | 说明 |
|------|------|
| 掌握 TCP 网络编程 | 使用 `net.Listen` / `net.Dial` / `net.Conn` 建立 TCP 连接 |
| 理解 JSON 序列化 | 用 `json.Encoder` / `json.Decoder` 实现结构化消息传输 |
| 理解 C/S 架构 | 服务端权威模型：客户端只发"意图"，服务器负责验证与状态更新 |
| 实现游戏逻辑 | 坐标边界检查、曼哈顿距离攻击范围判断 |

---

## 2. 游戏规则

```
地图：20 × 20，坐标 (X, Y)，X 向右，Y 向下
玩家 1 起始位置：(0, 0)   玩家 2 起始位置：(19, 19)
初始 HP：100   攻击伤害：30   攻击范围：曼哈顿距离 ≤ 2
药水：初始 3 瓶，每瓶回复 40 HP（上限 MaxHP）
胜利条件：将对手 HP 降至 0
```

### 操作键位

| 键 | 行动 |
|----|------|
| `w` | 向上移动 |
| `s` | 向下移动 |
| `a` | 向左移动 |
| `d` | 向右移动 |
| `f` | 攻击 |
| `h` | 使用药水 |

---

## 3. 文件结构

```
Lab1/
├── student/           ← 学生填写目录
│   ├── go.mod
│   ├── protocol/message.go   ★ 需填写 Send() / Receive()
│   ├── game/game.go          ★ 需填写 handleMove() / handleAttack()
│   └── cmd/                  
│       ├── server/main.go    已提供，无需修改
│       └── client/main.go    已提供，无需修改
│
└── test/
    ├── autotest.go    自动测试程序
    ├── run_test.sh    macOS / Linux 一键测试脚本
    ├── run_test.bat   Windows 一键测试脚本
    └── runner/main.go 跨平台测试入口
```

---

## 4. 实验任务

### 任务 A：实现网络消息收发（`student/protocol/message.go`）

#### A-1  `Send(msg Message) error`

**功能**：将 `msg` 序列化为 JSON，通过 TCP 连接发送给对端。

**提示**：
- `c.encoder` 是 `*json.Encoder`，调用 `Encode(msg)` 即可
- `Encode` 会自动在末尾追加 `\n` 作为消息边界
- 返回 `Encode` 的错误值
- 整个函数体 **1 行**

```go
func (c *Conn) Send(msg Message) error {
    // TODO
}
```

#### A-2  `Receive() (Message, error)`

**功能**：从 TCP 连接阻塞读取一条 JSON 消息并返回。

**提示**：
- `c.decoder` 是 `*json.Decoder`，调用 `Decode(&msg)` 即可
- 函数体 **3 行**

```go
func (c *Conn) Receive() (Message, error) {
    // TODO
}
```

> **为什么不能每次 new 一个 Decoder？**
> `json.Decoder` 内部维护读缓冲区，每次 `Read` 会多读若干字节到缓冲区。
> 若重新创建 Decoder，已缓冲的字节会丢失，导致消息解析错误。

---

### 任务 B：实现游戏逻辑（`student/game/game.go`）

#### B-1  `handleMove(p *Player, dir string) string`

**功能**：将玩家 `p` 向 `dir` 方向移动一步，越界时保持不动。

**要求**：
1. 保存旧坐标 `oldX, oldY`
2. `switch dir` 处理四个方向，**先检查边界再修改坐标**
3. 坐标未变时返回"撞墙"提示；否则返回含新坐标的移动成功信息

**边界约束**：`X ∈ [0, MapWidth-1]`，`Y ∈ [0, MapHeight-1]`

#### B-2  `handleAttack(actor, target *Player) string`

**功能**：actor 攻击 target，检查范围后扣血，HP 归零则标记死亡。

**要求**：
1. 计算曼哈顿距离：`|actor.X - target.X| + |actor.Y - target.Y|`（用 `math.Abs`）
2. 距离 > `protocol.AttackRange` → 返回攻击失败信息，HP **不变**
3. 否则：`target.HP -= protocol.AttackDmg`
4. `target.HP ≤ 0` → 置 0，`target.Alive = false`

---

## 5. 运行方法

> 平台兼容说明：
> Lab1 的服务端和客户端代码可在 Windows、macOS、Linux 下运行。
> 只要本机安装了 Go，即可直接使用 `go run` 启动。

### 5.1 手动运行（两个终端）

```bash
# 终端 1：启动服务器
cd student
go run ./cmd/server

# 终端 2：启动客户端 1
go run ./cmd/client

# 终端 3：启动客户端 2（另开一个窗口）
cd student
go run ./cmd/client
```


---

## 6. 测试方法

### 6.1 一键测试（推荐）

macOS / Linux：

```bash
cd test
./run_test.sh
```

Windows：

```bat
cd test
run_test.bat
```

也可以直接使用跨平台 Go 测试入口：

```bash
cd test
go run ./runner/main.go
```

测试脚本会：
1. 编译学生代码（语法错误立即报告）
2. 为每个测试用例自动启动/停止服务器
3. 模拟两个客户端进行协议交互
4. 验证功能是否符合预期，输出 ✅ PASS / ❌ FAIL

### 6.2 运行单项测试

```bash
# 先在另一个终端启动服务器
cd student
go run ./cmd/server

# 再在当前终端运行指定测试
cd ../test
go run autotest.go 1   # 测试连接握手
go run autotest.go 2   # 测试移动边界
go run autotest.go 4   # 测试攻击范围
```

> 说明：
> 单项测试本身是跨平台的，真正需要区分平台的是一键测试脚本：
> - macOS / Linux 使用 `run_test.sh`
> - Windows 使用 `run_test.bat`

### 6.3 测试用例说明

| 编号 | 测试内容 | 对应任务 |
|------|----------|----------|
| Test 1 | 连接握手、ID 分配 | 任务 A（Send/Receive） |
| Test 2 | 向上移动边界保护（Y 不低于 0） | 任务 B-1 |
| Test 3 | 方向映射正确性（向右 X+1，下边界保护） | 任务 B-1 |
| Test 4 | 超范围攻击失败（HP 不变） | 任务 B-2 |
| Test 5 | 攻击距离检测 | 任务 B-2 |
| Test 6 | 满血使用药水不超上限（已给实现） | 参考验证 |
| Test 7 | 断线自动游戏结束 | 综合 |

---

## 7. 常见问题

**Q：`panic: Send 尚未实现`**
A：删除 `panic(...)` 行，填入正确代码。

**Q：两个客户端连上后卡住不动**
A：检查 `Send` 是否正确实现；服务器发送 `init` 消息后才进行下一步。

**Q：攻击总是显示"距离太远"**
A：检查 `handleAttack` 中的距离计算，确保使用 `math.Abs` 取绝对值再求和。

**Q：移动后坐标没有变化**
A：检查 `handleMove` 中 switch 的 case 是否与 `protocol.DirXxx` 常量匹配。
