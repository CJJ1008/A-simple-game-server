// server 是 BattleWorld Lab1 的服务端程序。
//
// 架构：C/S（Client/Server）
//   - 服务器顺序接受恰好 2 个 TCP 连接
//   - 接受完毕后将连接交给 game.Game 进行回合制主循环
//   - 所有游戏逻辑在服务端执行（服务端权威模型）
//   - 客户端仅负责用户 I/O 和消息收发
//
// 启动方式：
//   go run ./server
package main

import (
	"fmt"
	"net"
	"os"

	"battleworld/game"
	"battleworld/protocol"
)

const (
	addr       = ":9000"
	numPlayers = 2
)

func main() {
    ln, err := net.Listen("tcp", addr)
    if err != nil {
        fmt.Fprintf(os.Stderr, "监听失败: %v\n", err)
        os.Exit(1)
    }
    defer ln.Close()
    fmt.Printf("🎮 BattleWorld 服务器启动，监听 %s\n等待 %d 名玩家...\n\n", addr, numPlayers)

    // 修复点 1：使用长度为 0、容量为 numPlayers 的动态切片，而不是提前分配满长度。
    // 这样只有 append 成功时，玩家数量才会增加。
    players := make([]*game.Player, 0, numPlayers)

    // 修复点 2：循环条件改为基于已成功连接的合法玩家数量，而不是固定的迭代次数。
    for len(players) < numPlayers {
        i := len(players) // 当前正在分配的玩家索引 (0 或 1)

        raw, err := ln.Accept()
        if err != nil {
            fmt.Fprintf(os.Stderr, "接受连接失败: %v\n", err)
            // 如果 Accept 失败，底层的 raw connection 通常是 nil，直接重试即可。
            continue
        }
        
        conn := protocol.NewConn(raw)

        // 客户端第一条消息：TypeJoin，Text 字段为玩家名
        msg, err := conn.Receive()
        if err != nil || msg.Type != protocol.TypeJoin {
            fmt.Fprintf(os.Stderr, "等待 join 消息失败: %v\n", err)
            // 彻底释放这个非法连接的句柄
            conn.Close() 
            // 阻断异常，继续等待，且此时 len(players) 不会增加，完美实现容错
            continue
        }

        name := msg.Text
        if name == "" {
            name = fmt.Sprintf("玩家%d", i+1)
        }

        // 玩家 0 在左上角，玩家 1 在右下角
        startX, startY := 0, 0
        if i == 1 {
            startX = protocol.MapWidth - 1
            startY = protocol.MapHeight - 1
        }

        // 只有完整通过了前面的协议验证安全门，才真正实例化 Player 并加入切片
        p := game.NewPlayer(i+1, name, startX, startY, conn)
        players = append(players, p)
        
        fmt.Printf("✅ 玩家 %d [%s] 已连接 → 起始位置 (%d,%d)\n", i+1, name, startX, startY)

        // 向新玩家发送初始化消息（含其 ID）
        conn.Send(protocol.Message{
            Type:   protocol.TypeInit,
            YourID: i + 1,
            Text:   fmt.Sprintf("欢迎，%s！等待对手...", name),
        })
    }

    fmt.Println("\n🚀 双方就绪，游戏开始！\n")
    g := game.NewGame(players[0], players[1])
    g.Run()
    fmt.Println("\n🏁 本局结束，服务器退出。")
}
