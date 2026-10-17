package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"quicksand/internal/agentproto"
)

// On Linux the agent changes the resolution of the desktop on seat0 (the
// login screen's or the user's): through GNOME's display configuration
// service (Wayland or X11), or xrandr on other X11 desktops. Both choose
// from the modes the display offers; exact sizes come from the console
// protocols themselves, which pass the client's size to the virtual GPU
// (QEMU's VNC server, or SPICE with spice-vdagent).

// graphicalSession is the active session on seat0.
type graphicalSession struct {
	id, user, kind string // kind: wayland or x11
	uid, gid       int
	leader         int
}

func setDisplay(ctx context.Context, width, height int) (agentproto.DisplayResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var res agentproto.DisplayResult
	s, err := activeSession(ctx)
	if err != nil {
		return res, err
	}
	if err := gnomeSetMode(ctx, s, width, height, &res); !errors.Is(err, errNoGNOME) {
		return res, err
	}
	if s.kind == "x11" {
		return res, xrandrSetMode(ctx, s, width, height, &res)
	}
	return res, fmt.Errorf("%w: the %s desktop isn't GNOME", errDisplayUnsupported, s.kind)
}

func activeSession(ctx context.Context) (graphicalSession, error) {
	var s graphicalSession
	id, err := loginctl(ctx, "show-seat", "seat0", "-p", "ActiveSession", "--value")
	if err != nil || id == "" {
		return s, fmt.Errorf("%w: no one is at the guest's screen", errDisplayUnsupported)
	}
	props, err := loginctl(ctx, "show-session", id, "-p", "Type", "-p", "Name", "-p", "User", "-p", "Leader")
	if err != nil {
		return s, err
	}
	s.id = id
	for _, line := range strings.Split(props, "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "Type":
			s.kind = v
		case "Name":
			s.user = v
		case "User":
			s.uid, _ = strconv.Atoi(v)
		case "Leader":
			s.leader, _ = strconv.Atoi(v)
		}
	}
	if s.kind != "wayland" && s.kind != "x11" {
		return s, fmt.Errorf("%w: the guest's screen shows a %s session, not a desktop", errDisplayUnsupported, s.kind)
	}
	// logind knows the group of dynamic users too (GDM's login screen runs
	// as one), which aren't in /etc/passwd.
	gid, err := loginctl(ctx, "show-user", strconv.Itoa(s.uid), "-p", "GID", "--value")
	if s.gid, err = strconv.Atoi(gid); err != nil {
		u, lookupErr := user.LookupId(strconv.Itoa(s.uid))
		if lookupErr != nil {
			return s, fmt.Errorf("the group of user %d: %w", s.uid, lookupErr)
		}
		s.gid, _ = strconv.Atoi(u.Gid)
	}
	return s, nil
}

func loginctl(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "loginctl", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// asUser runs a command as the session's user, with its session bus.
func asUser(ctx context.Context, s graphicalSession, extraEnv []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	runtimeDir := fmt.Sprintf("/run/user/%d", s.uid)
	cmd.Env = append([]string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"USER=" + s.user,
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"DBUS_SESSION_BUS_ADDRESS=unix:path=" + runtimeDir + "/bus",
	}, extraEnv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(s.uid), Gid: uint32(s.gid)}}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("%s: %s", name, msg)
		}
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

var errNoGNOME = errors.New("no GNOME display configuration")

type displayMode struct {
	id            string
	width, height int
	scales        []float64
}

var (
	gnomeSerialRe    = regexp.MustCompile(`^\(uint32 (\d+),`)
	gnomeConnectorRe = regexp.MustCompile(`\[\(\('([^']*)', '`)
	gnomeModeRe      = regexp.MustCompile(`\('([^']+)', (\d+), (\d+), [\d.]+, [\d.]+, \[([^\]]*)\], (@a\{sv\} \{\}|\{[^}]*\})\)`)
	gnomeLogicalRe   = regexp.MustCompile(`\[\((-?\d+), (-?\d+), ([\d.]+), (?:uint32 )?(\d+), (true|false), \[\('`)
)

// gnomeSetMode switches the first monitor to the closest mode through
// org.gnome.Mutter.DisplayConfig, keeping the current scale if the mode
// supports it. The change is temporary: nothing is saved to monitors.xml.
func gnomeSetMode(ctx context.Context, s graphicalSession, width, height int, res *agentproto.DisplayResult) error {
	call := func(method string, args ...string) ([]byte, error) {
		return asUser(ctx, s, nil, "gdbus", append([]string{"call", "--session",
			"--dest", "org.gnome.Mutter.DisplayConfig", "--object-path", "/org/gnome/Mutter/DisplayConfig",
			"--method", "org.gnome.Mutter.DisplayConfig." + method}, args...)...)
	}
	out, err := call("GetCurrentState")
	if err != nil {
		if _, lookErr := exec.LookPath("gdbus"); lookErr != nil || strings.Contains(err.Error(), "ServiceUnknown") ||
			strings.Contains(err.Error(), "No such file") {
			return errNoGNOME
		}
		return err
	}
	st, err := parseGnomeState(string(out))
	if err != nil {
		return err
	}
	modes, logical := st.modes, st.logical
	mode, exact, ok := pickMode(modes, width, height)
	if !ok {
		return errors.New("the display offers no modes")
	}
	scale := 1.0
	if current, err := strconv.ParseFloat(logical[3], 64); err == nil {
		for _, sc := range mode.scales {
			if abs(sc-current) < 0.001 {
				scale = sc
			}
		}
	}
	config := fmt.Sprintf("[(%s, %s, %s, uint32 %s, %s, [('%s', '%s', @a{sv} {})])]",
		logical[1], logical[2], strconv.FormatFloat(scale, 'f', -1, 64), logical[4], logical[5], st.connector, mode.id)
	// Method 1 applies temporarily (2 would save it as the user's setting).
	if _, err := call("ApplyMonitorsConfig", st.serial, "1", config, "@a{sv} {}"); err != nil {
		return err
	}
	res.Width, res.Height, res.Exact, res.Adapter = mode.width, mode.height, exact, "gnome"
	return nil
}

// gnomeState is what gnomeSetMode needs from GetCurrentState's output
// (gdbus's text form): the serial, the first monitor's connector and modes,
// and the first logical monitor (x, y, scale, transform, primary).
type gnomeState struct {
	serial, connector string
	modes             []displayMode
	logical           []string
}

func parseGnomeState(state string) (gnomeState, error) {
	var st gnomeState
	serial := gnomeSerialRe.FindStringSubmatch(state)
	connector := gnomeConnectorRe.FindStringSubmatch(state)
	logical := gnomeLogicalRe.FindStringSubmatch(state)
	if serial == nil || connector == nil || logical == nil {
		return st, fmt.Errorf("unexpected display state from GNOME: %.200s", state)
	}
	st.serial, st.connector, st.logical = serial[1], connector[1], logical
	// Modes up to the end of the first monitor's mode list: the last mode's
	// closing parenthesis, then the monitor's properties. (Each mode's scale
	// list is followed by "], {" too, after a number.)
	monitor := state[strings.Index(state, connector[0]):]
	if end := strings.Index(monitor, ")], {"); end >= 0 {
		monitor = monitor[:end+1]
	}
	for _, m := range gnomeModeRe.FindAllStringSubmatch(monitor, -1) {
		mode := displayMode{id: m[1]}
		mode.width, _ = strconv.Atoi(m[2])
		mode.height, _ = strconv.Atoi(m[3])
		for _, f := range strings.Split(m[4], ",") {
			if v, err := strconv.ParseFloat(strings.TrimSpace(f), 64); err == nil {
				mode.scales = append(mode.scales, v)
			}
		}
		st.modes = append(st.modes, mode)
	}
	if len(st.modes) == 0 {
		return st, errors.New("GNOME lists no display modes")
	}
	return st, nil
}

// pickMode chooses the mode of the requested size, else the largest that
// fits, else the smallest.
func pickMode(modes []displayMode, width, height int) (displayMode, bool, bool) {
	var best, smallest displayMode
	for _, m := range modes {
		if m.width == width && m.height == height {
			return m, true, true
		}
		if m.width <= width && m.height <= height && m.width*m.height > best.width*best.height {
			best = m
		}
		if smallest.id == "" || m.width*m.height < smallest.width*smallest.height {
			smallest = m
		}
	}
	if best.id != "" {
		return best, false, true
	}
	return smallest, false, smallest.id != ""
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

var xrandrOutputRe = regexp.MustCompile(`^(\S+) connected`)

// xrandrSetMode switches the first connected output of an X11 session to
// the closest mode xrandr lists.
func xrandrSetMode(ctx context.Context, s graphicalSession, width, height int, res *agentproto.DisplayResult) error {
	env := sessionDisplayEnv(s.leader)
	out, err := asUser(ctx, s, env, "xrandr", "--query")
	if err != nil {
		return err
	}
	var output string
	var modes []displayMode
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if m := xrandrOutputRe.FindStringSubmatch(line); m != nil {
			if output != "" {
				break // the first output only
			}
			output = m[1]
			continue
		}
		if output == "" || !strings.HasPrefix(line, "   ") {
			continue
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		w, h, ok := strings.Cut(f[0], "x")
		if !ok {
			continue
		}
		mode := displayMode{id: f[0]}
		mode.width, _ = strconv.Atoi(w)
		mode.height, _ = strconv.Atoi(strings.TrimRightFunc(h, func(r rune) bool { return r < '0' || r > '9' }))
		modes = append(modes, mode)
	}
	if output == "" {
		return errors.New("xrandr shows no connected output")
	}
	mode, exact, ok := pickMode(modes, width, height)
	if !ok {
		return errors.New("the display offers no modes")
	}
	if _, err := asUser(ctx, s, env, "xrandr", "--output", output, "--mode", mode.id); err != nil {
		return err
	}
	// Keep it, rather than following the console back to its preferred mode.
	holdDisplay(parseXrandr(out))
	res.Width, res.Height, res.Exact, res.Adapter = mode.width, mode.height, exact, "xrandr"
	return nil
}

// sessionDisplayEnv is DISPLAY and XAUTHORITY from the session leader's
// environment.
func sessionDisplayEnv(leader int) []string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", leader))
	if err != nil {
		return []string{"DISPLAY=:0"}
	}
	var env []string
	for _, kv := range bytes.Split(data, []byte{0}) {
		if bytes.HasPrefix(kv, []byte("DISPLAY=")) || bytes.HasPrefix(kv, []byte("XAUTHORITY=")) {
			env = append(env, string(kv))
		}
	}
	if len(env) == 0 {
		env = []string{"DISPLAY=:0"}
	}
	return env
}

// RunDisplayHelper is only used on Windows.
func RunDisplayHelper(args []string) int { return 2 }
