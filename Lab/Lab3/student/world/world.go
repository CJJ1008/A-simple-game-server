package world

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"

	"battleworld/protocol"
)

type MapConfig struct {
	ID     string
	Name   string
	Layout []string
	SpawnX int
	SpawnY int
	BossX  int
	BossY  int
}

type Player struct {
	Username   string
	MapID      string
	X          int
	Y          int
	HP         int
	MaxHP      int
	Attack     int
	Potions    int
	Treasures  int
	Kills      int
	Deaths     int
	Victories  int
	Alive      bool
	RespawnAt  time.Time
	LastUpdate time.Time
}

type NPC struct {
	ID     string
	Name   string
	X      int
	Y      int
	HP     int
	MaxHP  int
	Attack int
	Alive  bool
}

type Treasure struct {
	ID    string
	Kind  string
	X     int
	Y     int
	Value int
}

type World struct {
	mu           sync.RWMutex
	cfg          MapConfig
	terrain      [][]rune
	players      map[string]*Player
	npcs         map[string]*NPC
	treasures    map[string]*Treasure
	rng          *rand.Rand
	version      int64
	nextNPC      int
	nextTreasure int
}

func AvailableMaps() []MapConfig {
	return []MapConfig{
		buildGreenMap(),
		buildCaveMap(),
		buildRuinsMap(),
	}
}

func DefaultMapID() string {
	return "green"
}

func FindConfig(id string) (MapConfig, bool) {
	for _, cfg := range AvailableMaps() {
		if cfg.ID == id {
			return cfg, true
		}
	}
	return MapConfig{}, false
}

func NewWorld(cfg MapConfig) *World {
	h := fnv.New64a()
	_, _ = h.Write([]byte(cfg.ID))
	w := &World{
		cfg:       cfg,
		terrain:   stringsToGrid(cfg.Layout),
		players:   make(map[string]*Player),
		npcs:      make(map[string]*NPC),
		treasures: make(map[string]*Treasure),
		rng:       rand.New(rand.NewSource(int64(h.Sum64()))),
	}
	w.bootstrap()
	return w
}

func (w *World) MapID() string {
	return w.cfg.ID
}

func (w *World) MapName() string {
	return w.cfg.Name
}

func (w *World) AddOrRestorePlayer(profile *protocol.UserProfile) protocol.PlayerView {
	w.mu.Lock()
	defer w.mu.Unlock()

	x, y := profile.X, profile.Y
	if profile.LastMap != w.cfg.ID {
		x, y = w.cfg.SpawnX, w.cfg.SpawnY
	}
	x, y = w.findSafePositionLocked(x, y, "")
	player := &Player{
		Username:   profile.Username,
		MapID:      w.cfg.ID,
		X:          x,
		Y:          y,
		HP:         valueOr(profile.HP, protocol.InitHP),
		MaxHP:      valueOr(profile.MaxHP, protocol.InitHP),
		Attack:     valueOr(profile.Attack, protocol.InitAttack),
		Potions:    valueOr(profile.Potions, protocol.MaxPotions),
		Treasures:  profile.Treasures,
		Kills:      profile.Kills,
		Deaths:     profile.Deaths,
		Victories:  profile.Victories,
		Alive:      profile.Alive,
		LastUpdate: time.Now(),
	}
	if profile.HP == 0 && !profile.Alive {
		player.Alive = false
	}
	if player.HP <= 0 {
		player.HP = player.MaxHP
		player.Alive = true
	}
	w.players[player.Username] = player
	w.version++
	return w.playerViewLocked(player)
}

func (w *World) RemovePlayer(username string) (protocol.UserProfile, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	player, ok := w.players[username]
	if !ok {
		return protocol.UserProfile{}, false
	}
	profile := w.profileLocked(player)
	delete(w.players, username)
	w.version++
	return profile, true
}

func (w *World) ProfileOf(username string) (protocol.UserProfile, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	player, ok := w.players[username]
	if !ok {
		return protocol.UserProfile{}, false
	}
	w.refreshPlayerStateLocked(player)
	return w.profileLocked(player), true
}

func (w *World) MovePlayer(username, dir string) (string, protocol.UserProfile, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	player, ok := w.players[username]
	if !ok {
		return "", protocol.UserProfile{}, false
	}
	w.refreshPlayerStateLocked(player)
	if !player.Alive {
		return fmt.Sprintf("%s 当前倒地，%d 秒后复活", username, w.playerRespawnInLocked(player)), w.profileLocked(player), true
	}

	nx, ny := player.X, player.Y
	switch dir {
	case protocol.DirUp:
		ny--
	case protocol.DirDown:
		ny++
	case protocol.DirLeft:
		nx--
	case protocol.DirRight:
		nx++
	}
	if !w.walkableForLocked(nx, ny, player.Username) {
		return fmt.Sprintf("%s 被墙体或单位挡住了", username), w.profileLocked(player), true
	}

	player.X = nx
	player.Y = ny
	player.LastUpdate = time.Now()
	event := fmt.Sprintf("%s 移动到了 (%d,%d)", username, nx, ny)

	if treasureID, treasure, ok := w.treasureAtLocked(nx, ny); ok {
		player.Treasures += treasure.Value
		delete(w.treasures, treasureID)
		event = fmt.Sprintf("%s 在 (%d,%d) 拾取了%s，战利品 +%d", username, nx, ny, treasure.Kind, treasure.Value)
	}

	w.version++
	return event, w.profileLocked(player), true
}

func (w *World) HealPlayer(username string) (string, protocol.UserProfile, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	player, ok := w.players[username]
	if !ok {
		return "", protocol.UserProfile{}, false
	}
	w.refreshPlayerStateLocked(player)
	if !player.Alive {
		return fmt.Sprintf("%s 倒地时无法使用药剂，%d 秒后复活", username, w.playerRespawnInLocked(player)), w.profileLocked(player), true
	}
	if player.Potions <= 0 {
		return fmt.Sprintf("%s 的治疗药剂已经用完", username), w.profileLocked(player), true
	}
	player.Potions--
	before := player.HP
	player.HP += protocol.HealAmount
	if player.HP > player.MaxHP {
		player.HP = player.MaxHP
	}
	player.LastUpdate = time.Now()
	w.version++
	return fmt.Sprintf("%s 回复了 %d 点生命", username, player.HP-before), w.profileLocked(player), true
}

func (w *World) Attack(username string) (string, string, string, protocol.UserProfile, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	player, ok := w.players[username]
	if !ok {
		return "", "", "", protocol.UserProfile{}, false
	}
	w.refreshPlayerStateLocked(player)
	if !player.Alive {
		return fmt.Sprintf("%s 倒地时无法发起攻击，%d 秒后复活", username, w.playerRespawnInLocked(player)), "", "", w.profileLocked(player), true
	}

	var npcTarget *NPC
	for _, npc := range w.npcs {
		if !npc.Alive || distance(player.X, player.Y, npc.X, npc.Y) > protocol.AttackRange {
			continue
		}
		if npcTarget == nil || npc.HP < npcTarget.HP || (npc.HP == npcTarget.HP && npc.ID < npcTarget.ID) {
			npcTarget = npc
		}
	}
	if npcTarget != nil {
		npcTarget.HP -= player.Attack
		player.LastUpdate = time.Now()
		event := fmt.Sprintf("%s 对 %s 造成了 %d 点伤害", username, npcTarget.Name, player.Attack)
		if npcTarget.HP <= 0 {
			npcTarget.HP = 0
			npcTarget.Alive = false
			delete(w.npcs, npcTarget.ID)
			player.Kills++
			w.dropTreasureLocked(npcTarget.X, npcTarget.Y, 2+w.rng.Intn(3), "怪物战利品")
			event = fmt.Sprintf("%s 击败了 %s，掉落了一份战利品", username, npcTarget.Name)
		}
		w.version++
		return event, "", "", w.profileLocked(player), true
	}

	var playerTarget *Player
	for _, other := range w.players {
		if other.Username == username || !other.Alive || distance(player.X, player.Y, other.X, other.Y) > protocol.AttackRange {
			continue
		}
		if playerTarget == nil || other.HP < playerTarget.HP || (other.HP == playerTarget.HP && other.Username < playerTarget.Username) {
			playerTarget = other
		}
	}
	if playerTarget == nil {
		return fmt.Sprintf("%s 的攻击范围内没有目标", username), "", "", w.profileLocked(player), true
	}

	playerTarget.HP -= player.Attack
	player.LastUpdate = time.Now()
	event := fmt.Sprintf("%s 对 %s 造成了 %d 点伤害", username, playerTarget.Username, player.Attack)
	targetEvent := fmt.Sprintf("你遭到了 %s 的攻击，生命 -%d", username, player.Attack)
	if playerTarget.HP <= 0 {
		player.Kills++
		w.knockDownPlayerLocked(playerTarget)
		event = fmt.Sprintf("%s 击败了 %s", username, playerTarget.Username)
		targetEvent = fmt.Sprintf("你被 %s 击倒了，%d 秒后复活", username, w.playerRespawnInLocked(playerTarget))
	}

	w.version++
	return event, playerTarget.Username, targetEvent, w.profileLocked(player), true
}

func (w *World) BuyItem(username, item string) (string, protocol.UserProfile, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	player, ok := w.players[username]
	if !ok {
		return "", protocol.UserProfile{}, false
	}
	w.refreshPlayerStateLocked(player)
	if !player.Alive {
		return fmt.Sprintf("%s 倒地时无法打开商店，%d 秒后复活", username, w.playerRespawnInLocked(player)), w.profileLocked(player), true
	}

	switch item {
	case "potion":
		if player.Treasures < protocol.PotionPrice {
			return fmt.Sprintf("战利品不足，购买药剂需要 %d", protocol.PotionPrice), w.profileLocked(player), true
		}
		if player.Potions >= protocol.PotionCap {
			return fmt.Sprintf("药剂携带已达上限 %d", protocol.PotionCap), w.profileLocked(player), true
		}
		player.Treasures -= protocol.PotionPrice
		player.Potions++
		player.LastUpdate = time.Now()
		w.version++
		return fmt.Sprintf("%s 在商店购入药剂，药剂 +1，战利品 -%d", username, protocol.PotionPrice), w.profileLocked(player), true
	case "weapon":
		if player.Treasures < protocol.WeaponPrice {
			return fmt.Sprintf("战利品不足，强化武器需要 %d", protocol.WeaponPrice), w.profileLocked(player), true
		}
		player.Treasures -= protocol.WeaponPrice
		player.Attack += protocol.WeaponBoost
		player.LastUpdate = time.Now()
		w.version++
		return fmt.Sprintf("%s 在商店强化武器，攻击 +%d，战利品 -%d", username, protocol.WeaponBoost, protocol.WeaponPrice), w.profileLocked(player), true
	default:
		return "商店中没有这个商品", w.profileLocked(player), true
	}
}

func (w *World) BackgroundStep() []string {
	w.mu.Lock()
	defer w.mu.Unlock()

	events := make([]string, 0, 4)

	for len(w.npcs) < protocol.MinNPCs {
		if npc := w.spawnNPCLocked(); npc != nil {
			events = append(events, fmt.Sprintf("%s 刷新了 %s，位置 (%d,%d)", w.cfg.Name, npc.Name, npc.X, npc.Y))
		} else {
			break
		}
	}
	if len(w.treasures) < protocol.MaxTreasures && w.rng.Intn(100) < 45 {
		if treasure := w.spawnTreasureLocked("野外宝箱"); treasure != nil {
			events = append(events, fmt.Sprintf("%s 刷新了宝物，位置 (%d,%d)", w.cfg.Name, treasure.X, treasure.Y))
		}
	}

	ids := make([]string, 0, len(w.npcs))
	for id := range w.npcs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		npc := w.npcs[id]
		if npc == nil || !npc.Alive {
			continue
		}
		target := w.closestPlayerLocked(npc.X, npc.Y, 1)
		if target != nil {
			target.HP -= npc.Attack
			target.LastUpdate = time.Now()
			if target.HP <= 0 {
				w.knockDownPlayerLocked(target)
				events = append(events, fmt.Sprintf("%s 被 %s 击倒了", target.Username, npc.Name))
			} else {
				events = append(events, fmt.Sprintf("%s 对 %s 造成了 %d 点伤害", npc.Name, target.Username, npc.Attack))
			}
			w.version++
			continue
		}

		base := w.rng.Intn(4)
		for step := 0; step < 4; step++ {
			nx, ny := npc.X, npc.Y
			switch (base + step) % 4 {
			case 0:
				ny--
			case 1:
				ny++
			case 2:
				nx--
			default:
				nx++
			}
			if w.walkableForLocked(nx, ny, "") {
				npc.X = nx
				npc.Y = ny
				w.version++
				break
			}
		}
	}

	return events
}

func (w *World) Snapshot(nodeID string) protocol.MapView {
	w.mu.Lock()
	defer w.mu.Unlock()

	players := make([]protocol.PlayerView, 0, len(w.players))
	for _, player := range w.players {
		w.refreshPlayerStateLocked(player)
		players = append(players, w.playerViewLocked(player))
	}
	sort.Slice(players, func(i, j int) bool { return players[i].Username < players[j].Username })

	npcs := make([]protocol.NPCView, 0, len(w.npcs))
	for _, npc := range w.npcs {
		npcs = append(npcs, protocol.NPCView{
			ID:     npc.ID,
			Name:   npc.Name,
			X:      npc.X,
			Y:      npc.Y,
			HP:     npc.HP,
			MaxHP:  npc.MaxHP,
			Attack: npc.Attack,
			Alive:  npc.Alive,
		})
	}
	sort.Slice(npcs, func(i, j int) bool { return npcs[i].ID < npcs[j].ID })

	treasures := make([]protocol.TreasureView, 0, len(w.treasures))
	for _, treasure := range w.treasures {
		treasures = append(treasures, protocol.TreasureView{
			ID:    treasure.ID,
			Kind:  treasure.Kind,
			X:     treasure.X,
			Y:     treasure.Y,
			Value: treasure.Value,
		})
	}
	sort.Slice(treasures, func(i, j int) bool { return treasures[i].ID < treasures[j].ID })

	return protocol.MapView{
		ID:        w.cfg.ID,
		Name:      w.cfg.Name,
		NodeID:    nodeID,
		Width:     len(w.cfg.Layout[0]),
		Height:    len(w.cfg.Layout),
		Terrain:   gridToStrings(w.terrain),
		Players:   players,
		NPCs:      npcs,
		Treasures: treasures,
		Version:   w.version,
	}
}

func (w *World) Counts() (players, npcs, treasures int, version int64) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.players), len(w.npcs), len(w.treasures), w.version
}

func (w *World) CaptureCheckpoint(nodeID string) protocol.MapCheckpoint {
	snapshot := w.Snapshot(nodeID)
	return protocol.MapCheckpoint{
		MapID:      snapshot.ID,
		NodeID:     snapshot.NodeID,
		Version:    snapshot.Version,
		Terrain:    snapshot.Terrain,
		Players:    snapshot.Players,
		NPCs:       snapshot.NPCs,
		Treasures:  snapshot.Treasures,
		Checkpoint: time.Now(),
	}
}

func (w *World) RestoreCheckpoint(cp protocol.MapCheckpoint) {
	if cp.MapID != w.cfg.ID || cp.Version == 0 {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if len(cp.Terrain) > 0 {
		w.terrain = stringsToGrid(cp.Terrain)
	}
	w.players = make(map[string]*Player)
	for _, view := range cp.Players {
		w.players[view.Username] = &Player{
			Username:   view.Username,
			MapID:      w.cfg.ID,
			X:          view.X,
			Y:          view.Y,
			HP:         view.HP,
			MaxHP:      view.MaxHP,
			Attack:     view.Attack,
			Potions:    valueOr(view.Potions, protocol.MaxPotions),
			Treasures:  view.Treasures,
			Kills:      view.Kills,
			Deaths:     view.Deaths,
			Victories:  view.Victories,
			Alive:      view.Alive,
			LastUpdate: time.Now(),
		}
	}
	w.npcs = make(map[string]*NPC)
	for _, view := range cp.NPCs {
		w.npcs[view.ID] = &NPC{
			ID:     view.ID,
			Name:   view.Name,
			X:      view.X,
			Y:      view.Y,
			HP:     view.HP,
			MaxHP:  view.MaxHP,
			Attack: view.Attack,
			Alive:  view.Alive,
		}
	}
	w.treasures = make(map[string]*Treasure)
	for _, view := range cp.Treasures {
		w.treasures[view.ID] = &Treasure{
			ID:    view.ID,
			Kind:  view.Kind,
			X:     view.X,
			Y:     view.Y,
			Value: view.Value,
		}
	}
	w.version = cp.Version
	w.nextNPC = len(w.npcs) + 1
	w.nextTreasure = len(w.treasures) + 1
}

func (w *World) bootstrap() {
	w.mu.Lock()
	defer w.mu.Unlock()

	for len(w.npcs) < protocol.MinNPCs {
		w.spawnNPCLocked()
	}
	for len(w.treasures) < protocol.MaxTreasures/2 {
		w.spawnTreasureLocked("遗迹补给")
	}
}

func (w *World) respawnPlayer(username string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	player, ok := w.players[username]
	if !ok {
		return
	}
	if player.Alive {
		return
	}
	player.X, player.Y = w.findSafePositionLocked(w.cfg.SpawnX, w.cfg.SpawnY, username)
	player.HP = player.MaxHP
	player.Potions = protocol.MaxPotions
	player.Alive = true
	player.RespawnAt = time.Time{}
	player.LastUpdate = time.Now()
	w.version++
}

func (w *World) RewardPlayer(username string, treasureDelta, victoryDelta int) (protocol.UserProfile, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	player, ok := w.players[username]
	if !ok {
		return protocol.UserProfile{}, false
	}
	player.Treasures += treasureDelta
	player.Victories += victoryDelta
	player.LastUpdate = time.Now()
	w.version++
	return w.profileLocked(player), true
}

func (w *World) playerViewLocked(player *Player) protocol.PlayerView {
	return protocol.PlayerView{
		Username:   player.Username,
		MapID:      player.MapID,
		X:          player.X,
		Y:          player.Y,
		HP:         player.HP,
		MaxHP:      player.MaxHP,
		Attack:     player.Attack,
		Potions:    player.Potions,
		Treasures:  player.Treasures,
		Kills:      player.Kills,
		Deaths:     player.Deaths,
		Victories:  player.Victories,
		Alive:      player.Alive,
		RespawnIn:  w.playerRespawnInLocked(player),
		LastUpdate: player.LastUpdate.Format(time.RFC3339),
	}
}

func (w *World) profileLocked(player *Player) protocol.UserProfile {
	return protocol.UserProfile{
		Username:  player.Username,
		LastMap:   w.cfg.ID,
		X:         player.X,
		Y:         player.Y,
		HP:        player.HP,
		MaxHP:     player.MaxHP,
		Attack:    player.Attack,
		Potions:   player.Potions,
		Treasures: player.Treasures,
		Kills:     player.Kills,
		Deaths:    player.Deaths,
		Victories: player.Victories,
		Alive:     player.Alive,
	}
}

func (w *World) closestPlayerLocked(x, y, radius int) *Player {
	var best *Player
	for _, player := range w.players {
		w.refreshPlayerStateLocked(player)
		if !player.Alive || distance(x, y, player.X, player.Y) > radius {
			continue
		}
		if best == nil || player.HP < best.HP || (player.HP == best.HP && player.Username < best.Username) {
			best = player
		}
	}
	return best
}

func (w *World) spawnNPCLocked() *NPC {
	x, y, ok := w.randomOpenCellSpreadLocked("", 6)
	if !ok {
		return nil
	}
	names := []string{"史莱姆", "荒原狼", "流寇", "宝匣怪", "石像魔", "潜行兽"}
	id := fmt.Sprintf("%s-npc-%d", w.cfg.ID, w.nextNPC)
	npc := &NPC{
		ID:     id,
		Name:   names[w.nextNPC%len(names)],
		X:      x,
		Y:      y,
		HP:     70 + w.rng.Intn(30),
		MaxHP:  90,
		Attack: protocol.NPCDamage,
		Alive:  true,
	}
	w.nextNPC++
	w.npcs[id] = npc
	w.version++
	return npc
}

func (w *World) spawnTreasureLocked(kind string) *Treasure {
	x, y, ok := w.randomOpenCellSpreadLocked("", 3)
	if !ok {
		return nil
	}
	return w.dropTreasureLocked(x, y, 1+w.rng.Intn(4), kind)
}

func (w *World) dropTreasureLocked(x, y, value int, kind string) *Treasure {
	if x < 0 || y < 0 || y >= len(w.terrain) || x >= len(w.terrain[y]) {
		return nil
	}
	if _, treasure, ok := w.treasureAtLocked(x, y); ok {
		treasure.Value += value
		w.version++
		return treasure
	}
	id := fmt.Sprintf("%s-t-%d", w.cfg.ID, w.nextTreasure)
	w.nextTreasure++
	treasure := &Treasure{
		ID:    id,
		Kind:  kind,
		X:     x,
		Y:     y,
		Value: value,
	}
	w.treasures[id] = treasure
	w.version++
	return treasure
}

func (w *World) treasureAtLocked(x, y int) (string, *Treasure, bool) {
	for id, treasure := range w.treasures {
		if treasure.X == x && treasure.Y == y {
			return id, treasure, true
		}
	}
	return "", nil, false
}

func (w *World) walkableForLocked(x, y int, ignorePlayer string) bool {
	if y < 0 || y >= len(w.terrain) || x < 0 || x >= len(w.terrain[y]) {
		return false
	}
	if w.terrain[y][x] == '#' {
		return false
	}
	for _, player := range w.players {
		if player.Username != ignorePlayer && player.Alive && player.X == x && player.Y == y {
			return false
		}
	}
	for _, npc := range w.npcs {
		if npc.Alive && npc.X == x && npc.Y == y {
			return false
		}
	}
	return true
}

func (w *World) randomOpenCellLocked(ignorePlayer string) (int, int, bool) {
	for tries := 0; tries < 256; tries++ {
		x := w.rng.Intn(len(w.terrain[0]))
		y := w.rng.Intn(len(w.terrain))
		if w.walkableForLocked(x, y, ignorePlayer) {
			return x, y, true
		}
	}
	for y := 0; y < len(w.terrain); y++ {
		for x := 0; x < len(w.terrain[y]); x++ {
			if w.walkableForLocked(x, y, ignorePlayer) {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

func (w *World) findSafePositionLocked(preferredX, preferredY int, ignorePlayer string) (int, int) {
	if w.walkableForLocked(preferredX, preferredY, ignorePlayer) {
		return preferredX, preferredY
	}
	x, y, ok := w.randomOpenCellSpreadLocked(ignorePlayer, 4)
	if ok {
		return x, y
	}
	return w.cfg.SpawnX, w.cfg.SpawnY
}

func (w *World) randomOpenCellSpreadLocked(ignorePlayer string, minGap int) (int, int, bool) {
	bestX, bestY := 0, 0
	bestScore := -1
	found := false
	for tries := 0; tries < 96; tries++ {
		x := w.rng.Intn(len(w.terrain[0]))
		y := w.rng.Intn(len(w.terrain))
		if !w.walkableForLocked(x, y, ignorePlayer) {
			continue
		}
		score := w.spawnScoreLocked(x, y)
		if !found || score > bestScore {
			bestX, bestY = x, y
			bestScore = score
			found = true
		}
		if score >= minGap {
			return x, y, true
		}
	}
	if found {
		return bestX, bestY, true
	}
	return w.randomOpenCellLocked(ignorePlayer)
}

func (w *World) spawnScoreLocked(x, y int) int {
	bestNPC := protocol.MapWidth + protocol.MapHeight
	for _, npc := range w.npcs {
		if !npc.Alive {
			continue
		}
		if d := distance(x, y, npc.X, npc.Y); d < bestNPC {
			bestNPC = d
		}
	}
	bestPlayer := protocol.MapWidth + protocol.MapHeight
	for _, player := range w.players {
		if !player.Alive {
			continue
		}
		if d := distance(x, y, player.X, player.Y); d < bestPlayer {
			bestPlayer = d
		}
	}
	return bestNPC*3 + bestPlayer + distance(x, y, w.cfg.SpawnX, w.cfg.SpawnY)/2
}

func (w *World) knockDownPlayerLocked(player *Player) {
	player.HP = 0
	player.Alive = false
	player.Deaths++
	player.RespawnAt = time.Now().Add(4 * time.Second)
	player.LastUpdate = time.Now()
	go func(name string) {
		time.Sleep(4 * time.Second)
		w.respawnPlayer(name)
	}(player.Username)
}

func (w *World) refreshPlayerStateLocked(player *Player) {
	if player == nil || player.Alive || player.RespawnAt.IsZero() {
		return
	}
	if time.Now().Before(player.RespawnAt) {
		return
	}
	player.X, player.Y = w.findSafePositionLocked(w.cfg.SpawnX, w.cfg.SpawnY, player.Username)
	player.HP = player.MaxHP
	player.Potions = max(player.Potions, protocol.MaxPotions)
	player.Alive = true
	player.RespawnAt = time.Time{}
	player.LastUpdate = time.Now()
	w.version++
}

func (w *World) playerRespawnInLocked(player *Player) int {
	if player == nil || player.Alive || player.RespawnAt.IsZero() {
		return 0
	}
	remain := int(time.Until(player.RespawnAt).Seconds())
	if remain < 1 {
		return 1
	}
	return remain
}

func stringsToGrid(rows []string) [][]rune {
	grid := make([][]rune, len(rows))
	for i, row := range rows {
		grid[i] = []rune(row)
	}
	return grid
}

func gridToStrings(grid [][]rune) []string {
	rows := make([]string, len(grid))
	for i, row := range grid {
		rows[i] = string(row)
	}
	return rows
}

func valueOr(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

func distance(ax, ay, bx, by int) int {
	return int(math.Abs(float64(ax-bx)) + math.Abs(float64(ay-by)))
}

func blankGrid() [][]rune {
	grid := make([][]rune, protocol.MapHeight)
	for y := range grid {
		grid[y] = make([]rune, protocol.MapWidth)
		for x := range grid[y] {
			grid[y][x] = '.'
		}
	}
	return grid
}

func drawBorder(grid [][]rune) {
	for x := 0; x < len(grid[0]); x++ {
		grid[0][x] = '#'
		grid[len(grid)-1][x] = '#'
	}
	for y := 0; y < len(grid); y++ {
		grid[y][0] = '#'
		grid[y][len(grid[y])-1] = '#'
	}
}

func fillRect(grid [][]rune, x, y, width, height int) {
	for yy := y; yy < y+height && yy < len(grid); yy++ {
		for xx := x; xx < x+width && xx < len(grid[yy]); xx++ {
			grid[yy][xx] = '#'
		}
	}
}

func drawH(grid [][]rune, y, fromX, toX int) {
	if y < 0 || y >= len(grid) {
		return
	}
	for x := max(0, fromX); x <= toX && x < len(grid[y]); x++ {
		grid[y][x] = '#'
	}
}

func drawV(grid [][]rune, x, fromY, toY int) {
	for y := max(0, fromY); y <= toY && y < len(grid); y++ {
		if x >= 0 && x < len(grid[y]) {
			grid[y][x] = '#'
		}
	}
}

func carve(grid [][]rune, x, y int) {
	if y >= 0 && y < len(grid) && x >= 0 && x < len(grid[y]) {
		grid[y][x] = '.'
	}
}

func buildGreenMap() MapConfig {
	grid := blankGrid()
	drawBorder(grid)
	fillRect(grid, 8, 3, 14, 5)
	fillRect(grid, 33, 14, 14, 4)
	drawV(grid, 26, 1, 21)
	drawV(grid, 45, 2, 13)
	drawH(grid, 9, 1, 20)
	drawH(grid, 7, 30, 54)
	carve(grid, 26, 5)
	carve(grid, 26, 12)
	carve(grid, 26, 18)
	carve(grid, 45, 5)
	carve(grid, 45, 10)
	carve(grid, 6, 9)
	carve(grid, 12, 9)
	carve(grid, 18, 9)
	carve(grid, 37, 7)
	carve(grid, 44, 7)
	carve(grid, 50, 7)
	return MapConfig{
		ID:     "green",
		Name:   "青岚要塞",
		Layout: gridToStrings(grid),
		SpawnX: 4,
		SpawnY: 4,
		BossX:  50,
		BossY:  20,
	}
}

func buildCaveMap() MapConfig {
	grid := blankGrid()
	drawBorder(grid)
	fillRect(grid, 5, 5, 10, 9)
	fillRect(grid, 36, 12, 12, 7)
	drawV(grid, 28, 1, 22)
	drawH(grid, 4, 18, 51)
	drawH(grid, 18, 2, 25)
	drawV(grid, 18, 10, 20)
	carve(grid, 28, 4)
	carve(grid, 28, 11)
	carve(grid, 28, 18)
	carve(grid, 22, 4)
	carve(grid, 34, 4)
	carve(grid, 45, 4)
	carve(grid, 9, 18)
	carve(grid, 14, 18)
	carve(grid, 18, 15)
	return MapConfig{
		ID:     "cave",
		Name:   "玄矿地窟",
		Layout: gridToStrings(grid),
		SpawnX: 4,
		SpawnY: 20,
		BossX:  49,
		BossY:  20,
	}
}

func buildRuinsMap() MapConfig {
	grid := blankGrid()
	drawBorder(grid)
	drawV(grid, 12, 2, 20)
	drawV(grid, 24, 1, 18)
	drawV(grid, 37, 5, 22)
	drawH(grid, 6, 2, 22)
	drawH(grid, 13, 15, 40)
	fillRect(grid, 42, 3, 8, 5)
	fillRect(grid, 6, 15, 8, 4)
	carve(grid, 12, 5)
	carve(grid, 12, 11)
	carve(grid, 12, 17)
	carve(grid, 24, 4)
	carve(grid, 24, 10)
	carve(grid, 24, 16)
	carve(grid, 37, 9)
	carve(grid, 37, 17)
	carve(grid, 8, 6)
	carve(grid, 18, 6)
	carve(grid, 19, 13)
	carve(grid, 29, 13)
	carve(grid, 44, 13)
	return MapConfig{
		ID:     "ruins",
		Name:   "残星遗迹",
		Layout: gridToStrings(grid),
		SpawnX: 50,
		SpawnY: 4,
		BossX:  50,
		BossY:  20,
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
