package protocol

import (
	"encoding/json"
	"net"
	"sync"
	"time"
)

const (
	GatewayAddr = "127.0.0.1:9310"

	MapWidth  = 56
	MapHeight = 24

	InitHP       = 120
	InitAttack   = 35
	AttackRange  = 2
	BossAtkRange = 4
	NPCDamage    = 18
	HealAmount   = 30
	MaxPotions   = 3
	PotionCap    = 8
	PotionPrice  = 3
	WeaponPrice  = 8
	WeaponBoost  = 6
	MinNPCs      = 5
	MaxTreasures = 8
)

const (
	TypeRegister   = "register"
	TypeLogin      = "login"
	TypeQuickEnter = "quick_enter"
	TypeLogout     = "logout"
	TypeMove       = "move"
	TypeAttack     = "attack"
	TypeBossAttack = "boss_attack"
	TypeHeal       = "heal"
	TypeShop       = "shop"
	TypeSwitchMap  = "switch_map"
	TypeAdmin      = "admin"

	TypeAuth  = "auth"
	TypeState = "state"
	TypeError = "error"
)

const (
	DirUp    = "up"
	DirDown  = "down"
	DirLeft  = "left"
	DirRight = "right"
)

type PlayerView struct {
	Username   string `json:"username"`
	MapID      string `json:"map_id"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	HP         int    `json:"hp"`
	MaxHP      int    `json:"max_hp"`
	Attack     int    `json:"attack"`
	Potions    int    `json:"potions"`
	Treasures  int    `json:"treasures"`
	Kills      int    `json:"kills"`
	Deaths     int    `json:"deaths"`
	Victories  int    `json:"victories"`
	Alive      bool   `json:"alive"`
	RespawnIn  int    `json:"respawn_in"`
	LastUpdate string `json:"last_update,omitempty"`
}

type NPCView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	HP     int    `json:"hp"`
	MaxHP  int    `json:"max_hp"`
	Attack int    `json:"attack"`
	Alive  bool   `json:"alive"`
}

type TreasureView struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Value int    `json:"value"`
}

type MapBrief struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	NodeID     string `json:"node_id"`
	Players    int    `json:"players"`
	NPCs       int    `json:"npcs"`
	Treasures  int    `json:"treasures"`
	Version    int64  `json:"version"`
	Primary    bool   `json:"primary"`
	IsCurrent  bool   `json:"is_current"`
	Checkpoint int64  `json:"checkpoint"`
}

type NodeView struct {
	ID            string   `json:"id"`
	Addr          string   `json:"addr"`
	Healthy       bool     `json:"healthy"`
	PrimaryMaps   []string `json:"primary_maps"`
	ReplicaMaps   []string `json:"replica_maps"`
	LastHeartbeat string   `json:"last_heartbeat"`
}

type MapView struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	NodeID    string         `json:"node_id"`
	Width     int            `json:"width"`
	Height    int            `json:"height"`
	Terrain   []string       `json:"terrain"`
	Players   []PlayerView   `json:"players"`
	NPCs      []NPCView      `json:"npcs"`
	Treasures []TreasureView `json:"treasures"`
	Version   int64          `json:"version"`
}

type BossSite struct {
	MapID string `json:"map_id"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
}

type BossView struct {
	Name      string     `json:"name"`
	HP        int        `json:"hp"`
	MaxHP     int        `json:"max_hp"`
	Alive     bool       `json:"alive"`
	LastHit   string     `json:"last_hit"`
	RespawnIn int        `json:"respawn_in"`
	AttackGap int        `json:"attack_gap"`
	Sites     []BossSite `json:"sites"`
	Version   int64      `json:"version"`
}

type WorldState struct {
	Self           PlayerView `json:"self"`
	Map            MapView    `json:"map"`
	Maps           []MapBrief `json:"maps"`
	Nodes          []NodeView `json:"nodes"`
	Boss           BossView   `json:"boss"`
	Events         []string   `json:"events"`
	SessionVersion int64      `json:"session_version"`
}

type UserProfile struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	LastMap      string `json:"last_map"`
	LastNode     string `json:"last_node"`
	X            int    `json:"x"`
	Y            int    `json:"y"`
	HP           int    `json:"hp"`
	MaxHP        int    `json:"max_hp"`
	Attack       int    `json:"attack"`
	Potions      int    `json:"potions"`
	Treasures    int    `json:"treasures"`
	Kills        int    `json:"kills"`
	Deaths       int    `json:"deaths"`
	Victories    int    `json:"victories"`
	Alive        bool   `json:"alive"`
}

type HotSession struct {
	Username       string    `json:"username"`
	MapID          string    `json:"map_id"`
	NodeID         string    `json:"node_id"`
	X              int       `json:"x"`
	Y              int       `json:"y"`
	HP             int       `json:"hp"`
	Treasures      int       `json:"treasures"`
	SessionVersion int64     `json:"session_version"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type MapCheckpoint struct {
	MapID      string         `json:"map_id"`
	NodeID     string         `json:"node_id"`
	Version    int64          `json:"version"`
	Terrain    []string       `json:"terrain"`
	Players    []PlayerView   `json:"players"`
	NPCs       []NPCView      `json:"npcs"`
	Treasures  []TreasureView `json:"treasures"`
	Checkpoint time.Time      `json:"checkpoint"`
}

type Message struct {
	Type     string      `json:"type"`
	Action   string      `json:"action,omitempty"`
	Username string      `json:"username,omitempty"`
	Password string      `json:"password,omitempty"`
	Dir      string      `json:"dir,omitempty"`
	MapID    string      `json:"map_id,omitempty"`
	NodeID   string      `json:"node_id,omitempty"`
	Confirm  string      `json:"confirm,omitempty"`
	Item     string      `json:"item,omitempty"`
	Text     string      `json:"text,omitempty"`
	OK       bool        `json:"ok,omitempty"`
	Error    string      `json:"error,omitempty"`
	State    *WorldState `json:"state,omitempty"`
}

type Conn struct {
	raw     net.Conn
	encoder *json.Encoder
	decoder *json.Decoder
	sendMu  sync.Mutex
}

func NewConn(c net.Conn) *Conn {
	return &Conn{
		raw:     c,
		encoder: json.NewEncoder(c),
		decoder: json.NewDecoder(c),
	}
}

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

func (c *Conn) Close() error {
	return c.raw.Close()
}
