package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves the built web client, falling back to index.html for
// client-side routes.
func spaHandler(static fs.FS) http.Handler {
	files := http.FileServerFS(static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "."
		}
		if _, err := fs.Stat(static, name); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}
