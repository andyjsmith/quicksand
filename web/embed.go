//go:build embedweb

// Package web holds the built web client. Build with -tags embedweb after
// running the Vite build to embed it into the server binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built web client.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
