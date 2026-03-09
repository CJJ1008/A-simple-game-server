// Package protocol 定义客户端与服务器之间通信所用的消息结构和网络连接封装。
//
// ┌─────────────────────────────────────────────────────────────────────┐
// │  实验任务 A：实现 Send() 和 Receive() 两个函数                      │
// │                                                                     │
// │  知识点：                                                           │
// │    · net.Conn    —— Go 中 TCP 连接的抽象接口                       │
// │    · json.Encoder.Encode(v) —— 将 v 序列化为 JSON 写入流           │
// │    │               每次调用会在末尾自动追加 '\n' 作为消息边界       │
// │    · json.Decoder.Decode(&v) —— 从流中阻塞读取一条 JSON 消息       │
// │    · 关键约束：必须对同一连接持久使用同一 Encoder/Decoder 实例，    │
// │                否则 Decoder 内部缓冲区数据会丢失                   │
// └─────────────────────────────────────────────────────────────────────┘
package protocol

import (
	"encoding/json"
	"net"
)

// ─── 常量（无需修改） ────────────────────────────────────────────────────────

const (
	MapWidth    = 20
	MapHeight   = 20
	InitHP      = 100
	AttackDmg   = 30
	HealAmt     = 40
	MaxPotions  = 3
	AttackRange = 2
)

const (
	TypeJoin   = "join"
	TypeMove   = "move"
	TypeAttack = "attack"
	TypeHeal   = "heal"

	TypeInit     = "init"
	TypeState    = "state"
	TypeEvent    = "event"
	TypeYourTurn = "your_turn"
	TypeWait     = "wait"
	TypeGameOver = "gameover"

	DirUp    = "up"
	DirDown  = "down"
	DirLeft  = "left"
	DirRight = "right"
)

// PlayerInfo 是玩家状态的可序列化快照。
type PlayerInfo struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	HP      int    `json:"hp"`
	MaxHP   int    `json:"max_hp"`
	Potions int    `json:"potions"`
	Alive   bool   `json:"alive"`
}

// Message 是客户端与服务器通信的基本单元。
type Message struct {
	Type    string       `json:"type"`
	Dir     string       `json:"dir,omitempty"`
	Text    string       `json:"text,omitempty"`
	YourID  int          `json:"your_id,omitempty"`
	Players []PlayerInfo `json:"players,omitempty"`
	Turn    int          `json:"turn,omitempty"`
	Winner  string       `json:"winner,omitempty"`
}

// ─── Conn（无需修改结构体和 NewConn） ───────────────────────────────────────

// Conn 将一条 TCP 连接与持久的 JSON 编解码器绑定。
type Conn struct {
	raw     net.Conn
	encoder *json.Encoder
	decoder *json.Decoder
}

// NewConn 构造 Conn，已实现，无需修改。
func NewConn(c net.Conn) *Conn {
	return &Conn{
		raw:     c,
		encoder: json.NewEncoder(c),
		decoder: json.NewDecoder(c),
	}
}

// ╔═════════════════════════════════════════════════════════════════════════╗
// ║  任务 A-1：实现 Send 函数                                              ║
// ║                                                                         ║
// ║  功能：将 msg 序列化为 JSON，通过 TCP 连接发送给对端。                  ║
// ║                                                                         ║
// ║  实现要点：                                                             ║
// ║    使用 c.encoder（*json.Encoder），调用 Encode 方法传入 msg。          ║
// ║    Encode 会自动追加 '\n'，直接返回其错误值即可。                       ║
// ║                                                                         ║
// ║  提示：整个函数体只需 1 行代码。                                        ║
// ╚═════════════════════════════════════════════════════════════════════════╝
func (c *Conn) Send(msg Message) error {
	// TODO: 使用 c.encoder 将 msg 编码并发送
	// 参考：return c.encoder.Encode(???)
	panic("Send 尚未实现，请完成 TODO")
}

// ╔═════════════════════════════════════════════════════════════════════════╗
// ║  任务 A-2：实现 Receive 函数                                           ║
// ║                                                                         ║
// ║  功能：从 TCP 连接阻塞读取一条 JSON 消息并反序列化后返回。              ║
// ║                                                                         ║
// ║  实现要点：                                                             ║
// ║    1. 声明 var msg Message                                              ║
// ║    2. 调用 c.decoder.Decode(&msg)，将结果存入 err                       ║
// ║    3. 返回 msg 和 err                                                   ║
// ║                                                                         ║
// ║  提示：整个函数体约 3 行代码。                                          ║
// ╚═════════════════════════════════════════════════════════════════════════╝
func (c *Conn) Receive() (Message, error) {
	// TODO: 使用 c.decoder 从连接中解码一条消息并返回
	// 步骤：
	//   var msg Message
	//   err := c.decoder.Decode(???)
	//   return ???, ???
	panic("Receive 尚未实现，请完成 TODO")
}

// Close 关闭底层 TCP 连接，已实现，无需修改。
func (c *Conn) Close() {
	c.raw.Close()
}
