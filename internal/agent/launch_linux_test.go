package agent

import (
	"context"
	"io"
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

func (d fakeDesktop) Start(argv []string, stderr io.Writer) (process, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmdProcess{cmd}, nil
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
	return l.app.Pid()
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

func TestWatchDirs(t *testing.T) {
	dirs := []string{t.TempDir(), t.TempDir()}
	changes := make(chan struct{}, 100)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- watchDirs(ctx, dirs, func() { changes <- struct{}{} }) }()
	wait := func(what string) {
		t.Helper()
		select {
		case <-changes:
		case <-time.After(5 * time.Second):
			t.Fatalf("no change reported for %s", what)
		}
	}
	wait("the watch starting")
	// logind writes a temporary file and renames it into place.
	tmp := filepath.Join(dirs[0], ".#3abc")
	os.WriteFile(tmp, []byte("STATE=active\n"), 0o644)
	wait("a new file")
	os.Rename(tmp, filepath.Join(dirs[0], "3"))
	wait("a rename")
	os.WriteFile(filepath.Join(dirs[1], "seat0"), []byte("ACTIVE=3\n"), 0o644)
	wait("the second directory")
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watch didn't stop when cancelled")
	}
}

func TestLauncherHurriesAfterSessionChange(t *testing.T) {
	session := "1"
	l := testLauncher(t, &session)
	l.set(agentproto.LaunchRequest{LaunchSettings: agentproto.LaunchSettings{Command: []string{"sleep", "30"}}})
	if got := l.interval(); got != launchCheckInterval {
		t.Errorf("interval before a session change = %v", got)
	}
	l.sessionsChanged()
	if got := l.interval(); got != launchHurryInterval {
		t.Errorf("interval after a session change = %v", got)
	}
	if appPID(l) == 0 {
		t.Fatal("app not started")
	}
	if got := l.interval(); got != launchCheckInterval {
		t.Errorf("interval with the app running = %v", got)
	}
}

func TestLauncherKeepsPendingURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launch.json")
	l := newLauncher(path)
	// Nobody is signed in yet.
	l.findDesktop = func(context.Context) (desktop, error) { return nil, os.ErrNotExist }
	settings := agentproto.LaunchSettings{Command: []string{"sleep", "30"}, OpenURL: []string{"sleep", "30", "{url}"}, Restart: true}
	if _, err := l.set(agentproto.LaunchRequest{LaunchSettings: settings, Open: []string{"https://example.com/"}}); err != nil {
		t.Fatal(err)
	}
	l.step(context.Background())
	// The agent restarts before anyone signs in.
	session := "1"
	l2 := newLauncher(path)
	l2.findDesktop = func(context.Context) (desktop, error) { return fakeDesktop{session}, nil }
	t.Cleanup(func() { l2.mu.Lock(); l2.stopLocked(); l2.mu.Unlock() })
	if len(l2.pending) != 1 {
		t.Fatalf("pending after restart = %v", l2.pending)
	}
	if appPID(l2) == 0 {
		t.Fatal("app not started")
	}
	if len(l2.pending) != 0 || len(newLauncher(path).pending) != 0 {
		t.Errorf("URL still pending once opened: %v", l2.pending)
	}
}

func TestLauncherWakesForRestart(t *testing.T) {
	session := "1"
	l := testLauncher(t, &session)
	l.set(agentproto.LaunchRequest{LaunchSettings: agentproto.LaunchSettings{Command: []string{"sleep", "30"}, Restart: true}})
	l.mu.Lock()
	l.retryAt = time.Now().Add(time.Second)
	l.mu.Unlock()
	if got := l.interval(); got > 1100*time.Millisecond {
		t.Errorf("interval with a restart due in 1s = %v", got)
	}
}
