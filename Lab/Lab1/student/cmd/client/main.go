// client 是 BattleWorld Lab1 的客户端程序。
//
// 设计原则：
//   - 所有游戏规则由服务器判定，客户端只负责展示与输入
//   - 消息循环：收到 TypeYourTurn → 读取键盘输入 → 发送行动消息
//              收到 TypeWait     → 显示等待提示
//              收到 TypeState    → 渲染当前状态
//              收到 TypeGameOver → 显示结果并退出
//
// 启动方式：
//   go run ./client
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"

	"battleworld/protocol"
)

const serverAddr = "localhost:9000"

func main() {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("请输入你的名字: ")
	name, _ := reader.ReadString('\n')
	name = strings.TrimSpace(name)
	if name == "" {
		name = "无名勇士"
	}

	raw, err := net.Dial("tcp", serverAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接服务器失败: %v\n", err)
		os.Exit(1)
	}
	defer raw.Close()

	conn := protocol.NewConn(raw)
	// 第一步：发送加入消息
	conn.Send(protocol.Message{Type: protocol.TypeJoin, Text: name})
	fmt.Println("✅ 已连接服务器，等待对手...\n")

	// 进入消息主循环
	var myID int
	for {
		msg, err := conn.Receive()
		if err != nil {
			fmt.Println("\n与服务器连接断开。")
			return
		}
		switch msg.Type {
		case protocol.TypeInit:
			myID = msg.YourID
			fmt.Printf("🎮 %s\n", msg.Text)

		case protocol.TypeEvent:
			fmt.Printf("📢 %s\n", msg.Text)

		case protocol.TypeState:
			renderState(msg, myID)

		case protocol.TypeYourTurn:
			fmt.Printf("\n⚡ %s\n", msg.Text)
			sendAction(conn, reader)

		case protocol.TypeWait:
			fmt.Printf("⏳ %s\n", msg.Text)

		case protocol.TypeGameOver:
			fmt.Printf("\n🏁 游戏结束！获胜者：【%s】\n", msg.Winner)
			return
		}
	}
}

// sendAction 显示操作菜单，读取用户输入并发送对应消息。
func sendAction(conn *protocol.Conn, reader *bufio.Reader) {
	fmt.Println("  ┌──── 操作 ────────────────────────┐")
	fmt.Println("  │  w/s/a/d  → 上/下/左/右  移动    │")
	fmt.Println("  │  f        → 攻击                  │")
	fmt.Println("  │  h        → 使用药水              │")
	fmt.Println("  └──────────────────────────────────┘")
	fmt.Print("  > ")

	in, _ := reader.ReadString('\n')
	in = strings.TrimSpace(strings.ToLower(in))

	var msg protocol.Message
	switch in {
	case "w":
		msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirUp}
	case "s":
		msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirDown}
	case "a":
		msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirLeft}
	case "d":
		msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirRight}
	case "f":
		msg = protocol.Message{Type: protocol.TypeAttack}
	case "h":
		msg = protocol.Message{Type: protocol.TypeHeal}
	default:
		fmt.Println("  ⚠ 未知输入，默认向上移动")
		msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirUp}
	}
	conn.Send(msg)
}

// renderState 在终端渲染玩家状态和小地图。
func renderState(msg protocol.Message, myID int) {
	fmt.Println("\n  ╔════════════ 当前状态 ════════════╗")
	for _, p := range msg.Players {
		tag := "  "
		if p.ID == myID {
			tag = "▶ "
		}
		alive := "存活"
		if !p.Alive {
			alive = "☠ 阵亡"
		}
		bar := hpBar(p.HP, p.MaxHP, 10)
		fmt.Printf("  ║ %s%-10s %s 💊%d 📍(%d,%d) %s\n",
			tag, p.Name, bar, p.Potions, p.X, p.Y, alive)
	}
	fmt.Println("  ╚═════════════════════════════════╝")
	renderMap(msg.Players, myID)
}

func hpBar(hp, maxHP, w int) string {
	filled := 0
	if maxHP > 0 {
		filled = hp * w / maxHP
	}
	return fmt.Sprintf("HP[%s%s]%3d",
		strings.Repeat("█", filled), strings.Repeat("░", w-filled), hp)
}

func renderMap(players []protocol.PlayerInfo, myID int) {
	grid := make([][]rune, protocol.MapHeight)
	for i := range grid {
		row := make([]rune, protocol.MapWidth)
		for j := range row {
			row[j] = '·'
		}
		grid[i] = row
	}
	for _, p := range players {
		if !p.Alive || p.X < 0 || p.X >= protocol.MapWidth || p.Y < 0 || p.Y >= protocol.MapHeight {
			continue
		}
		if p.ID == myID {
			grid[p.Y][p.X] = 'M'
		} else {
			grid[p.Y][p.X] = 'E'
		}
	}
	fmt.Println("  地图（M=你  E=敌）:")
	for _, row := range grid {
		fmt.Print("  ")
		for _, c := range row {
			fmt.Printf("%c ", c)
		}
		fmt.Println()
	}
}
