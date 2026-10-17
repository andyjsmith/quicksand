package httpapi

import (
	"io"
	"net/http"
	"testing"

	"quicksand/internal/config"
)

func TestTemplateIcon(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	e := newTestEnvWith(t, &config.Config{Templates: []config.Template{
		{ID: "edge", IconData: svg, IconType: "image/svg+xml"},
		{ID: "plain"},
	}})
	user := e.client(t, "user", "user-password")

	// do closes the body, so fetch this one here.
	resp, err := user.Get(e.srv.URL + "/api/templates/edge/icon?v=x")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != string(svg) {
		t.Fatalf("icon: %s %q", resp.Status, body)
	}
	for k, want := range map[string]string{
		"Content-Type":            "image/svg+xml",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; sandbox",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	for path, want := range map[string]int{
		"/api/templates/plain/icon":   http.StatusNotFound,
		"/api/templates/missing/icon": http.StatusNotFound,
	} {
		if resp := do(t, user, "GET", e.srv.URL+path, "", nil); resp.StatusCode != want {
			t.Errorf("%s: %s, want %d", path, resp.Status, want)
		}
	}
	if resp := do(t, e.client(t, "", ""), "GET", e.srv.URL+"/api/templates/edge/icon", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("signed out: %s", resp.Status)
	}
}
