//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	enableLineInput = 0x0002
	enableEchoInput = 0x0004
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

func enterRawMode() (func(), error) {
	handle := syscall.Handle(os.Stdin.Fd())
	var original uint32
	if err := getConsoleMode(handle, &original); err != nil {
		return nil, fmt.Errorf("读取控制台模式失败: %w", err)
	}

	raw := original &^ (enableLineInput | enableEchoInput)
	if err := setConsoleMode(handle, raw); err != nil {
		return nil, fmt.Errorf("设置控制台模式失败: %w", err)
	}

	return func() {
		_ = setConsoleMode(handle, original)
	}, nil
}

func getConsoleMode(handle syscall.Handle, mode *uint32) error {
	r1, _, err := procGetConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(mode)))
	if r1 == 0 {
		return err
	}
	return nil
}

func setConsoleMode(handle syscall.Handle, mode uint32) error {
	r1, _, err := procSetConsoleMode.Call(uintptr(handle), uintptr(mode))
	if r1 == 0 {
		return err
	}
	return nil
}
