//go:build !windows && !linux

package agent

import (
	"context"

	"quicksand/internal/agentproto"
)

func setDisplay(ctx context.Context, width, height int) (agentproto.DisplayResult, error) {
	return agentproto.DisplayResult{}, errDisplayUnsupported
}

// RunDisplayHelper is only used on Windows.
func RunDisplayHelper(args []string) int { return 2 }
