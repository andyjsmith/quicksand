package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"quicksand/internal/auth"
	"quicksand/internal/config"
	"quicksand/internal/pve"
	"quicksand/internal/store"
	"quicksand/internal/vms"
)

func TestDevWebProxy(t *testing.T) {
	// A stand-in for Vite: pages, and a websocket like its hot reload.
	vite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"vite-hmr"}})
			if err != nil {
				return
			}
			defer c.CloseNow()
			c.Write(r.Context(), websocket.MessageText, []byte(`{"type":"connected"}`))
			return
		}
		io.WriteString(w, "vite page "+r.URL.Path)
	}))
	defer vite.Close()
	target, _ := url.Parse(vite.URL)

	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := &config.Config{PVE: config.PVE{URL: "https://127.0.0.1:1", TokenID: "t", TokenSecret: "s"}}
	client, _ := pve.New(cfg.PVE)
	hub := vms.NewHub()
	srv := httptest.NewServer(New(Deps{
		Config: cfg, PVE: client, Store: db, Sessions: auth.NewSessions(db, false),
		VMs: vms.NewService(cfg, client, db, hub, nil), Hub: hub, DevWeb: target,
	}))
	defer srv.Close()

	get := func(path string) (int, string) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if status, body := get("/login"); status != http.StatusOK || body != "vite page /login" {
		t.Fatalf("/login = %d %q, want Vite's page", status, body)
	}
	if status, body := get("/api/auth/me"); status != http.StatusUnauthorized || strings.Contains(body, "vite") {
		t.Fatalf("/api/auth/me = %d %q, want the API's 401", status, body)
	}

	ctx := context.Background()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/", &websocket.DialOptions{Subprotocols: []string{"vite-hmr"}})
	if err != nil {
		t.Fatalf("hot reload websocket through the proxy: %v", err)
	}
	defer c.CloseNow()
	if _, msg, err := c.Read(ctx); err != nil || string(msg) != `{"type":"connected"}` {
		t.Fatalf("websocket read = %q, %v", msg, err)
	}

	vite.Close()
	if status, body := get("/"); status != http.StatusBadGateway || !strings.Contains(body, "isn't reachable") {
		t.Fatalf("with Vite down, / = %d %q", status, body)
	}
}
