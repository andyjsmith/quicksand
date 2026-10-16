//go:build !linux && !windows

package agent

import (
	"context"
	"fmt"
)

func readClipboard(ctx context.Context) (string, error) {
	return "", fmt.Errorf("%w on this OS", errClipboardUnsupported)
}

func writeClipboard(ctx context.Context, text string) error {
	return fmt.Errorf("%w on this OS", errClipboardUnsupported)
}
