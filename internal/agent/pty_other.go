//go:build !linux && !windows

package agent

import (
	"errors"
	"os/exec"
)

// errNoPTY makes shells fall back to pipes.
var errNoPTY = errors.New("terminals aren't supported on this OS")

func startPTY(cmd *exec.Cmd, cols, rows int) (session, error) { return nil, errNoPTY }
