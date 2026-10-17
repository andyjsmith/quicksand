package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"quicksand/internal/agentproto"
)

// The launcher keeps the app the server asked for (its LaunchSettings)
// running in the guest's desktop session, as the signed-in user: it waits
// for someone to sign in, starts the app, and starts it again when it exits
// (with Restart) or when someone signs in afresh. It hears about sign-ins
// from the session manager where it can (see watchSessions), and checks
// every launchCheckInterval as well.

const (
	launchCheckInterval = 10 * time.Second
	// After a session change, the desktop may take a moment to be usable
	// (its processes to start), so it's checked every launchHurryInterval
	// for launchHurryFor.
	launchHurryInterval = 250 * time.Millisecond
	launchHurryFor      = 10 * time.Second
	// launchSteadyRun is how long the app must run for its restart delay to
	// start again from the shortest.
	launchSteadyRun = time.Minute
	launchMaxDelay  = time.Minute
)

// desktop is a signed-in desktop session apps can be started in.
type desktop interface {
	// ID changes when someone signs in afresh.
	ID() string
	// Start runs argv as the session's user, in the session, writing its
	// error output (where there is one) to stderr if that isn't nil.
	Start(argv []string, stderr io.Writer) (process, error)
}

// process is an app started in a desktop session.
type process interface {
	Pid() int
	// Wait waits for it to exit, returning an error if it failed.
	Wait() error
	// Terminate stops it and what it started.
	Terminate()
}

type launcher struct {
	// path is where the settings are saved, empty to not save them.
	path string
	// findDesktop finds the session apps start in; watch, if set, calls
	// changed whenever sessions may have changed, until ctx ends.
	findDesktop func(ctx context.Context) (desktop, error)
	watch       func(ctx context.Context, changed func())
	wake        chan struct{}

	mu   sync.Mutex
	want agentproto.LaunchSettings
	// pending are URLs to open; start asks for the app to be started even
	// if it was in this session before.
	pending []string
	start   bool
	// app is the running app, started in session appSession; started is
	// the session it was last started in.
	app        process
	appSession string
	started    string
	// failures counts quick exits in a row, which push retryAt back.
	failures int
	retryAt  time.Time
	err      string
	// hurryUntil is until when to check often, after a session change.
	hurryUntil time.Time
}

// savedLaunch is what the launcher keeps across restarts: its settings, and
// URLs not opened yet (say, the agent restarted before anyone signed in).
type savedLaunch struct {
	agentproto.LaunchSettings
	Pending []string `json:"pending,omitempty"`
}

func newLauncher(path string) *launcher {
	l := &launcher{path: path, findDesktop: findDesktop, watch: watchSessions, wake: make(chan struct{}, 1)}
	if path == "" {
		return l
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l
	}
	var saved savedLaunch
	if err == nil {
		err = json.Unmarshal(b, &saved)
	}
	if err == nil {
		err = saved.Validate()
	}
	if err != nil {
		slog.Warn("ignoring saved launch settings", "path", path, "err", err)
		return l
	}
	l.want = saved.LaunchSettings
	if len(l.want.OpenURL) > 0 {
		for _, u := range saved.Pending {
			if agentproto.CheckOpenURL(u) == nil {
				l.pending = append(l.pending, u)
			}
		}
	}
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
		l.saveLocked()
	}
	if len(req.Open) > 0 {
		l.pending = append(l.pending, req.Open...)
		l.saveLocked()
	}
	l.start = l.start || req.Start
	l.mu.Unlock()
	l.poke()
	return l.state(), nil
}

// saveLocked saves the settings and pending URLs; l.mu must be held.
func (l *launcher) saveLocked() {
	if l.path == "" {
		return
	}
	err := os.MkdirAll(filepath.Dir(l.path), 0o700)
	if err == nil {
		b, _ := json.Marshal(savedLaunch{LaunchSettings: l.want, Pending: l.pending})
		err = os.WriteFile(l.path, b, 0o600)
	}
	if err != nil {
		slog.Warn("save launch settings", "err", err)
	}
}

// dropPendingLocked forgets the first n pending URLs (all if n < 0), once
// they're opened; l.mu must be held.
func (l *launcher) dropPendingLocked(n int) {
	if len(l.pending) == 0 {
		return
	}
	if n < 0 || n > len(l.pending) {
		n = len(l.pending)
	}
	l.pending = l.pending[n:]
	if len(l.pending) == 0 {
		l.pending = nil
	}
	l.saveLocked()
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

// sessionsChanged checks right away, and often for a little while.
func (l *launcher) sessionsChanged() {
	l.mu.Lock()
	l.hurryUntil = time.Now().Add(launchHurryFor)
	l.mu.Unlock()
	l.poke()
}

// run keeps the app going until ctx ends, then stops it.
func (l *launcher) run(ctx context.Context) {
	if l.watch != nil {
		go l.watch(ctx, l.sessionsChanged)
	}
	for {
		l.step(ctx)
		select {
		case <-ctx.Done():
			l.mu.Lock()
			l.stopLocked()
			l.mu.Unlock()
			return
		case <-l.wake:
		case <-time.After(l.interval()):
		}
	}
}

// interval is how long to wait before checking again: not long while
// waiting for a desktop that's just appearing.
func (l *launcher) interval() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.app == nil && len(l.want.Command) > 0 {
		if time.Now().Before(l.hurryUntil) {
			return launchHurryInterval
		}
		// Wake up when the app is due to be started again.
		if wait := time.Until(l.retryAt); wait > 0 && wait < launchCheckInterval {
			return wait + 10*time.Millisecond
		}
	}
	return launchCheckInterval
}

func (l *launcher) step(ctx context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.want.Command) == 0 {
		l.stopLocked()
		l.dropPendingLocked(-1)
		l.start, l.err = false, ""
		return
	}
	if len(l.want.OpenURL) == 0 {
		l.dropPendingLocked(-1)
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
		l.dropPendingLocked(-1)
		return
	}
	again := l.want.Restart || l.start || len(l.pending) > 0
	if l.started == d.ID() && !again || time.Now().Before(l.retryAt) {
		return
	}
	argv := l.want.Command
	opening := len(l.pending) > 0
	if opening {
		argv = l.want.OpenURLCommand(l.pending[0])
	}
	stderr := &tailBuffer{}
	app, err := d.Start(argv, stderr)
	if err != nil {
		l.err = fmt.Sprintf("couldn't start %s: %v", argv[0], err)
		l.failedLocked()
		return
	}
	slog.Info("launched app", "argv", argv, "pid", app.Pid(), "session", d.ID())
	if opening {
		l.dropPendingLocked(1)
	}
	l.app, l.appSession, l.started, l.start, l.err = app, d.ID(), d.ID(), false, ""
	go l.wait(app, argv[0], stderr)
}

// wait notes when the app exits, so it can be started again.
func (l *launcher) wait(app process, name string, stderr *tailBuffer) {
	began := time.Now()
	err := app.Wait()
	l.mu.Lock()
	if l.app != app {
		// Stopped on purpose.
		l.mu.Unlock()
		return
	}
	l.app = nil
	ran := time.Since(began)
	slog.Info("app exited", "name", name, "pid", app.Pid(), "after", ran.Round(time.Second), "err", err)
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
	p, err := d.Start(l.want.OpenURLCommand(u), nil)
	if err != nil {
		slog.Warn("open URL", "url", u, "err", err)
		return
	}
	slog.Info("opened URL", "url", u, "pid", p.Pid())
	go p.Wait()
}

func (l *launcher) stopLocked() {
	if l.app == nil {
		return
	}
	slog.Info("stopping app", "pid", l.app.Pid())
	l.app.Terminate()
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
