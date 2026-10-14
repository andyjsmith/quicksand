//go:build !windows

package agent

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"quicksand/internal/agentproto"
)

func shellCommandFor(req agentproto.ShellRequest) *exec.Cmd {
	sh := shellPath(req)
	if req.Command != "" {
		return exec.Command(sh, "-c", req.Command)
	}
	switch filepath.Base(sh) {
	case "bash", "zsh", "sh", "dash", "ksh", "mksh", "ash", "fish":
		return exec.Command(sh, "-l")
	}
	// Something else (python, say): no login option to give it.
	return exec.Command(sh)
}

// shellPath is the requested shell, resolved in PATH, or the login shell.
func shellPath(req agentproto.ShellRequest) string {
	if req.Shell == "" {
		return loginShell()
	}
	if p, err := exec.LookPath(req.Shell); err == nil {
		return p
	}
	return req.Shell
}

func loginShell() string {
	for _, sh := range []string{os.Getenv("SHELL"), "/bin/bash", "/bin/sh"} {
		if sh == "" {
			continue
		}
		if _, err := os.Stat(sh); err == nil {
			return sh
		}
	}
	return "/bin/sh"
}

func shellEnv(req agentproto.ShellRequest) []string {
	env := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "LANG=") || strings.HasPrefix(kv, "LC_") {
			env = append(env, kv)
		}
	}
	if u, err := user.Current(); err == nil {
		env = append(env, "HOME="+u.HomeDir, "USER="+u.Username, "LOGNAME="+u.Username)
	}
	env = append(env, "SHELL="+shellPath(req))
	if req.PTY {
		term := req.Term
		if term == "" {
			term = "xterm-256color"
		}
		env = append(env, "TERM="+term)
	}
	return env
}
