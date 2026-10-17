package httpapi

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"quicksand/internal/agent"
	"quicksand/internal/agentclient"
	"quicksand/internal/agentproto"
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
	return newTestEnvWithAgents(t, nil)
}

func newTestEnvWithAgents(t *testing.T, agents *agentclient.Client) *testEnv {
	return newTestEnvFull(t, &config.Config{}, agents)
}

// newTestEnvWith uses cfg, with a placeholder Proxmox.
func newTestEnvWith(t *testing.T, cfg *config.Config) *testEnv {
	return newTestEnvFull(t, cfg, nil)
}

func newTestEnvFull(t *testing.T, cfg *config.Config, agents *agentclient.Client) *testEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cfg.PVE = config.PVE{URL: "https://127.0.0.1:1", TokenID: "t", TokenSecret: "s", InsecureSkipVerify: true}
	client, err := pve.New(cfg.PVE)
	if err != nil {
		t.Fatal(err)
	}
	var sso *auth.SSO
	if cfg.OIDC != nil {
		sso = auth.NewSSO(cfg.OIDC, db)
	}
	hub := vms.NewHub()
	srv := httptest.NewServer(New(Deps{
		Config: cfg, PVE: client, Store: db, Sessions: auth.NewSessions(db, false), SSO: sso,
		VMs: vms.NewService(cfg, client, db, hub, agents), Hub: hub,
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
		{"user reads agent settings", user, "GET", "/api/admin/agent", "", nil, 403},
		{"admin reads agent settings", admin, "GET", "/api/admin/agent", "", nil, 200},
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

func TestAgentExecThroughAPI(t *testing.T) {
	// A real agent, trusting a fresh server key.
	key, _ := agentproto.GenerateKey()
	pub := key.Public().(ed25519.PublicKey)
	a, err := agent.New(agent.Config{AuthorizedKeys: []string{agentproto.FormatPublicKey(pub)}})
	if err != nil {
		t.Fatal(err)
	}
	agentSrv := httptest.NewUnstartedServer(a.Handler())
	agentSrv.TLS, _ = a.TLSConfig()
	agentSrv.StartTLS()
	t.Cleanup(agentSrv.Close)
	agents, err := agentclient.New(agentclient.Options{Key: key})
	if err != nil {
		t.Fatal(err)
	}

	e := newTestEnvWithAgents(t, agents)
	ctx := context.Background()
	vm, err := e.store.CreateVM(ctx, store.VM{
		ID: "vm1", OwnerID: e.user.ID, Name: "vm1", TemplateID: "t", Node: "n",
		Cores: 1, MemoryMiB: 512, NetworkID: "n", State: store.VMReady,
		AgentIP: strings.TrimPrefix(agentSrv.URL, "https://"),
	}, 100, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	user := e.client(t, "user", "user-password")

	req, _ := http.NewRequest("POST", e.srv.URL+"/api/vms/"+vm.ID+"/exec", strings.NewReader(`{"shell":"echo hi; exit 2"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := user.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res agentproto.ExecResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil || resp.StatusCode != 200 {
		t.Fatalf("exec: %s, %v", resp.Status, err)
	}
	if res.ExitCode != 2 || strings.TrimSpace(string(res.Stdout)) != "hi" {
		t.Errorf("exec result = %+v", res)
	}

	if r := do(t, user, "GET", e.srv.URL+"/api/vms/"+vm.ID+"/agent", "", nil); r.StatusCode != 200 {
		t.Errorf("agent info: %s", r.Status)
	}
	if r := do(t, user, "POST", e.srv.URL+"/api/vms/"+vm.ID+"/exec", `{}`, nil); r.StatusCode != 400 {
		t.Errorf("empty exec: %s, want 400", r.Status)
	}
	// Another user can't reach this VM's agent.
	admin := e.client(t, "admin", "admin-password")
	if _, err := auth.CreateUser(ctx, e.store, "other", "other-password", false); err != nil {
		t.Fatal(err)
	}
	stranger := e.client(t, "other", "other-password")
	if r := do(t, stranger, "POST", e.srv.URL+"/api/vms/"+vm.ID+"/exec", `{"shell":"id"}`, nil); r.StatusCode != 404 {
		t.Errorf("stranger exec: %s, want 404", r.Status)
	}
	if r := do(t, admin, "POST", e.srv.URL+"/api/vms/"+vm.ID+"/exec", `{"shell":"true"}`, nil); r.StatusCode != 200 {
		t.Errorf("admin exec: %s, want 200", r.Status)
	}
}
