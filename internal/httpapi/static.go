package httpapi

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
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

// devWebProxy forwards requests, including Vite's hot reload websocket, to
// a web client dev server.
func devWebProxy(target *url.URL) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("web dev server unreachable", "url", target, "err", err)
			http.Error(w, fmt.Sprintf("The web dev server at %s isn't reachable; start it with \"npm run dev\" in web/.", target), http.StatusBadGateway)
		},
	}
}
