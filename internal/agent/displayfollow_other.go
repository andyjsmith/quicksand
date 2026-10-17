//go:build !linux

package agent

import "context"

// followDisplay is only needed on Linux desktops other than GNOME.
func followDisplay(ctx context.Context) {}
