//go:build !embedweb

// Package web holds the built web client. Build with -tags embedweb after
// running the Vite build to embed it into the server binary.
package web

import "io/fs"

// Dist returns nil: the web client is not embedded in this build.
func Dist() fs.FS { return nil }
