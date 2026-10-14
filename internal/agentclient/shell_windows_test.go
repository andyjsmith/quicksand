package agentclient

import (
	"context"
	"strings"
	"testing"

	"quicksand/internal/agentproto"
)

func TestShellPipes(t *testing.T) {
	srv, c := startAgent(t)
	conn, err := c.Shell(context.Background(), host(srv), agentproto.ShellRequest{Command: `findstr "^" & (echo err)1>&2 & exit 7`})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fw := agentproto.NewFrameWriter(conn)
	fw.Write(agentproto.FrameStdin, []byte("hello\r\n"))
	fw.Write(agentproto.FrameStdinEOF, nil)
	stdout, stderr, exit := readShell(t, conn)
	if strings.TrimSpace(stdout) != "hello" || strings.TrimSpace(stderr) != "err" || exit.Code != 7 {
		t.Errorf("stdout %q stderr %q exit %+v", stdout, stderr, exit)
	}
}

func TestShellPTY(t *testing.T) {
	srv, c := startAgent(t)
	conn, err := c.Shell(context.Background(), host(srv), agentproto.ShellRequest{
		Command: `powershell -NoProfile -Command "'width ' + $Host.UI.RawUI.WindowSize.Width; $x = Read-Host; 'got ' + $x"`,
		PTY:     true, Cols: 100, Rows: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fw := agentproto.NewFrameWriter(conn)
	fw.Write(agentproto.FrameStdin, []byte("ping\r"))
	stdout, _, exit := readShell(t, conn)
	for _, want := range []string{"width 100", "got ping"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output %q lacks %q", stdout, want)
		}
	}
	if exit.Code != 0 {
		t.Errorf("exit %+v", exit)
	}
}

// TestInteractiveShell runs the default shell (PowerShell on a
// pseudo-console) and checks resizing and the exit code.
func TestInteractiveShell(t *testing.T) {
	srv, c := startAgent(t)
	conn, err := c.Shell(context.Background(), host(srv), agentproto.ShellRequest{PTY: true, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fw := agentproto.NewFrameWriter(conn)
	fw.WriteJSON(agentproto.FrameResize, agentproto.Resize{Cols: 132, Rows: 40})
	fw.Write(agentproto.FrameStdin, []byte("'cols ' + $Host.UI.RawUI.WindowSize.Width\r"))
	fw.Write(agentproto.FrameStdin, []byte("exit 3\r"))
	stdout, _, exit := readShell(t, conn)
	if !strings.Contains(stdout, "cols 132") || exit.Code != 3 {
		t.Errorf("output %q, exit %+v", stdout, exit)
	}
}

func TestShellChoice(t *testing.T) {
	srv, c := startAgent(t)
	for _, tc := range []struct{ shell, command, want string }{
		{"cmd", "echo %COMSPEC%", `cmd.exe`},
		{"powershell", "$PSVersionTable.PSEdition", "Desktop"},
	} {
		conn, err := c.Shell(context.Background(), host(srv), agentproto.ShellRequest{Shell: tc.shell, Command: tc.command})
		if err != nil {
			t.Fatal(err)
		}
		stdout, _, exit := readShell(t, conn)
		conn.Close()
		if !strings.Contains(strings.ToLower(stdout), strings.ToLower(tc.want)) || exit.Code != 0 {
			t.Errorf("%s: stdout %q, exit %+v", tc.shell, stdout, exit)
		}
	}

	// Interactive cmd.exe on a pseudo-console.
	conn, err := c.Shell(context.Background(), host(srv), agentproto.ShellRequest{Shell: "cmd", PTY: true, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	agentproto.NewFrameWriter(conn).Write(agentproto.FrameStdin, []byte("echo cmd-%COMPUTERNAME%-ok\rexit 5\r"))
	stdout, _, exit := readShell(t, conn)
	if !strings.Contains(stdout, "-ok") || exit.Code != 5 {
		t.Errorf("interactive cmd: %q, exit %+v", stdout, exit)
	}
}
