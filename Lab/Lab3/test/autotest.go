package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const serverAddr = "127.0.0.1:9310"

const (
	TypeRegister   = "register"
	TypeLogin      = "login"
	TypeLogout     = "logout"
	TypeMove       = "move"
	TypeAttack     = "attack"
	TypeBossAttack = "boss_attack"
	TypeSwitchMap  = "switch_map"
	TypeAdmin      = "admin"
	TypeAuth       = "auth"
	TypeState      = "state"
	TypeError      = "error"

	DirUp    = "up"
	DirDown  = "down"
	DirLeft  = "left"
	DirRight = "right"
)

type Message struct {
	Type     string      `json:"type"`
	Action   string      `json:"action,omitempty"`
	Username string      `json:"username,omitempty"`
	Password string      `json:"password,omitempty"`
	Confirm  string      `json:"confirm,omitempty"`
	Dir      string      `json:"dir,omitempty"`
	MapID    string      `json:"map_id,omitempty"`
	Item     string      `json:"item,omitempty"`
	NodeID   string      `json:"node_id,omitempty"`
	Text     string      `json:"text,omitempty"`
	OK       bool        `json:"ok,omitempty"`
	Error    string      `json:"error,omitempty"`
	State    *WorldState `json:"state,omitempty"`
}

type PlayerView struct {
	Username  string `json:"username"`
	MapID     string `json:"map_id"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	HP        int    `json:"hp"`
	MaxHP     int    `json:"max_hp"`
	Attack    int    `json:"attack"`
	Potions   int    `json:"potions"`
	Treasures int    `json:"treasures"`
	Alive     bool   `json:"alive"`
	RespawnIn int    `json:"respawn_in"`
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
}

type MapView struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	NodeID  string       `json:"node_id"`
	Terrain []string     `json:"terrain"`
	Players []PlayerView `json:"players"`
}

type MapBrief struct {
	ID        string `json:"id"`
	NodeID    string `json:"node_id"`
	IsCurrent bool   `json:"is_current"`
}

type NodeView struct {
	ID      string `json:"id"`
	Healthy bool   `json:"healthy"`
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

type Client struct {
	conn  net.Conn
	enc   *json.Encoder
	msgCh chan Message
	errCh chan error
}

func connect(mode, username, password string, confirm ...string) (*Client, *WorldState, error) {
	conn, err := net.DialTimeout("tcp", serverAddr, 2*time.Second)
	if err != nil {
		return nil, nil, err
	}
	client := &Client{
		conn:  conn,
		enc:   json.NewEncoder(conn),
		msgCh: make(chan Message, 128),
		errCh: make(chan error, 1),
	}

	go func() {
		dec := json.NewDecoder(conn)
		for {
			var msg Message
			if err := dec.Decode(&msg); err != nil {
				client.errCh <- err
				return
			}
			client.msgCh <- msg
		}
	}()

	msg := Message{Type: mode, Username: username, Password: password}
	if len(confirm) > 0 {
		msg.Confirm = confirm[0]
	} else if mode == TypeRegister {
		msg.Confirm = password
	}
	if err := client.send(msg); err != nil {
		return nil, nil, err
	}

	reply, err := client.recvWithTimeout(3 * time.Second)
	if err != nil {
		client.close()
		return nil, nil, err
	}
	if reply.Type == TypeError {
		client.close()
		return nil, nil, fmt.Errorf(reply.Error)
	}
	if reply.Type != TypeAuth || !reply.OK || reply.State == nil {
		client.close()
		return nil, nil, fmt.Errorf("收到异常认证消息：%#v", reply)
	}
	return client, reply.State, nil
}

func sendAdmin(action, nodeID string) (string, error) {
	conn, err := net.DialTimeout("tcp", serverAddr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	if err := enc.Encode(Message{Type: TypeAdmin, Action: action, NodeID: nodeID}); err != nil {
		return "", err
	}

	var msg Message
	if err := dec.Decode(&msg); err != nil {
		return "", err
	}
	if msg.Type == TypeError || !msg.OK {
		return "", fmt.Errorf(msg.Error)
	}
	return msg.Text, nil
}

func (c *Client) send(msg Message) error {
	return c.enc.Encode(msg)
}

func (c *Client) recvWithTimeout(d time.Duration) (Message, error) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case msg := <-c.msgCh:
		return msg, nil
	case err := <-c.errCh:
		return Message{}, err
	case <-timer.C:
		return Message{}, fmt.Errorf("等待超时：%s", d)
	}
}

func (c *Client) waitState(d time.Duration, predicate func(*WorldState) bool) (*WorldState, error) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		msg, err := c.recvWithTimeout(time.Until(deadline))
		if err != nil {
			return nil, err
		}
		if msg.Type == TypeError {
			return nil, fmt.Errorf(msg.Error)
		}
		if (msg.Type == TypeState || msg.Type == TypeAuth) && msg.State != nil && predicate(msg.State) {
			return msg.State, nil
		}
	}
	return nil, fmt.Errorf("在 %s 内未等到目标状态", d)
}

func (c *Client) close() {
	_ = c.conn.Close()
}

var (
	mu     sync.Mutex
	passed int
	failed int
)

func main() {
	test注册与拓扑()
	test切图路由()
	test世界首领跨图协同()
	// test倒地禁止切图()
	test持久化恢复()
	test节点故障切换()

	fmt.Printf("\n测试完成：通过 %d 项，失败 %d 项\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func check(name string, ok bool, detail string) {
	mu.Lock()
	defer mu.Unlock()
	if ok {
		passed++
		fmt.Printf("  ✅ 通过  %s\n", name)
		return
	}
	failed++
	fmt.Printf("  ❌ 失败  %s -> %s\n", name, detail)
}

func uniqueUser(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func test注册与拓扑() {
	fmt.Println("【测试 1】注册登录与拓扑发布")
	user := uniqueUser("lab3_auth")
	client, state, err := connect(TypeRegister, user, "pw")
	if err != nil {
		check("注册", false, err.Error())
		return
	}
	defer client.close()

	check("初始地图为 green", state.Map.ID == "green", fmt.Sprintf("实际=%s", state.Map.ID))
	check("发布 3 张地图", len(state.Maps) == 3, fmt.Sprintf("数量=%d", len(state.Maps)))
	check("发布 3 个节点", len(state.Nodes) == 3, fmt.Sprintf("数量=%d", len(state.Nodes)))
}

func test切图路由() {
	fmt.Println("【测试 2】多地图切换与节点路由")
	user := uniqueUser("lab3_switch")
	client, _, err := connect(TypeRegister, user, "pw")
	if err != nil {
		check("注册", false, err.Error())
		return
	}
	defer client.close()

	_ = client.send(Message{Type: TypeSwitchMap, MapID: "cave"})
	state, err := client.waitState(3*time.Second, func(state *WorldState) bool {
		return state.Map.ID == "cave"
	})
	if err != nil {
		check("切换到 cave", false, err.Error())
		return
	}
	check("cave 由 node-b 承载", state.Map.NodeID == "node-b", fmt.Sprintf("实际=%s", state.Map.NodeID))

	_ = client.send(Message{Type: TypeSwitchMap, MapID: "ruins"})
	state, err = client.waitState(3*time.Second, func(state *WorldState) bool {
		return state.Map.ID == "ruins"
	})
	if err != nil {
		check("切换到 ruins", false, err.Error())
		return
	}
	check("ruins 由 node-a 承载", state.Map.NodeID == "node-a", fmt.Sprintf("实际=%s", state.Map.NodeID))
}

func test世界首领跨图协同() {
	fmt.Println("【测试 3】世界首领跨图共享血量与广播")
	userA := uniqueUser("lab3_boss_a")
	userB := uniqueUser("lab3_boss_b")

	clientA, stateA, err := connect(TypeRegister, userA, "pw")
	if err != nil {
		check("注册 A", false, err.Error())
		return
	}
	defer clientA.close()

	clientB, _, err := connect(TypeRegister, userB, "pw")
	if err != nil {
		check("注册 B", false, err.Error())
		return
	}
	defer clientB.close()

	_ = clientB.send(Message{Type: TypeSwitchMap, MapID: "cave"})
	stateB, err := clientB.waitState(3*time.Second, func(state *WorldState) bool {
		return state.Map.ID == "cave"
	})
	if err != nil {
		check("B 切换到 cave", false, err.Error())
		return
	}

	stateA, err = walkToBossRange(clientA, stateA)
	check("A 靠近 green 首领投影", err == nil, errString(err))
	if err != nil {
		return
	}
	stateB, err = walkToBossRange(clientB, stateB)
	check("B 靠近 cave 首领投影", err == nil, errString(err))
	if err != nil {
		return
	}

	initialHP := stateA.Boss.HP
	damageA := bossDamage(stateA.Self)
	damageB := bossDamage(stateB.Self)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = clientA.send(Message{Type: TypeBossAttack})
	}()
	go func() {
		defer wg.Done()
		_ = clientB.send(Message{Type: TypeBossAttack})
	}()
	wg.Wait()

	expectedHP := initialHP - damageA - damageB
	stateA, err = clientA.waitState(5*time.Second, func(state *WorldState) bool {
		return state.Boss.HP == expectedHP && strings.Contains(strings.Join(state.Events, " "), userA) && strings.Contains(strings.Join(state.Events, " "), userB)
	})
	check("A 看到共享血量与广播", err == nil, errString(err))
	if err != nil {
		return
	}
	stateB, err = clientB.waitState(5*time.Second, func(state *WorldState) bool {
		return state.Boss.HP == expectedHP && strings.Contains(strings.Join(state.Events, " "), userA) && strings.Contains(strings.Join(state.Events, " "), userB)
	})
	check("B 看到共享血量与广播", err == nil, errString(err))
	if err != nil {
		return
	}

	for stateA.Boss.HP > damageB {
		beforeHP := stateA.Boss.HP
		beforeVersion := stateA.SessionVersion
		_ = clientA.send(Message{Type: TypeBossAttack})
		stateA, err = clientA.waitState(5*time.Second, func(state *WorldState) bool {
			return state.SessionVersion != beforeVersion && state.Boss.HP < beforeHP
		})
		if err != nil {
			check("持续压低首领血量", false, err.Error())
			return
		}
	}

	_ = clientB.send(Message{Type: TypeBossAttack})
	stateA, err = clientA.waitState(5*time.Second, func(state *WorldState) bool {
		return !state.Boss.Alive && state.Boss.LastHit == userB && strings.Contains(strings.Join(state.Events, " "), "终结")
	})
	check("A 看到 B 终结首领", err == nil, errString(err))
	if err != nil {
		return
	}
	stateB, err = clientB.waitState(5*time.Second, func(state *WorldState) bool {
		return !state.Boss.Alive && state.Boss.LastHit == userB && strings.Contains(strings.Join(state.Events, " "), "终结")
	})
	check("B 的终结者显示正确", err == nil, errString(err))
}

func test倒地禁止切图() {
	fmt.Println("【测试 4】倒地状态禁止切换地图")
	attacker := uniqueUser("lab3_down_a")
	victim := uniqueUser("lab3_down_b")

	clientA, stateA, err := connect(TypeRegister, attacker, "pw")
	if err != nil {
		check("注册攻击者", false, err.Error())
		return
	}
	defer clientA.close()

	clientB, _, err := connect(TypeRegister, victim, "pw")
	if err != nil {
		check("注册受击者", false, err.Error())
		return
	}
	defer clientB.close()

	stateA, err = clientA.waitState(3*time.Second, func(state *WorldState) bool {
		return len(state.Map.Players) >= 2
	})
	if err != nil {
		check("攻击者看到受击者进入地图", false, err.Error())
		return
	}
	target := findPlayer(stateA, victim)
	if target == nil {
		check("定位受击者", false, "地图中未找到受击者")
		return
	}

	stateA, err = walkToRange(clientA, stateA, target.X, target.Y, 2)
	check("攻击者靠近受击者", err == nil, errString(err))
	if err != nil {
		return
	}

	var stateB *WorldState
	for i := 0; i < 6; i++ {
		_ = clientA.send(Message{Type: TypeAttack})
		stateB, err = clientB.waitState(5*time.Second, func(state *WorldState) bool {
			return !state.Self.Alive
		})
		if err == nil {
			break
		}
	}
	check("受击者进入倒地状态", err == nil, errString(err))
	if err != nil {
		return
	}

	beforeVersion := stateB.SessionVersion
	_ = clientB.send(Message{Type: TypeSwitchMap, MapID: "cave"})
	stateB, err = clientB.waitState(5*time.Second, func(state *WorldState) bool {
		return state.SessionVersion > beforeVersion &&
			state.Map.ID == "green" &&
			strings.Contains(strings.Join(state.Events, " "), "复活前不能切换地图")
	})
	check("倒地时切图被拒绝且留在原地图", err == nil, errString(err))
	if err != nil {
		return
	}
	check("倒地时地图未变化", stateB.Map.ID == "green", fmt.Sprintf("实际=%s", stateB.Map.ID))
}

func test持久化恢复() {
	fmt.Println("【测试 4】退出重登后的冷数据恢复")
	user := uniqueUser("lab3_persist")
	client, _, err := connect(TypeRegister, user, "pw")
	if err != nil {
		check("注册", false, err.Error())
		return
	}

	_ = client.send(Message{Type: TypeSwitchMap, MapID: "cave"})
	state, err := client.waitState(3*time.Second, func(state *WorldState) bool {
		return state.Map.ID == "cave"
	})
	if err != nil {
		check("切换到 cave", false, err.Error())
		client.close()
		return
	}

	_ = client.send(Message{Type: TypeMove, Dir: DirDown})
	_ = client.send(Message{Type: TypeMove, Dir: DirDown})
	state, err = client.waitState(3*time.Second, func(state *WorldState) bool {
		return state.Map.ID == "cave" && state.Self.Y >= 22
	})
	if err != nil {
		check("在 cave 中移动", false, err.Error())
		client.close()
		return
	}
	savedX, savedY := state.Self.X, state.Self.Y

	_ = client.send(Message{Type: TypeLogout})
	client.close()
	time.Sleep(300 * time.Millisecond)

	client2, state2, err := connect(TypeLogin, user, "pw")
	if err != nil {
		check("重新登录", false, err.Error())
		return
	}
	defer client2.close()

	check("地图恢复成功", state2.Map.ID == "cave", fmt.Sprintf("实际=%s", state2.Map.ID))
	check("坐标恢复成功", state2.Self.X == savedX && state2.Self.Y == savedY,
		fmt.Sprintf("期望=(%d,%d) 实际=(%d,%d)", savedX, savedY, state2.Self.X, state2.Self.Y))
	check("事件日志不为空", len(strings.Join(state2.Events, "")) > 0, "日志为空")
}

func test节点故障切换() {
	fmt.Println("【测试 5】管理命令触发故障切换与恢复")
	user := uniqueUser("lab3_failover")
	client, _, err := connect(TypeRegister, user, "pw")
	if err != nil {
		check("注册", false, err.Error())
		return
	}
	defer client.close()

	text, err := sendAdmin("故障", "node-a")
	check("发送故障命令", err == nil, errString(err))
	if err != nil {
		return
	}
	check("故障命令返回成功文本", strings.Contains(text, "故障"), text)

	state, err := client.waitState(4*time.Second, func(state *WorldState) bool {
		return state.Map.ID == "green" && state.Map.NodeID == "node-c"
	})
	if err != nil {
		check("green 漂移到 node-c", false, err.Error())
		return
	}
	check("green 主节点切换为 node-c", state.Map.NodeID == "node-c", fmt.Sprintf("实际=%s", state.Map.NodeID))

	text, err = sendAdmin("恢复", "node-a")
	check("发送恢复命令", err == nil, errString(err))
	if err != nil {
		return
	}
	check("恢复命令返回成功文本", strings.Contains(text, "恢复"), text)

	state, err = client.waitState(4*time.Second, func(state *WorldState) bool {
		for _, node := range state.Nodes {
			if node.ID == "node-a" && node.Healthy {
				return true
			}
		}
		return false
	})
	if err != nil {
		check("node-a 恢复在线", false, err.Error())
		return
	}
	check("node-a 在线恢复成功", true, "")
}

func bossSite(state *WorldState, mapID string) *BossSite {
	for _, site := range state.Boss.Sites {
		if site.MapID == mapID {
			copySite := site
			return &copySite
		}
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func walkToBossRange(client *Client, state *WorldState) (*WorldState, error) {
	site := bossSite(state, state.Map.ID)
	if site == nil {
		return nil, fmt.Errorf("地图 %s 未找到首领投影", state.Map.ID)
	}
	return walkToRange(client, state, site.X, site.Y, state.Boss.AttackGap)
}

func walkToRange(client *Client, state *WorldState, targetX, targetY, attackGap int) (*WorldState, error) {
	current := state
	for step := 0; step < 666; step++ {
		if manhattan(current.Self.X, current.Self.Y, targetX, targetY) <= attackGap {
			return current, nil
		}
		path, ok := buildPath(current.Map.Terrain, current.Self.X, current.Self.Y, func(x, y int) bool {
			return manhattan(x, y, targetX, targetY) <= attackGap
		})
		if !ok || len(path) == 0 {
			return nil, fmt.Errorf("未找到前往目标区域的路径")
		}
		beforeVersion := current.SessionVersion
		beforeX, beforeY := current.Self.X, current.Self.Y
		_ = client.send(Message{Type: TypeMove, Dir: path[0]})
		next, err := client.waitState(3*time.Second, func(state *WorldState) bool {
			return state.SessionVersion != beforeVersion
		})
		if err != nil {
			return nil, err
		}
		current = next
		if current.Self.X == beforeX && current.Self.Y == beforeY {
			continue
		}
	}
	return nil, fmt.Errorf("移动步数超限，未能抵达目标区域")
}

func buildPath(terrain []string, startX, startY int, goal func(x, y int) bool) ([]string, bool) {
	if len(terrain) == 0 || len(terrain[0]) == 0 {
		return nil, false
	}
	type point struct {
		x int
		y int
	}
	dirs := []struct {
		name string
		dx   int
		dy   int
	}{
		{name: DirUp, dx: 0, dy: -1},
		{name: DirDown, dx: 0, dy: 1},
		{name: DirLeft, dx: -1, dy: 0},
		{name: DirRight, dx: 1, dy: 0},
	}

	start := point{x: startX, y: startY}
	if goal(startX, startY) {
		return []string{}, true
	}

	queue := []point{start}
	visited := map[point]bool{start: true}
	prev := make(map[point]point)
	prevDir := make(map[point]string)

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dir := range dirs {
			next := point{x: cur.x + dir.dx, y: cur.y + dir.dy}
			if next.y < 0 || next.y >= len(terrain) || next.x < 0 || next.x >= len(terrain[next.y]) {
				continue
			}
			if terrain[next.y][next.x] == '#' || visited[next] {
				continue
			}
			visited[next] = true
			prev[next] = cur
			prevDir[next] = dir.name
			if goal(next.x, next.y) {
				path := []string{}
				for at := next; at != start; at = prev[at] {
					path = append(path, prevDir[at])
				}
				reverseStrings(path)
				return path, true
			}
			queue = append(queue, next)
		}
	}
	return nil, false
}

func reverseStrings(items []string) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func findPlayer(state *WorldState, username string) *PlayerView {
	for _, player := range state.Map.Players {
		if player.Username == username {
			copyPlayer := player
			return &copyPlayer
		}
	}
	return nil
}

func bossDamage(player PlayerView) int {
	damage := player.Attack + player.Treasures/2
	if damage < 20 {
		return 20
	}
	return damage
}

func manhattan(ax, ay, bx, by int) int {
	dx := ax - bx
	if dx < 0 {
		dx = -dx
	}
	dy := ay - by
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}
