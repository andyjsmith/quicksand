//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// makeRaw puts the terminal in raw mode, so keys reach the guest as typed.
func makeRaw() (restore func(), err error) {
	fd := int(os.Stdin.Fd())
	st, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return func() { term.Restore(fd, st) }, nil
}

// watchResize calls fn with the new size whenever the terminal is resized.
func watchResize(fn func(cols, rows int)) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			fn(terminalSize())
		}
	}()
	return func() {
		signal.Stop(ch)
		close(ch)
	}
}
