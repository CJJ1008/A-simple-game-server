// Package world 实现多人开放世界的共享状态与并发安全操作。
//
// 并发架构说明：
//   - 每个客户端连接由独立的 Goroutine（handleClient）处理
//   - 多个 Goroutine 并发读写同一个 World，必须通过锁保护
//   - 读多写少场景 → 使用 sync.RWMutex：
//       读操作（GetSnapshot）用 RLock/RUnlock，允许并发读
//       写操作（Add/Remove/Move/Attack）用 Lock/Unlock，互斥写
//   - 死亡后自动复活（Goroutine + time.Sleep），体现 Goroutine 的实用场景
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

// Player 是世界中的玩家实体，同时持有其网络连接。
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

func newPlayer(id int, name string, conn *protocol.Conn) *Player {
	x := rand.Intn(protocol.MapWidth)
	y := rand.Intn(protocol.MapHeight)
	return &Player{
		ID:      id,
		Name:    name,
		X:       x,
		Y:       y,
		HP:      protocol.InitHP,
		MaxHP:   protocol.InitHP,
		Potions: protocol.MaxPotions,
		Alive:   true,
		Conn:    conn,
	}
}

func (p *Player) toInfo() protocol.PlayerInfo {
	return protocol.PlayerInfo{
		ID:      p.ID,
		Name:    p.Name,
		X:       p.X,
		Y:       p.Y,
		HP:      p.HP,
		MaxHP:   p.MaxHP,
		Potions: p.Potions,
		Alive:   p.Alive,
		Kills:   p.Kills,
	}
}

// ─── World ───────────────────────────────────────────────────────────────────

// World 是所有玩家共享的游戏世界，通过 RWMutex 保证并发安全。
//
// 核心不变量：任何对 players map 的读写都必须在持有 mu 的情况下进行。
type World struct {
	mu      sync.RWMutex   // 读写锁：保护 players 和 nextID
	players map[int]*Player // 玩家 ID → 玩家实体
	nextID  int            // 自增 ID，每次 AddPlayer 后递增
}

// NewWorld 创建一个空的游戏世界。
func NewWorld() *World {
	return &World{
		players: make(map[int]*Player),
		nextID:  1,
	}
}

// ─── 写操作（需要排他锁） ────────────────────────────────────────────────────

// AddPlayer 向世界中加入一名新玩家，返回其 ID 和初始状态。
// 并发安全：调用时获取写锁，完成后释放。
func (w *World) AddPlayer(name string, conn *protocol.Conn) (int, *Player) {
	w.mu.Lock()
	defer w.mu.Unlock()

	id := w.nextID
	w.nextID++
	p := newPlayer(id, name, conn)
	w.players[id] = p
	return id, p
}

// RemovePlayer 从世界中移除指定玩家。
// 并发安全：调用时获取写锁，完成后释放。
func (w *World) RemovePlayer(id int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	delete(w.players, id)
}

// MovePlayer 将 id 对应的玩家向 dir 方向移动一步（越界保持原位）。
// 返回描述移动结果的事件字符串。
// 并发安全：调用时获取写锁。
func (w *World) MovePlayer(id int, dir string) string {
	w.mu.Lock()
	defer w.mu.Unlock()

	p, ok := w.players[id]
	if !ok || !p.Alive {
		return ""
	}

	oldX, oldY := p.X, p.Y
	switch dir {
	case protocol.DirUp:
		if p.Y > 0 {
			p.Y--
		}
	case protocol.DirDown:
		if p.Y < protocol.MapHeight-1 {
			p.Y++
		}
	case protocol.DirLeft:
		if p.X > 0 {
			p.X--
		}
	case protocol.DirRight:
		if p.X < protocol.MapWidth-1 {
			p.X++
		}
	}

	if p.X == oldX && p.Y == oldY {
		return fmt.Sprintf("%s 撞到边界", p.Name)
	}
	return fmt.Sprintf("🚶 %s 移动到 (%d,%d)", p.Name, p.X, p.Y)
}

// AttackPlayer 让 attackerID 攻击攻击范围内血量最低的存活对手。
// 若命中：扣血、判断死亡、触发复活 Goroutine。
// 返回（事件字符串, 击杀者名字（若本次击杀）, 被杀者名字（若本次击杀））。
// 并发安全：调用时获取写锁。
func (w *World) AttackPlayer(attackerID int, broadcastFn func(string)) string {
	w.mu.Lock()
	defer w.mu.Unlock()

	attacker, ok := w.players[attackerID]
	if !ok || !attacker.Alive {
		return ""
	}

	// 在攻击范围内找血量最低的存活对手
	var target *Player
	for _, p := range w.players {
		if p.ID == attackerID || !p.Alive {
			continue
		}
		dist := math.Abs(float64(attacker.X-p.X)) + math.Abs(float64(attacker.Y-p.Y))
		if dist > float64(protocol.AttackRange) {
			continue
		}
		if target == nil || p.HP < target.HP {
			target = p
		}
	}

	if target == nil {
		return fmt.Sprintf("⚔️  %s 攻击，但范围内没有敌人（范围 %d 格）", attacker.Name, protocol.AttackRange)
	}

	target.HP -= protocol.AttackDmg
	killed := false
	if target.HP <= 0 {
		target.HP = 0
		target.Alive = false
		killed = true
		attacker.Kills++
	}

	event := fmt.Sprintf("⚔️  %s 攻击 %s，造成 %d 伤害！（%s 剩余 %d/%d HP）",
		attacker.Name, target.Name, protocol.AttackDmg, target.Name, target.HP, target.MaxHP)

	if killed {
		killerName := attacker.Name
		victimName := target.Name
		victimConn := target.Conn
		targetID := target.ID
		event += fmt.Sprintf(" 💀 %s 被击杀！5秒后复活...", target.Name)

		// 启动复活 Goroutine：等待 5 秒后重置玩家状态
		go func() {
			time.Sleep(5 * time.Second)
			w.respawn(targetID)
			broadcastFn(fmt.Sprintf("🔄 %s 已复活！", victimName))
			// 通知被击杀玩家
			victimConn.Send(protocol.Message{
				Type: protocol.TypeEvent,
				Text: fmt.Sprintf("你被 %s 击杀，已复活！", killerName),
			})
		}()
	}

	return event
}

// HealPlayer 让指定玩家使用一瓶药水，返回事件字符串。
// 并发安全：调用时获取写锁。
func (w *World) HealPlayer(id int) string {
	w.mu.Lock()
	defer w.mu.Unlock()

	p, ok := w.players[id]
	if !ok || !p.Alive {
		return ""
	}
	if p.Potions <= 0 {
		return fmt.Sprintf("🧪 %s 药水已耗尽！", p.Name)
	}
	p.Potions--
	before := p.HP
	p.HP += protocol.HealAmt
	if p.HP > p.MaxHP {
		p.HP = p.MaxHP
	}
	return fmt.Sprintf("🧪 %s 使用药水，恢复 %d HP（%d/%d，剩余 %d 瓶）",
		p.Name, p.HP-before, p.HP, p.MaxHP, p.Potions)
}

// respawn 在锁外调用可能导致死锁，因此单独提取并在写锁中调用。
// 注意：此函数由 AttackPlayer 内的 goroutine 通过 w.mu.Lock() 单独调用。
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

// ─── 读操作（只需读锁） ──────────────────────────────────────────────────────

// GetSnapshot 返回当前所有玩家状态的快照切片，用于广播。
// 并发安全：使用读锁，允许与其他读操作并发。
func (w *World) GetSnapshot() []protocol.PlayerInfo {
	w.mu.RLock()
	defer w.mu.RUnlock()

	infos := make([]protocol.PlayerInfo, 0, len(w.players))
	for _, p := range w.players {
		infos = append(infos, p.toInfo())
	}
	return infos
}

// BroadcastAll 向所有在线玩家发送消息。
// 并发安全：先在读锁下收集 Conn 列表，再释放锁后逐个发送。
func (w *World) BroadcastAll(msg protocol.Message) {
	w.mu.RLock()
	conns := make([]*protocol.Conn, 0, len(w.players))
	for _, p := range w.players {
		conns = append(conns, p.Conn)
	}
	w.mu.RUnlock()

	// 锁外发送，避免在持锁期间阻塞在网络 I/O
	for _, c := range conns {
		c.Send(msg)
	}
}

// BroadcastEvent 向所有玩家广播一条纯文本事件。
func (w *World) BroadcastEvent(text string) {
	fmt.Printf("[事件] %s\n", text)
	w.BroadcastAll(protocol.Message{Type: protocol.TypeEvent, Text: text})
}
