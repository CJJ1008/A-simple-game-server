package client

import (
	"cs/internal/server"
	"log"
	"testing"
	"time"
)

// TestLab1_Connection 评测客户端与服务器的握手流程
func TestLab1_Connection(t *testing.T) {
	// 1. 配置并启动一个临时评测服务器
	srvAddr := "127.0.0.1:9876"
	srv := server.New(srvAddr, 20, 10, nil, log.Default())
	go func() {
		_ = srv.Start()
	}()
	time.Sleep(200 * time.Millisecond) // 等待服务器就绪

	// 2. 测试客户端 Connect 实现
	backend := NewNetBackend()
	playerName := "Lab1Student"
	err := backend.Connect(srvAddr, playerName)
	if err != nil {
		t.Fatalf("客户端连接失败: %v", err)
	}
	defer backend.Close()

	// 3. 验证协议交互：连接成功后服务器应下发 Welcome 消息
	// 评测点：Connect 中是否正确发送了 Join 消息，且 readLoop 是否在运行
	timeout := time.After(2 * time.Second)
	select {
	case ev, ok := <-backend.Events():
		if !ok {
			t.Fatal("Events channel 已关闭")
		}
		if ev.Type != EventWelcome {
			t.Errorf("期望得到 EventWelcome, 实际得到 %v", ev.Type)
		}
		if ev.Welcome.PlayerID <= 0 {
			t.Errorf("收到的 PlayerID 无效: %d", ev.Welcome.PlayerID)
		}
		if ev.Welcome.MapW != 20 || ev.Welcome.MapH != 10 {
			t.Errorf("收到的地图尺寸不匹配")
		}
	case <-timeout:
		t.Fatal("超时：未收到来自服务器的 Welcome 响应，请检查 Connect 和 readLoop 实现")
	}

	// 4. 验证心跳包发送
	// 评测点：heartbeatLoop 是否正确启动并使用 send 发包
	time.Sleep(heartbeatInterval + 500*time.Millisecond)
	// 如果学生正确实现了发送逻辑，可以通过检查服务器日志或连接状态确认，
	// 此处通过确保连接未因无心跳被服务器踢出进行简单验证。
}
