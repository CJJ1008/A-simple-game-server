// Lab2 自动测试程序
//
// 用法：
//   1. 启动学生服务器：cd ../student && go run ./cmd/server &
//   2. 运行此测试：  go run autotest.go [测试编号]
//
// 测试内容：
//   Test 1 - 多客户端并发连接     验证 Goroutine 正确启动（任务 D-2）
//   Test 2 - AddPlayer 并发安全   验证写锁保护，ID 唯一不重复（任务 C-1）
//   Test 3 - RemovePlayer 正确性  验证从快照中消失（任务 C-2）
//   Test 4 - MovePlayer 边界检查  验证坐标范围（任务 C-3）
//   Test 5 - GetSnapshot 并发读   验证多客户端同时请求快照不崩溃（任务 C-5）
//   Test 6 - AttackPlayer 伤害    验证扣血与死亡标记（任务 C-4）
//   Test 7 - 广播 Goroutine       验证 TypeBroadcast 消息定期到达（任务 D-1）
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

const serverAddr = "localhost:9001"

// ─── 协议常量 ────────────────────────────────────────────────────────────────

const (
	MapWidth    = 30
	MapHeight   = 20
	InitHP      = 100
	AttackDmg   = 30
	AttackRange = 2

	TypeJoin      = "join"
	TypeMove      = "move"
	TypeAttack    = "attack"
	TypeHeal      = "heal"
	TypeInit      = "init"
	TypeBroadcast = "broadcast"
	TypeEvent     = "event"
	TypeGameOver  = "gameover"

	DirUp    = "up"
	DirDown  = "down"
	DirLeft  = "left"
	DirRight = "right"
)

type Msg struct {
	Type    string  `json:"type"`
	Dir     string  `json:"dir,omitempty"`
	Text    string  `json:"text,omitempty"`
	YourID  int     `json:"your_id,omitempty"`
	Players []PInfo `json:"players,omitempty"`
	Winner  string  `json:"winner,omitempty"`
}

type PInfo struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	HP      int    `json:"hp"`
	MaxHP   int    `json:"max_hp"`
	Potions int    `json:"potions"`
	Alive   bool   `json:"alive"`
	Kills   int    `json:"kills"`
}

// 自定义超时错误，用于模拟 net.Error
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// ─── Client ──────────────────────────────────────────────────────────────────

type Client struct {
	conn  net.Conn
	enc   *json.Encoder
	msgCh chan Msg
	errCh chan error
}

func connect(name string) (*Client, error) {
	var conn net.Conn
	var err error
	for i := 0; i < 10; i++ {
		conn, err = net.DialTimeout("tcp", serverAddr, time.Second)
		if err == nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		return nil, err
	}

	c := &Client{
		conn:  conn,
		enc:   json.NewEncoder(conn),
		msgCh: make(chan Msg, 1024),
		errCh: make(chan error, 1),
	}
	c.send(Msg{Type: TypeJoin, Text: name})

	// 启动独立的 Goroutine 持续读取，不设置 Deadline
	// 彻底避免网络超时截断导致 json.Decoder 状态不可逆损坏的问题
	go func() {
		dec := json.NewDecoder(conn)
		for {
			var m Msg
			if err := dec.Decode(&m); err != nil {
				c.errCh <- err
				return
			}
			c.msgCh <- m
		}
	}()

	return c, nil
}

func (c *Client) send(m Msg) error { return c.enc.Encode(m) }

// recv 读取一条消息（无限等待）
func (c *Client) recv() (Msg, error) {
	select {
	case m := <-c.msgCh:
		return m, nil
	case err := <-c.errCh:
		return Msg{}, err
	}
}

// recvWithTimeout 使用 select 实现安全的超时控制
func (c *Client) recvWithTimeout(d time.Duration) (Msg, error) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case m := <-c.msgCh:
		return m, nil
	case err := <-c.errCh:
		return Msg{}, err
	case <-timer.C:
		return Msg{}, timeoutError{}
	}
}

// drain 丢弃消息直到收到指定类型（每条等最多 3s）。
func (c *Client) drain(wantType string) (Msg, error) {
	for i := 0; i < 40; i++ {
		m, err := c.recvWithTimeout(3 * time.Second)
		if err != nil {
			return Msg{}, fmt.Errorf("drain(%s): %w", wantType, err)
		}
		if m.Type == wantType {
			return m, nil
		}
	}
	return Msg{}, fmt.Errorf("40 条消息内未找到 %s", wantType)
}

// collectFor 在 window 时间窗口内持续收集所有 wantType 类型的消息。
func (c *Client) collectFor(window time.Duration, wantType string) []Msg {
	deadline := time.Now().Add(window)
	var results []Msg
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		perTimeout := remaining
		if perTimeout > 600*time.Millisecond {
			perTimeout = 600 * time.Millisecond
		}
		
		m, err := c.recvWithTimeout(perTimeout)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue 
			}
			break
		}
		if m.Type == wantType {
			results = append(results, m)
		}
	}
	return results
}

// latestSnap 在 window 内收集广播，返回最新的一条快照
func (c *Client) latestSnap(window time.Duration) (Msg, bool) {
	snaps := c.collectFor(window, TypeBroadcast)
	if len(snaps) == 0 {
		return Msg{}, false
	}
	return snaps[len(snaps)-1], true
}

func (c *Client) close() { c.conn.Close() }

// ─── 测试框架 ────────────────────────────────────────────────────────────────

var (
	passed int
	failed int
)

func check(name string, ok bool, detail string) {
	if ok {
		passed++
		fmt.Printf("  ✅ PASS  %s\n", name)
	} else {
		failed++
		fmt.Printf("  ❌ FAIL  %s\n       → %s\n", name, detail)
	}
}

// ─── Test 1：多客户端并发连接 ─────────────────────────────────────────────────

func test1_ConcurrentConnect() {
	fmt.Println("【Test 1】多客户端并发连接（任务 D-2：go handleClient）")
	const N = 5
	type res struct {
		id  int
		err error
	}
	ch := make(chan res, N)
	var wg sync.WaitGroup

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := connect(fmt.Sprintf("T1P%d", i))
			if err != nil {
				ch <- res{err: err}
				return
			}
			defer c.close()
			m, err := c.drain(TypeInit)
			ch <- res{id: m.YourID, err: err}
		}(i)
	}
	wg.Wait()
	close(ch)

	ids := map[int]bool{}
	errCount := 0
	for r := range ch {
		if r.err != nil {
			errCount++
		} else {
			ids[r.id] = true
		}
	}
	check(fmt.Sprintf("%d 个客户端全部连接并收到 init", N),
		errCount == 0, fmt.Sprintf("失败 %d 个（未用 go 启动时，第2个起会超时）", errCount))
	check("所有玩家 ID 唯一（AddPlayer 无 race）",
		len(ids) == N, fmt.Sprintf("唯一 ID=%d，期望 %d", len(ids), N))
}

// ─── Test 2：AddPlayer 并发安全 ───────────────────────────────────────────────

func test2_AddPlayerIDUnique() {
	fmt.Println("【Test 2】AddPlayer 并发安全：ID 唯一（任务 C-1）")
	clients := []*Client{}
	defer func() {
		for _, c := range clients {
			c.close()
		}
	}()
	idList := []int{}

	for i := 0; i < 4; i++ {
		c, err := connect(fmt.Sprintf("T2P%d", i))
		if err != nil {
			check("连接", false, err.Error())
			return
		}
		m, err := c.drain(TypeInit)
		if err != nil {
			check("init", false, err.Error())
			c.close()
			return
		}
		idList = append(idList, m.YourID)
		clients = append(clients, c)
	}

	check("收到 4 个 ID", len(idList) == 4, fmt.Sprintf("len=%d", len(idList)))
	idSet := map[int]bool{}
	for _, id := range idList {
		idSet[id] = true
	}
	check("ID 全部唯一", len(idSet) == 4, fmt.Sprintf("唯一=%d，列表=%v", len(idSet), idList))
	allPos := true
	for _, id := range idList {
		if id <= 0 {
			allPos = false
		}
	}
	check("所有 ID > 0", allPos, fmt.Sprintf("%v", idList))
}

// ─── Test 3：RemovePlayer ────────────────────────────────────────────────────

func test3_RemovePlayer() {
	fmt.Println("【Test 3】RemovePlayer 正确性（任务 C-2）")

	c1, err := connect("T3Alice")
	if err != nil {
		check("连接 Alice", false, err.Error())
		return
	}
	defer c1.close()
	c1.drain(TypeInit)

	c2, err := connect("T3Bob")
	if err != nil {
		check("连接 Bob", false, err.Error())
		return
	}
	c2.drain(TypeInit)

	snap1, ok1 := c1.latestSnap(1200 * time.Millisecond)
	check("连接后收到广播", ok1, "1.2s 内未收到广播")
	if !ok1 {
		c2.close()
		return
	}
	found := 0
	for _, p := range snap1.Players {
		if p.Name == "T3Alice" || p.Name == "T3Bob" {
			found++
		}
	}
	check("广播包含 Alice 和 Bob", found == 2, fmt.Sprintf("找到 %d 个（期望 2）", found))

	c2.close()
	time.Sleep(400 * time.Millisecond)

	snap2, ok2 := c1.latestSnap(1500 * time.Millisecond)
	if !ok2 {
		check("Bob 断线后广播更新", false, "未收到新广播")
		return
	}
	hasBob := false
	for _, p := range snap2.Players {
		if p.Name == "T3Bob" {
			hasBob = true
		}
	}
	check("Bob 断线后从快照消失（RemovePlayer 生效）",
		!hasBob, "T3Bob 仍在快照中（RemovePlayer 未实现或未正确加锁）")
}

// ─── Test 4：MovePlayer 边界检查 ─────────────────────────────────────────────

func test4_MovePlayerBoundary() {
	fmt.Println("【Test 4】MovePlayer 边界检查（任务 C-3）")

	c, err := connect("T4Mover")
	if err != nil {
		check("连接", false, err.Error())
		return
	}
	defer c.close()

	m, err := c.drain(TypeInit)
	check("收到 init", err == nil, fmt.Sprintf("%v", err))
	if err != nil {
		return
	}
	myID := m.YourID

	for i := 0; i < MapHeight+5; i++ {
		c.send(Msg{Type: TypeMove, Dir: DirUp})
	}
	
	snapA, okA := c.latestSnap(2500 * time.Millisecond)
	if okA {
		for _, p := range snapA.Players {
			if p.ID == myID {
				check("向上越界：Y ≥ 0（边界保护）", p.Y >= 0,
					fmt.Sprintf("Y=%d（出现负数，边界未保护）", p.Y))
				check("向上越界：Y == 0（移到顶边）", p.Y == 0,
					fmt.Sprintf("Y=%d，期望 0", p.Y))
			}
		}
	} else {
		check("向上移动后收到快照", false, "2.5s 内未收到广播")
	}

	for i := 0; i < MapWidth+5; i++ {
		c.send(Msg{Type: TypeMove, Dir: DirRight})
	}
	snapB, okB := c.latestSnap(2500 * time.Millisecond)
	if okB {
		for _, p := range snapB.Players {
			if p.ID == myID {
				check("向右越界：X ≤ MapWidth-1（边界保护）", p.X <= MapWidth-1,
					fmt.Sprintf("X=%d（超出地图）", p.X))
				check(fmt.Sprintf("向右越界：X == %d（移到右边界）", MapWidth-1),
					p.X == MapWidth-1, fmt.Sprintf("X=%d，期望 %d", p.X, MapWidth-1))
			}
		}
	} else {
		check("向右移动后收到快照", false, "2.5s 内未收到广播")
	}
}

// ─── Test 5：GetSnapshot 并发读 ──────────────────────────────────────────────

func test5_ConcurrentSnapshot() {
	fmt.Println("【Test 5】GetSnapshot 并发读安全（任务 C-5）")
	const N = 10
	clients := make([]*Client, 0, N)
	for i := 0; i < N; i++ {
		c, err := connect(fmt.Sprintf("T5S%d", i))
		if err != nil {
			check("连接", false, err.Error())
			for _, cc := range clients {
				cc.close()
			}
			return
		}
		c.drain(TypeInit)
		clients = append(clients, c)
	}
	defer func() {
		for _, c := range clients {
			c.close()
		}
	}()

	hitCount := 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			snaps := c.collectFor(1500*time.Millisecond, TypeBroadcast)
			if len(snaps) >= 1 {
				mu.Lock()
				hitCount++
				mu.Unlock()
			}
		}(c)
	}
	wg.Wait()

	check(fmt.Sprintf("%d 个客户端均收到广播（并发 GetSnapshot 安全）", N),
		hitCount == N, fmt.Sprintf("收到广播的客户端=%d（若未加读锁可能 panic 或广播失败）", hitCount))
}

// ─── Test 6：AttackPlayer 伤害与死亡 ─────────────────────────────────────────

func test6_AttackDamage() {
	fmt.Println("【Test 6】AttackPlayer 伤害计算与死亡判断（任务 C-4）")

	atk, err := connect("T6Atk")
	if err != nil {
		check("连接 Attacker", false, err.Error())
		return
	}
	defer atk.close()
	ma, err := atk.drain(TypeInit)
	if err != nil {
		check("Attacker init", false, err.Error())
		return
	}
	atkID := ma.YourID

	vic, err := connect("T6Vic")
	if err != nil {
		check("连接 Victim", false, err.Error())
		return
	}
	defer vic.close()
	vic.drain(TypeInit)

	for i := 0; i < MapWidth+5; i++ {
		atk.send(Msg{Type: TypeMove, Dir: DirLeft})
	}
	for i := 0; i < MapHeight+5; i++ {
		atk.send(Msg{Type: TypeMove, Dir: DirUp})
	}
	for i := 0; i < MapWidth+5; i++ {
		vic.send(Msg{Type: TypeMove, Dir: DirLeft})
	}
	for i := 0; i < MapHeight+5; i++ {
		vic.send(Msg{Type: TypeMove, Dir: DirUp})
	}
	vic.send(Msg{Type: TypeMove, Dir: DirRight}) 

	time.Sleep(1500 * time.Millisecond)

	diagSnap, diagOK := atk.latestSnap(800 * time.Millisecond)
	if diagOK {
		for _, p := range diagSnap.Players {
			if p.ID == atkID {
				fmt.Printf("  ℹ  Attacker 当前位置 (%d,%d)\n", p.X, p.Y)
			}
			if p.Name == "T6Vic" {
				fmt.Printf("  ℹ  Victim   当前位置 (%d,%d)  HP=%d\n", p.X, p.Y, p.HP)
			}
		}
	}

	for i := 0; i < 4; i++ {
		atk.send(Msg{Type: TypeAttack})
		time.Sleep(60 * time.Millisecond)
	}

	snap, ok := atk.latestSnap(1500 * time.Millisecond)
	check("攻击后收到状态快照", ok, "1.5s 内未收到广播")
	if !ok {
		return
	}

	anyDamaged := false
	victimDead := false
	victimHP := -1
	for _, p := range snap.Players {
		if p.HP < p.MaxHP && p.MaxHP > 0 {
			anyDamaged = true
		}
		if p.Name == "T6Vic" {
			victimDead = !p.Alive
			victimHP = p.HP
		}
	}
	check("至少一名玩家 HP 减少（攻击命中）",
		anyDamaged, "所有玩家 HP 仍满（AttackPlayer 未实现，或两者距离 > AttackRange）")
	check("4 次攻击后 Victim 被击杀（Alive=false）",
		victimDead, fmt.Sprintf("Victim.Alive 仍为 true（死亡判断未实现）"))
	if victimHP >= 0 {
		check("死亡后 HP 被夹紧到 0（不出现负数）",
			victimHP == 0, fmt.Sprintf("HP=%d（应为 0，未做 max(0,HP) 处理）", victimHP))
	}
}

// ─── Test 7：广播 Goroutine ───────────────────────────────────────────────────

func test7_BroadcastGoroutine() {
	fmt.Println("【Test 7】广播 Goroutine 定期推送（任务 D-1）")

	c, err := connect("T7Watch")
	if err != nil {
		check("连接", false, err.Error())
		return
	}
	defer c.close()
	c.drain(TypeInit)

	snaps := c.collectFor(2200*time.Millisecond, TypeBroadcast)
	check("2.2 秒内收到 ≥ 2 次广播",
		len(snaps) >= 2,
		fmt.Sprintf("实际收到 %d 次（D-1 广播 Goroutine 未以 go func(){} 形式启动时为 0 次）",
			len(snaps)))
}

// ─── main ────────────────────────────────────────────────────────────────────

func waitServer() bool {
	fmt.Print("等待服务器就绪...")
	for i := 0; i < 30; i++ {
		conn, err := net.DialTimeout("tcp", serverAddr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			fmt.Println("OK")
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Println("\n❌ 无法连接服务器")
	return false
}

func main() {
	fmt.Println("═══════════════════════════════════════════════════")
	fmt.Println("   BattleWorld Lab2 自动测试")
	fmt.Println("   被测服务器：" + serverAddr)
	fmt.Println("═══════════════════════════════════════════════════")

	if !waitServer() {
		os.Exit(1)
	}
	fmt.Println()

	tid := ""
	if len(os.Args) > 1 {
		tid = os.Args[1]
	}

	switch tid {
	case "1":
		test1_ConcurrentConnect()
	case "2":
		test2_AddPlayerIDUnique()
	case "3":
		test3_RemovePlayer()
	case "4":
		test4_MovePlayerBoundary()
	case "5":
		test5_ConcurrentSnapshot()
	case "6":
		test6_AttackDamage()
	case "7":
		test7_BroadcastGoroutine()
	default:
		test1_ConcurrentConnect()
		fmt.Println()
		test2_AddPlayerIDUnique()
		fmt.Println()
		test3_RemovePlayer()
		fmt.Println()
		test4_MovePlayerBoundary()
		fmt.Println()
		test5_ConcurrentSnapshot()
		fmt.Println()
		test6_AttackDamage()
		fmt.Println()
		test7_BroadcastGoroutine()
	}

	fmt.Println()
	fmt.Printf("═══════════════════════════════════════════════════\n")
	fmt.Printf("  结果：%d 通过，%d 失败\n", passed, failed)
	fmt.Printf("═══════════════════════════════════════════════════\n")
	if failed > 0 {
		os.Exit(1)
	}
}