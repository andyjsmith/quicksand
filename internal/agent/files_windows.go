//go:build windows

package agent

import (
	"os"

	"golang.org/x/sys/windows"

	"quicksand/internal/agentproto"
)

// The agent runs as SYSTEM, whose home directory is no use to anyone.
func defaultFilesDir() string {
	if public := os.Getenv("PUBLIC"); public != "" {
		return public
	}
	return `C:\Users\Public`
}

// "/" stands for the list of drives above the drive roots.
func isDriveList(p string) bool { return p == "/" || p == `\` }
func driveListParent() string   { return "/" }

func driveEntries() []agentproto.FileEntry {
	out := []agentproto.FileEntry{}
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return out
	}
	for i := range 26 {
		if mask&(1<<i) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		e := agentproto.FileEntry{Name: root[:2], Path: root, Dir: true}
		if _, err := os.Stat(root); err != nil {
			continue // e.g. an empty optical drive
		}
		out = append(out, e)
	}
	return out
}
