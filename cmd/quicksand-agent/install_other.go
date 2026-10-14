//go:build !linux && !windows

package main

import (
	"errors"
	"runtime"
)

func install(installOptions) error {
	return errors.New("the installer doesn't support " + runtime.GOOS + "; see docs/agent.md to install by hand")
}
