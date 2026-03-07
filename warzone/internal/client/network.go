package client

import (
	"warzone/internal/protocol"
	"bufio"
	"encoding/binary"
	"net"
	"sync/atomic"
	"time"
)

// RecvWorker reads packets from conn and updates shared client state.
// It runs in its own goroutine and sets running=false when the connection drops.
func RecvWorker(
	conn net.Conn,
	running *atomic.Bool,
	state *SharedState,
) {
	RecvWorkerBuffered(conn, bufio.NewReader(conn), running, state)
}

// RecvWorkerBuffered is like RecvWorker but accepts an existing *bufio.Reader.
// Use this when bytes have already been read from conn via the same reader
// (e.g. during the login phase) to avoid losing buffered data.
func RecvWorkerBuffered(
	conn net.Conn,
	reader *bufio.Reader,
	running *atomic.Bool,
	state *SharedState,
) {
	for running.Load() {
		hdr, err := protocol.RecvHeader(reader)
		if err != nil {
			running.Store(false)
			return
		}
		switch hdr.Type {
		case protocol.PktStateUpdate:
			var p protocol.StateUpdatePayload
			if int(hdr.Length) >= binary.Size(p) {
				if err := protocol.RecvInto(reader, &p); err != nil {
					running.Store(false)
					return
				}
			} else {
				protocol.DiscardN(reader, int(hdr.Length))
				break
			}
			state.mu.Lock()
			state.GameState = p
			state.MyID = int(p.YourID)
			state.GameDirty = true
			state.mu.Unlock()

		case protocol.PktStatsResponse:
			var p protocol.StatsResponsePayload
			if int(hdr.Length) >= binary.Size(p) {
				_ = protocol.RecvInto(reader, &p)
			} else {
				protocol.DiscardN(reader, int(hdr.Length))
				break
			}
			state.mu.Lock()
			state.StatsResp = p
			state.StatsDirty = true
			state.mu.Unlock()

		case protocol.PktHeartbeat:
			// Respond immediately.
			SendPacket(conn, protocol.PktHeartbeatAck, nil)

		case protocol.PktHeartbeatAck:
			// Nothing to do.

		case protocol.PktDisconnect:
			running.Store(false)
			return

		default:
			protocol.DiscardN(reader, int(hdr.Length))
		}
	}
}

// HeartbeatWorker sends a HEARTBEAT every HeartbeatInterval seconds.
func HeartbeatWorker(conn net.Conn, running *atomic.Bool) {
	for running.Load() {
		time.Sleep(protocol.HeartbeatInterval * time.Second)
		if !running.Load() {
			break
		}
		if err := SendPacket(conn, protocol.PktHeartbeat, nil); err != nil {
			running.Store(false)
			return
		}
	}
}

// SendPacket is a convenience wrapper used by the client.
func SendPacket(conn net.Conn, pktType protocol.PacketType, payload interface{}) error {
	return protocol.SendPacket(conn, pktType, payload)
}
