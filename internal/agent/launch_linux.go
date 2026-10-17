package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// linuxDesktop is the graphical session on seat0 and its user's
// environment there.
type linuxDesktop struct {
	s   graphicalSession
	env []string
}

func findDesktop(ctx context.Context) (desktop, error) {
	s, err := activeSession(ctx)
	if err != nil {
		return nil, errors.New(strings.TrimPrefix(err.Error(), errDisplayUnsupported.Error()+": "))
	}
	env := sessionUserEnv(s.uid)
	if !slicesHasPrefix(env, "DISPLAY=") && !slicesHasPrefix(env, "WAYLAND_DISPLAY=") {
		return nil, errors.New("the desktop session is still starting")
	}
	home := homeOf(s.uid)
	runtimeDir := fmt.Sprintf("/run/user/%d", s.uid)
	for _, kv := range []string{
		"HOME=" + home, "USER=" + s.user, "LOGNAME=" + s.user, "PATH=/usr/local/bin:/usr/bin:/bin:/snap/bin",
		"XDG_RUNTIME_DIR=" + runtimeDir, "DBUS_SESSION_BUS_ADDRESS=unix:path=" + runtimeDir + "/bus",
	} {
		if k, _, _ := strings.Cut(kv, "="); !slicesHasPrefix(env, k+"=") {
			env = append(env, kv)
		}
	}
	return linuxDesktop{s: s, env: env}, nil
}

func (d linuxDesktop) ID() string { return d.s.id }

func (d linuxDesktop) Command(argv []string) (*exec.Cmd, error) {
	path, err := lookPathIn(argv[0], envValue(d.env, "PATH"))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Env = d.env
	cmd.Dir = envValue(d.env, "HOME")
	cred := &syscall.Credential{Uid: uint32(d.s.uid), Gid: uint32(d.s.gid)}
	if u, err := user.LookupId(strconv.Itoa(d.s.uid)); err == nil {
		if ids, err := u.GroupIds(); err == nil {
			for _, id := range ids {
				if g, err := strconv.Atoi(id); err == nil {
					cred.Groups = append(cred.Groups, uint32(g))
				}
			}
		}
	}
	// Its own process group, so stopping it stops what it started too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred, Setpgid: true}
	return cmd, nil
}

// sessionDirs are where systemd-logind keeps the state of sessions (a file
// each) and seats (which session is on screen).
var sessionDirs = []string{"/run/systemd/sessions", "/run/systemd/seats"}

// watchSessions calls changed whenever logind's session or seat files
// change, e.g. when someone signs in, until ctx ends. If the directories
// aren't there yet (logind starting after the agent), it tries again every
// few seconds.
func watchSessions(ctx context.Context, changed func()) {
	for ctx.Err() == nil {
		if err := watchDirs(ctx, sessionDirs, changed); err != nil && ctx.Err() == nil {
			slog.Debug("watch sessions", "err", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

func watchDirs(ctx context.Context, dirs []string, changed func()) error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return err
	}
	// Non-blocking, so reads go through Go's poller and Close ends them.
	f := os.NewFile(uintptr(fd), "inotify")
	defer f.Close()
	for _, dir := range dirs {
		const mask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_TO | unix.IN_CLOSE_WRITE | unix.IN_DELETE_SELF
		if _, err := unix.InotifyAddWatch(fd, dir, mask); err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
	}
	stop := context.AfterFunc(ctx, func() { f.Close() })
	defer stop()
	// Something may have changed before the watch began.
	changed()
	buf := make([]byte, 64<<10)
	for {
		n, err := f.Read(buf)
		if err != nil {
			return err
		}
		// Which file changed doesn't matter, but a directory going away
		// (logind restarting) needs the watch set up again.
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			if ev.Mask&(unix.IN_DELETE_SELF|unix.IN_IGNORED) != 0 {
				changed()
				return errors.New("a watched directory went away")
			}
			off += unix.SizeofInotifyEvent + int(ev.Len)
		}
		changed()
	}
}

// terminate asks the app and its process group to quit.
func terminate(cmd *exec.Cmd) {
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

// sessionUserEnv is the whole environment of the user's process with the
// most display variables (see sessionGUIEnv): what an app started from
// their desktop would get.
func sessionUserEnv(uid int) []string {
	var best []string
	bestScore := 0
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
		score := 0
		for _, kv := range bytes.Split(data, []byte{0}) {
			if len(kv) == 0 {
				continue
			}
			env = append(env, string(kv))
			for _, k := range []string{"DISPLAY=", "XAUTHORITY=", "WAYLAND_DISPLAY="} {
				if bytes.HasPrefix(kv, []byte(k)) {
					score++
				}
			}
		}
		if score > bestScore {
			best, bestScore = env, score
		}
	}
	return best
}

// lookPathIn finds a program like exec.LookPath, but in the given PATH.
func lookPathIn(name, path string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	for _, dir := range filepath.SplitList(path) {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s isn't installed (not found in %s)", name, path)
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

func slicesHasPrefix(env []string, prefix string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}
