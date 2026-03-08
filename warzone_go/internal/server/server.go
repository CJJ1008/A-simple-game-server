// Package server implements the game server.
// Architecture mirrors the C++ version:
//   • Connection layer (maxConn=20): every TCP connection lives here
//   • Game layer (MaxPlayers=5):     only players who sent JOIN live here
package server

import (
	"warzone/internal/database"
	"warzone/internal/protocol"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const maxConn = 20

// Server is the game server.
type Server struct {
	db   *database.Database

	connMu   sync.Mutex
	conns    [maxConn]connEntry
	writeMus [maxConn]sync.Mutex // per-connection write serialisation

	gameMu   sync.Mutex
	state    serverState
	slotConn [protocol.MaxPlayers]int // game-slot → conn-slot (-1 = empty)

	running atomic.Bool
}

// New creates a Server with the given data directory.
func New(dataDir string) *Server {
	s := &Server{
		db: database.New(dataDir),
	}
	for i := range s.slotConn {
		s.slotConn[i] = -1
	}
	s.state.WinnerConn = -1
	s.state.LastEvent = "服务器已启动"
	rand.Seed(time.Now().UnixNano())
	return s
}

// Start listens on port and serves forever (blocking).
func (srv *Server) Start(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	defer ln.Close()

	srv.running.Store(true)

	go srv.weaponWorker()
	go srv.heartbeatWatcher()

	fmt.Printf("╔══════════════════════════════════════════════╗\n")
	fmt.Printf("║  多人对战服务器 v3.0 Go  游戏:%d人  连接:%d人  ║\n",
		protocol.MaxPlayers, maxConn)
	fmt.Printf("╚══════════════════════════════════════════════╝\n")
	fmt.Printf("[server] 监听 0.0.0.0:%d\n", port)
	fmt.Printf("[server] 数据目录：./data/\n")
	fmt.Printf("[server] 地图：%d×%d\n\n", protocol.MapW, protocol.MapH)

	for srv.running.Load() {
		conn, err := ln.Accept()
		if err != nil {
			if srv.running.Load() {
				log.Printf("[server] accept error: %v", err)
			}
			continue
		}

		// Enable TCP_NODELAY for low latency.
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.SetNoDelay(true)
		}

		// Find a free conn slot.
		cid := -1
		srv.connMu.Lock()
		for i := range srv.conns {
			if !srv.conns[i].active {
				srv.conns[i] = connEntry{
					conn:     conn,
					active:   true,
					gameSlot: -1,
					lastHB:   time.Now(),
				}
				cid = i
				break
			}
		}
		srv.connMu.Unlock()

		if cid < 0 {
			log.Println("[server] 连接池已满，拒绝连接")
			conn.Close()
			continue
		}

		log.Printf("[server] 新连接 %s → 槽 %d", conn.RemoteAddr(), cid)
		go srv.connThread(cid)
	}
	return nil
}

// Stop signals the server to stop.
func (srv *Server) Stop() {
	srv.running.Store(false)
}

// ── Background workers ────────────────────────────────────────────────────────

// weaponWorker spawns a weapon every WeaponInterval seconds during an active game.
func (srv *Server) weaponWorker() {
	for srv.running.Load() {
		time.Sleep(protocol.WeaponInterval * time.Second)
		srv.gameMu.Lock()
		if srv.state.GameStarted && !srv.state.GameOver {
			srv.spawnWeaponLocked()
			srv.broadcastStateLocked()
		}
		srv.gameMu.Unlock()
	}
}

// heartbeatWatcher closes connections that have not sent a heartbeat in time.
func (srv *Server) heartbeatWatcher() {
	for srv.running.Load() {
		time.Sleep(2 * time.Second)
		now := time.Now()
		for i := 0; i < maxConn; i++ {
			srv.connMu.Lock()
			active := srv.conns[i].active
			conn := srv.conns[i].conn
			last := srv.conns[i].lastHB
			srv.connMu.Unlock()
			if active && conn != nil && now.Sub(last) > protocol.HeartbeatTimeout*time.Second {
				log.Printf("[server] 连接 %d 心跳超时，踢出", i)
				conn.Close()
			}
		}
	}
}
