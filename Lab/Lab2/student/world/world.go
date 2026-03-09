// Package world 实现多人开放世界的共享状态与并发安全操作。
//
// ┌─────────────────────────────────────────────────────────────────────┐
// │  实验任务：为下列函数添加正确的并发控制（锁）并实现其核心逻辑       │
// │                                                                     │
// │  背景：服务器为每个玩家启动一个独立 Goroutine（见 server/main.go）  │
// │  多个 Goroutine 同时读写 World.players，必须通过锁保证安全。        │
// │                                                                     │
// │  本文件使用 sync.RWMutex（读写锁）：                                │
// │    · 写操作（修改 players）：w.mu.Lock() / defer w.mu.Unlock()     │
// │    · 读操作（只读 players）：w.mu.RLock() / defer w.mu.RUnlock()   │
// │    · RWMutex 允许多个 Goroutine 同时持有读锁，但写锁是互斥的       │
// │                                                                     │
// │  任务列表：                                                         │
// │    C-1：AddPlayer    —— 加入玩家（写锁）                           │
// │    C-2：RemovePlayer —— 移除玩家（写锁）                           │
// │    C-3：MovePlayer   —— 移动玩家（写锁 + 边界检查）                │
// │    C-4：AttackPlayer —— 攻击（写锁 + 距离检查 + 扣血 + 死亡判断）  │
// │    C-5：GetSnapshot  —— 获取快照（读锁）                           │
// └─────────────────────────────────────────────────────────────────────┘
package world

import (
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"battleworld/protocol"
)

// ─── Player ─────────────────────────────────────────────────────────────────

// Player 是世界中的玩家实体。
type Player struct {
	ID      int
	Name    string
	X, Y    int
	HP      int
	MaxHP   int
	Potions int
	Alive   bool
	Kills   int
	Conn    *protocol.Conn
}

// newPlayer 在随机位置创建玩家，已实现，无需修改。
func newPlayer(id int, name string, conn *protocol.Conn) *Player {
	return &Player{
		ID:      id,
		Name:    name,
		X:       rand.Intn(protocol.MapWidth),
		Y:       rand.Intn(protocol.MapHeight),
		HP:      protocol.InitHP,
		MaxHP:   protocol.InitHP,
		Potions: protocol.MaxPotions,
		Alive:   true,
		Conn:    conn,
	}
}

func (p *Player) toInfo() protocol.PlayerInfo {
	return protocol.PlayerInfo{
		ID: p.ID, Name: p.Name, X: p.X, Y: p.Y,
		HP: p.HP, MaxHP: p.MaxHP, Potions: p.Potions, Alive: p.Alive, Kills: p.Kills,
	}
}

// ─── World ───────────────────────────────────────────────────────────────────

// World 是所有玩家共享的游戏世界。
//
// 并发模型：每个客户端连接对应一个 Goroutine，多个 Goroutine
// 并发调用 World 的方法。RWMutex 确保：
//   - 写操作之间互斥（同时只有一个写者）
//   - 读操作可并发（多个读者同时读取）
//   - 写者与读者互斥（写时不能读，读时不能写）
type World struct {
	mu      sync.RWMutex   // 读写锁，保护以下字段
	players map[int]*Player // 玩家 ID → 玩家实体
	nextID  int            // 下一个可用的玩家 ID
}

// NewWorld 创建空世界，已实现，无需修改。
func NewWorld() *World {
	return &World{players: make(map[int]*Player), nextID: 1}
}

// ╔═════════════════════════════════════════════════════════════════════════╗
// ║  任务 C-1：实现 AddPlayer                                              ║
// ║                                                                         ║
// ║  功能：向世界中加入一名新玩家，返回其 ID 和玩家对象指针。              ║
// ║                                                                         ║
// ║  并发要求：必须使用写锁（修改了 players 和 nextID）。                  ║
// ║                                                                         ║
// ║  实现步骤：                                                             ║
// ║    1. 加写锁：w.mu.Lock()，并用 defer w.mu.Unlock() 确保释放           ║
// ║    2. 取当前 nextID 作为新玩家 ID，然后 nextID++                       ║
// ║    3. 调用 newPlayer(id, name, conn) 创建玩家                          ║
// ║    4. 将玩家存入 w.players[id]                                         ║
// ║    5. 返回 id 和玩家指针                                                ║
// ╚═════════════════════════════════════════════════════════════════════════╝
func (w *World) AddPlayer(name string, conn *protocol.Conn) (int, *Player) {
	// TODO: 加写锁，创建玩家，存入 map，返回 id 和玩家

	// 提示框架：
	// w.mu.Lock()
	// defer w.mu.Unlock()
	// id := w.nextID
	// w.nextID++
	// p := newPlayer(id, name, conn)
	// w.players[id] = p
	// return id, p

	panic("AddPlayer 尚未实现，请完成 TODO")
}

// ╔═════════════════════════════════════════════════════════════════════════╗
// ║  任务 C-2：实现 RemovePlayer                                           ║
// ║                                                                         ║
// ║  功能：从世界中删除指定 ID 的玩家。                                    ║
// ║                                                                         ║
// ║  并发要求：必须使用写锁（修改了 players map）。                         ║
// ║                                                                         ║
// ║  实现步骤：                                                             ║
// ║    1. 加写锁（使用 defer 释放）                                         ║
// ║    2. delete(w.players, id)                                             ║
// ╚═════════════════════════════════════════════════════════════════════════╝
func (w *World) RemovePlayer(id int) {
	// TODO: 加写锁，从 map 中删除 id 对应的玩家

	panic("RemovePlayer 尚未实现，请完成 TODO")
}

// ╔═════════════════════════════════════════════════════════════════════════╗
// ║  任务 C-3：实现 MovePlayer                                             ║
// ║                                                                         ║
// ║  功能：将指定玩家向 dir 方向移动一步（越界时保持原位）。               ║
// ║                                                                         ║
// ║  并发要求：必须使用写锁（修改了玩家坐标）。                             ║
// ║                                                                         ║
// ║  实现步骤：                                                             ║
// ║    1. 加写锁（defer 释放）                                              ║
// ║    2. 从 w.players[id] 取玩家，若不存在或已死亡，返回 ""               ║
// ║    3. 记录 oldX, oldY                                                   ║
// ║    4. switch dir：                                                      ║
// ║       · DirUp    → if p.Y > 0 { p.Y-- }                                ║
// ║       · DirDown  → if p.Y < MapHeight-1 { p.Y++ }                      ║
// ║       · DirLeft  → if p.X > 0 { p.X-- }                                ║
// ║       · DirRight → if p.X < MapWidth-1 { p.X++ }                       ║
// ║    5. 若坐标未变，返回"撞墙"字符串；否则返回移动成功字符串             ║
// ╚═════════════════════════════════════════════════════════════════════════╝
func (w *World) MovePlayer(id int, dir string) string {
	// TODO: 加写锁，查找玩家，移动并做边界检查，返回事件字符串

	panic("MovePlayer 尚未实现，请完成 TODO")
}

// ╔═════════════════════════════════════════════════════════════════════════╗
// ║  任务 C-4：实现 AttackPlayer                                           ║
// ║                                                                         ║
// ║  功能：攻击者攻击其周围 AttackRange 格内血量最低的存活对手。           ║
// ║                                                                         ║
// ║  并发要求：必须使用写锁（修改 HP、Alive、Kills）。                     ║
// ║                                                                         ║
// ║  实现步骤：                                                             ║
// ║    1. 加写锁（defer 释放）                                              ║
// ║    2. 查找攻击者，若不存在或死亡，返回 ""                               ║
// ║    3. 遍历 w.players，计算曼哈顿距离，找到范围内血量最低的活着的对手    ║
// ║       dist := math.Abs(float64(attacker.X-p.X)) +                      ║
// ║               math.Abs(float64(attacker.Y-p.Y))                        ║
// ║    4. 若没找到目标，返回"范围内没有敌人"                               ║
// ║    5. target.HP -= protocol.AttackDmg                                   ║
// ║    6. 若 target.HP <= 0：                                               ║
// ║       a. target.HP = 0（⚠️ 先夹紧到 0，再构建事件字符串，避免负数）    ║
// ║       b. target.Alive = false; attacker.Kills++                         ║
// ║       c. 启动 Goroutine 等待 5s 后调用 w.respawn(targetID)              ║
// ║          并调用 broadcastFn("xxx 已复活")                               ║
// ║    7. 构建并返回包含 target.HP（已夹紧）的攻击事件字符串               ║
// ║                                                                         ║
// ║  ⚠️  注意：复活 Goroutine 内部会重新加锁（w.respawn 内部加锁），       ║
// ║      因此主函数释放锁后，复活 Goroutine 才能获取锁，不会死锁。          ║
// ╚═════════════════════════════════════════════════════════════════════════╝
func (w *World) AttackPlayer(attackerID int, broadcastFn func(string)) string {
	// TODO: 加写锁，查找攻击者，找最弱目标，扣血，处理死亡与复活 Goroutine

	_ = math.Abs  // 防止 import 报错，实现后可删除
	_ = time.Sleep // 防止 import 报错

	panic("AttackPlayer 尚未实现，请完成 TODO")
}

// HealPlayer 使用药水，已实现，无需修改。
func (w *World) HealPlayer(id int) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	p, ok := w.players[id]
	if !ok || !p.Alive {
		return ""
	}
	if p.Potions <= 0 {
		return fmt.Sprintf("🧪 %s 药水耗尽！", p.Name)
	}
	p.Potions--
	before := p.HP
	p.HP += protocol.HealAmt
	if p.HP > p.MaxHP {
		p.HP = p.MaxHP
	}
	return fmt.Sprintf("🧪 %s 恢复 %d HP（%d/%d，剩余 %d 瓶）",
		p.Name, p.HP-before, p.HP, p.MaxHP, p.Potions)
}

// respawn 在写锁内重置玩家到随机位置并恢复满血（由复活 Goroutine 调用）。
// 已实现，无需修改。
func (w *World) respawn(id int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	p, ok := w.players[id]
	if !ok {
		return
	}
	p.X = rand.Intn(protocol.MapWidth)
	p.Y = rand.Intn(protocol.MapHeight)
	p.HP = p.MaxHP
	p.Potions = protocol.MaxPotions
	p.Alive = true
}

// ╔═════════════════════════════════════════════════════════════════════════╗
// ║  任务 C-5：实现 GetSnapshot                                            ║
// ║                                                                         ║
// ║  功能：返回当前所有玩家的状态快照（[]PlayerInfo），用于广播。           ║
// ║                                                                         ║
// ║  并发要求：使用读锁（只读 players，不修改）。                           ║
// ║    读锁：w.mu.RLock() / defer w.mu.RUnlock()                           ║
// ║    允许多个 Goroutine 同时调用 GetSnapshot（并发读无冲突）。            ║
// ║                                                                         ║
// ║  实现步骤：                                                             ║
// ║    1. 加读锁（defer 释放）                                              ║
// ║    2. 创建 infos := make([]protocol.PlayerInfo, 0, len(w.players))     ║
// ║    3. 遍历 w.players，将每个 p.toInfo() 追加到 infos                   ║
// ║    4. 返回 infos                                                        ║
// ╚═════════════════════════════════════════════════════════════════════════╝
func (w *World) GetSnapshot() []protocol.PlayerInfo {
	// TODO: 加读锁，遍历 players，收集并返回所有玩家的 Info 快照

	panic("GetSnapshot 尚未实现，请完成 TODO")
}

// ─── 广播辅助（无需修改） ────────────────────────────────────────────────────

// BroadcastAll 向所有在线玩家发送消息。先读锁收集连接，再锁外发送。
func (w *World) BroadcastAll(msg protocol.Message) {
	w.mu.RLock()
	conns := make([]*protocol.Conn, 0, len(w.players))
	for _, p := range w.players {
		conns = append(conns, p.Conn)
	}
	w.mu.RUnlock()
	for _, c := range conns {
		c.Send(msg)
	}
}

// BroadcastEvent 向所有玩家广播纯文本事件，无需修改。
func (w *World) BroadcastEvent(text string) {
	fmt.Printf("[事件] %s\n", text)
	w.BroadcastAll(protocol.Message{Type: protocol.TypeEvent, Text: text})
}
