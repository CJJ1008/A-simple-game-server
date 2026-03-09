// Package protocol 定义客户端与服务器之间通信所用的消息结构和网络连接封装。
//
// 通信模型：所有消息以 JSON 格式编码，通过 TCP 传输。
// json.Encoder/Decoder 在连接上必须持久使用同一对实例，避免缓冲区数据丢失。
package protocol

import (
	"encoding/json"
	"net"
)

// ─── 地图与游戏常量 ─────────────────────────────────────────────────────────

const (
	MapWidth    = 20          // 地图列数
	MapHeight   = 20          // 地图行数
	InitHP      = 100         // 玩家初始血量
	AttackDmg   = 30          // 每次攻击伤害值
	HealAmt     = 40          // 每瓶药水恢复量
	MaxPotions  = 3           // 初始药水瓶数
	AttackRange = 2           // 攻击范围（曼哈顿距离）
)

// ─── 消息类型（客户端 → 服务器） ────────────────────────────────────────────

const (
	TypeJoin   = "join"   // 加入游戏，Text 字段携带玩家名
	TypeMove   = "move"   // 移动，Dir 字段携带方向
	TypeAttack = "attack" // 攻击（攻击范围内的对手）
	TypeHeal   = "heal"   // 使用一瓶药水
)

// ─── 消息类型（服务器 → 客户端） ────────────────────────────────────────────

const (
	TypeInit     = "init"      // 初始化，YourID 字段携带玩家编号
	TypeState    = "state"     // 游戏状态快照
	TypeEvent    = "event"     // 纯文本事件通知
	TypeYourTurn = "your_turn" // 轮到你行动
	TypeWait     = "wait"      // 等待对手行动
	TypeGameOver = "gameover"  // 游戏结束，Winner 字段携带胜者名字
)

// ─── 方向常量 ────────────────────────────────────────────────────────────────

const (
	DirUp    = "up"
	DirDown  = "down"
	DirLeft  = "left"
	DirRight = "right"
)

// ─── 数据结构 ────────────────────────────────────────────────────────────────

// PlayerInfo 是玩家状态的可序列化快照，用于传输。
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
// omitempty 确保未使用的字段不出现在 JSON 中，减小传输体积。
type Message struct {
	Type    string       `json:"type"`
	Dir     string       `json:"dir,omitempty"`
	Text    string       `json:"text,omitempty"`
	YourID  int          `json:"your_id,omitempty"`
	Players []PlayerInfo `json:"players,omitempty"`
	Turn    int          `json:"turn,omitempty"`
	Winner  string       `json:"winner,omitempty"`
}

// ─── 连接封装 ────────────────────────────────────────────────────────────────

// Conn 将一条 TCP 连接与持久的 JSON 编解码器绑定。
//
// 设计原因：json.Decoder 内部维护读缓冲区。若每次调用时重新创建 Decoder，
// 已预读入缓冲的字节将丢失，导致消息乱序或解析错误。因此必须对每条连接
// 持久保持同一个 Encoder/Decoder 对。
type Conn struct {
	raw     net.Conn
	encoder *json.Encoder
	decoder *json.Decoder
}

// NewConn 用原始 TCP 连接构造 Conn，并初始化编解码器。
func NewConn(c net.Conn) *Conn {
	return &Conn{
		raw:     c,
		encoder: json.NewEncoder(c),
		decoder: json.NewDecoder(c),
	}
}

// Send 将消息序列化为 JSON 并通过连接发送。
// json.Encoder.Encode 会在末尾自动追加 '\n' 作为消息边界。
func (c *Conn) Send(msg Message) error {
	return c.encoder.Encode(msg)
}

// Receive 从连接中阻塞读取并反序列化一条 JSON 消息。
func (c *Conn) Receive() (Message, error) {
	var msg Message
	err := c.decoder.Decode(&msg)
	return msg, err
}

// Close 关闭底层 TCP 连接。
func (c *Conn) Close() {
	c.raw.Close()
}
