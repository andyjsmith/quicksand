//go:build !windows

package agent

import "quicksand/internal/agentproto"

// defaultFilesDir is where files and shells start when the server doesn't
// say (the template's filesDir): the root of the filesystem.
func defaultFilesDir() string { return "/" }

func isDriveList(string) bool              { return false }
func driveListParent() string              { return "" }
func driveEntries() []agentproto.FileEntry { return nil }
