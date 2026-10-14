//go:build !windows

package agentclient

import (
	"os/exec"
	"testing"
)

func startSleep(t *testing.T) *exec.Cmd {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	return cmd
}
