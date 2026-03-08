package server

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	"cs/internal/proto"
)

type Server struct {
	addr             string
	mapW             int
	mapH             int
	nextID           int
	nextReq          int
	store            Store
	heartbeatTimeout time.Duration
	logger           *log.Logger

	mu       sync.Mutex
	players  map[int]*player
	clients  map[int]*clientConn
	pending  map[int]pendingChallenge
	listener net.Listener
}

type player struct {
	ID   int
	Name string
	X    int
	Y    int
	HP   int
}

type clientConn struct {
	id            int
	conn          net.Conn
	enc           *json.Encoder
	send          chan proto.Message
	done          chan struct{}
	closed        bool
	lastHeartbeat time.Time
}

type pendingChallenge struct {
	fromID int
	toID   int
}

func New(addr string, mapW, mapH int, store Store, logger *log.Logger) *Server {
	return &Server{
		addr:             addr,
		mapW:             mapW,
		mapH:             mapH,
		nextID:           1,
		nextReq:          1,
		store:            store,
		heartbeatTimeout: 15 * time.Second,
		logger:           logger,
		players:          make(map[int]*player),
		clients:          make(map[int]*clientConn),
		pending:          make(map[int]pendingChallenge),
	}
}


func (s *Server) Start() error {
	// 【实验挖空：开启监听】
	// 1. 使用 net.Listen 在 s.addr 上开启 TCP 监听
	// 2. 将得到的 listener 保存到 s.listener
	
	go s.monitorHeartbeats()

	for {
		// 【实验挖空：接收连接循环】
		// 1. 调用 s.listener.Accept 接收新连接
		// 2. 如果成功，开启一个新协程调用 s.handleConn(conn) 处理该连接
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var joinMsg proto.Message
	if err := dec.Decode(&joinMsg); err != nil {
		return
	}
	if joinMsg.Type != proto.TypeJoin || joinMsg.Join == nil || joinMsg.Join.Name == "" {
		return
	}

	clientID, welcome := s.registerClient(joinMsg.Join.Name, conn, enc)
	if clientID == 0 {
		return
	}

	if err := enc.Encode(welcome); err != nil {
		s.removeClient(clientID)
		return
	}

	c := s.getClient(clientID)
	if c == nil {
		return
	}

	go s.writerLoop(c)
	if state, ok := s.getPlayerState(clientID); ok {
		s.broadcastDelta([]proto.PlayerState{state}, nil)
	}
	s.logf("client %d (%s) connected", clientID, joinMsg.Join.Name)
	c.send <- proto.Message{Type: proto.TypeInfo, Info: &proto.Info{Message: "Welcome!"}}
	c.send <- proto.Message{Type: proto.TypeState, State: s.buildState()}
	c.send <- proto.Message{Type: proto.TypeInfo, Info: &proto.Info{Message: "Use arrow keys to move, 'c' to challenge."}}
	s.readLoop(clientID, dec)
}

func (s *Server) writerLoop(c *clientConn) {
	for {
		select {
		case msg := <-c.send:
			_ = c.enc.Encode(msg)
		case <-c.done:
			return
		}
	}
}

func (s *Server) readLoop(clientID int, dec *json.Decoder) {
	for {
		var msg proto.Message
		if err := dec.Decode(&msg); err != nil {
			s.removeClient(clientID)
			return
		}
		s.touchClient(clientID)
		// receiveInput: client message arrives here.
		if !isValidInput(msg) {
			continue
		}
		s.handleMessage(clientID, msg)
	}
}

func (s *Server) handleMessage(clientID int, msg proto.Message) {
	switch msg.Type {
	case proto.TypeMove:
		if msg.Move == nil {
			return
		}
		// updateGameState: validate and apply movement.
		s.movePlayer(clientID, msg.Move.DX, msg.Move.DY)
	case proto.TypeChallenge:
		if msg.Challenge == nil {
			return
		}
		// updateGameState: validate and apply challenge request.
		s.challengePlayer(clientID, msg.Challenge.TargetID)
	case proto.TypeChallengeResponse:
		if msg.ChallengeResponse == nil {
			return
		}
		// updateGameState: validate and apply challenge response.
		s.respondChallenge(clientID, msg.ChallengeResponse.RequestID, msg.ChallengeResponse.Accept)
	case proto.TypeHeartbeat:
		return
	}
}

func (s *Server) registerClient(name string, conn net.Conn, enc *json.Encoder) (int, proto.Message) {
	var restored *StoredPlayer
	if s.store != nil {
		stored, err := s.store.LoadPlayer(name)
		if err == nil {
			restored = stored
		}
	}

	s.mu.Lock()

	id := s.nextID
	s.nextID++

	p := &player{ID: id, Name: name, X: rand.Intn(s.mapW), Y: rand.Intn(s.mapH), HP: 100}
	if restored != nil {
		p.X = clamp(restored.X, 0, s.mapW-1)
		p.Y = clamp(restored.Y, 0, s.mapH-1)
		if restored.HP > 0 {
			p.HP = restored.HP
		}
	}
	s.players[id] = p

	client := &clientConn{id: id, conn: conn, enc: enc, send: make(chan proto.Message, 16), done: make(chan struct{}), lastHeartbeat: time.Now()}
	s.clients[id] = client

	welcome := proto.Message{Type: proto.TypeWelcome, Welcome: &proto.Welcome{
		PlayerID: id,
		MapW:     s.mapW,
		MapH:     s.mapH,
		Players:  s.snapshotPlayersLocked(),
	}}
	copy := *p
	s.mu.Unlock()

	s.persistPlayer(copy)
	return id, welcome
}

func (s *Server) getClient(id int) *clientConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clients[id]
}

func (s *Server) removeClient(id int) {
	s.mu.Lock()
	client := s.clients[id]
	var snapshot *player
	if p := s.players[id]; p != nil {
		copy := *p
		snapshot = &copy
	}
	delete(s.clients, id)
	delete(s.players, id)
	for reqID, pending := range s.pending {
		if pending.fromID == id || pending.toID == id {
			delete(s.pending, reqID)
		}
	}
	if client != nil && !client.closed {
		client.closed = true
		close(client.done)
	}
	s.mu.Unlock()

	if client != nil {
		_ = client.conn.Close()
	}
	if snapshot != nil {
		s.persistPlayer(*snapshot)
	}
	if snapshot != nil {
		s.broadcastDelta(nil, []int{snapshot.ID})
	}
	if snapshot != nil {
		s.logf("client %d (%s) disconnected", snapshot.ID, snapshot.Name)
	}
}

func (s *Server) movePlayer(id, dx, dy int) {
	s.mu.Lock()
	p := s.players[id]
	if p == nil {
		s.mu.Unlock()
		return
	}

	nx := clamp(p.X+dx, 0, s.mapW-1)
	ny := clamp(p.Y+dy, 0, s.mapH-1)
	if nx == p.X && ny == p.Y {
		s.mu.Unlock()
		return
	}
	p.X = nx
	p.Y = ny
	copy := *p
	s.mu.Unlock()

	s.persistPlayer(copy)
	s.broadcastDelta([]proto.PlayerState{{ID: copy.ID, Name: copy.Name, X: copy.X, Y: copy.Y, HP: copy.HP}}, nil)
}

func (s *Server) challengePlayer(fromID, targetID int) {
	s.mu.Lock()
	from := s.players[fromID]
	to := s.players[targetID]
	if from == nil || to == nil || fromID == targetID {
		s.mu.Unlock()
		s.sendInfo(fromID, "Invalid challenge target.")
		return
	}

	if manhattan(from.X, from.Y, to.X, to.Y) > 3 {
		s.mu.Unlock()
		s.sendInfo(fromID, "Target is out of range.")
		return
	}

	reqID := s.nextReq
	s.nextReq++
	s.pending[reqID] = pendingChallenge{fromID: fromID, toID: targetID}
	s.mu.Unlock()

	s.sendInfo(fromID, fmt.Sprintf("Challenge request sent to %s.", to.Name))
	s.sendTo(targetID, proto.Message{Type: proto.TypeChallengeRequest, ChallengeRequest: &proto.ChallengeRequest{
		RequestID: reqID,
		FromID:    fromID,
		FromName:  from.Name,
	}})
}

func (s *Server) respondChallenge(responderID, requestID int, accept bool) {
	s.mu.Lock()
	pending, ok := s.pending[requestID]
	if !ok || pending.toID != responderID {
		s.mu.Unlock()
		s.sendInfo(responderID, "Challenge request not found.")
		return
	}
	delete(s.pending, requestID)

	from := s.players[pending.fromID]
	to := s.players[pending.toID]
	if from == nil || to == nil {
		s.mu.Unlock()
		return
	}

	var fromCopy, toCopy player
	var dmgFrom, dmgTo int
	if accept {
		dmgFrom = rand.Intn(10) + 1
		dmgTo = rand.Intn(10) + 1
		applyDamage(from, dmgFrom)
		applyDamage(to, dmgTo)
		fromCopy = *from
		toCopy = *to
	}
	s.mu.Unlock()

	resultMsg := "Challenge declined."
	if accept {
		resultMsg = "Challenge accepted!"
	}

	result := proto.Message{Type: proto.TypeChallengeResult, ChallengeResult: &proto.ChallengeResult{
		RequestID: requestID,
		Accepted:  accept,
		Message:   resultMsg,
	}}

	s.sendTo(pending.fromID, result)
	s.sendTo(pending.toID, result)
	if accept {
		s.sendInfo(pending.fromID, fmt.Sprintf("You lose %d HP.", dmgFrom))
		s.sendInfo(pending.toID, fmt.Sprintf("You lose %d HP.", dmgTo))
		s.persistPlayer(fromCopy)
		s.persistPlayer(toCopy)
		s.broadcastDelta(
			[]proto.PlayerState{{ID: fromCopy.ID, Name: fromCopy.Name, X: fromCopy.X, Y: fromCopy.Y, HP: fromCopy.HP}, {ID: toCopy.ID, Name: toCopy.Name, X: toCopy.X, Y: toCopy.Y, HP: toCopy.HP}},
			nil,
		)
		s.logf("challenge %d accepted: %s-%d, %s-%d", requestID, fromCopy.Name, dmgFrom, toCopy.Name, dmgTo)
	} else {
		s.logf("challenge %d declined", requestID)
	}
}

func (s *Server) sendTo(id int, msg proto.Message) {
	s.mu.Lock()
	client := s.clients[id]
	if client != nil && client.closed {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if client == nil {
		return
	}
	select {
	case client.send <- msg:
	default:
	}
}

func (s *Server) sendInfo(id int, text string) {
	s.sendTo(id, proto.Message{Type: proto.TypeInfo, Info: &proto.Info{Message: text}})
}

func (s *Server) broadcastDelta(players []proto.PlayerState, removedIDs []int) {
	msg := proto.Message{Type: proto.TypeDeltaState, DeltaState: &proto.DeltaState{Players: players, RemovedIDs: removedIDs}}
	s.mu.Lock()
	clients := make([]*clientConn, 0, len(s.clients))
	for _, c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	for _, c := range clients {
		if c.closed {
			continue
		}
		select {
		case c.send <- msg:
		default:
		}
	}
}

func (s *Server) broadcastState() {
	// broadcastGameState: publish authoritative state.
	state := proto.Message{Type: proto.TypeState, State: s.buildState()}
	s.mu.Lock()
	clients := make([]*clientConn, 0, len(s.clients))
	for _, c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	for _, c := range clients {
		if c.closed {
			continue
		}
		select {
		case c.send <- state:
		default:
		}
	}
}

func (s *Server) getPlayerState(id int) (proto.PlayerState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.players[id]
	if p == nil {
		return proto.PlayerState{}, false
	}
	return proto.PlayerState{ID: p.ID, Name: p.Name, X: p.X, Y: p.Y, HP: p.HP}, true
}

func (s *Server) touchClient(id int) {
	s.mu.Lock()
	if c := s.clients[id]; c != nil && !c.closed {
		c.lastHeartbeat = time.Now()
	}
	s.mu.Unlock()
}

func (s *Server) monitorHeartbeats() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		var stale []int
		s.mu.Lock()
		for id, c := range s.clients {
			if c.closed {
				continue
			}
			if now.Sub(c.lastHeartbeat) > s.heartbeatTimeout {
				stale = append(stale, id)
			}
		}
		s.mu.Unlock()

		for _, id := range stale {
			s.logf("heartbeat timeout: client %d", id)
			s.removeClient(id)
		}
	}
}

func (s *Server) buildState() *proto.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &proto.State{Players: s.snapshotPlayersLocked()}
}

func (s *Server) snapshotPlayersLocked() []proto.PlayerState {
	players := make([]proto.PlayerState, 0, len(s.players))
	for _, p := range s.players {
		players = append(players, proto.PlayerState{ID: p.ID, Name: p.Name, X: p.X, Y: p.Y, HP: p.HP})
	}
	return players
}

func (s *Server) persistPlayer(p player) {
	if s.store == nil {
		return
	}
	if err := s.store.SavePlayer(StoredPlayer{Name: p.Name, X: p.X, Y: p.Y, HP: p.HP}); err != nil {
		s.logf("persist player %s failed: %v", p.Name, err)
	}
}

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func manhattan(x1, y1, x2, y2 int) int {
	dx := x1 - x2
	if dx < 0 {
		dx = -dx
	}
	dy := y1 - y2
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}

func applyDamage(p *player, dmg int) {
	if dmg <= 0 {
		return
	}
	p.HP -= dmg
	if p.HP < 0 {
		p.HP = 0
	}
}

func isValidInput(msg proto.Message) bool {
	switch msg.Type {
	case proto.TypeMove:
		if msg.Move == nil {
			return false
		}
		dx, dy := msg.Move.DX, msg.Move.DY
		if dx < -1 || dx > 1 || dy < -1 || dy > 1 {
			return false
		}
		return dx != 0 || dy != 0
	case proto.TypeChallenge:
		return msg.Challenge != nil && msg.Challenge.TargetID > 0
	case proto.TypeChallengeResponse:
		return msg.ChallengeResponse != nil && msg.ChallengeResponse.RequestID > 0
	case proto.TypeHeartbeat:
		return true
	default:
		return true
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.logger == nil {
		return
	}
	s.logger.Printf(format, args...)
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
