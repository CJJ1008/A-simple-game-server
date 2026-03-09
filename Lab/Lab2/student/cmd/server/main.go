// server 是 BattleWorld Lab2 的多人并发服务端。
//
// ┌─────────────────────────────────────────────────────────────────────┐
// │  实验任务 D：在正确的位置启动 Goroutine                             │
// │                                                                     │
// │  要求：                                                             │
// │    D-1：为每个新连接启动独立的 handleClient Goroutine               │
// │    D-2：启动定时广播 Goroutine（推送世界快照给所有玩家）            │
// │                                                                     │
// │  关键概念：                                                         │
// │    · go funcName(args) 启动一个新的 Goroutine                       │
// │    · Goroutine 与 main goroutine 并发执行，不阻塞主流程             │
// │    · 多个 handleClient Goroutine 共享同一个 *world.World，          │
// │      对 World 的并发访问由 RWMutex 保护（见 world/world.go）        │
// └─────────────────────────────────────────────────────────────────────┘
package main

import (
	"fmt"
	"net"
	"os"
	"time"

	"battleworld/protocol"
	"battleworld/world"
)

const addr = ":9001"

func main() {
	w := world.NewWorld()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "监听失败: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()
	fmt.Printf("🌍 BattleWorld 多人服务器启动，监听 %s\n", addr)

	// ╔═══════════════════════════════════════════════════════════════════╗
	// ║  任务 D-1：启动广播 Goroutine                                    ║
	// ║                                                                   ║
	// ║  功能：每 500ms 调用 w.GetSnapshot() 获取快照，通过              ║
	// ║        w.BroadcastAll 推送 TypeBroadcast 消息给所有玩家。         ║
	// ║                                                                   ║
	// ║  要求：整个 func 必须以 go func(){...}() 形式在后台运行，        ║
	// ║        不能阻塞 main goroutine 的 Accept 循环。                   ║
	// ║                                                                   ║
	// ║  提示框架：                                                       ║
	// ║    go func() {                                                    ║
	// ║        ticker := time.NewTicker(500 * time.Millisecond)          ║
	// ║        defer ticker.Stop()                                        ║
	// ║        for range ticker.C {                                       ║
	// ║            snapshot := w.GetSnapshot()                           ║
	// ║            if len(snapshot) == 0 { continue }                    ║
	// ║            w.BroadcastAll(protocol.Message{                      ║
	// ║                Type:    protocol.TypeBroadcast,                  ║
	// ║                Players: snapshot,                                 ║
	// ║            })                                                     ║
	// ║        }                                                          ║
	// ║    }()                                                            ║
	// ╚═══════════════════════════════════════════════════════════════════╝

	// TODO D-1: 在此处启动广播 Goroutine
	_ = time.Millisecond // 防止 import 报错，实现后可删除

	// 主循环：持续接受新连接
	for {
		raw, err := ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "接受连接错误: %v\n", err)
			continue
		}

		// ╔═══════════════════════════════════════════════════════════════╗
		// ║  任务 D-2：为每个连接启动 handleClient Goroutine             ║
		// ║                                                               ║
		// ║  功能：调用 handleClient(w, raw)，但必须以 Goroutine 方式    ║
		// ║        启动，否则服务器在处理第一个玩家时无法接受第二个连接。 ║
		// ║                                                               ║
		// ║  要求：将下面这行从普通调用改为 Goroutine 调用。             ║
		// ║                                                               ║
		// ║  错误写法（阻塞）：  handleClient(w, raw)                    ║
		// ║  正确写法（并发）：  go handleClient(w, raw)                 ║
		// ╚═══════════════════════════════════════════════════════════════╝

		// TODO D-2: 将此行改为 Goroutine 调用
		handleClient(w, raw) // ← 请在此行前加 go 关键字
	}
}

// handleClient 处理一个客户端连接的全生命周期，已实现，无需修改。
// 此函数将在独立 Goroutine 中运行（完成 D-2 后）。
func handleClient(w *world.World, raw net.Conn) {
	conn := protocol.NewConn(raw)
	defer conn.Close()

	joinMsg, err := conn.Receive()
	if err != nil || joinMsg.Type != protocol.TypeJoin {
		return
	}
	name := joinMsg.Text
	if name == "" {
		name = "匿名玩家"
	}

	id, player := w.AddPlayer(name, conn)
	defer w.RemovePlayer(id)

	fmt.Printf("✅ [%s] 加入（ID=%d，位置=(%d,%d)）\n", name, id, player.X, player.Y)
	w.BroadcastEvent(fmt.Sprintf("🆕 %s 加入了战场！", name))

	conn.Send(protocol.Message{
		Type:   protocol.TypeInit,
		YourID: id,
		Text:   fmt.Sprintf("欢迎，%s！你在 (%d,%d)", name, player.X, player.Y),
	})

	for {
		msg, err := conn.Receive()
		if err != nil {
			break
		}
		var event string
		switch msg.Type {
		case protocol.TypeMove:
			event = w.MovePlayer(id, msg.Dir)
		case protocol.TypeAttack:
			event = w.AttackPlayer(id, w.BroadcastEvent)
		case protocol.TypeHeal:
			event = w.HealPlayer(id)
		}
		if event != "" {
			w.BroadcastEvent(event)
		}
	}

	fmt.Printf("👋 [%s] 离线\n", name)
	w.BroadcastEvent(fmt.Sprintf("👋 %s 离开了战场", name))
}
