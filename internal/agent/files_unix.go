//go:build !windows

package agent

import (
	"os"

	"quicksand/internal/agentproto"
)

func defaultFilesDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

func isDriveList(string) bool              { return false }
func driveListParent() string              { return "" }
func driveEntries() []agentproto.FileEntry { return nil }
