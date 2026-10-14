//go:build !linux && !windows

package agent

import (
	"errors"

	"quicksand/internal/agentproto"
)

func listConnections() ([]agentproto.Connection, error) {
	return nil, errors.New("listing connections isn't supported on this OS")
}
