package client

import (
	"encoding/json"
	"net"
	"sync"
	"time"

	"cs/internal/proto"
)

type NetBackend struct {
	conn       net.Conn
	enc        *json.Encoder
	dec        *json.Decoder
	mu         sync.Mutex
	events     chan Event
	closed     chan struct{}
	closeOnce  sync.Once
	closedOnce sync.Once
}

const heartbeatInterval = 5 * time.Second

func NewNetBackend() *NetBackend {
	return &NetBackend{
		events: make(chan Event, 32),
		closed: make(chan struct{}),
	}
}


func (b *NetBackend) Connect(addr, name string) error {
	// 【实验挖空：建立连接】
	// 1. 使用 net.Dial 建立到 addr 的 TCP 连接
	// 2. 将连接保存到 b.conn
	// 3. 初始化 b.enc (json.NewEncoder) 和 b.dec (json.NewDecoder)

	// 【实验挖空：发送初始 Join 消息】
	// 使用 b.send 发送一条类型为 proto.TypeJoin 的消息，包含玩家姓名 name

	// 启动后台协程
	go b.readLoop()
	go b.heartbeatLoop()
	return nil
}

func (b *NetBackend) Close() error {
	b.closedOnce.Do(func() {
		close(b.closed)
	})
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}

func (b *NetBackend) Events() <-chan Event {
	return b.events
}

func (b *NetBackend) Move(dx, dy int) error {
	return b.send(proto.Message{Type: proto.TypeMove, Move: &proto.Move{DX: dx, DY: dy}})
}

func (b *NetBackend) Challenge(targetID int) error {
	return b.send(proto.Message{Type: proto.TypeChallenge, Challenge: &proto.Challenge{TargetID: targetID}})
}

func (b *NetBackend) RespondChallenge(requestID int, accept bool) error {
	return b.send(proto.Message{Type: proto.TypeChallengeResponse, ChallengeResponse: &proto.ChallengeResponse{RequestID: requestID, Accept: accept}})
}

func (b *NetBackend) send(msg proto.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.enc == nil {
		return nil
	}
	return b.enc.Encode(msg)
}


func (b *NetBackend) readLoop() {
	for {
		// 【实验挖空：消息读取循环】
		// 1. 检查 b.closed 状态
		// 2. 使用 b.dec.Decode 读取一条 proto.Message
		// 3. 如果读取出错，调用 b.closeEvents() 并退出循环
		// 4. 如果读取成功，调用 b.handleMessage(msg) 处理消息
	}
}

func (b *NetBackend) closeEvents() {
	b.closeOnce.Do(func() {
		close(b.events)
	})
}

func (b *NetBackend) handleMessage(msg proto.Message) {
	switch msg.Type {
	case proto.TypeWelcome:
		b.events <- Event{Type: EventWelcome, Welcome: msg.Welcome}
	case proto.TypeState:
		b.events <- Event{Type: EventState, State: msg.State}
	case proto.TypeDeltaState:
		b.events <- Event{Type: EventDeltaState, DeltaState: msg.DeltaState}
	case proto.TypeInfo:
		if msg.Info != nil {
			b.events <- Event{Type: EventInfo, Info: msg.Info.Message}
		}
	case proto.TypeChallengeRequest:
		b.events <- Event{Type: EventChallengeRequest, ChallengeRequest: msg.ChallengeRequest}
	case proto.TypeChallengeResult:
		b.events <- Event{Type: EventChallengeResult, ChallengeResult: msg.ChallengeResult}
	}
}

func (b *NetBackend) heartbeatLoop() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.closed:
			return
		case <-ticker.C:
			_ = b.send(proto.Message{Type: proto.TypeHeartbeat, Heartbeat: &proto.Heartbeat{AtUnix: time.Now().Unix()}})
		}
	}
}
