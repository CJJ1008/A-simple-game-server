//go:build !windows && !linux && !darwin

package main

func enterRawMode() (func(), error) {
	return func() {}, nil
}
