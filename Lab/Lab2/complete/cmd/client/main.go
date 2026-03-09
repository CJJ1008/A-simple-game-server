// client 是 BattleWorld Lab2 的多人客户端。
//
// 与 Lab1 客户端的关键区别（Goroutine 的客户端应用）：
//
//   Lab1：单线程。等待服务器 your_turn → 读键盘 → 发送 → 等待服务器响应
//   Lab2：两个 Goroutine 并行：
//     ├─ 接收 Goroutine：持续读取服务器推送的状态广播和事件
//     └─ 输入 Goroutine（main）：持续读取键盘输入并发送指令
//
// 两个 Goroutine 通过 done channel 协调退出。
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"

	"battleworld/protocol"
)

const serverAddr = "localhost:9001"

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
	conn.Send(protocol.Message{Type: protocol.TypeJoin, Text: name})
	fmt.Println("✅ 已连接，开始游戏！（随时可输入指令）\n")

	var myID int
	done := make(chan struct{})

	// ★ 核心并发点：启动独立 Goroutine 专门负责接收服务器消息
	//   这样 main goroutine 可以同时阻塞在键盘读取，两者互不干扰
	go func() {
		defer close(done)
		for {
			msg, err := conn.Receive()
			if err != nil {
				fmt.Println("\n与服务器的连接已断开。")
				return
			}
			switch msg.Type {
			case protocol.TypeInit:
				myID = msg.YourID
				fmt.Printf("🎮 %s（你的ID: %d）\n", msg.Text, myID)
				printHelp()
			case protocol.TypeBroadcast:
				renderState(msg, myID)
			case protocol.TypeEvent:
				fmt.Printf("\r📢 %s\n> ", msg.Text)
			case protocol.TypeGameOver:
				fmt.Printf("\n💀 游戏通知: %s\n> ", msg.Winner)
			}
		}
	}()

	// main goroutine：持续读取键盘输入
	for {
		select {
		case <-done:
			return
		default:
		}
		fmt.Print("> ")
		in, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		in = strings.TrimSpace(strings.ToLower(in))
		if in == "" {
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
			printHelp()
			continue
		case "q", "quit":
			return
		default:
			fmt.Println("  ⚠ 未知指令，输入 ? 查看帮助")
			continue
		}
		conn.Send(msg)
	}
}

func printHelp() {
	fmt.Println("  ┌──── 操作指令 ─────────────────────────┐")
	fmt.Println("  │  w/s/a/d → 上/下/左/右 移动            │")
	fmt.Println("  │  f       → 攻击（攻击范围内最弱敌人）  │")
	fmt.Println("  │  h       → 使用药水                    │")
	fmt.Println("  │  ?/help  → 显示帮助  q/quit → 退出     │")
	fmt.Println("  └────────────────────────────────────────┘")
}

func renderState(msg protocol.Message, myID int) {
	fmt.Printf("\r\033[K") // 清除当前行
	fmt.Println("─── 战场快报 ───────────────────────────────────")
	for _, p := range msg.Players {
		tag := "  "
		if p.ID == myID {
			tag = "▶ "
		}
		status := "存活"
		if !p.Alive {
			status = "☠ 复活中"
		}
		bar := hpBar(p.HP, p.MaxHP, 8)
		fmt.Printf("  %s%-10s %s 💊%d 🗡%d 📍(%2d,%2d) %s\n",
			tag, p.Name, bar, p.Potions, p.Kills, p.X, p.Y, status)
	}
	fmt.Println("────────────────────────────────────────────────")
	fmt.Print("> ")
}

func hpBar(hp, maxHP, w int) string {
	filled := 0
	if maxHP > 0 {
		filled = hp * w / maxHP
	}
	return fmt.Sprintf("[%s%s]%3d",
		strings.Repeat("█", filled), strings.Repeat("░", w-filled), hp)
}
