package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"quicksand/internal/agentproto"
)

// escapeKey (Ctrl-]) ends a serial console session, which has no exit of
// its own.
const escapeKey = 0x1d

// cmdShell runs a command, or an interactive shell, in the guest. Like
// ssh, it gets a terminal when interactive and pipes otherwise. Without a
// connected agent it falls back to the serial console, where the template
// enables that.
func cmdShell(ctx context.Context, cmd *cli.Command) error {
	forceTTY, noTTY := cmd.Bool("t"), cmd.Bool("T")
	serial, shellProg := cmd.Bool("serial"), cmd.String("shell")
	// Flags come before the VM; everything after it is the command.
	args := cmd.Args().Slice()
	if len(args) < 1 {
		return errors.New("usage: qs shell [-t | -T] [--shell program | --serial] <vm> [command...]")
	}
	c, err := signedIn()
	if err != nil {
		return err
	}
	v, err := findVM(c, args[0])
	if err != nil {
		return err
	}
	command := strings.Join(args[1:], " ")
	stdinTTY := term.IsTerminal(int(os.Stdin.Fd()))
	agentUp := v.Agent != nil && v.Agent.Status == "connected"
	if serial || (!agentUp && v.Serial && shellProg == "") {
		if shellProg != "" {
			return errors.New("--shell doesn't apply to the serial console")
		}
		return serialShell(c, v, command, stdinTTY)
	}
	pty := !noTTY && (forceTTY || (command == "" && stdinTTY))

	q := url.Values{}
	if command != "" {
		q.Set("command", command)
	}
	if shellProg != "" {
		q.Set("shell", shellProg)
	}
	if pty {
		cols, rows := terminalSize()
		termName := os.Getenv("TERM")
		if termName == "" || termName == "dumb" {
			termName = "xterm-256color"
		}
		q.Set("pty", "1")
		q.Set("cols", strconv.Itoa(cols))
		q.Set("rows", strconv.Itoa(rows))
		q.Set("term", termName)
	}
	conn, err := c.stream("/api/vms/" + url.PathEscape(v.ID) + "/shell?" + q.Encode())
	if err != nil {
		return err
	}
	code, err := runShell(conn, pty, pty && stdinTTY, false)
	conn.Close()
	if err != nil {
		return err
	}
	os.Exit(code)
	return nil
}

func serialShell(c *client, v vm, command string, stdinTTY bool) error {
	if !v.Serial {
		return fmt.Errorf("%s has no serial console shell (its template doesn't enable one)", v.Name)
	}
	if command != "" {
		return errors.New("running a command needs the agent; the serial console only offers a terminal")
	}
	cols, rows := terminalSize()
	conn, err := c.stream(fmt.Sprintf("/api/vms/%s/serial?cols=%d&rows=%d", url.PathEscape(v.ID), cols, rows))
	if err != nil {
		return err
	}
	defer conn.Close()
	fmt.Fprintf(os.Stderr, "Connected to the serial console of %s. Press Enter for a prompt, Ctrl-] to disconnect.\n", v.Name)
	_, err = runShell(conn, true, stdinTTY, true)
	if stdinTTY {
		fmt.Fprintln(os.Stderr)
	}
	return err
}

// runShell relays the terminal (or stdin and stdout) to a shell stream
// until the command exits, and returns its exit code. With escape, typing
// escapeKey disconnects.
func runShell(conn net.Conn, pty, raw, escape bool) (int, error) {
	if raw {
		restore, err := makeRaw()
		if err != nil {
			return 0, err
		}
		defer restore()
	}
	fw := agentproto.NewFrameWriter(conn)
	var escaped atomic.Bool
	go func() {
		stdin := fw.StreamWriter(agentproto.FrameStdin)
		buf := make([]byte, 32<<10)
		for {
			n, err := os.Stdin.Read(buf)
			p := buf[:n]
			if i := bytes.IndexByte(p, escapeKey); escape && i >= 0 {
				stdin.Write(p[:i])
				escaped.Store(true)
				conn.Close()
				return
			}
			if len(p) > 0 {
				if _, err := stdin.Write(p); err != nil {
					return
				}
			}
			if err == io.EOF {
				fw.Write(agentproto.FrameStdinEOF, nil)
			}
			if err != nil {
				return
			}
		}
	}()
	if pty {
		stop := watchResize(func(cols, rows int) {
			fw.WriteJSON(agentproto.FrameResize, agentproto.Resize{Cols: cols, Rows: rows})
		})
		defer stop()
	}
	for {
		t, payload, err := agentproto.ReadFrame(conn)
		if escaped.Load() {
			return 0, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				err = errors.New("the connection closed before the command exited")
			}
			return 0, err
		}
		switch t {
		case agentproto.FrameStdout:
			os.Stdout.Write(payload)
		case agentproto.FrameStderr:
			os.Stderr.Write(payload)
		case agentproto.FrameExit:
			var exit agentproto.ShellExit
			if err := json.Unmarshal(payload, &exit); err != nil {
				return 0, err
			}
			if exit.Error != "" {
				return 0, errors.New(exit.Error)
			}
			return exit.Code, nil
		}
	}
}

// terminalSize is the size of the terminal qs runs in, or 80×24.
func terminalSize() (cols, rows int) {
	for _, f := range []*os.File{os.Stdout, os.Stdin, os.Stderr} {
		if c, r, err := term.GetSize(int(f.Fd())); err == nil && c > 0 && r > 0 {
			return c, r
		}
	}
	return 80, 24
}
