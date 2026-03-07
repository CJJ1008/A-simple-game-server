// Package client implements the game client.
package client

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// ── Terminal state ─────────────────────────────────────────────────────────────

var origTermState *term.State

// EnterRaw puts stdin into raw mode and hides the cursor.
func EnterRaw() {
	var err error
	origTermState, err = term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		panic("EnterRaw: " + err.Error())
	}
}

// LeaveRaw restores the terminal to its original state.
func LeaveRaw() {
	if origTermState != nil {
		_ = term.Restore(int(os.Stdin.Fd()), origTermState)
		origTermState = nil
	}
}

// GetTermSize returns the current terminal dimensions.
func GetTermSize() (rows, cols int) {
	c, r, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 24, 80
	}
	return r, c
}

// StdinReady returns true if stdin has data within ms milliseconds.
func StdinReady(ms int) bool {
	var rfds unix.FdSet
	rfds.Set(int(os.Stdin.Fd()))
	tv := unix.NsecToTimeval(int64(ms) * 1_000_000)
	n, err := unix.Select(int(os.Stdin.Fd())+1, &rfds, nil, nil, &tv)
	return err == nil && n > 0
}

// ReadByte reads exactly one byte from stdin (blocking).
func ReadByte() (byte, error) {
	var buf [1]byte
	n, err := os.Stdin.Read(buf[:])
	if n > 0 {
		return buf[0], nil
	}
	return 0, err
}

// consumeEscapeSeq drains the remainder of an ANSI escape sequence.
func consumeEscapeSeq() {
	if !StdinReady(50) {
		return 
	}
	b, err := ReadByte()
	if err != nil || b != '[' {
		return 
	}
	for {
		if !StdinReady(50) {
			return
		}
		c, err := ReadByte()
		if err != nil {
			return
		}
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '~' {
			return
		}
	}
}

// ReadLineRaw 修改版：修复了 Raw 模式下的阶梯缩进和退格删除问题
func ReadLineRaw(prompt string, echo bool) (string, error) {
	if prompt != "" {
		// 使用 \r 确保提示符从行首开始，解决阶梯缩进问题
		fmt.Print("\r" + prompt)
	}

	var runes []rune

	for {
		b, err := ReadByte()
		if err != nil {
			return string(runes), err
		}

		switch {
		case b == '\n' || b == '\r':
			// 无论是否回显，换行都必须输出 \r\n 才能回到下一行行首
			fmt.Print("\r\n")
			return string(runes), nil

		case b == 3: // Ctrl+C
			return "\x03", nil

		case b == 127 || b == 8: // DEL / Backspace
			if len(runes) > 0 {
				runes = runes[:len(runes)-1]
				// 核心修复：即使是密码模式 (echo=false)，也需要通过退格擦除屏幕上的 '*'
				fmt.Print("\b \b")
			}

		case b == 0x1b: // ESC 
			consumeEscapeSeq()

		case b >= 32: // printable ASCII
			runes = append(runes, rune(b))
			if echo {
				fmt.Printf("%c", rune(b))
			} else {
				// 密码模式回显星号
				fmt.Print("*")
			}
		}
	}
}
