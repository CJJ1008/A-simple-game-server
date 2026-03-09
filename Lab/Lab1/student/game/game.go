// Package game 实现双人回合制对战的核心逻辑。
//
// ┌─────────────────────────────────────────────────────────────────────┐
// │  实验任务 B：实现 handleMove() 和 handleAttack() 两个函数           │
// │                                                                     │
// │  设计背景（服务端权威模型）：                                        │
// │    客户端只发送"意图"（移动方向 / 攻击），服务器负责验证合法性并     │
// │    更新状态。这避免了客户端作弊，是网络游戏的标准设计。              │
// │                                                                     │
// │  坐标系：                                                           │
// │    X 轴向右为正，Y 轴向下为正。                                     │
// │    有效范围：X ∈ [0, MapWidth-1]，Y ∈ [0, MapHeight-1]             │
// │    DirUp    → Y - 1    DirDown → Y + 1                              │
// │    DirLeft  → X - 1    DirRight → X + 1                             │
// └─────────────────────────────────────────────────────────────────────┘
package game

import (
	"fmt"
	"math"

	"battleworld/protocol"
)

// ─── Player ─────────────────────────────────────────────────────────────────

// Player 代表游戏内的一名参与者。
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

// NewPlayer 创建带有默认属性的玩家，已实现，无需修改。
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

// ToInfo 将 Player 转换为可序列化的 PlayerInfo 快照，已实现，无需修改。
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

// Game 持有双人对局的全部状态，已实现，无需修改。
type Game struct {
	Players [2]*Player
	Turn    int
	Round   int
}

// NewGame 初始化一局游戏，已实现，无需修改。
func NewGame(p1, p2 *Player) *Game {
	return &Game{Players: [2]*Player{p1, p2}, Turn: 0, Round: 1}
}

// Run 游戏主循环，已实现，无需修改。
func (g *Game) Run() {
	fmt.Println("[Game] 对局开始")
	g.broadcastEvent(fmt.Sprintf("══ 决斗开始！%s  VS  %s ══", g.Players[0].Name, g.Players[1].Name))
	g.broadcastState()
	for {
		cur := g.Players[g.Turn]
		opp := g.Players[1-g.Turn]
		cur.Conn.Send(protocol.Message{Type: protocol.TypeYourTurn, Text: fmt.Sprintf("第 %d 回合，轮到你！", g.Round)})
		opp.Conn.Send(protocol.Message{Type: protocol.TypeWait, Text: fmt.Sprintf("第 %d 回合，等待 %s...", g.Round, cur.Name)})
		msg, err := cur.Conn.Receive()
		if err != nil {
			g.broadcastEvent(fmt.Sprintf("%s 断线！%s 自动获胜！", cur.Name, opp.Name))
			g.sendGameOver(opp.Name)
			return
		}
		if event := g.processAction(cur, opp, msg); event != "" {
			g.broadcastEvent(event)
		}
		g.broadcastState()
		if !opp.Alive {
			g.broadcastEvent(fmt.Sprintf("🏆 %s 击败了 %s！", cur.Name, opp.Name))
			g.sendGameOver(cur.Name)
			return
		}
		g.Turn = 1 - g.Turn
		g.Round++
	}
}

// processAction 分发行动，已实现，无需修改。
func (g *Game) processAction(actor, target *Player, msg protocol.Message) string {
	switch msg.Type {
	case protocol.TypeMove:
		return g.handleMove(actor, msg.Dir)
	case protocol.TypeAttack:
		return g.handleAttack(actor, target)
	case protocol.TypeHeal:
		return g.handleHeal(actor)
	default:
		return fmt.Sprintf("[%s] 未知指令，回合跳过", actor.Name)
	}
}

// ╔═════════════════════════════════════════════════════════════════════════╗
// ║  任务 B-1：实现 handleMove 函数                                        ║
// ║                                                                         ║
// ║  功能：根据 dir 将玩家 p 移动一步；越出地图边界时保持原位，             ║
// ║        并返回描述移动结果的中文字符串（供广播给玩家）。                 ║
// ║                                                                         ║
// ║  要求：                                                                 ║
// ║    1. 保存旧坐标 oldX, oldY                                             ║
// ║    2. 用 switch dir 分支处理四个方向，先检查边界再修改坐标              ║
// ║       · DirUp    → p.Y > 0            → p.Y--                          ║
// ║       · DirDown  → p.Y < MapHeight-1  → p.Y++                          ║
// ║       · DirLeft  → p.X > 0            → p.X--                          ║
// ║       · DirRight → p.X < MapWidth-1   → p.X++                          ║
// ║    3. 若坐标未变（撞墙），返回包含"边界"语义的提示字符串               ║
// ║    4. 否则返回包含新坐标的移动成功字符串                                ║
// ║                                                                         ║
// ║  可用常量：protocol.DirUp/Down/Left/Right, MapWidth, MapHeight          ║
// ╚═════════════════════════════════════════════════════════════════════════╝
func (g *Game) handleMove(p *Player, dir string) string {
	// TODO: 实现玩家移动逻辑
	//
	// 参考框架：
	//
	//   oldX, oldY := p.X, p.Y
	//   switch dir {
	//   case protocol.DirUp:
	//       if p.Y > 0 { p.Y-- }
	//   case protocol.DirDown:
	//       // ...
	//   case protocol.DirLeft:
	//       // ...
	//   case protocol.DirRight:
	//       // ...
	//   default:
	//       return fmt.Sprintf("[%s] 无效方向 '%s'", p.Name, dir)
	//   }
	//   if p.X == oldX && p.Y == oldY {
	//       return fmt.Sprintf("🚧 %s 撞到了边界", p.Name)
	//   }
	//   return fmt.Sprintf("🚶 %s 移动到 (%d,%d)", p.Name, p.X, p.Y)

	panic("handleMove 尚未实现，请完成 TODO")
}

// ╔═════════════════════════════════════════════════════════════════════════╗
// ║  任务 B-2：实现 handleAttack 函数                                      ║
// ║                                                                         ║
// ║  功能：actor 向 target 发起攻击。                                       ║
// ║                                                                         ║
// ║  要求：                                                                 ║
// ║    1. 计算曼哈顿距离：dist = |actor.X - target.X| + |actor.Y - target.Y|║
// ║       可使用 math.Abs(float64(...))                                     ║
// ║    2. 若 dist > float64(protocol.AttackRange)，攻击失败，               ║
// ║       返回包含"距离太远"语义的字符串                                    ║
// ║    3. 否则：                                                            ║
// ║       a. target.HP -= protocol.AttackDmg                                ║
// ║       b. 若 target.HP ≤ 0，将 target.HP 置 0，target.Alive = false     ║
// ║       c. 返回包含伤害数值和剩余 HP 的攻击成功字符串                     ║
// ║                                                                         ║
// ║  可用常量：protocol.AttackRange, protocol.AttackDmg                     ║
// ╚═════════════════════════════════════════════════════════════════════════╝
func (g *Game) handleAttack(actor, target *Player) string {
	// TODO: 实现攻击逻辑
	//
	// 步骤提示：
	//   dist := math.Abs(float64(actor.X-target.X)) + math.Abs(float64(actor.Y-target.Y))
	//   if dist > float64(protocol.AttackRange) {
	//       return "攻击失败：目标超出范围..."
	//   }
	//   target.HP -= protocol.AttackDmg
	//   if target.HP <= 0 { target.HP = 0; target.Alive = false }
	//   return "攻击成功..."
	//
	// 注意：直接使用 math.Abs，已导入 math 包

	_ = math.Abs // 防止 import 报错，实现后可删除此行
	panic("handleAttack 尚未实现，请完成 TODO")
}

// handleHeal 使用药水，已实现，无需修改。
func (g *Game) handleHeal(p *Player) string {
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

// ─── 广播辅助（无需修改） ────────────────────────────────────────────────────

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
