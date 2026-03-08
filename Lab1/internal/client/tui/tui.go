package tui

import (
	"fmt"
	"sort"
	"time"

	"cs/internal/client"
	"cs/internal/proto"

	"github.com/gdamore/tcell/v2"
)

type App struct {
	backend client.Backend
	screen  tcell.Screen

	mapW       int
	mapH       int
	playerID   int
	players    map[int]proto.PlayerState
	selectedID int
	pending    *proto.ChallengeRequest
	messages   []string
}

func New(backend client.Backend) *App {
	return &App{
		backend: backend,
		players: make(map[int]proto.PlayerState),
	}
}

func (a *App) Run(addr, name string) error {
	if err := a.backend.Connect(addr, name); err != nil {
		return err
	}

	s, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	if err := s.Init(); err != nil {
		return err
	}
	defer s.Fini()
	defer a.backend.Close()

	a.screen = s
	a.pushMessage("Connecting...")
	a.render()

	termEvents := make(chan tcell.Event, 8)
	go func() {
		for {
			termEvents <- s.PollEvent()
		}
	}()

	for {
		select {
		case ev := <-termEvents:
			switch e := ev.(type) {
			case *tcell.EventKey:
				if a.handleKey(e) {
					return nil
				}
			}
		case ev, ok := <-a.backend.Events():
			if !ok {
				a.pushMessage("Disconnected.")
				a.render()
				time.Sleep(800 * time.Millisecond)
				return nil
			}
			a.handleEvent(ev)
		}
		a.render()
	}
}

func (a *App) handleEvent(ev client.Event) {
	switch ev.Type {
	case client.EventWelcome:
		if ev.Welcome == nil {
			return
		}
		a.playerID = ev.Welcome.PlayerID
		a.mapW = ev.Welcome.MapW
		a.mapH = ev.Welcome.MapH
		a.players = make(map[int]proto.PlayerState)
		for _, p := range ev.Welcome.Players {
			a.players[p.ID] = p
		}
		a.selectFirstTarget()
		a.pushMessage(fmt.Sprintf("You are player %d", a.playerID))
	case client.EventState:
		if ev.State == nil {
			return
		}
		a.players = make(map[int]proto.PlayerState)
		for _, p := range ev.State.Players {
			a.players[p.ID] = p
		}
		if a.selectedID != 0 {
			if _, ok := a.players[a.selectedID]; !ok {
				a.selectFirstTarget()
			}
		}
	case client.EventDeltaState:
		if ev.DeltaState == nil {
			return
		}
		for _, p := range ev.DeltaState.Players {
			a.players[p.ID] = p
		}
		for _, id := range ev.DeltaState.RemovedIDs {
			delete(a.players, id)
		}
		if a.selectedID != 0 {
			if _, ok := a.players[a.selectedID]; !ok {
				a.selectFirstTarget()
			}
		}
	case client.EventInfo:
		a.pushMessage(ev.Info)
	case client.EventChallengeRequest:
		a.pending = ev.ChallengeRequest
		if ev.ChallengeRequest != nil {
			a.pushMessage(fmt.Sprintf("Challenge from %s. Press y/d.", ev.ChallengeRequest.FromName))
		}
	case client.EventChallengeResult:
		if ev.ChallengeResult != nil {
			a.pushMessage(ev.ChallengeResult.Message)
		}
	}
}

func (a *App) handleKey(e *tcell.EventKey) bool {
	switch e.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		return true
	case tcell.KeyUp:
		_ = a.backend.Move(0, -1)
	case tcell.KeyDown:
		_ = a.backend.Move(0, 1)
	case tcell.KeyLeft:
		_ = a.backend.Move(-1, 0)
	case tcell.KeyRight:
		_ = a.backend.Move(1, 0)
	default:
		switch e.Rune() {
		case 'q':
			return true
		case 'c':
			if a.selectedID != 0 {
				_ = a.backend.Challenge(a.selectedID)
			}
		case 'n':
			a.selectNextTarget()
		case 'p':
			a.selectPrevTarget()
		case 'y':
			if a.pending != nil {
				_ = a.backend.RespondChallenge(a.pending.RequestID, true)
				a.pending = nil
			}
		case 'd':
			if a.pending != nil {
				_ = a.backend.RespondChallenge(a.pending.RequestID, false)
				a.pending = nil
			}
		}
	}
	return false
}

func (a *App) render() {
	a.screen.Clear()

	style := tcell.StyleDefault
	styleTarget := style.Foreground(tcell.ColorYellow)
	width, height := a.screen.Size()

	mapX := 1
	mapY := 1
	for y := 0; y < a.mapH; y++ {
		for x := 0; x < a.mapW; x++ {
			if mapX+x >= width || mapY+y >= height {
				continue
			}
			a.screen.SetContent(mapX+x, mapY+y, '.', nil, style)
		}
	}

	for _, p := range a.players {
		x := mapX + p.X
		y := mapY + p.Y
		if x >= width || y >= height {
			continue
		}
		r := playerRune(p.Name)
		cellStyle := style
		if p.ID == a.selectedID {
			cellStyle = styleTarget
		}
		a.screen.SetContent(x, y, r, nil, cellStyle)
	}

	sideX := mapX + a.mapW + 3
	a.drawString(sideX, 1, fmt.Sprintf("Player: %d", a.playerID))
	self, ok := a.players[a.playerID]
	if ok {
		a.drawString(sideX, 2, fmt.Sprintf("Pos: (%d,%d)", self.X, self.Y))
		a.drawString(sideX, 3, fmt.Sprintf("HP: %d", self.HP))
	} else {
		a.drawString(sideX, 2, "Pos: (-,-)")
		a.drawString(sideX, 3, "HP: -")
	}

	if ok && a.selectedID != 0 {
		if target, exists := a.players[a.selectedID]; exists {
			span := manhattan(self.X, self.Y, target.X, target.Y)
			a.drawString(sideX, 5, fmt.Sprintf("Target: %s", target.Name))
			a.drawString(sideX, 6, fmt.Sprintf("Range: %d", span))
			a.drawString(sideX, 7, fmt.Sprintf("Target HP: %d", target.HP))
		} else {
			a.drawString(sideX, 5, "Target: -")
			a.drawString(sideX, 6, "Range: -")
			a.drawString(sideX, 7, "Target HP: -")
		}
	} else {
		a.drawString(sideX, 5, "Target: -")
		a.drawString(sideX, 6, "Range: -")
		a.drawString(sideX, 7, "Target HP: -")
	}

	a.drawString(sideX, 9, "Keys: arrows move")
	a.drawString(sideX, 10, "c challenge, n/p target")
	a.drawString(sideX, 11, "y accept, d decline, q quit")

	msgY := mapY + a.mapH + 2
	if msgY < 13 {
		msgY = 13
	}
	a.drawString(1, msgY, "Messages:")
	for i := 0; i < len(a.messages); i++ {
		if msgY+1+i >= height {
			break
		}
		a.drawString(1, msgY+1+i, a.messages[i])
	}

	a.screen.Show()
}

func (a *App) drawString(x, y int, text string) {
	width, height := a.screen.Size()
	if y < 0 || y >= height {
		return
	}
	for i, r := range text {
		if x+i >= width {
			break
		}
		a.screen.SetContent(x+i, y, r, nil, tcell.StyleDefault)
	}
}

func playerRune(name string) rune {
	for _, r := range name {
		return r
	}
	return '?'
}

func (a *App) pushMessage(text string) {
	if text == "" {
		return
	}
	a.messages = append(a.messages, text)
	if len(a.messages) > 6 {
		a.messages = a.messages[len(a.messages)-6:]
	}
}

func (a *App) selectFirstTarget() {
	ids := make([]int, 0, len(a.players))
	for id := range a.players {
		if id != a.playerID {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	if len(ids) > 0 {
		a.selectedID = ids[0]
	} else {
		a.selectedID = 0
	}
}

func (a *App) selectNextTarget() {
	ids := a.otherPlayerIDs()
	if len(ids) == 0 {
		a.selectedID = 0
		return
	}
	idx := indexOf(ids, a.selectedID)
	idx = (idx + 1) % len(ids)
	a.selectedID = ids[idx]
}

func (a *App) selectPrevTarget() {
	ids := a.otherPlayerIDs()
	if len(ids) == 0 {
		a.selectedID = 0
		return
	}
	idx := indexOf(ids, a.selectedID)
	idx = (idx - 1 + len(ids)) % len(ids)
	a.selectedID = ids[idx]
}

func (a *App) otherPlayerIDs() []int {
	ids := make([]int, 0, len(a.players))
	for id := range a.players {
		if id != a.playerID {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

func indexOf(ids []int, target int) int {
	for i, id := range ids {
		if id == target {
			return i
		}
	}
	return 0
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
