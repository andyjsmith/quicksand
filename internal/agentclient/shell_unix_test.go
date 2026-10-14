//go:build !windows

package agentclient

import (
	"context"
	"strings"
	"testing"

	"quicksand/internal/agentproto"
)

func TestShellPipes(t *testing.T) {
	srv, c := startAgent(t)
	conn, err := c.Shell(context.Background(), host(srv), agentproto.ShellRequest{Command: "cat; echo err >&2; exit 7"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fw := agentproto.NewFrameWriter(conn)
	fw.Write(agentproto.FrameStdin, []byte("hello\n"))
	fw.Write(agentproto.FrameStdinEOF, nil)
	stdout, stderr, exit := readShell(t, conn)
	if stdout != "hello\n" || stderr != "err\n" || exit.Code != 7 {
		t.Errorf("stdout %q stderr %q exit %+v", stdout, stderr, exit)
	}
}

func TestShellPTY(t *testing.T) {
	srv, c := startAgent(t)
	conn, err := c.Shell(context.Background(), host(srv), agentproto.ShellRequest{
		Command: "stty size; tty >/dev/null && echo has-tty; read x; echo got $x", PTY: true, Cols: 100, Rows: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fw := agentproto.NewFrameWriter(conn)
	fw.Write(agentproto.FrameStdin, []byte("ping\n"))
	stdout, _, exit := readShell(t, conn)
	for _, want := range []string{"30 100", "has-tty", "got ping"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output %q lacks %q", stdout, want)
		}
	}
	if exit.Code != 0 {
		t.Errorf("exit %+v", exit)
	}
}

func TestShellChoice(t *testing.T) {
	srv, c := startAgent(t)
	conn, err := c.Shell(context.Background(), host(srv), agentproto.ShellRequest{Shell: "sh", Command: `echo "$0"`})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stdout, _, exit := readShell(t, conn)
	if !strings.HasSuffix(strings.TrimSpace(stdout), "/sh") || exit.Code != 0 {
		t.Errorf("stdout %q, exit %+v", stdout, exit)
	}

	// An interactive non-default shell, on a terminal.
	conn, err = c.Shell(context.Background(), host(srv), agentproto.ShellRequest{Shell: "/bin/sh", PTY: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	agentproto.NewFrameWriter(conn).Write(agentproto.FrameStdin, []byte("echo shell=$SHELL; exit 4\n"))
	stdout, _, exit = readShell(t, conn)
	if !strings.Contains(stdout, "shell=/bin/sh") || exit.Code != 4 {
		t.Errorf("stdout %q, exit %+v", stdout, exit)
	}

	if _, err := c.Shell(context.Background(), host(srv), agentproto.ShellRequest{Shell: "no-such-shell"}); !isStatus(err, 400) {
		t.Errorf("missing shell: %v", err)
	}
}
