//go:build !windows

package main

import "context"

// runAsService only applies on Windows.
func runAsService(func(ctx context.Context) error) (bool, error) { return false, nil }

func isService() bool { return false }
