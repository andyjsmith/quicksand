package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"quicksand/internal/agentproto"
)

func shellCommandFor(req agentproto.ShellRequest) *exec.Cmd {
	sh := req.Shell
	if req.Command != "" {
		switch shellKind(sh) {
		case "cmd":
			return shellCommand(context.Background(), req.Command)
		case "powershell":
			return exec.Command(sh, "-NoLogo", "-NoProfile", "-Command", req.Command)
		}
		// bash and the like.
		return exec.Command(sh, "-c", req.Command)
	}
	switch {
	case sh == "" && req.PTY && hasPTY():
		return exec.Command(powerShell(), "-NoLogo")
	case sh == "":
		// Without a terminal, cmd.exe copes with commands arriving on a pipe.
		return exec.Command("cmd.exe")
	case shellKind(sh) == "powershell":
		return exec.Command(sh, "-NoLogo")
	}
	return exec.Command(sh)
}

// shellKind classifies a requested shell by how it takes a command:
// "cmd" (the default), "powershell" (powershell, pwsh) or "" (-c).
func shellKind(sh string) string {
	if sh == "" {
		return "cmd"
	}
	switch strings.TrimSuffix(strings.ToLower(filepath.Base(sh)), ".exe") {
	case "cmd":
		return "cmd"
	case "powershell", "pwsh":
		return "powershell"
	}
	return ""
}

// powerShell prefers PowerShell 7 when it's installed.
func powerShell() string {
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		return p
	}
	return "powershell.exe"
}

func shellEnv(req agentproto.ShellRequest) []string {
	return os.Environ()
}
