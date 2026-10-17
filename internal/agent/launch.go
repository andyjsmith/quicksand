package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"quicksand/internal/agentproto"
)

// The launcher keeps the app the server asked for (its LaunchSettings)
// running in the guest's desktop session, as the signed-in user: it waits
// for someone to sign in, starts the app, and starts it again when it exits
// (with Restart) or when someone signs in afresh.

const (
	launchCheckInterval = 3 * time.Second
	// launchSteadyRun is how long the app must run for its restart delay to
	// start again from the shortest.
	launchSteadyRun = time.Minute
	launchMaxDelay  = time.Minute
)

// desktop is a signed-in desktop session apps can be started in.
type desktop interface {
	// ID changes when someone signs in afresh.
	ID() string
	// Command prepares argv to run as the session's user, in the session.
	Command(argv []string) (*exec.Cmd, error)
}

type launcher struct {
	// path is where the settings are saved, empty to not save them.
	path string
	// findDesktop finds the session apps start in.
	findDesktop func(ctx context.Context) (desktop, error)
	wake        chan struct{}

	mu   sync.Mutex
	want agentproto.LaunchSettings
	// pending are URLs to open; start asks for the app to be started even
	// if it was in this session before.
	pending []string
	start   bool
	// app is the running app, started in session appSession; started is
	// the session it was last started in.
	app        *exec.Cmd
	appSession string
	started    string
	// failures counts quick exits in a row, which push retryAt back.
	failures int
	retryAt  time.Time
	err      string
}

func newLauncher(path string) *launcher {
	l := &launcher{path: path, findDesktop: findDesktop, wake: make(chan struct{}, 1)}
	if path == "" {
		return l
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l
	}
	var want agentproto.LaunchSettings
	if err == nil {
		err = json.Unmarshal(b, &want)
	}
	if err == nil {
		err = want.Validate()
	}
	if err != nil {
		slog.Warn("ignoring saved launch settings", "path", path, "err", err)
		return l
	}
	l.want = want
	return l
}

// set applies new settings (restarting the app if they changed) and queues
// URLs to open.
func (l *launcher) set(req agentproto.LaunchRequest) (agentproto.LaunchState, error) {
	if err := req.Validate(); err != nil {
		return agentproto.LaunchState{}, err
	}
	if len(req.Open) > 0 && len(req.OpenURL) == 0 {
		return agentproto.LaunchState{}, errors.New("this app can't open URLs (it has no openURL)")
	}
	for _, u := range req.Open {
		if err := agentproto.CheckOpenURL(u); err != nil {
			return agentproto.LaunchState{}, err
		}
	}
	l.mu.Lock()
	if !l.want.Equal(req.LaunchSettings) {
		slog.Info("launch settings changed", "command", req.Command, "openURL", req.OpenURL, "restart", req.Restart)
		l.want = req.LaunchSettings
		l.stopLocked()
		l.started, l.failures, l.retryAt, l.err = "", 0, time.Time{}, ""
		if err := l.save(); err != nil {
			slog.Warn("save launch settings", "err", err)
		}
	}
	l.pending = append(l.pending, req.Open...)
	l.start = l.start || req.Start
	l.mu.Unlock()
	l.poke()
	return l.state(), nil
}

func (l *launcher) save() error {
	if l.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(l.want)
	return os.WriteFile(l.path, b, 0o600)
}

// state reports the settings and whether the app runs.
func (l *launcher) state() agentproto.LaunchState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return agentproto.LaunchState{LaunchSettings: l.want, Running: l.app != nil, Error: l.err}
}

func (l *launcher) poke() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// run keeps the app going until ctx ends, then stops it.
func (l *launcher) run(ctx context.Context) {
	for {
		l.step(ctx)
		select {
		case <-ctx.Done():
			l.mu.Lock()
			l.stopLocked()
			l.mu.Unlock()
			return
		case <-l.wake:
		case <-time.After(launchCheckInterval):
		}
	}
}

func (l *launcher) step(ctx context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.want.Command) == 0 {
		l.stopLocked()
		l.pending, l.start, l.err = nil, false, ""
		return
	}
	if len(l.want.OpenURL) == 0 {
		l.pending = nil
	}
	l.mu.Unlock()
	fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	d, err := l.findDesktop(fctx)
	cancel()
	l.mu.Lock()
	if err != nil {
		l.err = err.Error()
		return
	}
	if l.app != nil && l.appSession != d.ID() {
		// Its session ended; someone signed in afresh.
		l.stopLocked()
	}
	if l.app != nil {
		for _, u := range l.pending {
			l.openLocked(d, u)
		}
		l.pending = nil
		return
	}
	again := l.want.Restart || l.start || len(l.pending) > 0
	if l.started == d.ID() && !again || time.Now().Before(l.retryAt) {
		return
	}
	argv := l.want.Command
	if len(l.pending) > 0 {
		argv = l.want.OpenURLCommand(l.pending[0])
	}
	cmd, err := d.Command(argv)
	var stderr *tailBuffer
	if err == nil {
		stderr = &tailBuffer{}
		cmd.Stdout, cmd.Stderr = nil, stderr
		err = cmd.Start()
	}
	if err != nil {
		l.err = fmt.Sprintf("couldn't start %s: %v", argv[0], err)
		l.failedLocked()
		return
	}
	slog.Info("launched app", "argv", argv, "pid", cmd.Process.Pid, "session", d.ID())
	if len(l.pending) > 0 {
		l.pending = l.pending[1:]
	}
	l.app, l.appSession, l.started, l.start, l.err = cmd, d.ID(), d.ID(), false, ""
	go l.wait(cmd, argv[0], stderr)
}

// wait notes when the app exits, so it can be started again.
func (l *launcher) wait(cmd *exec.Cmd, name string, stderr *tailBuffer) {
	began := time.Now()
	err := cmd.Wait()
	l.mu.Lock()
	if l.app != cmd {
		// Stopped on purpose.
		l.mu.Unlock()
		return
	}
	l.app = nil
	ran := time.Since(began)
	slog.Info("app exited", "name", name, "pid", cmd.Process.Pid, "after", ran.Round(time.Second), "err", err)
	if ran >= launchSteadyRun {
		l.failures, l.retryAt = 0, time.Now().Add(time.Second)
	} else {
		if err != nil {
			l.err = fmt.Sprintf("%s exited after %s: %v", name, ran.Round(time.Second), err)
			if tail := stderr.lastLine(); tail != "" {
				l.err += ": " + tail
			}
		}
		l.failedLocked()
	}
	l.mu.Unlock()
	l.poke()
}

// failedLocked delays the next start: 1s, 2s, 4s ... up to launchMaxDelay.
func (l *launcher) failedLocked() {
	l.failures++
	l.retryAt = time.Now().Add(min(time.Second<<min(l.failures-1, 6), launchMaxDelay))
}

// openLocked hands a URL to the running app.
func (l *launcher) openLocked(d desktop, u string) {
	argv := l.want.OpenURLCommand(u)
	cmd, err := d.Command(argv)
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		slog.Warn("open URL", "url", u, "err", err)
		return
	}
	slog.Info("opened URL", "url", u, "pid", cmd.Process.Pid)
	go cmd.Wait()
}

func (l *launcher) stopLocked() {
	if l.app == nil {
		return
	}
	slog.Info("stopping app", "pid", l.app.Process.Pid)
	terminate(l.app)
	l.app = nil
}

// tailBuffer keeps the last few hundred bytes written, for error messages.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > 512 {
		b.buf = b.buf[len(b.buf)-512:]
	}
	return len(p), nil
}

func (b *tailBuffer) lastLine() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(b.buf)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (a *Agent) handleSetLaunch(w http.ResponseWriter, r *http.Request) {
	var req agentproto.LaunchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "bad request: " + err.Error()})
		return
	}
	st, err := a.launcher.set(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}
