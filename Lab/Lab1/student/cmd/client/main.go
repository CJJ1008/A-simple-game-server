// client 是 BattleWorld Lab1 的异步多线程客户端程序。
//
// 启动方式：
//   go run ./cmd/client
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

const serverAddr = "localhost:9000"

// ─── 客户端全局 UI 状态 ────────────────────────────────────────────────────
var (
	latestSnapshot protocol.Message
	myID           int
	eventLog       []string
	uiMu           sync.Mutex
)

func addEvent(text string) {
	uiMu.Lock()
	defer uiMu.Unlock()
	eventLog = append(eventLog, text)
	if len(eventLog) > 6 { // 控制日志行数，适配屏幕
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
	// \033[H 将光标移动到屏幕左上角
	sb.WriteString("\033[H")
	// \033[K 清除从光标到行尾的所有内容，防止旧字符残留
	sb.WriteString("═══ BattleWorld 战场 (Lab1) ═══\033[K\n")

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
				grid[p.Y][p.X] = " x " // 阵亡
			} else if p.ID == myID {
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
		sb.WriteString("\033[K\n") // 渲染完一行后清理右侧残留
	}

	// 2. 绘制玩家状态列表
	sb.WriteString("\n─── 战场快报 ─────────────────────────────────────────\033[K\n")
	for _, p := range latestSnapshot.Players {
		tag := "  "
		if p.ID == myID {
			tag = "▶ "
		}
		status := "存活"
		if !p.Alive {
			status = "☠ 阵亡"
		}
		bar := hpBar(p.HP, p.MaxHP, 10)
		sb.WriteString(fmt.Sprintf("%s%-10s %s 💊%d 📍(%2d,%2d) %s\033[K\n",
			tag, p.Name, bar, p.Potions, p.X, p.Y, status))
	}
	sb.WriteString("──────────────────────────────────────────────────────\033[K\n")

	// 3. 绘制近期事件日志
	for _, e := range eventLog {
		sb.WriteString("📢 " + e + "\033[K\n")
	}

	// 4. 清除屏幕下方可能多余的旧数据，并渲染输入提示符
	sb.WriteString("\033[J")
	sb.WriteString("> \033[K")

	fmt.Print(sb.String())
}

func hpBar(hp, maxHP, w int) string {
	filled := 0
	if maxHP > 0 {
		filled = hp * w / maxHP
	}
	return fmt.Sprintf("HP[%s%s]%3d",
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

	// 进入终端的 Alternate Screen Buffer
	fmt.Print("\033[?1049h\033[2J\033[H")
	defer fmt.Print("\033[?1049l")

	raw, err := net.Dial("tcp", serverAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接服务器失败: %v\n", err)
		os.Exit(1)
	}
	defer raw.Close()

	conn := protocol.NewConn(raw)
	conn.Send(protocol.Message{Type: protocol.TypeJoin, Text: name})

	addEvent("✅ 已连接服务器，等待游戏开始...")
	drawUI()

	done := make(chan struct{})

	// 启动独立的 Goroutine 处理网络接收流水线
	go func() {
		defer close(done)
		for {
			msg, err := conn.Receive()
			if err != nil {
				addEvent("与服务器连接断开。")
				drawUI()
				return
			}
			// 整合 Lab1 的所有消息类型到事件日志和状态机中
			switch msg.Type {
			case protocol.TypeInit:
				myID = msg.YourID
				addEvent(fmt.Sprintf("🎮 %s", msg.Text))
				drawUI()
			case protocol.TypeState:
				updateSnapshot(msg)
				drawUI()
			case protocol.TypeEvent:
				addEvent(msg.Text)
				drawUI()
			case protocol.TypeYourTurn:
				addEvent(fmt.Sprintf("⚡ %s", msg.Text))
				drawUI()
			case protocol.TypeWait:
				addEvent(fmt.Sprintf("⏳ %s", msg.Text))
				drawUI()
			case protocol.TypeGameOver:
				addEvent(fmt.Sprintf("🏁 游戏结束！获胜者：【%s】", msg.Winner))
				drawUI()
			}
		}
	}()

	// 主 Goroutine 处理键盘输入流水线
	for {
		select {
		case <-done:
			return
		default:
		}

		in, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		in = strings.TrimSpace(strings.ToLower(in))
		if in == "" {
			drawUI() // 敲击空回车时强制刷新，抹掉换行符残影
			continue
		}

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
		case "?", "help":
			addEvent("操作指令: w/s/a/d(移动) f(攻击) h(药水) q(退出)")
			drawUI()
			continue
		case "q", "quit":
			return
		default:
			addEvent("⚠ 未知指令，输入 ? 查看帮助")
			drawUI()
			continue
		}
		conn.Send(msg)
		// 输入有效指令后立即重绘
		drawUI()
	}
}