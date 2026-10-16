//go:build !windows

package agent

// RunClipboardHelper is only used on Windows.
func RunClipboardHelper(args []string) int { return 2 }
