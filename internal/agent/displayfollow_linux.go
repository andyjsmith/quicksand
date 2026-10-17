package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// On X11 desktops other than GNOME (such as a bare Openbox session), the
// agent does two things GNOME does by itself (followDisplay):
//
//   - It keeps each display on its preferred mode, which QEMU sets to the
//     size of the Quicksand console (VNC or SPICE), so the guest's screen
//     follows the console's.
//   - It restarts spice-vdagent whenever the screen size settles on a new
//     value, whoever changed it. Outside GNOME, spice-vdagent scales SPICE
//     mouse positions by the screen size it started with, so after a resize
//     the pointer lands off target, by the ratio of the old size to the new.
//     The new spice-vdagent is moved into the desktop's login session (its
//     cgroup), as spice-vdagentd only sends resize requests to the agent of
//     the active session.

const followInterval = time.Second

// xrandrState is what xrandr --query says: the screen size ("1280x800")
// and the connected outputs.
type xrandrState struct {
	size    string
	outputs []xrandrOutput
}

// xrandrOutput is a connected output and its preferred and current modes
// ("" if none).
type xrandrOutput struct {
	name, preferred, current string
}

var xrandrScreenRe = regexp.MustCompile(`current (\d+) x (\d+)`)

func parseXrandr(out []byte) xrandrState {
	var st xrandrState
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "Screen ") {
			if m := xrandrScreenRe.FindStringSubmatch(line); m != nil && st.size == "" {
				st.size = m[1] + "x" + m[2]
			}
			continue
		}
		if m := xrandrOutputRe.FindStringSubmatch(line); m != nil {
			st.outputs = append(st.outputs, xrandrOutput{name: m[1]})
			continue
		}
		if !strings.HasPrefix(line, " ") {
			// A disconnected output: its modes aren't ours.
			if f := strings.Fields(line); len(f) > 1 && f[1] == "disconnected" {
				st.outputs = append(st.outputs, xrandrOutput{name: ""})
			}
			continue
		}
		if len(st.outputs) == 0 || st.outputs[len(st.outputs)-1].name == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		o := &st.outputs[len(st.outputs)-1]
		for _, rate := range f[1:] {
			if strings.Contains(rate, "+") && o.preferred == "" {
				o.preferred = f[0]
			}
			if strings.Contains(rate, "*") {
				o.current = f[0]
			}
		}
	}
	connected := st.outputs[:0]
	for _, o := range st.outputs {
		if o.name != "" {
			connected = append(connected, o)
		}
	}
	st.outputs = connected
	return st
}

// preferredKey identifies the preferred modes of all outputs.
func (st xrandrState) preferredKey() string {
	var b strings.Builder
	for _, o := range st.outputs {
		fmt.Fprintf(&b, "%s=%s;", o.name, o.preferred)
	}
	return b.String()
}

// displayHold pauses following after an explicit resolution change through
// the agent (POST /v1/display): prefs is the preferred modes when it was
// made, and following resumes once they change (the console was resized).
var displayHold struct {
	sync.Mutex
	prefs string
}

func holdDisplay(st xrandrState) {
	displayHold.Lock()
	displayHold.prefs = st.preferredKey()
	displayHold.Unlock()
}

// held reports whether following is paused for st, ending the pause once
// the preferred modes have changed.
func held(st xrandrState) bool {
	displayHold.Lock()
	defer displayHold.Unlock()
	if displayHold.prefs == "" {
		return false
	}
	if displayHold.prefs == st.preferredKey() {
		return true
	}
	displayHold.prefs = ""
	return false
}

// followDisplay keeps a non-GNOME X11 desktop's displays on their preferred
// modes and spice-vdagent's screen size current, until ctx ends.
func followDisplay(ctx context.Context) {
	var session string
	var env []string
	// started is the screen size spice-vdagent was last (re)started at, and
	// prev the size a moment ago: it's restarted once a new size settles.
	var started, prev string
	for sleep(ctx, followInterval) {
		s, err := activeSession(ctx)
		if err != nil || s.kind != "x11" || len(userProcesses(s.uid, "gnome-shell")) > 0 {
			session, started, prev = "", "", ""
			continue
		}
		if s.id != session || env == nil {
			session, started, prev = s.id, "", ""
			env = sessionGUIEnv(s.uid)
		}
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		out, err := asUser(qctx, s, env, "xrandr", "--query")
		cancel()
		if err != nil {
			// The desktop may still be starting; look for its display again.
			env = nil
			continue
		}
		st := parseXrandr(out)
		if !held(st) {
			for _, o := range st.outputs {
				if o.preferred != "" && o.preferred != o.current {
					slog.Info("display follows the console", "output", o.name, "mode", o.preferred)
					sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
					if _, err := asUser(sctx, s, env, "xrandr", "--output", o.name, "--preferred"); err != nil {
						slog.Warn("set preferred mode", "output", o.name, "err", err)
					}
					cancel()
				}
			}
		}
		if st.size != "" && st.size == prev && st.size != started {
			if err := restartVdagent(ctx, s); err != nil {
				slog.Warn("restart spice-vdagent", "err", err)
			}
			started = st.size
		}
		prev = st.size
	}
}

// restartVdagent restarts the session user's spice-vdagent, if it runs, in
// the session's cgroup.
func restartVdagent(ctx context.Context, s graphicalSession) error {
	pids := userProcesses(s.uid, "spice-vdagent")
	if len(pids) == 0 {
		return nil
	}
	for _, pid := range pids {
		syscall.Kill(pid, syscall.SIGTERM)
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if len(userProcesses(s.uid, "spice-vdagent")) == 0 {
			break
		}
	}
	d, err := findLinuxDesktop(ctx)
	if err != nil {
		return err
	}
	// The shell waits until it's in the session's cgroup, so spice-vdagent
	// (which daemonizes) connects to spice-vdagentd as part of the session.
	cmd, err := d.Command([]string{"/bin/sh", "-c", "read _ && exec spice-vdagent"})
	if err != nil {
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	moveErr := joinSessionCgroup(s.leader, cmd.Process.Pid)
	stdin.Write([]byte("\n"))
	stdin.Close()
	slog.Info("restarted spice-vdagent for the new screen size", "pid", cmd.Process.Pid)
	if moveErr != nil {
		return fmt.Errorf("it may not get resize requests: %w", moveErr)
	}
	return nil
}

// joinSessionCgroup moves pid into the cgroup (cgroup v2) of the session
// whose leader is leader, making it part of that login session.
func joinSessionCgroup(leader, pid int) error {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", leader))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if path, ok := strings.CutPrefix(line, "0::"); ok && strings.HasSuffix(path, ".scope") {
			return os.WriteFile(filepath.Join("/sys/fs/cgroup", path, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0)
		}
	}
	return errors.New("the session has no cgroup v2 scope")
}

// userProcesses lists the processes of uid whose command name is comm.
func userProcesses(uid int, comm string) []int {
	var pids []int
	procs, _ := filepath.Glob("/proc/[0-9]*")
	for _, p := range procs {
		var st syscall.Stat_t
		if syscall.Stat(p, &st) != nil || int(st.Uid) != uid {
			continue
		}
		if b, err := os.ReadFile(p + "/comm"); err == nil && strings.TrimSpace(string(b)) == comm {
			if pid, err := strconv.Atoi(filepath.Base(p)); err == nil {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}
