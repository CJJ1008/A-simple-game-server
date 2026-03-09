// Package game 实现双人回合制对战的核心逻辑。
//
// 游戏规则摘要：
//   - 地图 20×20，玩家 1 从左上角 (0,0) 出发，玩家 2 从右下角 (19,19) 出发
//   - 两人交替行动：移动 / 攻击 / 使用药水
//   - 攻击条件：曼哈顿距离 |Δx|+|Δy| ≤ 2，伤害 30 HP
//   - 药水：每瓶回复 40 HP，上限 MaxHP，初始 3 瓶
//   - 胜负：将对手 HP 降至 0 即获胜
package game

import (
	"fmt"
	"math"

	"battleworld/protocol"
)

// ─── Player ─────────────────────────────────────────────────────────────────

// Player 代表游戏内的一名参与者，持有其网络连接。
type Player struct {
	ID      int
	Name    string
	X, Y    int
	HP      int
	MaxHP   int
	Potions int
	Alive   bool
	Conn    *protocol.Conn
}

// NewPlayer 创建带有默认属性的玩家。
func NewPlayer(id int, name string, x, y int, conn *protocol.Conn) *Player {
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

// ToInfo 将 Player 转换为可序列化的 PlayerInfo 快照。
func (p *Player) ToInfo() protocol.PlayerInfo {
	return protocol.PlayerInfo{
		ID:      p.ID,
		Name:    p.Name,
		X:       p.X,
		Y:       p.Y,
		HP:      p.HP,
		MaxHP:   p.MaxHP,
		Potions: p.Potions,
		Alive:   p.Alive,
	}
}

// ─── Game ────────────────────────────────────────────────────────────────────

// Game 持有双人对局的全部状态。
type Game struct {
	Players [2]*Player
	Turn    int // 当前行动方索引（0 或 1）
	Round   int // 当前回合数（从 1 开始）
}

// NewGame 用两名玩家初始化一局游戏。
func NewGame(p1, p2 *Player) *Game {
	return &Game{Players: [2]*Player{p1, p2}, Turn: 0, Round: 1}
}

// ─── 主循环 ──────────────────────────────────────────────────────────────────

// Run 启动游戏主循环，阻塞直至游戏结束。
// 所有游戏逻辑（权威逻辑）均在此函数及其调用链内执行。
func (g *Game) Run() {
	fmt.Println("[Game] 对局开始")
	g.broadcastEvent(fmt.Sprintf("══ 决斗开始！%s  VS  %s ══", g.Players[0].Name, g.Players[1].Name))
	g.broadcastState()

	for {
		cur := g.Players[g.Turn]
		opp := g.Players[1-g.Turn]

		// 通知双方当前回合状态
		cur.Conn.Send(protocol.Message{
			Type: protocol.TypeYourTurn,
			Text: fmt.Sprintf("第 %d 回合，轮到你行动！", g.Round),
		})
		opp.Conn.Send(protocol.Message{
			Type: protocol.TypeWait,
			Text: fmt.Sprintf("第 %d 回合，等待 %s 行动...", g.Round, cur.Name),
		})

		// 阻塞等待当前玩家的行动指令
		msg, err := cur.Conn.Receive()
		if err != nil {
			g.broadcastEvent(fmt.Sprintf("⚡ %s 断线！%s 自动获胜！", cur.Name, opp.Name))
			g.sendGameOver(opp.Name)
			return
		}

		// 处理行动并广播结果
		if event := g.processAction(cur, opp, msg); event != "" {
			g.broadcastEvent(event)
		}
		g.broadcastState()

		// 胜负判断
		if !opp.Alive {
			g.broadcastEvent(fmt.Sprintf("🏆 %s 击败了 %s！", cur.Name, opp.Name))
			g.sendGameOver(cur.Name)
			return
		}

		// 交换行动方，推进回合
		g.Turn = 1 - g.Turn
		g.Round++
	}
}

// processAction 根据消息类型将行动分发到对应处理函数。
func (g *Game) processAction(actor, target *Player, msg protocol.Message) string {
	switch msg.Type {
	case protocol.TypeMove:
		return g.handleMove(actor, msg.Dir)
	case protocol.TypeAttack:
		return g.handleAttack(actor, target)
	case protocol.TypeHeal:
		return g.handleHeal(actor)
	default:
		return fmt.Sprintf("[%s] 发出未知指令，回合跳过", actor.Name)
	}
}

// ─── 行动处理 ────────────────────────────────────────────────────────────────

// handleMove 将玩家向 dir 方向移动一步，越界时保持原位。
// 坐标系：X 向右增大，Y 向下增大；有效范围 [0, MapWidth) × [0, MapHeight)
func (g *Game) handleMove(p *Player, dir string) string {
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
	default:
		return fmt.Sprintf("[%s] 无效方向 '%s'", p.Name, dir)
	}

	if p.X == oldX && p.Y == oldY {
		return fmt.Sprintf("🚧 %s 向%s走但撞到了边界", p.Name, dirCN(dir))
	}
	return fmt.Sprintf("🚶 %s 向%s移动 → (%d,%d)", p.Name, dirCN(dir), p.X, p.Y)
}

// handleAttack 尝试对目标发起攻击。
// 攻击条件：曼哈顿距离 = |actor.X - target.X| + |actor.Y - target.Y| ≤ AttackRange
// 攻击命中后目标 HP 减少 AttackDmg，HP 不低于 0；若归零则标记 Alive = false。
func (g *Game) handleAttack(actor, target *Player) string {
	dist := math.Abs(float64(actor.X-target.X)) + math.Abs(float64(actor.Y-target.Y))
	if dist > float64(protocol.AttackRange) {
		return fmt.Sprintf("⚔️  %s 攻击失败：%s 在 %.0f 格外（范围 %d 格）",
			actor.Name, target.Name, dist, protocol.AttackRange)
	}

	target.HP -= protocol.AttackDmg
	if target.HP <= 0 {
		target.HP = 0
		target.Alive = false
	}
	return fmt.Sprintf("⚔️  %s 攻击 %s，造成 %d 伤害！（%s 剩余 %d/%d HP）",
		actor.Name, target.Name, protocol.AttackDmg,
		target.Name, target.HP, target.MaxHP)
}

// handleHeal 消耗一瓶药水回复 HP（药水耗尽时提示失败）。
func (g *Game) handleHeal(p *Player) string {
	if p.Potions <= 0 {
		return fmt.Sprintf("🧪 %s 想使用药水，但已耗尽！", p.Name)
	}
	p.Potions--
	before := p.HP
	p.HP += protocol.HealAmt
	if p.HP > p.MaxHP {
		p.HP = p.MaxHP
	}
	return fmt.Sprintf("🧪 %s 使用药水，恢复 %d HP（%d/%d HP，剩余 %d 瓶）",
		p.Name, p.HP-before, p.HP, p.MaxHP, p.Potions)
}

// ─── 广播辅助 ────────────────────────────────────────────────────────────────

func (g *Game) broadcastState() {
	msg := protocol.Message{
		Type:    protocol.TypeState,
		Players: []protocol.PlayerInfo{g.Players[0].ToInfo(), g.Players[1].ToInfo()},
		Turn:    g.Turn,
	}
	for _, p := range g.Players {
		p.Conn.Send(msg)
	}
}

func (g *Game) broadcastEvent(text string) {
	fmt.Printf("[事件] %s\n", text)
	msg := protocol.Message{Type: protocol.TypeEvent, Text: text}
	for _, p := range g.Players {
		p.Conn.Send(msg)
	}
}

func (g *Game) sendGameOver(winner string) {
	msg := protocol.Message{Type: protocol.TypeGameOver, Winner: winner}
	for _, p := range g.Players {
		p.Conn.Send(msg)
	}
}

// ─── 工具函数 ────────────────────────────────────────────────────────────────

func dirCN(dir string) string {
	switch dir {
	case protocol.DirUp:
		return "上"
	case protocol.DirDown:
		return "下"
	case protocol.DirLeft:
		return "左"
	case protocol.DirRight:
		return "右"
	}
	return dir
}
