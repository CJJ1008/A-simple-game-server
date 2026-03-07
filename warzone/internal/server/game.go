package server

import (
	"warzone/internal/protocol"
	"fmt"
	"log"
	"math/rand"
)

// serverState holds the authoritative game state.
// All methods that mutate it must be called while holding Server.gameMu.
type serverState struct {
	Players     [protocol.MaxPlayers]protocol.PlayerState
	Weapons     [protocol.MaxWeapons]protocol.WeaponItem
	GameStarted bool
	GameOver    bool
	WinnerConn  int    // conn-slot index of the winner, -1 if none
	LastEvent   string

	// Per-game-slot stats accumulation (reset each round).
	killsGame [protocol.MaxPlayers]int
	diedGame  [protocol.MaxPlayers]bool
}

// ── Locked helpers (call while holding s.gameMu) ──────────────────────────────

// connToGameSlot returns the game-slot index of the given conn-slot, or -1.
func (srv *Server) connToGameSlotLocked(cid int) int {
	for g := 0; g < protocol.MaxPlayers; g++ {
		if srv.slotConn[g] == cid {
			return g
		}
	}
	return -1
}

// gameOnlineLocked counts occupied game slots.
func (srv *Server) gameOnlineLocked() int {
	n := 0
	for g := 0; g < protocol.MaxPlayers; g++ {
		if srv.slotConn[g] >= 0 {
			n++
		}
	}
	return n
}

// gameReadyLocked counts players whose Ready flag is set.
func (srv *Server) gameReadyLocked() int {
	n := 0
	for g := 0; g < protocol.MaxPlayers; g++ {
		if srv.slotConn[g] >= 0 && srv.state.Players[g].Ready != 0 {
			n++
		}
	}
	return n
}

// resetGameLocked initialises a new round for all currently online players.
func (srv *Server) resetGameLocked() {
	// Collect occupied slots in order.
	var online [protocol.MaxPlayers]int
	oc := 0
	for g := 0; g < protocol.MaxPlayers; g++ {
		if srv.slotConn[g] >= 0 {
			online[oc] = g
			oc++
		}
	}

	// Spawn-point candidates based on map boundaries (2-cell margin).
	x0, x1 := 2, protocol.MapW-3
	y0, y1 := 2, protocol.MapH-3
	cx, cy := (x0+x1)/2, (y0+y1)/2

	type pos struct{ x, y int }
	candidates := [5]pos{
		{x0, y0}, // top-left
		{x1, y0}, // top-right
		{x0, y1}, // bottom-left
		{x1, y1}, // bottom-right
		{cx, cy}, // centre
	}
	// order[count-1][rank] → candidate index
	order := [5][5]int{
		{4, 0, 0, 0, 0},
		{0, 3, 0, 0, 0},
		{0, 3, 4, 0, 0},
		{0, 1, 2, 3, 0},
		{0, 1, 2, 3, 4},
	}

	for rank := 0; rank < oc; rank++ {
		gs := online[rank]
		sp := candidates[order[oc-1][rank]]
		p := &srv.state.Players[gs]
		p.X = int8(sp.x)
		p.Y = int8(sp.y)
		p.Health = protocol.MaxHealth
		p.Alive = 1
		p.Connected = 1
		p.Ready = 0
		p.HasWeapon = 0
		srv.state.killsGame[gs] = 0
		srv.state.diedGame[gs] = false
	}

	// Clear disconnected slots.
	for g := 0; g < protocol.MaxPlayers; g++ {
		if srv.slotConn[g] < 0 {
			srv.state.Players[g].Connected = 0
			srv.state.Players[g].Alive = 0
		}
	}

	// Clear weapons.
	for i := range srv.state.Weapons {
		srv.state.Weapons[i].Active = 0
	}

	srv.state.GameStarted = true
	srv.state.GameOver = false
	srv.state.WinnerConn = -1
	srv.state.LastEvent = fmt.Sprintf("🎮 游戏开始！%d 名玩家", oc)

	log.Printf("[server] 出生点 (%d 人):", oc)
	for r := 0; r < oc; r++ {
		gs := online[r]
		p := &srv.state.Players[gs]
		log.Printf("  %s (%d,%d)", p.NameStr(), p.X, p.Y)
	}
}

// spawnWeaponLocked places one weapon at a random unoccupied cell.
func (srv *Server) spawnWeaponLocked() {
	slot := -1
	for i := range srv.state.Weapons {
		if srv.state.Weapons[i].Active == 0 {
			slot = i
			break
		}
	}
	if slot < 0 {
		return
	}
	for attempt := 0; attempt < 50; attempt++ {
		x := 2 + rand.Intn(protocol.MapW-4)
		y := 2 + rand.Intn(protocol.MapH-4)
		busy := false
		for g := 0; g < protocol.MaxPlayers; g++ {
			if srv.slotConn[g] >= 0 && srv.state.Players[g].Alive != 0 &&
				int(srv.state.Players[g].X) == x && int(srv.state.Players[g].Y) == y {
				busy = true
				break
			}
		}
		for i := range srv.state.Weapons {
			if srv.state.Weapons[i].Active != 0 &&
				int(srv.state.Weapons[i].X) == x && int(srv.state.Weapons[i].Y) == y {
				busy = true
				break
			}
		}
		if !busy {
			srv.state.Weapons[slot] = protocol.WeaponItem{X: int8(x), Y: int8(y), Active: 1}
			srv.state.LastEvent = fmt.Sprintf("⚔  强力武器出现在 (%d,%d)！", x, y)
			return
		}
	}
}

// checkPickupLocked checks if game-slot gs is standing on a weapon.
func (srv *Server) checkPickupLocked(gs int) {
	p := &srv.state.Players[gs]
	for i := range srv.state.Weapons {
		w := &srv.state.Weapons[i]
		if w.Active != 0 && w.X == p.X && w.Y == p.Y {
			w.Active = 0
			p.HasWeapon = 1
			srv.state.LastEvent = fmt.Sprintf("⚡ %s 拾取武器！下次攻击 ×2", p.NameStr())
			return
		}
	}
}

// applyActionLocked processes a player action.
func (srv *Server) applyActionLocked(cid int, action protocol.ActionType) {
	if !srv.state.GameStarted || srv.state.GameOver {
		return
	}
	gs := srv.connToGameSlotLocked(cid)
	if gs < 0 {
		return
	}
	me := &srv.state.Players[gs]
	if me.Alive == 0 {
		return
	}

	switch action {
	case protocol.ActionMoveUp:
		if me.Y > 0 {
			me.Y--
		}
	case protocol.ActionMoveDown:
		if int(me.Y) < protocol.MapH-1 {
			me.Y++
		}
	case protocol.ActionMoveLeft:
		if me.X > 0 {
			me.X--
		}
	case protocol.ActionMoveRight:
		if int(me.X) < protocol.MapW-1 {
			me.X++
		}
	case protocol.ActionAttack:
		// Find nearest alive opponent.
		bestDist := -1
		bestGS := -1
		for g := 0; g < protocol.MaxPlayers; g++ {
			if g == gs || srv.slotConn[g] < 0 || srv.state.Players[g].Alive == 0 {
				continue
			}
			d := abs(int(me.X)-int(srv.state.Players[g].X)) +
				abs(int(me.Y)-int(srv.state.Players[g].Y))
			if bestGS < 0 || d < bestDist {
				bestDist = d
				bestGS = g
			}
		}
		if bestGS < 0 {
			srv.state.LastEvent = fmt.Sprintf("%s 攻击！场上无存活对手", me.NameStr())
			return
		}
		if bestDist > protocol.AttackRange {
			srv.state.LastEvent = fmt.Sprintf("%s 攻击 %s，距离太远（%d格）",
				me.NameStr(), srv.state.Players[bestGS].NameStr(), bestDist)
			return
		}

		dmg := int16(protocol.AttackDamage)
		powered := me.HasWeapon != 0
		if powered {
			dmg = protocol.PowerDamage
			me.HasWeapon = 0
		}

		tgt := &srv.state.Players[bestGS]
		tgt.Health -= dmg

		if powered {
			srv.state.LastEvent = fmt.Sprintf("⚡ %s 强力攻击 %s！-%d HP",
				me.NameStr(), tgt.NameStr(), dmg)
		} else {
			srv.state.LastEvent = fmt.Sprintf("%s 攻击 %s，-%d HP",
				me.NameStr(), tgt.NameStr(), dmg)
		}

		if tgt.Health <= 0 {
			tgt.Health = 0
			tgt.Alive = 0
			srv.state.killsGame[gs]++
			tcid := srv.slotConn[bestGS]
			if tcid >= 0 {
				tgtGS := srv.connToGameSlotLocked(tcid)
				if tgtGS >= 0 {
					srv.state.diedGame[tgtGS] = true
				}
			}
			srv.state.LastEvent = fmt.Sprintf("💀 %s 击败了 %s！", me.NameStr(), tgt.NameStr())
			srv.checkGameOverLocked(cid)
		}
		return
	}

	// After any move, check for weapon pickup.
	srv.checkPickupLocked(gs)
}

// checkGameOverLocked checks if the game has ended and updates state accordingly.
func (srv *Server) checkGameOverLocked(winnerConnHint int) {
	alive := 0
	lastAliveGS := -1
	for g := 0; g < protocol.MaxPlayers; g++ {
		if srv.slotConn[g] >= 0 && srv.state.Players[g].Alive != 0 {
			alive++
			lastAliveGS = g
		}
	}
	if alive <= 1 && lastAliveGS >= 0 {
		srv.state.GameOver = true
		srv.state.WinnerConn = srv.slotConn[lastAliveGS]
		srv.state.LastEvent = fmt.Sprintf("🏆 %s 是最后的幸存者！游戏结束",
			srv.state.Players[lastAliveGS].NameStr())
		srv.saveStatsLocked()
	} else if alive <= 1 && lastAliveGS < 0 {
		// Everyone died simultaneously.
		srv.state.GameOver = true
		srv.state.WinnerConn = winnerConnHint
		srv.state.LastEvent = "游戏结束"
		srv.saveStatsLocked()
	}
}

// removeFromGameLocked removes a player by conn-slot from the game.
func (srv *Server) removeFromGameLocked(cid int) {
	for g := 0; g < protocol.MaxPlayers; g++ {
		if srv.slotConn[g] != cid {
			continue
		}
		srv.state.Players[g].Connected = 0
		srv.state.Players[g].Alive = 0
		srv.slotConn[g] = -1

		if srv.state.GameStarted && !srv.state.GameOver {
			srv.checkGameOverLocked(-1)
		}
		if srv.gameOnlineLocked() == 0 {
			srv.state.GameStarted = false
			srv.state.GameOver = false
		}
		return
	}
}

// saveStatsLocked persists game results for all participating players.
// Must be called while holding gameMu.
func (srv *Server) saveStatsLocked() {
	for g := 0; g < protocol.MaxPlayers; g++ {
		cid := srv.slotConn[g]
		if cid < 0 {
			continue
		}
		srv.connMu.Lock()
		username := srv.conns[cid].username
		srv.connMu.Unlock()
		if username == "" {
			continue
		}
		isWinner := srv.state.WinnerConn == cid
		srv.db.UpdateStats(username, isWinner, srv.state.killsGame[g], srv.state.diedGame[g])
	}
	log.Println("[server] 战绩已写入数据库")
}

// broadcastStateLocked sends a STATE_UPDATE to every player currently in-game.
// Must be called while holding gameMu.
func (srv *Server) broadcastStateLocked() {
	pc := srv.gameOnlineLocked()
	rc := srv.gameReadyLocked()

	var winnerID uint8 = 0xFF
	if srv.state.WinnerConn >= 0 {
		wgs := srv.connToGameSlotLocked(srv.state.WinnerConn)
		if wgs >= 0 {
			winnerID = uint8(wgs)
		}
	}

	for g := 0; g < protocol.MaxPlayers; g++ {
		cid := srv.slotConn[g]
		if cid < 0 {
			continue
		}
		srv.connMu.Lock()
		conn := srv.conns[cid].conn
		srv.connMu.Unlock()
		if conn == nil {
			continue
		}

		pkt := protocol.StateUpdatePayload{
			Players:     srv.state.Players,
			Weapons:     srv.state.Weapons,
			YourID:      uint8(g),
			PlayerCount: uint8(pc),
			ReadyCount:  uint8(rc),
			GameStarted: protocol.BoolToU8(srv.state.GameStarted),
			GameOver:    protocol.BoolToU8(srv.state.GameOver),
			WinnerID:    winnerID,
		}
		protocol.StringToFixedBytes(srv.state.LastEvent, pkt.LastEvent[:])

		srv.writeMus[cid].Lock()
		_ = protocol.SendPacket(conn, protocol.PktStateUpdate, &pkt)
		srv.writeMus[cid].Unlock()
	}
}



func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
