//go:build windows

package client

import (
	"os"
	"syscall"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procWaitForSingleObject = kernel32.NewProc("WaitForSingleObject")
)

const (
	waitObject0 = 0x00000000
)

// StdinReady returns true if stdin becomes readable within ms milliseconds.
//
// For Windows console stdin, WaitForSingleObject works on the console input handle.
// This is enough for typical terminal interactive input scenarios.
func StdinReady(ms int) bool {
	if ms < 0 {
		ms = 0
	}

	h := syscall.Handle(os.Stdin.Fd())

	r1, _, _ := procWaitForSingleObject.Call(
		uintptr(h),
		uintptr(uint32(ms)),
	)

	return uint32(r1) == waitObject0
}
