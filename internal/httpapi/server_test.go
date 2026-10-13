package httpapi

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"quicksand/internal/auth"
	"quicksand/internal/config"
	"quicksand/internal/pve"
	"quicksand/internal/store"
	"quicksand/internal/vms"
)

type testEnv struct {
	srv   *httptest.Server
	store *store.Store
	admin store.User
	user  store.User
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := &config.Config{
		PVE: config.PVE{URL: "https://127.0.0.1:1", TokenID: "t", TokenSecret: "s", InsecureSkipVerify: true},
	}
	client, err := pve.New(cfg.PVE)
	if err != nil {
		t.Fatal(err)
	}
	hub := vms.NewHub()
	srv := httptest.NewServer(New(Deps{
		Config: cfg, PVE: client, Store: db, Sessions: auth.NewSessions(db, false),
		VMs: vms.NewService(cfg, client, db, hub), Hub: hub,
	}))
	t.Cleanup(srv.Close)

	ctx := context.Background()
	admin, err := auth.CreateUser(ctx, db, "admin", "admin-password", true)
	if err != nil {
		t.Fatal(err)
	}
	user, err := auth.CreateUser(ctx, db, "user", "user-password", false)
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{srv: srv, store: db, admin: admin, user: user}
}

// client returns an HTTP client signed in as username (or anonymous if empty).
func (e *testEnv) client(t *testing.T, username, password string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if username != "" {
		resp := do(t, c, "POST", e.srv.URL+"/api/auth/login", `{"username":"`+username+`","password":"`+password+`"}`, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("login %s: %s", username, resp.Status)
		}
	}
	return c
}

func do(t *testing.T, c *http.Client, method, url, body string, header http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestAuthAndAccessControl(t *testing.T) {
	e := newTestEnv(t)
	anon := e.client(t, "", "")
	user := e.client(t, "user", "user-password")
	admin := e.client(t, "admin", "admin-password")

	// The admin owns a VM still deploying (so no Proxmox calls are needed).
	vm, err := e.store.CreateVM(context.Background(), store.VM{
		ID: "vm1", OwnerID: e.admin.ID, Name: "vm1", TemplateID: "t", Node: "n",
		Cores: 1, MemoryMiB: 512, NetworkID: "n", State: store.VMDeploying,
	}, 100, 100, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		client *http.Client
		method string
		path   string
		body   string
		header http.Header
		want   int
	}{
		{"wrong password", anon, "POST", "/api/auth/login", `{"username":"user","password":"nope"}`, nil, 401},
		{"unknown user", anon, "POST", "/api/auth/login", `{"username":"ghost","password":"nope"}`, nil, 401},
		{"anonymous me", anon, "GET", "/api/auth/me", "", nil, 401},
		{"anonymous vms", anon, "GET", "/api/vms", "", nil, 401},
		{"me", user, "GET", "/api/auth/me", "", nil, 200},
		{"user lists users", user, "GET", "/api/admin/users", "", nil, 403},
		{"admin lists users", admin, "GET", "/api/admin/users", "", nil, 200},
		{"user lists all vms", user, "GET", "/api/vms?all=1", "", nil, 403},
		{"user reads admin's vm", user, "GET", "/api/vms/" + vm.ID, "", nil, 404},
		{"user destroys admin's vm", user, "DELETE", "/api/vms/" + vm.ID, "", nil, 404},
		{"admin reads own vm", admin, "GET", "/api/vms/" + vm.ID, "", nil, 200},
		{"power on unready vm", admin, "POST", "/api/vms/" + vm.ID + "/power", `{"action":"start"}`, nil, 409},
		{"cross-origin post", admin, "POST", "/api/admin/users", `{"username":"x","password":"long-enough"}`,
			http.Header{"Origin": {"https://evil.example"}}, 403},
		{"short password", admin, "POST", "/api/admin/users", `{"username":"new","password":"short"}`, nil, 400},
		{"create user", admin, "POST", "/api/admin/users", `{"username":"new","password":"long-enough"}`, nil, 201},
		{"duplicate user", admin, "POST", "/api/admin/users", `{"username":"NEW","password":"long-enough"}`, nil, 409},
		{"delete self", admin, "DELETE", "/api/admin/users/" + strconv.FormatInt(e.admin.ID, 10), "", nil, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(t, tc.client, tc.method, e.srv.URL+tc.path, tc.body, tc.header)
			if resp.StatusCode != tc.want {
				t.Errorf("%s %s: got %s, want %d", tc.method, tc.path, resp.Status, tc.want)
			}
		})
	}

	// Logging out ends the session.
	do(t, user, "POST", e.srv.URL+"/api/auth/logout", "", nil)
	if resp := do(t, user, "GET", e.srv.URL+"/api/auth/me", "", nil); resp.StatusCode != 401 {
		t.Errorf("after logout: got %s, want 401", resp.Status)
	}
}
