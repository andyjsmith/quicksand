//go:build !linux && !windows

package agent

import (
	"context"
	"fmt"
	"runtime"
)

func findDesktop(ctx context.Context) (desktop, error) {
	return nil, fmt.Errorf("launching apps isn't supported on %s", runtime.GOOS)
}

func watchSessions(ctx context.Context, changed func()) {}
