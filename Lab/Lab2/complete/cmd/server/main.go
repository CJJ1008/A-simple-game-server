// server 是 BattleWorld Lab2 的多人并发服务端。
//
// 并发架构（相比 Lab1 的核心变化）：
//
//   ┌─────────────────────────────────────────────────────────┐
//   │  main goroutine                                         │
//   │    └─ net.Accept() 循环，每接受一个连接立即启动：        │
//   │         ├─ go handleClient(...)  ← 每人一个 Goroutine   │
//   │         └─ （继续等待下一个连接）                        │
//   │  broadcast goroutine（独立）                             │
//   │    └─ 每 500ms 向所有玩家推送全量世界状态               │
//   └─────────────────────────────────────────────────────────┘
//
// 共享状态 world.World 通过 sync.RWMutex 保证并发安全。
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

	// 启动独立的状态广播 Goroutine
	// 每 500ms 推送一次全量世界快照给所有在线玩家
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			snapshot := w.GetSnapshot()
			if len(snapshot) == 0 {
				continue
			}
			w.BroadcastAll(protocol.Message{
				Type:    protocol.TypeBroadcast,
				Players: snapshot,
			})
		}
	}()

	// 主循环：持续接受新连接
	for {
		raw, err := ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "接受连接错误: %v\n", err)
			continue
		}
		// ★ 核心并发点：为每个客户端启动独立 Goroutine
		//   这使得多个玩家可以同时游戏，互不阻塞
		go handleClient(w, raw)
	}
}

// handleClient 在独立 Goroutine 中处理一个客户端的全生命周期。
// 流程：握手(Join) → 游戏循环(接收指令并更新世界) → 退出(RemovePlayer)
func handleClient(w *world.World, raw net.Conn) {
	conn := protocol.NewConn(raw)
	defer conn.Close()

	// 1. 等待 Join 消息
	joinMsg, err := conn.Receive()
	if err != nil || joinMsg.Type != protocol.TypeJoin {
		return
	}
	name := joinMsg.Text
	if name == "" {
		name = "匿名玩家"
	}

	// 2. 加入世界，获得 ID 和初始位置
	id, player := w.AddPlayer(name, conn)
	defer w.RemovePlayer(id)

	fmt.Printf("✅ [%s] 加入游戏（ID=%d，位置=(%d,%d)）\n", name, id, player.X, player.Y)
	w.BroadcastEvent(fmt.Sprintf("🆕 %s 加入了战场！", name))

	// 3. 向客户端发送初始化消息
	conn.Send(protocol.Message{
		Type:   protocol.TypeInit,
		YourID: id,
		Text:   fmt.Sprintf("欢迎，%s！你在 (%d,%d)", name, player.X, player.Y),
	})

	// 4. 游戏循环：处理该玩家的所有后续指令
	for {
		msg, err := conn.Receive()
		if err != nil {
			// 连接断开，退出循环，defer 会自动 RemovePlayer
			break
		}
		var event string
		switch msg.Type {
		case protocol.TypeMove:
			event = w.MovePlayer(id, msg.Dir)
		case protocol.TypeAttack:
			// 传入 BroadcastEvent 回调，供复活 Goroutine 广播消息
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
