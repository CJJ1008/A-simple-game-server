// Package protocol 定义 BattleWorld 多人游戏的消息格式与连接封装。
// 在 Lab1 基础上新增：TypeBroadcast（服务器定期推送全量状态）。
package protocol

import (
	"encoding/json"
	"net"
	"sync"
)

const (
	MapWidth    = 30
	MapHeight   = 20
	InitHP      = 100
	AttackDmg   = 30
	HealAmt     = 40
	MaxPotions  = 5
	AttackRange = 2
)

// 客户端 → 服务器
const (
	TypeJoin   = "join"
	TypeMove   = "move"
	TypeAttack = "attack"
	TypeHeal   = "heal"
)

// 服务器 → 客户端
const (
	TypeInit      = "init"
	TypeBroadcast = "broadcast" // 定期广播：全量世界状态（多人新增）
	TypeEvent     = "event"
	TypeGameOver  = "gameover"
)

const (
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
	Kills   int    `json:"kills"` // 击杀数（多人新增）
}

// Message 是客户端与服务器通信的基本单元。
type Message struct {
	Type    string       `json:"type"`
	Dir     string       `json:"dir,omitempty"`
	Text    string       `json:"text,omitempty"`
	YourID  int          `json:"your_id,omitempty"`
	Players []PlayerInfo `json:"players,omitempty"`
	Winner  string       `json:"winner,omitempty"`
}

// Conn 将 TCP 连接与持久的 JSON 编解码器绑定。
//
// 并发安全说明：
//   - Send 用 sendMu 互斥锁保护，允许多个 Goroutine 同时向同一连接发送消息
//     （例如：广播 Goroutine 和 handleClient Goroutine 可能同时调用 Send）
//   - Receive 不加锁，因为每条连接只有一个 Goroutine 负责读取
type Conn struct {
	raw     net.Conn
	encoder *json.Encoder
	decoder *json.Decoder
	sendMu  sync.Mutex // 保护 encoder，防止并发 Send 导致数据竞争
}

func NewConn(c net.Conn) *Conn {
	return &Conn{
		raw:     c,
		encoder: json.NewEncoder(c),
		decoder: json.NewDecoder(c),
	}
}

// Send 将消息序列化为 JSON 发送。加锁以支持多 Goroutine 并发调用。
func (c *Conn) Send(msg Message) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.encoder.Encode(msg)
}

func (c *Conn) Receive() (Message, error) {
	var msg Message
	err := c.decoder.Decode(&msg)
	return msg, err
}

func (c *Conn) Close() {
	c.raw.Close()
}
