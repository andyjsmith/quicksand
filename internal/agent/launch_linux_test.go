package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"quicksand/internal/agentproto"
)

// fakeDesktop runs apps as the test's own user.
type fakeDesktop struct{ id string }

func (d fakeDesktop) ID() string { return d.id }

func (d fakeDesktop) Command(argv []string) (*exec.Cmd, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd, nil
}

func testLauncher(t *testing.T, session *string) *launcher {
	t.Helper()
	l := newLauncher(filepath.Join(t.TempDir(), "launch.json"))
	l.findDesktop = func(context.Context) (desktop, error) { return fakeDesktop{*session}, nil }
	t.Cleanup(func() {
		l.mu.Lock()
		l.stopLocked()
		l.mu.Unlock()
	})
	return l
}

// appPID steps the launcher and returns its app's PID (0 if none).
func appPID(l *launcher) int {
	l.step(context.Background())
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.app == nil {
		return 0
	}
	return l.app.Process.Pid
}

// killApp kills the app and waits for the launcher to notice.
func killApp(t *testing.T, l *launcher, pid int) {
	t.Helper()
	syscall.Kill(pid, syscall.SIGKILL)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		l.mu.Lock()
		gone := l.app == nil
		l.mu.Unlock()
		if gone {
			return
		}
	}
	t.Fatal("the launcher didn't notice the app exit")
}

func TestLauncherRestarts(t *testing.T) {
	session := "1"
	l := testLauncher(t, &session)
	if _, err := l.set(agentproto.LaunchRequest{LaunchSettings: agentproto.LaunchSettings{
		Command: []string{"sleep", "30"}, Restart: true,
	}}); err != nil {
		t.Fatal(err)
	}
	pid := appPID(l)
	if pid == 0 {
		t.Fatal("app not started")
	}
	if again := appPID(l); again != pid {
		t.Fatalf("app restarted while running: %d, then %d", pid, again)
	}
	killApp(t, l, pid)
	if got := appPID(l); got != 0 {
		t.Fatal("app restarted at once after a quick exit")
	}
	l.mu.Lock()
	l.retryAt = time.Time{}
	l.mu.Unlock()
	next := appPID(l)
	if next == 0 || next == pid {
		t.Fatalf("app not restarted after its delay: %d", next)
	}
	// A new session gets a new app.
	session = "2"
	if third := appPID(l); third == 0 || third == next {
		t.Fatalf("app not restarted for a new session: %d", third)
	}
}

func TestLauncherOncePerSession(t *testing.T) {
	session := "1"
	l := testLauncher(t, &session)
	l.set(agentproto.LaunchRequest{LaunchSettings: agentproto.LaunchSettings{Command: []string{"sleep", "30"}}})
	pid := appPID(l)
	killApp(t, l, pid)
	l.mu.Lock()
	l.retryAt = time.Time{}
	l.mu.Unlock()
	if got := appPID(l); got != 0 {
		t.Fatal("app without restart was restarted")
	}
	if st, _ := l.set(agentproto.LaunchRequest{LaunchSettings: l.want, Start: true}); st.Running {
		t.Error("state says running before it's started")
	}
	if got := appPID(l); got == 0 {
		t.Fatal("app not started when asked to")
	}
}

func TestLauncherOpensURLs(t *testing.T) {
	session := "1"
	l := testLauncher(t, &session)
	out := filepath.Join(t.TempDir(), "opened")
	record := `echo "$1" >> ` + out + `; exec sleep 30`
	_, err := l.set(agentproto.LaunchRequest{
		LaunchSettings: agentproto.LaunchSettings{
			Command: []string{"sh", "-c", "exec sleep 30"},
			OpenURL: []string{"sh", "-c", record, "sh", "{url}"},
			Restart: true,
		},
		Open: []string{"https://example.com/a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if appPID(l) == 0 {
		t.Fatal("app not started")
	}
	// Once running, URLs are handed to it with another openURL command.
	l.set(agentproto.LaunchRequest{LaunchSettings: l.want, Open: []string{"https://example.com/b"}})
	appPID(l)
	var got string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		b, _ := os.ReadFile(out)
		if got = string(b); strings.Count(got, "\n") == 2 {
			break
		}
	}
	if got != "https://example.com/a\nhttps://example.com/b\n" {
		t.Errorf("opened %q", got)
	}

	for _, req := range []agentproto.LaunchRequest{
		{LaunchSettings: agentproto.LaunchSettings{Command: []string{"x"}}, Open: []string{"https://example.com"}},
		{LaunchSettings: l.want, Open: []string{"-flag"}},
		{LaunchSettings: l.want, Open: []string{"file:///etc/passwd"}},
		{LaunchSettings: agentproto.LaunchSettings{Command: []string{"x"}, OpenURL: []string{"x"}}},
	} {
		if _, err := l.set(req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
}

func TestLauncherSavesSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "launch.json")
	want := agentproto.LaunchSettings{Command: []string{"firefox"}, OpenURL: []string{"firefox", "{url}"}, Restart: true}
	l := newLauncher(path)
	l.findDesktop = func(context.Context) (desktop, error) { return nil, os.ErrNotExist }
	if _, err := l.set(agentproto.LaunchRequest{LaunchSettings: want}); err != nil {
		t.Fatal(err)
	}
	if got := newLauncher(path).want; !got.Equal(want) {
		t.Errorf("loaded %+v, want %+v", got, want)
	}
}
