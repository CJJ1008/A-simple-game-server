// client 是 BattleWorld Lab2 的多人客户端。
//
// ┌─────────────────────────────────────────────────────────────────────┐
// │  实验任务 E：启动接收 Goroutine，实现并发 I/O                       │
// │                                                                     │
// │  背景：                                                             │
// │    Lab1 客户端是单线程的：只有在 TypeYourTurn 时才读键盘。          │
// │    Lab2 是实时多人游戏：服务器随时推送状态，玩家随时可发指令。      │
// │    若用单线程，读服务器和读键盘只能二选一，无法兼顾。               │
// │                                                                     │
// │  解决方案（Goroutine 的典型应用）：                                 │
// │    ├─ Goroutine 1（main）：专门阻塞读键盘，发送指令                │
// │    └─ Goroutine 2（go ...）：专门阻塞读服务器，渲染状态            │
// │    两者并发，通过 done channel 协调退出。                           │
// └─────────────────────────────────────────────────────────────────────┘
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
	fmt.Println("✅ 已连接，开始游戏！\n")

	var myID int
	done := make(chan struct{})

	// ╔═══════════════════════════════════════════════════════════════════╗
	// ║  任务 E：启动接收 Goroutine                                      ║
	// ║                                                                   ║
	// ║  功能：在后台持续接收服务器消息，根据消息类型做对应展示：         ║
	// ║    · TypeInit      → 保存 myID，显示欢迎信息，打印帮助            ║
	// ║    · TypeBroadcast → 调用 renderState(msg, myID) 渲染状态        ║
	// ║    · TypeEvent     → 打印事件文本                                 ║
	// ║    · TypeGameOver  → 打印通知                                     ║
	// ║    · 连接错误时    → 打印断线消息，close(done)，return            ║
	// ║                                                                   ║
	// ║  要求：整个接收循环必须以 go func(){...}() 启动，                ║
	// ║        这样 main goroutine 才能同时阻塞在键盘读取。               ║
	// ║                                                                   ║
	// ║  提示框架：                                                       ║
	// ║    go func() {                                                    ║
	// ║        defer close(done)                                          ║
	// ║        for {                                                      ║
	// ║            msg, err := conn.Receive()                            ║
	// ║            if err != nil { fmt.Println("断开"); return }         ║
	// ║            switch msg.Type {                                      ║
	// ║            case protocol.TypeInit:                                ║
	// ║                myID = msg.YourID                                  ║
	// ║                fmt.Printf("🎮 %s\n", msg.Text)                   ║
	// ║                printHelp()                                        ║
	// ║            case protocol.TypeBroadcast:                           ║
	// ║                renderState(msg, myID)                             ║
	// ║            case protocol.TypeEvent:                               ║
	// ║                fmt.Printf("\r📢 %s\n> ", msg.Text)               ║
	// ║            case protocol.TypeGameOver:                            ║
	// ║                fmt.Printf("\n💀 %s\n> ", msg.Winner)             ║
	// ║            }                                                      ║
	// ║        }                                                          ║
	// ║    }()                                                            ║
	// ╚═══════════════════════════════════════════════════════════════════╝

	// TODO E: 在此处启动接收 Goroutine
	// （删除下面这行 _ = done 后，填入上方提示框架）
	_ = myID // 防止报错，实现后删除
	_ = done // 防止报错，实现后删除

	// main goroutine：持续读取键盘输入并发送，已实现，无需修改
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

// printHelp 显示操作帮助，已实现，无需修改。
func printHelp() {
	fmt.Println("  ┌──── 操作 ─────────────────────────────┐")
	fmt.Println("  │  w/s/a/d → 上/下/左/右               │")
	fmt.Println("  │  f       → 攻击   h → 药水           │")
	fmt.Println("  │  ?/help  → 帮助   q → 退出           │")
	fmt.Println("  └───────────────────────────────────────┘")
}

// renderState 渲染状态，已实现，无需修改。
func renderState(msg protocol.Message, myID int) {
	fmt.Printf("\r\033[K")
	fmt.Println("─── 战场快报 ───────────────────────────────")
	for _, p := range msg.Players {
		tag := "  "
		if p.ID == myID {
			tag = "▶ "
		}
		status := "存活"
		if !p.Alive {
			status = "☠"
		}
		bar := hpBar(p.HP, p.MaxHP, 8)
		fmt.Printf("  %s%-10s %s 💊%d 🗡%d 📍(%2d,%2d) %s\n",
			tag, p.Name, bar, p.Potions, p.Kills, p.X, p.Y, status)
	}
	fmt.Println("────────────────────────────────────────────")
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
