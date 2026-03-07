package client

import (
	"warzone/internal/protocol"
	"fmt"
	"strings"
)

// ── ANSI escape constants ──────────────────────────────────────────────────────
const (
	aReset   = "\033[0m"
	aBold    = "\033[1m"
	aDim     = "\033[2m"
	aRed     = "\033[91m"
	aGreen   = "\033[92m"
	aYellow  = "\033[93m"
	aBlue    = "\033[94m"
	aMagenta = "\033[95m"
	aCyan    = "\033[96m"
	aWhite   = "\033[97m"
	aBgBlue  = "\033[44m"
	aHide    = "\033[?25l"
	aShow    = "\033[?25h"
	aCls     = "\033[2J\033[H"
)

var playerColors = [protocol.MaxPlayers]string{aCyan, aYellow, aGreen, aMagenta, aRed}
var playerSymbols = [protocol.MaxPlayers]string{"A", "B", "C", "D", "E"}

// ── FrameBuffer ───────────────────────────────────────────────────────────────

const maxFrameRows = 80

// FrameBuffer holds one rendered frame as a slice of lines.
type FrameBuffer struct {
	Lines [maxFrameRows]string
}

// Clear resets all lines to empty.
func (fb *FrameBuffer) Clear() {
	for i := range fb.Lines {
		fb.Lines[i] = ""
	}
}

// SetLine sets a line if the row index is in bounds.
func (fb *FrameBuffer) SetLine(row int, s string) {
	if row >= 0 && row < maxFrameRows {
		fb.Lines[row] = s
	}
}

// FlushDiff writes only changed lines to stdout using absolute cursor positioning.
// Passing full=true forces every line to be rewritten (e.g. after a clear screen).
func (fb *FrameBuffer) FlushDiff(prev *FrameBuffer, rows int, full bool) {
	lim := rows
	if lim > maxFrameRows {
		lim = maxFrameRows
	}
	var sb strings.Builder
	sb.Grow(8192)
	for i := 0; i < lim; i++ {
		if !full && fb.Lines[i] == prev.Lines[i] {
			continue
		}
		fmt.Fprintf(&sb, "\033[%d;1H%s\033[K", i+1, fb.Lines[i])
	}
	if sb.Len() > 0 {
		fmt.Print(sb.String())
		*prev = *fb
	}
}

// ── HP bar ───────────────────────────────────────────────────────────────────

func hpBar(hp, maxHP, width int) string {
	n := 0
	if hp > 0 {
		n = hp * width / maxHP
	}
	if n < 0 {
		n = 0
	}
	if n > width {
		n = width
	}
	col := aGreen
	if hp <= maxHP/3 {
		col = aRed
	}
	return col + aBold + strings.Repeat("|", n) + aReset + aDim +
		strings.Repeat(".", width-n) + aReset
}

// ── BuildGame ─────────────────────────────────────────────────────────────────

// BuildGame renders the full game frame into fb.
func BuildGame(fb *FrameBuffer, s protocol.StateUpdatePayload, myID int, user string) {
	fb.Clear()
	row := 0

	// ── Status bar ──
	status := "等待准备"
	if s.GameStarted != 0 {
		if s.GameOver != 0 {
			status = "游戏结束"
		} else {
			status = "游戏中"
		}
	}
	header := fmt.Sprintf("  %-12s  玩家:%d  准备:%d/%d  [%s]",
		user, s.PlayerCount, s.ReadyCount, s.PlayerCount, status)
	fb.SetLine(row, aBold+aBgBlue+aWhite+header+aReset)
	row += 2

	// ── Map ──
	border := "  +" + strings.Repeat("-", protocol.MapW*2) + "+"
	fb.SetLine(row, border)
	row++

	// Build position lookup grids.
	var playerAt [protocol.MapH][protocol.MapW]int
	var weaponAt [protocol.MapH][protocol.MapW]bool
	for y := range playerAt {
		for x := range playerAt[y] {
			playerAt[y][x] = -1
		}
	}
	for i := protocol.MaxPlayers - 1; i >= 0; i-- {
		p := s.Players[i]
		if p.Connected != 0 && p.Alive != 0 &&
			p.X >= 0 && int(p.X) < protocol.MapW &&
			p.Y >= 0 && int(p.Y) < protocol.MapH {
			playerAt[p.Y][p.X] = i
		}
	}
	for i := range s.Weapons {
		w := s.Weapons[i]
		if w.Active != 0 &&
			w.X >= 0 && int(w.X) < protocol.MapW &&
			w.Y >= 0 && int(w.Y) < protocol.MapH {
			weaponAt[w.Y][w.X] = true
		}
	}

	for y := 0; y < protocol.MapH; y++ {
		var ln strings.Builder
		ln.WriteString("  |")
		for x := 0; x < protocol.MapW; x++ {
			pid := playerAt[y][x]
			switch {
			case pid >= 0:
				sym := playerSymbols[pid]
				if pid == myID {
					sym = "@"
				}
				wpn := " "
				if s.Players[pid].HasWeapon != 0 {
					wpn = aYellow + "*" + aReset
				}
				ln.WriteString(aBold + playerColors[pid] + sym + aReset + wpn)
			case weaponAt[y][x]:
				ln.WriteString(aBold + aYellow + "W " + aReset)
			default:
				ln.WriteString(aDim + ". " + aReset)
			}
		}
		ln.WriteString("|")
		fb.SetLine(row, ln.String())
		row++
	}

	fb.SetLine(row, border)
	row += 2

	// ── Player list ──
	fb.SetLine(row, aBold+"  玩家状态："+aReset)
	row++
	for i := 0; i < protocol.MaxPlayers; i++ {
		p := s.Players[i]
		if p.Connected == 0 {
			continue
		}
		prefix := "  "
		if i == myID {
			prefix = "▶ "
		}
		name := protocol.BytesToString(p.Name[:])
		line := "  " + playerColors[i] + aBold + prefix + fmt.Sprintf("%-12s", name) + aReset
		if p.Alive == 0 {
			line += aRed + "  【阵亡】" + aReset
		} else {
			line += fmt.Sprintf("  (%2d,%2d) HP:%3d [", p.X, p.Y, p.Health)
			line += hpBar(int(p.Health), protocol.MaxHealth, 14) + "]"
			if p.HasWeapon != 0 {
				line += aYellow + aBold + " ⚡×2" + aReset
			}
		}
		if s.GameStarted == 0 {
			if p.Ready != 0 {
				line += "  " + aGreen + "✓" + aReset
			} else {
				line += "  " + aDim + "未准备" + aReset
			}
		}
		fb.SetLine(row, line)
		row++
	}

	row++
	fb.SetLine(row, "  "+aMagenta+"► "+aReset+protocol.BytesToString(s.LastEvent[:]))
	row++

	if s.GameOver != 0 {
		iw := s.WinnerID == uint8(myID)
		msg := aRed + "✗ 你已落败。"
		if iw {
			msg = aGreen + "★ 恭喜你获胜！"
		}
		fb.SetLine(row, "  "+aBold+msg+"  Q=退出  T=战绩"+aReset)
		row++
	} else if s.GameStarted == 0 {
		need := int(s.PlayerCount) - int(s.ReadyCount)
		if need > 0 {
			fb.SetLine(row, fmt.Sprintf("  "+aBlue+aBold+"⏳ 还需%d名玩家按R准备…"+aReset, need))
			row++
		}
	}

	row++
	fb.SetLine(row, fmt.Sprintf("  "+aBold+"操作："+aReset+" WASD/方向键=移动  空格/F=攻击(范围≤%d)  R=准备  T=战绩  Q=退出",
		protocol.AttackRange))
	row++

	var leg strings.Builder
	leg.WriteString("  ")
	for i := 0; i < protocol.MaxPlayers; i++ {
		leg.WriteString(playerColors[i] + aBold + playerSymbols[i] + aReset +
			fmt.Sprintf("=P%d  ", i))
	}
	leg.WriteString(aYellow + aBold + "W" + aReset + "=武器  " + aBold + "@" + aReset + "=自己")
	fb.SetLine(row, leg.String())
}

// ── BuildStats ────────────────────────────────────────────────────────────────

// BuildStats renders the stats view into fb.
func BuildStats(fb *FrameBuffer, sr protocol.StatsResponsePayload) {
	fb.Clear()
	row := 0
	fb.SetLine(row, aBold+aBgBlue+aWhite+"  战绩查询  "+aReset)
	row += 2

	if sr.Found == 0 {
		fb.SetLine(row, aRed+"  用户不存在"+aReset)
		row++
	} else {
		fb.SetLine(row, "  "+aBold+aCyan+"用户名："+
			protocol.BytesToString(sr.Username[:])+aReset)
		row += 2

		addRow := func(label, col, val string) {
			fb.SetLine(row, "    "+aDim+label+aReset+"  "+col+aBold+val+aReset)
			row++
		}

		addRow("总 局 数：", aWhite, fmt.Sprintf("%d", sr.Games))
		addRow("胜    场：", aGreen, fmt.Sprintf("%d", sr.Wins))
		addRow("败    场：", aRed, fmt.Sprintf("%d", sr.Losses))

		var wr float64
		if sr.Games > 0 {
			wr = float64(sr.Wins) * 100.0 / float64(sr.Games)
		}
		addRow("胜    率：", aYellow, fmt.Sprintf("%.1f%%", wr))
		addRow("总 击 杀：", aCyan, fmt.Sprintf("%d", sr.Kills))
		addRow("总 死 亡：", aMagenta, fmt.Sprintf("%d", sr.Deaths))

		var kd float64
		if sr.Deaths > 0 {
			kd = float64(sr.Kills) / float64(sr.Deaths)
		} else {
			kd = float64(sr.Kills)
		}
		addRow("K / D ：", aYellow, fmt.Sprintf("%.2f", kd))
		row++

		fb.SetLine(row, "    "+aDim+"最后游戏："+aReset+"  "+aWhite+
			protocol.BytesToString(sr.LastPlayed[:])+aReset)
		row++
	}

	row++
	fb.SetLine(row, aDim+"  Q=返回游戏  S=查询其他玩家"+aReset)
}
