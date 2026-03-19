// client 是 BattleWorld Lab2 的多人客户端。
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	"battleworld/protocol"
)

const serverAddr = "localhost:9001"

// ─── 客户端全局 UI 状态 ────────────────────────────────────────────────────
var (
	latestSnapshot protocol.Message
	myPlayerID     int
	eventLog       []string
	uiMu           sync.Mutex
)

func addEvent(text string) {
	uiMu.Lock()
	defer uiMu.Unlock()
	eventLog = append(eventLog, text)
	if len(eventLog) > 6 { // 控制日志行数，适配小屏幕
		eventLog = eventLog[len(eventLog)-6:]
	}
}

func updateSnapshot(msg protocol.Message) {
	uiMu.Lock()
	defer uiMu.Unlock()
	latestSnapshot = msg
}

func drawUI() {
	uiMu.Lock()
	defer uiMu.Unlock()

	var sb strings.Builder
	// \033[H 将光标移动到屏幕左上角 (0,0)
	sb.WriteString("\033[H")
	// \033[K 是核心：清除从光标到行尾的所有内容，防止旧的过长字符残留
	sb.WriteString("═══ BattleWorld 战场 ═══\033[K\n")

	// 1. 绘制 2D 地图
	grid := make([][]string, protocol.MapHeight)
	for i := range grid {
		grid[i] = make([]string, protocol.MapWidth)
		for j := range grid[i] {
			grid[i][j] = " . "
		}
	}

	for _, p := range latestSnapshot.Players {
		if p.X >= 0 && p.X < protocol.MapWidth && p.Y >= 0 && p.Y < protocol.MapHeight {
			if !p.Alive {
				grid[p.Y][p.X] = " x " // 尸体
			} else if p.ID == myPlayerID {
				grid[p.Y][p.X] = " @ " // 自己
			} else {
				grid[p.Y][p.X] = " * " // 敌人
			}
		}
	}

	for _, row := range grid {
		for _, cell := range row {
			sb.WriteString(cell)
		}
		sb.WriteString("\033[K\n") // 每画完一行都要清空右侧残留
	}

	// 2. 绘制玩家状态列表
	sb.WriteString("\n─── 战场快报 ─────────────────────────────────────────\033[K\n")
	for _, p := range latestSnapshot.Players {
		tag := "  "
		if p.ID == myPlayerID {
			tag = "▶ "
		}
		status := "存活"
		if !p.Alive {
			status = "☠ 复活中"
		}
		bar := hpBar(p.HP, p.MaxHP, 8)
		sb.WriteString(fmt.Sprintf("%s%-10s %s 💊%d 🗡%d 📍(%2d,%2d) %s\033[K\n",
			tag, p.Name, bar, p.Potions, p.Kills, p.X, p.Y, status))
	}
	sb.WriteString("──────────────────────────────────────────────────────\033[K\n")

	// 3. 绘制近期事件日志
	for _, e := range eventLog {
		sb.WriteString("📢 " + e + "\033[K\n")
	}

	// 4. \033[J 清除屏幕下方可能多余的旧事件行
	sb.WriteString("\033[J")
	sb.WriteString("> \033[K")

	fmt.Print(sb.String())
}

func hpBar(hp, maxHP, w int) string {
	filled := 0
	if maxHP > 0 {
		filled = hp * w / maxHP
	}
	return fmt.Sprintf("[%s%s]%3d",
		strings.Repeat("█", filled), strings.Repeat("░", w-filled), hp)
}

func main() {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("请输入你的名字: ")
	name, _ := reader.ReadString('\n')
	name = strings.TrimSpace(name)
	if name == "" {
		name = "无名勇士"
	}

	// ★ 核心修复：进入终端的 Alternate Screen Buffer (类似 vim 的全屏模式)
	// 这样就不会污染终端滚动历史，也不会因为超出高度导致画面无限下卷
	fmt.Print("\033[?1049h\033[2J\033[H")
	// 退出程序时，恢复终端原本的视图
	defer fmt.Print("\033[?1049l")

	raw, err := net.Dial("tcp", serverAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接服务器失败: %v\n", err)
		os.Exit(1)
	}
	defer raw.Close()

	conn := protocol.NewConn(raw)
	conn.Send(protocol.Message{Type: protocol.TypeJoin, Text: name})

	addEvent("✅ 已连接，开始游戏！（随时可输入指令）")
	addEvent("⌨ 即时按键：W/A/S/D 移动 F 攻击 H 治疗 Q 退出")

	restoreTTY, err := enterRawMode()
	if err != nil {
		fmt.Fprintf(os.Stderr, "切换终端即时输入模式失败: %v\n", err)
		os.Exit(1)
	}
	defer restoreTTY()

	done := make(chan struct{})

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

	keyReader := bufio.NewReader(os.Stdin)
	for {
		select {
		case <-done:
			return
		default:
		}

		key, err := keyReader.ReadByte()
		if err != nil {
			return
		}
		if key == 3 {
			return
		}
		if key == '\r' || key == '\n' {
			continue
		}

		var msg protocol.Message
		switch key {
		case 'w', 'W':
			msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirUp}
		case 's', 'S':
			msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirDown}
		case 'a', 'A':
			msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirLeft}
		case 'd', 'D':
			msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirRight}
		case 'f', 'F':
			msg = protocol.Message{Type: protocol.TypeAttack}
		case 'h', 'H':
			msg = protocol.Message{Type: protocol.TypeHeal}
		case '?':
			addEvent("操作指令: W/A/S/D(移动) F(攻击) H(治疗) Q(退出)")
			drawUI()
			continue
		case 'q', 'Q':
			return
		default:
			continue
		}
		conn.Send(msg)
		// 输入指令后立即强制重绘，将输入框的残余字符抹掉
		drawUI()
	}
}
