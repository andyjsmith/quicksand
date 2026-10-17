package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
