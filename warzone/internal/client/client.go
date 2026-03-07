package client

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"warzone/internal/protocol"
)

const (
	minCols = 80
	minRows = 32
	// 在 Raw Mode 下，必须使用 \r\n 才能正确换行并对齐行首
	nl = "\r\n"
)

// ── View enum ─────────────────────────────────────────────────────────────────

type View int

const (
	ViewGame  View = 0
	ViewStats View = 1
)

// ── SharedState ───────────────────────────────────────────────────────────────

type SharedState struct {
	mu         sync.Mutex
	GameState  protocol.StateUpdatePayload
	StatsResp  protocol.StatsResponsePayload
	MyID       int
	GameDirty  bool
	StatsDirty bool
}

// ── Client ────────────────────────────────────────────────────────────────────

type Client struct {
	conn       net.Conn
	reader     *bufio.Reader 
	state      SharedState
	running    atomic.Bool
	user       string
	view       View
	curr       FrameBuffer
	prev       FrameBuffer
	firstPaint bool
}

func Run(host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("无法连接到 %s：%w"+nl+"提示：先启动服务器", addr, err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}

	c := &Client{
		conn:       conn,
		reader:     bufio.NewReader(conn),
		firstPaint: true,
	}
	c.running.Store(true)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		c.running.Store(false)
	}()

	go HeartbeatWorker(conn, &c.running)

	waitResize()

	EnterRaw()
	authenticated := c.loginUI()
	LeaveRaw()

	if !authenticated {
		_ = SendPacket(conn, protocol.PktDisconnect, nil)
		conn.Close()
		fmt.Print(aCls + aShow + "再见！" + nl)
		return nil
	}

	_ = SendPacket(conn, protocol.PktJoin, nil)

	go RecvWorkerBuffered(conn, c.reader, &c.running, &c.state)

	fmt.Print(aCls + aHide)
	EnterRaw()
	defer func() {
		LeaveRaw()
		fmt.Print(aCls + aShow + "已退出游戏。再见！" + nl)
	}()

	c.firstPaint = true
	c.prev.Clear()
	c.gameLoop()

	_ = SendPacket(conn, protocol.PktDisconnect, nil)
	conn.Close()
	return nil
}

// ── Login UI ──────────────────────────────────────────────────────────────────

func (c *Client) loginUI() bool {
	// 这里的 \n 全部改为 nl (\r\n) 解决阶梯排版
	fmt.Print(aCls + aShow +
		aBold + aBgBlue + aWhite + "  多人对战游戏 v3.0 Go  ──  账号登录  " + aReset + nl +
		aDim + fmt.Sprintf("  建议终端：%d列 × %d行以上", minCols, minRows) + aReset + nl)

	for {
		fmt.Print(nl + aBlue + aBold + "  [1] 登录" + nl + "  [2] 注册" + nl + "  [3] 退出" + nl + nl + aReset)

		ch, err := ReadLineRaw("  请选择 > ", true)
		if err != nil || ch == "\x03" || ch == "3" || ch == "q" || ch == "Q" {
			return false
		}
		if ch != "1" && ch != "2" {
			fmt.Print(aRed + "  请输入 1、2 或 3" + nl + aReset)
			continue
		}
		isReg := ch == "2"

		user, err := ReadLineRaw("  用户名 > ", true)
		if err != nil || user == "\x03" {
			return false
		}
		user = trimRight(user)
		if user == "" {
			fmt.Print(aRed + "  用户名不能为空" + nl + aReset)
			continue
		}

		pass, err := ReadLineRaw("  密  码 > ", false)
		if err != nil || pass == "\x03" {
			return false
		}

		if isReg {
			conf, err := ReadLineRaw("  确认密码 > ", false)
			if err != nil || conf == "\x03" {
				return false
			}
			if pass != conf {
				fmt.Print(aRed + "  两次密码不一致" + nl + aReset)
				continue
			}
		}

		var ap protocol.AuthPayload
		protocol.StringToFixedBytes(user, ap.Username[:])
		protocol.StringToFixedBytes(pass, ap.Password[:])
		pktType := protocol.PktLogin
		if isReg {
			pktType = protocol.PktRegister
		}
		if err := SendPacket(c.conn, pktType, &ap); err != nil {
			fmt.Print(aRed + "  网络错误" + nl + aReset)
			return false
		}

		ar, ok := c.waitAuthResult()
		if !ok {
			fmt.Print(aRed + "  连接断开或无响应，请重试" + nl + aReset)
			continue
		}
		if ar.Success != 0 {
			c.user = protocol.BytesToString(ar.Username[:])
			fmt.Printf(aGreen+aBold+nl+"  ✓ %s，欢迎 %s！"+nl+aReset,
				protocol.BytesToString(ar.Message[:]), c.user)
			time.Sleep(500 * time.Millisecond)
			return true
		}
		fmt.Printf(aRed+nl+"  ✗ %s"+nl+aReset, protocol.BytesToString(ar.Message[:]))
	}
}

func (c *Client) waitAuthResult() (protocol.AuthResultPayload, bool) {
	for i := 0; i < 64; i++ {
		hdr, err := protocol.RecvHeader(c.reader)
		if err != nil {
			return protocol.AuthResultPayload{}, false
		}
		switch hdr.Type {
		case protocol.PktHeartbeat:
			_ = SendPacket(c.conn, protocol.PktHeartbeatAck, nil)
		case protocol.PktHeartbeatAck:
			// nothing
		case protocol.PktAuthResult:
			var ar protocol.AuthResultPayload
			if int(hdr.Length) >= binary.Size(ar) {
				if err := protocol.RecvInto(c.reader, &ar); err != nil {
					return ar, false
				}
			} else {
				protocol.DiscardN(c.reader, int(hdr.Length))
			}
			return ar, true
		default:
			protocol.DiscardN(c.reader, int(hdr.Length))
		}
	}
	return protocol.AuthResultPayload{}, false
}

// ── Game loop ─────────────────────────────────────────────────────────────────

func (c *Client) gameLoop() {
	for c.running.Load() {
		rows, cols := GetTermSize()
		if rows < minRows || cols < minCols {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if StdinReady(10) {
			b, err := ReadByte()
			if err == nil {
				c.handleKey(b)
			}
		}

		c.render(rows)
	}
}

func (c *Client) handleKey(b byte) {
	switch c.view {
	case ViewGame:
		switch b {
		case 'w', 'W':
			c.sendAction(protocol.ActionMoveUp)
		case 's', 'S':
			c.sendAction(protocol.ActionMoveDown)
		case 'a', 'A':
			c.sendAction(protocol.ActionMoveLeft)
		case 'd', 'D':
			c.sendAction(protocol.ActionMoveRight)
		case ' ', 'f', 'F':
			c.sendAction(protocol.ActionAttack)
		case 'r', 'R':
			_ = SendPacket(c.conn, protocol.PktReady, nil)
		case 't', 'T':
			c.queryStatsDialog()
		case 'q', 'Q':
			c.running.Store(false)
		case 0x1b: 
			if !StdinReady(20) {
				break
			}
			b2, err := ReadByte()
			if err != nil || b2 != '[' {
				break
			}
			if !StdinReady(20) {
				break
			}
			b3, err := ReadByte()
			if err != nil {
				break
			}
			switch b3 {
			case 'A':
				c.sendAction(protocol.ActionMoveUp)
			case 'B':
				c.sendAction(protocol.ActionMoveDown)
			case 'C':
				c.sendAction(protocol.ActionMoveRight)
			case 'D':
				c.sendAction(protocol.ActionMoveLeft)
			default:
				if b3 >= '0' && b3 <= '9' {
					consumeEscapeSeq()
				}
			}
		}

	case ViewStats:
		switch b {
		case 'q', 'Q':
			fmt.Print(aCls + aHide)
			c.prev.Clear()
			c.view = ViewGame
			c.firstPaint = true
			c.state.mu.Lock()
			c.state.GameDirty = true
			c.state.mu.Unlock()
		case 's', 'S':
			c.queryStatsDialog()
		case 0x1b:
			consumeEscapeSeq()
		}
	}
}

func (c *Client) sendAction(a protocol.ActionType) {
	ap := protocol.ActionPayload{Action: a}
	_ = SendPacket(c.conn, protocol.PktAction, &ap)
}

func (c *Client) queryStatsDialog() {
	LeaveRaw()
	fmt.Print(aShow + nl + aBold + "  查询战绩（回车=查自己）> " + aReset)

	EnterRaw() 
	target, _ := ReadLineRaw("", true)
	if target == "\x03" {
		target = ""
	}
	target = trimRight(target)

	var srp protocol.StatsRequestPayload
	protocol.StringToFixedBytes(target, srp.Username[:])
	_ = SendPacket(c.conn, protocol.PktStatsRequest, &srp)

	fmt.Print(aCls + aHide)
	c.prev.Clear()
	c.view = ViewStats
	c.firstPaint = true
}

func (c *Client) render(rows int) {
	switch c.view {
	case ViewGame:
		c.state.mu.Lock()
		dirty := c.state.GameDirty || c.firstPaint
		snap := c.state.GameState
		myID := c.state.MyID
		c.state.GameDirty = false
		c.state.mu.Unlock()

		if dirty {
			BuildGame(&c.curr, snap, myID, c.user)
			c.curr.FlushDiff(&c.prev, rows, c.firstPaint)
			c.firstPaint = false
		}

	case ViewStats:
		c.state.mu.Lock()
		dirty := c.state.StatsDirty || c.firstPaint
		snap := c.state.StatsResp
		c.state.StatsDirty = false
		c.state.mu.Unlock()

		if dirty {
			BuildStats(&c.curr, snap)
			c.curr.FlushDiff(&c.prev, rows, c.firstPaint)
			c.firstPaint = false
		}
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func trimRight(s string) string {
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func waitResize() {
	for {
		rows, cols := GetTermSize()
		if rows >= minRows && cols >= minCols {
			break
		}
		fmt.Printf(aCls+aShow+nl+nl+"  "+aRed+aBold+"终端太小！"+aReset+
			nl+nl+"  当前：%d列 × %d行"+nl+"  需要：%d列 × %d行（建议 ≥ 80×40）"+nl+nl+
			"  请拖大终端窗口，程序自动继续…"+nl,
			cols, rows, minCols, minRows)
		time.Sleep(400 * time.Millisecond)
	}
	fmt.Print(aCls + aShow)
}

// ── 提示：如果你的 ReadLineRaw 无法删除字符，请确保逻辑如下： ───────────────────
/*
func ReadLineRaw(prompt string, echo bool) (string, error) {
	fmt.Print(prompt)
	var line []byte
	for {
		b, err := ReadByte() // 假设你已有这个读取单字节的函数
		if err != nil { return "", err }
		if b == '\r' || b == '\n' {
			fmt.Print(nl)
			return string(line), nil
		}
		if b == '\x7f' || b == '\x08' { // Backspace 或 Delete
			if len(line) > 0 {
				line = line[:len(line)-1]
				if echo {
					fmt.Print("\b \b") // 关键：回退、空格覆盖、再回退
				}
			}
			continue
		}
		if b == '\x03' { return "\x03", nil } // Ctrl+C
		line = append(line, b)
		if echo {
			fmt.Printf("%c", b)
		} else {
			fmt.Print("*")
		}
	}
}
*/
