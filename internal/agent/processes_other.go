//go:build !linux && !windows

package agent

import (
	"errors"
	"os"

	"quicksand/internal/agentproto"
)

func listProcesses() ([]agentproto.Process, error) {
	return nil, errors.New("listing processes isn't supported on this OS")
}

func memoryTotal() uint64 { return 0 }

func killProcess(pid int, force bool) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
