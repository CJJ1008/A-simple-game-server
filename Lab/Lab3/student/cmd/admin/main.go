package main

import (
	"fmt"
	"net"
	"os"

	"battleworld/protocol"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	action := os.Args[1]
	nodeID := ""
	addr := protocol.GatewayAddr
	if action == "状态" || action == "status" {
		if len(os.Args) >= 3 {
			addr = os.Args[2]
		}
	} else if len(os.Args) >= 3 {
		nodeID = os.Args[2]
	}
	if action != "状态" && action != "status" && len(os.Args) >= 4 {
		addr = os.Args[3]
	}

	raw, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接网关失败：%v\n", err)
		os.Exit(1)
	}
	defer raw.Close()

	conn := protocol.NewConn(raw)
	if err := conn.Send(protocol.Message{
		Type:   protocol.TypeAdmin,
		Action: action,
		NodeID: nodeID,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "发送管理指令失败：%v\n", err)
		os.Exit(1)
	}

	reply, err := conn.Receive()
	if err != nil {
		fmt.Fprintf(os.Stderr, "接收管理结果失败：%v\n", err)
		os.Exit(1)
	}
	if reply.Type == protocol.TypeError || !reply.OK {
		fmt.Fprintf(os.Stderr, "管理命令失败：%s\n", reply.Error)
		os.Exit(1)
	}
	fmt.Println(reply.Text)
}

func usage() {
	fmt.Println("用法：")
	fmt.Println("  go run ./cmd/admin 状态")
	fmt.Println("  go run ./cmd/admin 状态 127.0.0.1:9310")
	fmt.Println("  go run ./cmd/admin 故障 node-a")
	fmt.Println("  go run ./cmd/admin 故障 node-a 127.0.0.1:9310")
	fmt.Println("  go run ./cmd/admin 恢复 node-a")
	fmt.Println("  go run ./cmd/admin 恢复 node-a 127.0.0.1:9310")
}
