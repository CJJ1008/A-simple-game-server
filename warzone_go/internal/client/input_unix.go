//go:build !windows

package client

import (
	"os"

	"golang.org/x/sys/unix"
)

// StdinReady returns true if stdin has data within ms milliseconds.
func StdinReady(ms int) bool {
	if ms < 0 {
		ms = 0
	}

	var rfds unix.FdSet
	fd := int(os.Stdin.Fd())
	rfds.Set(fd)

	tv := unix.NsecToTimeval(int64(ms) * 1_000_000)
	n, err := unix.Select(fd+1, &rfds, nil, nil, &tv)
	return err == nil && n > 0
}
