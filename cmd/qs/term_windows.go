package main

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

const cpUTF8 = 65001

// makeRaw puts the console in raw mode with VT input and output, so keys
// reach the guest as escape sequences and its output renders, in UTF-8.
func makeRaw() (restore func(), err error) {
	in := int(os.Stdin.Fd())
	st, err := term.MakeRaw(in)
	if err != nil {
		return nil, err
	}
	out := windows.Handle(os.Stdout.Fd())
	var outMode uint32
	hasOut := windows.GetConsoleMode(out, &outMode) == nil
	if hasOut {
		windows.SetConsoleMode(out, outMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN)
	}
	inCP, _ := windows.GetConsoleCP()
	outCP, _ := windows.GetConsoleOutputCP()
	windows.SetConsoleCP(cpUTF8)
	windows.SetConsoleOutputCP(cpUTF8)
	return func() {
		term.Restore(in, st)
		if hasOut {
			windows.SetConsoleMode(out, outMode)
		}
		if inCP != 0 {
			windows.SetConsoleCP(inCP)
		}
		if outCP != 0 {
			windows.SetConsoleOutputCP(outCP)
		}
	}, nil
}

// watchResize calls fn with the new size whenever the console is resized.
// Consoles have no resize signal; poll.
func watchResize(fn func(cols, rows int)) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		cols, rows := terminalSize()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if c, r := terminalSize(); c != cols || r != rows {
					cols, rows = c, r
					fn(c, r)
				}
			}
		}
	}()
	return func() { close(done) }
}
