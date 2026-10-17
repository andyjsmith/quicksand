//go:build !linux

package agent

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

func findDesktop(ctx context.Context) (desktop, error) {
	return nil, fmt.Errorf("launching apps isn't supported on %s yet", runtime.GOOS)
}

func terminate(cmd *exec.Cmd) { cmd.Process.Kill() }
