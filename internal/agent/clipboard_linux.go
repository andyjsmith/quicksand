package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// On Linux the clipboard belongs to the desktop session on seat0, and
// there's no common service for reaching it from outside: the agent runs
// a clipboard tool as the session's user. X11 tools (xclip, xsel) are
// preferred, also on Wayland desktops, which bridge their clipboard to
// Xwayland (GNOME's Mutter has no protocol for wl-clipboard to use without
// a window); wl-clipboard covers Wayland desktops without Xwayland.

type clipboardTool struct {
	name        string
	read, write []string
	wayland     bool
}

var clipboardTools = []clipboardTool{
	{name: "xclip", read: []string{"-selection", "clipboard", "-o"}, write: []string{"-selection", "clipboard", "-i"}},
	{name: "xsel", read: []string{"--clipboard", "--output"}, write: []string{"--clipboard", "--input"}},
	{name: "wl-paste", read: []string{"--no-newline", "--type", "text"}, wayland: true},
}

func readClipboard(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s, env, tool, err := clipboardSession(ctx)
	if err != nil {
		return "", err
	}
	cmd := clipboardCommand(ctx, s, env, tool.name, tool.read...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		// An empty clipboard is an error to all of them ("target STRING
		// not available", "Nothing is copied", ...).
		if _, ok := err.(*exec.ExitError); ok {
			return "", nil
		}
		return "", fmt.Errorf("%s: %w", tool.name, err)
	}
	return out.String(), nil
}

func writeClipboard(ctx context.Context, text string) error {
	s, env, tool, err := clipboardSession(ctx)
	if err != nil {
		return err
	}
	name, args := tool.name, tool.write
	if tool.wayland {
		name, args = "wl-copy", []string{"--type", "text/plain;charset=utf-8"}
	}
	// The tool forks to serve the clipboard until something else takes it;
	// it gets no pipes but stdin, so waiting for it ends when it forks.
	cmd := clipboardCommand(ctx, s, env, name, args...)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// clipboardSession finds the desktop session, its display variables and a
// clipboard tool that can reach it.
func clipboardSession(ctx context.Context) (graphicalSession, []string, clipboardTool, error) {
	s, err := activeSession(ctx)
	if err != nil {
		return s, nil, clipboardTool{}, fmt.Errorf("%w: %s", errClipboardUnsupported,
			strings.TrimPrefix(err.Error(), errDisplayUnsupported.Error()+": "))
	}
	env := sessionGUIEnv(s.uid)
	has := func(prefix string) bool {
		for _, kv := range env {
			if strings.HasPrefix(kv, prefix) {
				return true
			}
		}
		return false
	}
	for _, t := range clipboardTools {
		if _, err := exec.LookPath(t.name); err != nil {
			continue
		}
		if t.wayland && has("WAYLAND_DISPLAY=") || !t.wayland && has("DISPLAY=") {
			return s, env, t, nil
		}
	}
	return s, nil, clipboardTool{}, fmt.Errorf("%w: install xclip (or xsel, or wl-clipboard) in the guest to share its clipboard", errClipboardUnsupported)
}

func clipboardCommand(ctx context.Context, s graphicalSession, env []string, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append([]string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"USER=" + s.user,
		"HOME=" + homeOf(s.uid),
		fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d", s.uid),
	}, env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(s.uid), Gid: uint32(s.gid)}}
	return cmd
}

func homeOf(uid int) string {
	if data, err := os.ReadFile("/etc/passwd"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if f := strings.Split(line, ":"); len(f) > 5 && f[2] == strconv.Itoa(uid) {
				return f[5]
			}
		}
	}
	return "/"
}

// sessionGUIEnv finds DISPLAY, XAUTHORITY and WAYLAND_DISPLAY in the
// environment of the user's processes: the session leader often predates
// them (GNOME's shell sets them up), so it's whichever process has the most.
func sessionGUIEnv(uid int) []string {
	var best []string
	procs, _ := filepath.Glob("/proc/[0-9]*")
	for _, p := range procs {
		var st syscall.Stat_t
		if syscall.Stat(p, &st) != nil || int(st.Uid) != uid {
			continue
		}
		data, err := os.ReadFile(p + "/environ")
		if err != nil {
			continue
		}
		var env []string
		for _, kv := range bytes.Split(data, []byte{0}) {
			for _, k := range []string{"DISPLAY=", "XAUTHORITY=", "WAYLAND_DISPLAY="} {
				if bytes.HasPrefix(kv, []byte(k)) {
					env = append(env, string(kv))
				}
			}
		}
		if len(env) > len(best) {
			best = env
			if len(best) == 3 {
				break
			}
		}
	}
	return best
}
