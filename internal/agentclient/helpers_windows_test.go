package agentclient

import (
	"os/exec"
	"testing"
)

func startSleep(t *testing.T) *exec.Cmd {
	cmd := exec.Command("ping", "-n", "60", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	return cmd
}
