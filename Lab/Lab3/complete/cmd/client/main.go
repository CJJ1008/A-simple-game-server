package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"battleworld/protocol"
)

const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorCyan   = "\033[38;5;51m"
	colorTeal   = "\033[38;5;44m"
	colorGold   = "\033[38;5;220m"
	colorOrange = "\033[38;5;208m"
	colorRed    = "\033[38;5;196m"
	colorGreen  = "\033[38;5;82m"
	colorGray   = "\033[38;5;245m"
	colorBlue   = "\033[38;5;39m"
	bgPanel     = "\033[48;5;235m"
)

var (
	uiMu        sync.Mutex
	current     *protocol.WorldState
	clientNotes []string
	shopOpen    bool
)

func main() {
	reader := bufio.NewReader(os.Stdin)
	addr := chooseGateway(reader)

	var conn *protocol.Conn
	var state *protocol.WorldState
	var err error
	for {
		mode, username, password, confirm := chooseAuth(reader)
		conn, state, err = auth(addr, mode, username, password, confirm)
		if err == nil {
			break
		}
		fmt.Printf("%s进入失败：%v%s\n", colorRed, err, colorReset)
	}
	defer conn.Close()

	restoreTTY, err := enterRawMode()
	if err != nil {
		fmt.Fprintf(os.Stderr, "切换终端即时输入模式失败：%v\n", err)
		os.Exit(1)
	}
	defer restoreTTY()

	fmt.Print("\033[?1049h\033[2J\033[H\033[?25l")
	defer fmt.Print("\033[?25h\033[?1049l")

	setState(state)
	drawUI()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			msg, err := conn.Receive()
			if err != nil {
				addClientNote("与网关连接已断开")
				drawUI()
				return
			}
			switch msg.Type {
			case protocol.TypeAuth, protocol.TypeState:
				if msg.State != nil {
					setState(msg.State)
				}
			case protocol.TypeError:
				addClientNote(msg.Error)
			}
			drawUI()
		}
	}()

	keyReader := bufio.NewReader(os.Stdin)
	for {
		select {
		case <-done:
			return
		default:
		}

		key, err := keyReader.ReadByte()
		if err != nil {
			return
		}
		if key == 3 {
			_ = conn.Send(protocol.Message{Type: protocol.TypeLogout})
			return
		}

		var msg protocol.Message
		var note string

		if shopOpen {
			switch key {
			case 'p', 'P', 27:
				setShopOpen(false)
				drawUI()
				continue
			case '1':
				msg = protocol.Message{Type: protocol.TypeShop, Item: "potion"}
				note = "购买药剂"
			case '2':
				msg = protocol.Message{Type: protocol.TypeShop, Item: "weapon"}
				note = "强化武器"
			default:
				continue
			}
		} else {
			switch key {
			case 'w', 'W':
				msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirUp}
				note = "向上移动"
			case 's', 'S':
				msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirDown}
				note = "向下移动"
			case 'a', 'A':
				msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirLeft}
				note = "向左移动"
			case 'd', 'D':
				msg = protocol.Message{Type: protocol.TypeMove, Dir: protocol.DirRight}
				note = "向右移动"
			case 'j', 'J', 'f', 'F':
				msg = protocol.Message{Type: protocol.TypeAttack}
				note = "发起近战攻击"
			case 'k', 'K', 'h', 'H':
				msg = protocol.Message{Type: protocol.TypeHeal}
				note = "使用药剂"
			case 'b', 'B':
				msg = protocol.Message{Type: protocol.TypeBossAttack}
				note = "挑战世界首领"
			case 'p', 'P':
				setShopOpen(true)
				drawUI()
				continue
			case '1':
				msg = protocol.Message{Type: protocol.TypeSwitchMap, MapID: "green"}
				note = "切换到青岚要塞"
			case '2':
				msg = protocol.Message{Type: protocol.TypeSwitchMap, MapID: "cave"}
				note = "切换到玄矿地窟"
			case '3':
				msg = protocol.Message{Type: protocol.TypeSwitchMap, MapID: "ruins"}
				note = "切换到残星遗迹"
			case 'r', 'R':
				addClientNote("已刷新界面")
				drawUI()
				continue
			case 'q', 'Q':
				_ = conn.Send(protocol.Message{Type: protocol.TypeLogout})
				return
			default:
				continue
			}
		}

		addClientNote(note)
		if err := conn.Send(msg); err != nil {
			addClientNote("指令发送失败")
			drawUI()
			return
		}
		drawUI()
	}
}

func chooseGateway(reader *bufio.Reader) string {
	for {
		fmt.Print("\033[2J\033[H")
		fmt.Println(renderBanner("战场入口", "1. 单机测试  2. 连接指定网关"))
		fmt.Print(colorGold + "[1/2] 请选择连接方式：" + colorReset)
		choice := readLine(reader)

		switch choice {
		case "1", "":
			return protocol.GatewayAddr
		case "2":
			fmt.Print(colorCyan + "请输入网关 IP：" + colorReset)
			host := readLine(reader)
			if host == "" {
				host = "127.0.0.1"
			}
			fmt.Print(colorCyan + "请输入网关端口：" + colorReset)
			port := readLine(reader)
			if port == "" {
				port = "9310"
			}
			return net.JoinHostPort(host, port)
		default:
			fmt.Println(colorRed + "无效选择，请重新输入。" + colorReset)
			time.Sleep(700 * time.Millisecond)
		}
	}
}

func chooseAuth(reader *bufio.Reader) (string, string, string, string) {
	for {
		fmt.Print("\033[2J\033[H")
		fmt.Println(renderBanner("身份验证", "1. 登录  2. 注册"))
		fmt.Print(colorGold + "[1/2] 请选择：" + colorReset)
		choice := readLine(reader)
		mode := protocol.TypeLogin
		switch choice {
		case "1", "":
			mode = protocol.TypeLogin
		case "2":
			mode = protocol.TypeRegister
		default:
			fmt.Println(colorRed + "无效选择，请重新输入。" + colorReset)
			time.Sleep(700 * time.Millisecond)
			continue
		}

		fmt.Print(colorCyan + "用户名：" + colorReset)
		username := readLine(reader)
		fmt.Print(colorCyan + "密码：" + colorReset)
		password := readLine(reader)
		confirm := ""
		if mode == protocol.TypeRegister {
			fmt.Print(colorCyan + "确认密码：" + colorReset)
			confirm = readLine(reader)
			if password != confirm {
				fmt.Println(colorRed + "两次输入的密码不一致，请重新输入。" + colorReset)
				time.Sleep(900 * time.Millisecond)
				continue
			}
		}
		return mode, username, password, confirm
	}
}

func auth(addr, mode, username, password, confirm string) (*protocol.Conn, *protocol.WorldState, error) {
	raw, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("连接网关失败：%w", err)
	}

	conn := protocol.NewConn(raw)
	if err := conn.Send(protocol.Message{
		Type:     mode,
		Username: username,
		Password: password,
		Confirm:  confirm,
	}); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("发送认证消息失败：%w", err)
	}

	reply, err := conn.Receive()
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("接收认证结果失败：%w", err)
	}
	if reply.Type == protocol.TypeError || !reply.OK || reply.State == nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("%s", reply.Error)
	}
	return conn, reply.State, nil
}

func setState(state *protocol.WorldState) {
	uiMu.Lock()
	defer uiMu.Unlock()
	current = state
}

func setShopOpen(open bool) {
	uiMu.Lock()
	defer uiMu.Unlock()
	shopOpen = open
}

func addClientNote(text string) {
	if shouldSuppressClientNote(text) {
		return
	}
	uiMu.Lock()
	defer uiMu.Unlock()
	clientNotes = append(clientNotes, text)
	if len(clientNotes) > 5 {
		clientNotes = clientNotes[len(clientNotes)-5:]
	}
}

func shouldSuppressClientNote(text string) bool {
	switch text {
	case "向上移动", "向下移动", "向左移动", "向右移动",
		"发起近战攻击", "使用药剂", "挑战世界首领",
		"切换到青岚要塞", "切换到玄矿地窟", "切换到残星遗迹",
		"购买药剂", "强化武器":
		return true
	default:
		return false
	}
}

func drawUI() {
	uiMu.Lock()
	defer uiMu.Unlock()

	if current == nil {
		return
	}

	mapLines := buildMapLines(current)
	sideLines := buildSideLines(current)
	eventLines := buildEventLines(current)

	height := max(len(mapLines), len(sideLines))
	var sb strings.Builder
	sb.WriteString("\033[H")
	sb.WriteString(renderBanner("烬原战境 Lab3", "即时战斗 / 中文彩色界面 / 多地图并行 / 多节点协同"))
	sb.WriteString("\n")
	for i := 0; i < height; i++ {
		left := ""
		if i < len(mapLines) {
			left = mapLines[i]
		}
		right := ""
		if i < len(sideLines) {
			right = sideLines[i]
		}
		sb.WriteString(left)
		sb.WriteString("  ")
		sb.WriteString(right)
		sb.WriteString("\033[K\n")
	}
	sb.WriteString("\n")
	for _, line := range eventLines {
		sb.WriteString(line)
		sb.WriteString("\033[K\n")
	}
	if shopOpen {
		sb.WriteString(colorGold + "商店模式：" + colorReset + colorDim + "1 购买药剂  2 强化武器  P/Esc 关闭商店" + colorReset + "\n")
	} else {
		sb.WriteString(colorDim + "按键：W/A/S/D 移动  J 攻击  K 治疗  B 世界首领  P 商店  1/2/3 切图  R 刷新  Q 退出" + colorReset + "\n")
	}
	sb.WriteString("\033[J")
	fmt.Print(sb.String())
}

func buildMapLines(state *protocol.WorldState) []string {
	grid := make([][]rune, len(state.Map.Terrain))
	for y, row := range state.Map.Terrain {
		grid[y] = []rune(row)
	}
	for _, treasure := range state.Map.Treasures {
		if inBounds(grid, treasure.X, treasure.Y) {
			grid[treasure.Y][treasure.X] = '$'
		}
	}
	for _, npc := range state.Map.NPCs {
		if inBounds(grid, npc.X, npc.Y) {
			grid[npc.Y][npc.X] = 'n'
		}
	}
	if site, ok := bossSiteOnMap(state); ok && inBounds(grid, site.X, site.Y) {
		if state.Boss.Alive {
			grid[site.Y][site.X] = 'b'
		} else {
			grid[site.Y][site.X] = 'o'
		}
	}
	for _, player := range state.Map.Players {
		if !inBounds(grid, player.X, player.Y) {
			continue
		}
		switch {
		case player.Username == state.Self.Username:
			grid[player.Y][player.X] = '@'
		case !player.Alive:
			grid[player.Y][player.X] = 'x'
		default:
			grid[player.Y][player.X] = 'p'
		}
	}

	lines := []string{
		colorGold + "┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓" + colorReset,
		colorGold + fmt.Sprintf("┃ 地图：%-12s 节点：%-8s 版本：%-6d ┃", state.Map.Name, state.Map.NodeID, state.Map.Version) + colorReset,
		colorGold + "┣━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┫" + colorReset,
	}
	for _, row := range grid {
		var line strings.Builder
		line.WriteString(colorGold + "┃" + colorReset)
		for _, cell := range row {
			line.WriteString(paintCell(cell))
		}
		line.WriteString(colorGold + "┃" + colorReset)
		lines = append(lines, line.String())
	}
	lines = append(lines, colorGold+"┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛"+colorReset)
	return lines
}

func buildSideLines(state *protocol.WorldState) []string {
	lines := []string{
		panelTitle("勇士面板"),
		panelLine("角色", colorCyan+colorBold+state.Self.Username+colorReset),
		panelLine("位置", fmt.Sprintf("(%d,%d)", state.Self.X, state.Self.Y)),
		panelLine("生命", hpBar(state.Self.HP, state.Self.MaxHP, 18, colorGreen)),
		panelLine("攻击", fmt.Sprintf("%d", state.Self.Attack)),
		panelLine("药剂", fmt.Sprintf("%d", state.Self.Potions)),
		panelLine("战利品", fmt.Sprintf("%d", state.Self.Treasures)),
		panelLine("击杀", fmt.Sprintf("%d", state.Self.Kills)),
		panelLine("阵亡", fmt.Sprintf("%d", state.Self.Deaths)),
		panelLine("胜场", fmt.Sprintf("%d", state.Self.Victories)),
	}
	if !state.Self.Alive {
		lines = append(lines, panelLine("复活", colorOrange+fmt.Sprintf("%d 秒", state.Self.RespawnIn)+colorReset))
	}
	lines = append(lines, panelTitle("战备商店"))
	if shopOpen {
		lines = append(lines,
			panelLine("1号商品", fmt.Sprintf("药剂 +1  价格 %d", protocol.PotionPrice)),
			panelLine("2号商品", fmt.Sprintf("武器强化 +%d  价格 %d", protocol.WeaponBoost, protocol.WeaponPrice)),
			panelLine("提示", colorGold+"按 1/2 立即购买"+colorReset),
		)
	} else {
		lines = append(lines, panelLine("入口", colorGold+"按 P 打开战备商店"+colorReset))
	}
	lines = append(lines, panelTitle("世界首领"))

	bossStatus := colorRed + "征战中" + colorReset
	if !state.Boss.Alive {
		bossStatus = colorGray + fmt.Sprintf("重组中 %ds", state.Boss.RespawnIn) + colorReset
	}
	siteText := "当前地图无投影"
	distText := "-"
	if site, ok := bossSiteOnMap(state); ok {
		siteText = fmt.Sprintf("(%d,%d)", site.X, site.Y)
		distText = fmt.Sprintf("%d", manhattan(state.Self.X, state.Self.Y, site.X, site.Y))
	}
	lastHit := state.Boss.LastHit
	if lastHit == "" {
		lastHit = "暂无"
	}
	lines = append(lines,
		panelLine("首领", colorOrange+colorBold+state.Boss.Name+colorReset),
		panelLine("状态", bossStatus),
		panelLine("生命", hpBar(state.Boss.HP, state.Boss.MaxHP, 18, colorRed)),
		panelLine("投影", siteText),
		panelLine("距离", distText),
		panelLine("开战线", fmt.Sprintf("%d 格", state.Boss.AttackGap)),
		panelLine("终结", lastHit),
		panelTitle("地图并行"),
	)

	for _, brief := range state.Maps {
		marker := colorDim + "○" + colorReset
		if brief.IsCurrent {
			marker = colorGold + "◆" + colorReset
		}
		lines = append(lines, fmt.Sprintf("%s %-5s %-8s 玩家:%d 怪:%d 宝:%d",
			marker, brief.Name, brief.NodeID, brief.Players, brief.NPCs, brief.Treasures))
	}

	lines = append(lines, panelTitle("节点心跳"))
	for _, node := range state.Nodes {
		status := colorRed + "离线" + colorReset
		if node.Healthy {
			status = colorGreen + "在线" + colorReset
		}
		lines = append(lines, fmt.Sprintf("• %-8s %s 主:%d 备:%d",
			node.ID, status, len(node.PrimaryMaps), len(node.ReplicaMaps)))
	}
	return lines
}

func buildEventLines(state *protocol.WorldState) []string {
	lines := []string{panelTitle("战场播报")}
	events := append([]string(nil), state.Events...)
	events = append(events, clientNotes...)
	start := 0
	if len(events) > 8 {
		start = len(events) - 8
	}
	for _, event := range events[start:] {
		lines = append(lines, colorGray+"│ "+colorReset+event)
	}
	return lines
}

func renderBanner(title, subtitle string) string {
	return colorBold + colorGold + "╔══════════════════════════════════════════════════════════════════════════════╗\n" +
		"║ " + title + strings.Repeat(" ", max(0, 74-len([]rune(title)))) + "║\n" +
		"║ " + colorReset + colorDim + subtitle + colorGold + strings.Repeat(" ", max(0, 74-len([]rune(subtitle)))) + "║\n" +
		"╚══════════════════════════════════════════════════════════════════════════════╝" + colorReset
}

func panelTitle(title string) string {
	return bgPanel + colorBold + " " + title + " " + colorReset
}

func panelLine(label, value string) string {
	return fmt.Sprintf("%s%-6s%s %s", colorDim, label+"：", colorReset, value)
}

func hpBar(hp, maxHP, width int, color string) string {
	if maxHP <= 0 {
		maxHP = 1
	}
	if hp < 0 {
		hp = 0
	}
	filled := hp * width / maxHP
	return fmt.Sprintf("[%s%s%s%s]%s %4d/%-4d%s",
		color, strings.Repeat("█", filled), colorGray, strings.Repeat("░", width-filled), colorReset, hp, maxHP, colorReset)
}

func paintCell(cell rune) string {
	switch cell {
	case '#':
		return colorGray + "▓" + colorReset
	case '$':
		return colorGold + "✦" + colorReset
	case 'n':
		return colorOrange + "♞" + colorReset
	case 'b':
		return colorRed + colorBold + "♛" + colorReset
	case 'o':
		return colorDim + "◉" + colorReset
	case '@':
		return colorCyan + colorBold + "◆" + colorReset
	case 'p':
		return colorBlue + "◎" + colorReset
	case 'x':
		return colorDim + "☓" + colorReset
	default:
		return colorDim + "·" + colorReset
	}
}

func bossSiteOnMap(state *protocol.WorldState) (protocol.BossSite, bool) {
	for _, site := range state.Boss.Sites {
		if site.MapID == state.Map.ID {
			return site, true
		}
	}
	return protocol.BossSite{}, false
}

func inBounds(grid [][]rune, x, y int) bool {
	return y >= 0 && y < len(grid) && x >= 0 && x < len(grid[y])
}

func readLine(reader *bufio.Reader) string {
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
