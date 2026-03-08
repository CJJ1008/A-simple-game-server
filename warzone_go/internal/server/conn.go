package server

import (
	"warzone/internal/protocol"
	"bufio"
	"encoding/binary"
	"log"
	"net"
	"time"
)

// connThread handles a single TCP connection through three phases:
//  1. Auth     – REGISTER / LOGIN (up to 10 attempts)
//  2. Lobby    – JOIN / STATS_REQUEST (wait for a free game slot)
//  3. Game     – ACTION / READY / STATS_REQUEST
func (srv *Server) connThread(cid int) {
	srv.connMu.Lock()
	conn := srv.conns[cid].conn
	srv.connMu.Unlock()

	reader := bufio.NewReader(conn)

	touchHB := func() {
		srv.connMu.Lock()
		srv.conns[cid].lastHB = time.Now()
		srv.connMu.Unlock()
	}

	sendPkt := func(pktType protocol.PacketType, payload interface{}) error {
		srv.writeMus[cid].Lock()
		defer srv.writeMus[cid].Unlock()
		return protocol.SendPacket(conn, pktType, payload)
	}

	cleanup := func() {
		// Remove from game if still there.
		srv.gameMu.Lock()
		if srv.connToGameSlotLocked(cid) >= 0 {
			gs := srv.connToGameSlotLocked(cid)
			srv.state.LastEvent = srv.state.Players[gs].NameStr() + " 断线了"
			srv.removeFromGameLocked(cid)
			srv.broadcastStateLocked()
		}
		srv.gameMu.Unlock()

		// Release the conn slot.
		conn.Close()
		srv.connMu.Lock()
		srv.conns[cid] = connEntry{}
		srv.connMu.Unlock()
		log.Printf("[server] 连接 %d 线程退出", cid)
	}
	defer cleanup()

	log.Printf("[server] 连接 %d 已建立", cid)

	// ══════════════════════════════════════════════════════════════════════
	// Phase 1: Authentication (up to 10 attempts)
	// ══════════════════════════════════════════════════════════════════════
	authed := false
	for attempt := 0; attempt < 10; {
		hdr, err := protocol.RecvHeader(reader)
		if err != nil {
			return
		}

		switch hdr.Type {
		case protocol.PktDisconnect:
			return
		case protocol.PktHeartbeat:
			touchHB()
			_ = sendPkt(protocol.PktHeartbeatAck, nil)
			continue
		case protocol.PktHeartbeatAck:
			touchHB()
			continue
		case protocol.PktRegister, protocol.PktLogin:
			// continue below
		default:
			protocol.DiscardN(reader, int(hdr.Length))
			continue
		}

		attempt++
		var ap protocol.AuthPayload
		if int(hdr.Length) >= binary.Size(ap) {
			if err := protocol.RecvInto(reader, &ap); err != nil {
				return
			}
		} else {
			protocol.DiscardN(reader, int(hdr.Length))
		}

		username := protocol.BytesToString(ap.Username[:])
		password := protocol.BytesToString(ap.Password[:])

		var ok bool
		var msg string
		if hdr.Type == protocol.PktRegister {
			ok, msg = srv.db.RegisterUser(username, password)
			log.Printf("[server] 注册 %s: %s", username, msg)
		} else {
			ok, msg = srv.db.Login(username, password)
			log.Printf("[server] 登录 %s: %s", username, msg)
		}

		var ar protocol.AuthResultPayload
		ar.Success = protocol.BoolToU8(ok)
		protocol.StringToFixedBytes(msg, ar.Message[:])
		protocol.StringToFixedBytes(username, ar.Username[:])
		if err := sendPkt(protocol.PktAuthResult, &ar); err != nil {
			return
		}

		if ok {
			srv.connMu.Lock()
			srv.conns[cid].authed = true
			srv.conns[cid].username = username
			srv.connMu.Unlock()
			authed = true
			break
		}
	}
	if !authed {
		log.Printf("[server] 连接 %d 认证失败超过上限", cid)
		return
	}

	// ══════════════════════════════════════════════════════════════════════
	// Phase 2: Lobby (waiting for JOIN)
	// ══════════════════════════════════════════════════════════════════════
	inGame := false
	for !inGame {
		hdr, err := protocol.RecvHeader(reader)
		if err != nil {
			return
		}
		switch hdr.Type {
		case protocol.PktDisconnect:
			return
		case protocol.PktHeartbeat:
			touchHB()
			_ = sendPkt(protocol.PktHeartbeatAck, nil)
		case protocol.PktHeartbeatAck:
			touchHB()
		case protocol.PktJoin:
			srv.gameMu.Lock()
			gs := -1
			for g := 0; g < protocol.MaxPlayers; g++ {
				if srv.slotConn[g] < 0 {
					gs = g
					break
				}
			}
			if gs < 0 {
				// Room full – inform client and keep waiting.
				var tmp protocol.StateUpdatePayload
				tmp.YourID = 0xFF
				tmp.PlayerCount = protocol.MaxPlayers
				protocol.StringToFixedBytes("游戏房间已满，请稍后再试", tmp.LastEvent[:])
				_ = sendPkt(protocol.PktStateUpdate, &tmp)
				srv.gameMu.Unlock()
				continue
			}
			srv.slotConn[gs] = cid
			srv.connMu.Lock()
			srv.conns[cid].gameSlot = gs
			srv.connMu.Unlock()

			srv.connMu.Lock()
			uname := srv.conns[cid].username
			srv.connMu.Unlock()

			p := &srv.state.Players[gs]
			protocol.StringToFixedBytes(uname, p.Name[:])
			p.Connected = 1
			p.Alive = 0
			p.Ready = 0
			p.HasWeapon = 0
			p.Health = 0
			p.X = 0
			p.Y = 0

			cnt := srv.gameOnlineLocked()
			srv.state.LastEvent = uname + " 进入房间，按 R 准备"
			log.Printf("[server] %s 进入房间（槽%d，共%d人）", uname, gs, cnt)
			srv.broadcastStateLocked()
			srv.gameMu.Unlock()
			inGame = true

		case protocol.PktStatsRequest:
			srv.handleStatsRequest(cid, reader, int(hdr.Length), sendPkt)
		default:
			protocol.DiscardN(reader, int(hdr.Length))
		}
	}

	// ══════════════════════════════════════════════════════════════════════
	// Phase 3: Game loop
	// ══════════════════════════════════════════════════════════════════════
	for {
		hdr, err := protocol.RecvHeader(reader)
		if err != nil {
			return
		}
		switch hdr.Type {
		case protocol.PktReady:
			srv.gameMu.Lock()
			if !srv.state.GameStarted {
				gs := srv.connToGameSlotLocked(cid)
				if gs >= 0 {
					srv.state.Players[gs].Ready = 1
					rc := srv.gameReadyLocked()
					oc := srv.gameOnlineLocked()
					srv.state.LastEvent = srv.state.Players[gs].NameStr() +
						" 已准备"
					log.Printf("[server] %s 准备 %d/%d",
						srv.state.Players[gs].NameStr(), rc, oc)
					if rc == oc && oc >= 2 {
						srv.resetGameLocked()
						log.Printf("[server] 游戏开始！%d 名玩家", oc)
					}
					srv.broadcastStateLocked()
				}
			}
			srv.gameMu.Unlock()

		case protocol.PktAction:
			var ap protocol.ActionPayload
			if int(hdr.Length) >= binary.Size(ap) {
				if err := protocol.RecvInto(reader, &ap); err != nil {
					return
				}
			} else {
				protocol.DiscardN(reader, int(hdr.Length))
			}
			srv.gameMu.Lock()
			srv.applyActionLocked(cid, ap.Action)
			srv.broadcastStateLocked()
			srv.gameMu.Unlock()

		case protocol.PktStatsRequest:
			srv.handleStatsRequest(cid, reader, int(hdr.Length), sendPkt)

		case protocol.PktHeartbeat:
			touchHB()
			_ = sendPkt(protocol.PktHeartbeatAck, nil)
		case protocol.PktHeartbeatAck:
			touchHB()
		case protocol.PktDisconnect:
			return
		default:
			protocol.DiscardN(reader, int(hdr.Length))
		}
	}
}

// handleStatsRequest reads a STATS_REQUEST and sends back a STATS_RESPONSE.
func (srv *Server) handleStatsRequest(
	cid int,
	reader *bufio.Reader,
	length int,
	sendPkt func(protocol.PacketType, interface{}) error,
) {
	var srp protocol.StatsRequestPayload
	if length >= binary.Size(srp) {
		_ = protocol.RecvInto(reader, &srp)
	} else {
		protocol.DiscardN(reader, length)
	}

	target := protocol.BytesToString(srp.Username[:])
	if target == "" {
		srv.connMu.Lock()
		target = srv.conns[cid].username
		srv.connMu.Unlock()
	}

	rec, found := srv.db.GetStats(target)
	var resp protocol.StatsResponsePayload
	protocol.StringToFixedBytes(target, resp.Username[:])
	if found {
		resp.Found = 1
		resp.Games = int32(rec.Games)
		resp.Wins = int32(rec.Wins)
		resp.Losses = int32(rec.Losses)
		resp.Kills = int32(rec.Kills)
		resp.Deaths = int32(rec.Deaths)
		protocol.StringToFixedBytes(rec.LastPlayed, resp.LastPlayed[:])
	}
	_ = sendPkt(protocol.PktStatsResponse, &resp)
}

// connEntry holds all state for one TCP connection.
type connEntry struct {
	conn      net.Conn
	active    bool
	authed    bool
	username  string
	gameSlot  int // -1 if not in game
	lastHB    time.Time
}
