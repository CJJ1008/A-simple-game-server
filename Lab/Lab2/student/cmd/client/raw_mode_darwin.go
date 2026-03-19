//go:build darwin

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func enterRawMode() (func(), error) {
	fd := os.Stdin.Fd()
	var original syscall.Termios
	if err := ioctlTermios(fd, syscall.TIOCGETA, &original); err != nil {
		return nil, fmt.Errorf("读取终端模式失败: %w", err)
	}

	raw := original
	raw.Lflag &^= syscall.ICANON | syscall.ECHO
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctlTermios(fd, syscall.TIOCSETA, &raw); err != nil {
		return nil, fmt.Errorf("设置终端模式失败: %w", err)
	}

	return func() {
		_ = ioctlTermios(fd, syscall.TIOCSETA, &original)
	}, nil
}

func ioctlTermios(fd uintptr, req uintptr, termios *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(termios)))
	if errno != 0 {
		return errno
	}
	return nil
}
