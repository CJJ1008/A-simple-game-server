package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"battleworld/cluster"
	"battleworld/protocol"
	"battleworld/storage"
)

func main() {
	dataRoot := resolveDataRoot()
	store, err := storage.NewStore(dataRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化存储失败：%v\n", err)
		os.Exit(1)
	}

	gameCluster, err := cluster.NewCluster(store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化集群失败：%v\n", err)
		os.Exit(1)
	}
	if err := gameCluster.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "启动集群失败：%v\n", err)
		os.Exit(1)
	}

	ln, err := net.Listen("tcp", protocol.GatewayAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "网关监听失败：%v\n", err)
		os.Exit(1)
	}
	defer ln.Close()

	fmt.Printf("网关已启动：%s\n", protocol.GatewayAddr)
	fmt.Println("控制面节点：node-a=9311 node-b=9312 node-c=9313")

	for {
		raw, err := ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "接入连接失败：%v\n", err)
			continue
		}
		go handleClient(gameCluster, raw)
	}
}

func resolveDataRoot() string {
	if root := strings.TrimSpace(os.Getenv("LAB3_DATA_ROOT")); root != "" {
		return root
	}
	return "."
}

func handleClient(gameCluster *cluster.Cluster, raw net.Conn) {
	conn := protocol.NewConn(raw)
	defer conn.Close()

	authMsg, err := conn.Receive()
	if err != nil {
		return
	}

	if authMsg.Type == protocol.TypeAdmin {
		text, err := gameCluster.ExecuteAdmin(authMsg.Action, authMsg.NodeID)
		if err != nil {
			_ = conn.Send(protocol.Message{Type: protocol.TypeError, Error: err.Error()})
			return
		}
		_ = conn.Send(protocol.Message{Type: protocol.TypeAdmin, OK: true, Text: text})
		return
	}

	var state *protocol.WorldState
	switch authMsg.Type {
	case protocol.TypeRegister:
		if err := gameCluster.Register(authMsg.Username, authMsg.Password, authMsg.Confirm); err != nil {
			_ = conn.Send(protocol.Message{Type: protocol.TypeError, Error: err.Error()})
			return
		}
		state, err = gameCluster.Login(authMsg.Username, authMsg.Password)
	case protocol.TypeLogin:
		state, err = gameCluster.Login(authMsg.Username, authMsg.Password)
	case protocol.TypeQuickEnter:
		state, err = gameCluster.QuickEnter(authMsg.Username, authMsg.Password)
	default:
		_ = conn.Send(protocol.Message{Type: protocol.TypeError, Error: "首条消息必须是登录或注册请求"})
		return
	}
	if err != nil {
		_ = conn.Send(protocol.Message{Type: protocol.TypeError, Error: err.Error()})
		return
	}

	username := authMsg.Username
	if err := conn.Send(protocol.Message{Type: protocol.TypeAuth, OK: true, State: state}); err != nil {
		_ = gameCluster.Logout(username)
		return
	}

	var once sync.Once
	done := make(chan struct{})
	stop := func() {
		once.Do(func() {
			close(done)
			_ = gameCluster.Logout(username)
		})
	}
	defer stop()

	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ticker.C:
				state, err := gameCluster.SnapshotFor(username)
				if err != nil {
					_ = conn.Send(protocol.Message{Type: protocol.TypeError, Error: err.Error()})
					stop()
					return
				}
				if err := conn.Send(protocol.Message{Type: protocol.TypeState, State: state}); err != nil {
					stop()
					return
				}
			case <-done:
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
		}

		msg, err := conn.Receive()
		if err != nil {
			return
		}

		var next *protocol.WorldState
		switch msg.Type {
		case protocol.TypeMove:
			next, err = gameCluster.Move(username, msg.Dir)
		case protocol.TypeAttack:
			next, err = gameCluster.Attack(username)
		case protocol.TypeBossAttack:
			next, err = gameCluster.AttackBoss(username)
		case protocol.TypeHeal:
			next, err = gameCluster.Heal(username)
		case protocol.TypeShop:
			next, err = gameCluster.BuyItem(username, msg.Item)
		case protocol.TypeSwitchMap:
			next, err = gameCluster.SwitchMap(username, msg.MapID)
		case protocol.TypeLogout:
			return
		default:
			err = fmt.Errorf("未知指令：%q", msg.Type)
		}

		if err != nil {
			if sendErr := conn.Send(protocol.Message{Type: protocol.TypeError, Error: err.Error()}); sendErr != nil {
				return
			}
			continue
		}

		if next != nil {
			if err := conn.Send(protocol.Message{Type: protocol.TypeState, State: next}); err != nil {
				return
			}
		}
	}
}
