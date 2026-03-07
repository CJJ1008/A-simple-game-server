// Package protocol defines the shared network protocol between client and server.
// Binary layout matches the C++ __attribute__((packed)) structs exactly,
// using encoding/binary with LittleEndian for serialization.
package protocol

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
)

// ── Game constants ─────────────────────────────────────────────────────────────

const (
	MaxPlayers       = 5
	MapW             = 36
	MapH             = 12
	MaxHealth        = 100
	AttackDamage     = 15
	PowerDamage      = 30
	AttackRange      = 3
	MaxWeapons       = 6
	WeaponInterval   = 8  // seconds
	HeartbeatInterval = 2 // seconds
	HeartbeatTimeout = 8  // seconds
)

// ── Packet types ───────────────────────────────────────────────────────────────

type PacketType uint8

const (
	PktRegister      PacketType = 1
	PktLogin         PacketType = 2
	PktAuthResult    PacketType = 3
	PktJoin          PacketType = 4
	PktAction        PacketType = 5
	PktStateUpdate   PacketType = 6
	PktReady         PacketType = 7
	PktStatsRequest  PacketType = 8
	PktStatsResponse PacketType = 9
	PktHeartbeat     PacketType = 10
	PktHeartbeatAck  PacketType = 11
	PktDisconnect    PacketType = 12
)

// ── Action types ───────────────────────────────────────────────────────────────

type ActionType uint8

const (
	ActionMoveUp    ActionType = 1
	ActionMoveDown  ActionType = 2
	ActionMoveLeft  ActionType = 3
	ActionMoveRight ActionType = 4
	ActionAttack    ActionType = 5
)

// ── Binary protocol structs (must match C++ packed layout exactly) ─────────────
// encoding/binary.Read/Write traverses fields sequentially with no padding,
// which mirrors __attribute__((packed)) in C++.

// PacketHeader is the 3-byte framing header sent before every packet.
type PacketHeader struct {
	Type   PacketType
	Length uint16
}

// HeaderSize is the wire size of PacketHeader.
const HeaderSize = 3

// AuthPayload is sent for REGISTER and LOGIN packets.
type AuthPayload struct {
	Username [32]byte
	Password [64]byte
}

// AuthResultPayload is sent from server after authentication.
type AuthResultPayload struct {
	Success  uint8
	Message  [64]byte
	Username [32]byte
}

// StatsRequestPayload requests stats for a given username.
// Empty username means "query myself".
type StatsRequestPayload struct {
	Username [32]byte
}

// StatsResponsePayload carries the stats data back to the client.
// Wire layout: [32]username + uint8 found + 5×int32 + [24]last_played = 77 bytes
type StatsResponsePayload struct {
	Username   [32]byte
	Found      uint8
	Games      int32
	Wins       int32
	Losses     int32
	Kills      int32
	Deaths     int32
	LastPlayed [24]byte
}

// ActionPayload carries a single player action.
type ActionPayload struct {
	Action ActionType
}

// PlayerState is the per-player state broadcast to all clients (24 bytes).
type PlayerState struct {
	X         int8
	Y         int8
	Health    int16
	Alive     uint8
	Connected uint8
	Ready     uint8
	HasWeapon uint8
	Name      [16]byte
}

// WeaponItem represents one weapon on the map (3 bytes).
type WeaponItem struct {
	X      int8
	Y      int8
	Active uint8
}

// StateUpdatePayload is the authoritative state broadcast (208 bytes).
// Players[5]×24 + Weapons[6]×3 + 6×uint8 + LastEvent[64] = 208
type StateUpdatePayload struct {
	Players     [MaxPlayers]PlayerState
	Weapons     [MaxWeapons]WeaponItem
	YourID      uint8
	PlayerCount uint8
	ReadyCount  uint8
	GameStarted uint8
	GameOver    uint8
	WinnerID    uint8 // 0xFF = no winner
	LastEvent   [64]byte
}

// ── String helpers ─────────────────────────────────────────────────────────────

// BytesToString converts a null-terminated byte array to a Go string.
func BytesToString(b []byte) string {
	n := bytes.IndexByte(b, 0)
	if n < 0 {
		return string(b)
	}
	return string(b[:n])
}

// StringToFixedBytes copies a string into a fixed-size byte slice, zero-filling the rest.
func StringToFixedBytes(s string, dst []byte) {
	for i := range dst {
		dst[i] = 0
	}
	copy(dst, s)
}

// NameStr returns the player's name as a Go string.
func (p *PlayerState) NameStr() string {
	return strings.TrimRight(string(p.Name[:]), "\x00")
}

// ── Wire I/O helpers ───────────────────────────────────────────────────────────

// SendPacket serialises header + optional payload into a single Write call.
// Pass nil for payload to send a header-only packet (e.g. HEARTBEAT, READY, JOIN).
func SendPacket(w io.Writer, pktType PacketType, payload interface{}) error {
	var body bytes.Buffer
	if payload != nil {
		if err := binary.Write(&body, binary.LittleEndian, payload); err != nil {
			return err
		}
	}
	hdr := PacketHeader{Type: pktType, Length: uint16(body.Len())}
	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, hdr)
	out.Write(body.Bytes())
	_, err := w.Write(out.Bytes())
	return err
}

// RecvHeader reads exactly HeaderSize bytes and decodes a PacketHeader.
func RecvHeader(r io.Reader) (PacketHeader, error) {
	var hdr PacketHeader
	err := binary.Read(r, binary.LittleEndian, &hdr)
	return hdr, err
}

// RecvInto reads a binary-encoded value from r.
// The caller must ensure r has exactly binary.Size(v) bytes available.
func RecvInto(r io.Reader, v interface{}) error {
	return binary.Read(r, binary.LittleEndian, v)
}

// DiscardN discards n bytes from r.
func DiscardN(r io.Reader, n int) {
	if n > 0 {
		io.CopyN(io.Discard, r, int64(n))
	}
}

// BoolToU8 converts a bool to uint8.
func BoolToU8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
