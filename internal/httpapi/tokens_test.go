package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"quicksand/internal/auth"
	"quicksand/internal/config"
	"quicksand/internal/store"
)

// bearer is a client that signs in with an API token.
func bearer(token string) *http.Client {
	return &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		r.Header.Set("Authorization", "Bearer "+token)
		return http.DefaultTransport.RoundTrip(r)
	})}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTokens(t *testing.T) {
	e := newTestEnv(t)
	anon := &http.Client{}

	// qs signs in with a password and gets a token, not a session.
	var login struct {
		Token string
		User  struct{ Username string }
	}
	resp := doJSON(t, anon, "POST", e.srv.URL+"/api/auth/login", `{"username":"user","password":"user-password","token":"qs on laptop"}`, &login)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(login.Token, "qst_") || login.User.Username != "user" || len(resp.Cookies()) != 0 {
		t.Fatalf("login for a token: %s %+v, cookies %v", resp.Status, login, resp.Cookies())
	}
	qs := bearer(login.Token)
	if resp := do(t, qs, "GET", e.srv.URL+"/api/auth/me", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("token: %s", resp.Status)
	}
	if resp := do(t, bearer("qst_bogus"), "GET", e.srv.URL+"/api/auth/me", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bogus token: %s", resp.Status)
	}

	// Tokens made by hand, listed and revoked.
	web := e.client(t, "user", "user-password")
	var made struct {
		Token  store.Token
		Secret string
	}
	if resp := doJSON(t, web, "POST", e.srv.URL+"/api/tokens", `{"name":"ci","expiresInDays":30}`, &made); resp.StatusCode != http.StatusCreated || made.Token.ExpiresAt.IsZero() {
		t.Fatalf("create: %s %+v", resp.Status, made)
	}
	if resp := do(t, web, "POST", e.srv.URL+"/api/tokens", `{"name":"x","expiresInDays":9999}`, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("too long a lifetime: %s", resp.Status)
	}
	var list []store.Token
	doJSON(t, web, "GET", e.srv.URL+"/api/tokens", "", &list)
	if len(list) != 2 {
		t.Fatalf("tokens = %+v", list)
	}
	admin := e.client(t, "admin", "admin-password")
	if resp := do(t, admin, "DELETE", e.srv.URL+"/api/tokens/"+strconv.FormatInt(made.Token.ID, 10), "", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("revoking another user's token: %s", resp.Status)
	}
	if resp := do(t, web, "DELETE", e.srv.URL+"/api/tokens/"+strconv.FormatInt(made.Token.ID, 10), "", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: %s", resp.Status)
	}
	if resp := do(t, bearer(made.Secret), "GET", e.srv.URL+"/api/auth/me", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token: %s", resp.Status)
	}

	// "qs logout" revokes its token.
	if resp := do(t, qs, "POST", e.srv.URL+"/api/auth/logout", "", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %s", resp.Status)
	}
	if resp := do(t, qs, "GET", e.srv.URL+"/api/auth/me", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token after logout: %s", resp.Status)
	}

	// A password reset signs out tokens too.
	doJSON(t, anon, "POST", e.srv.URL+"/api/auth/login", `{"username":"user","password":"user-password","token":"qs"}`, &login)
	do(t, admin, "PATCH", e.srv.URL+"/api/admin/users/"+strconv.FormatInt(e.user.ID, 10), `{"password":"new-password"}`, nil)
	if resp := do(t, bearer(login.Token), "GET", e.srv.URL+"/api/auth/me", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token after a password reset: %s", resp.Status)
	}

	var v struct{ Version string }
	if doJSON(t, admin, "GET", e.srv.URL+"/api/version", "", &v); v.Version == "" {
		t.Fatal("no version")
	}
}

func TestAuditLog(t *testing.T) {
	e := newTestEnv(t)
	do(t, &http.Client{}, "POST", e.srv.URL+"/api/auth/login", `{"username":"user","password":"wrong"}`, nil)
	admin := e.client(t, "admin", "admin-password")
	do(t, admin, "POST", e.srv.URL+"/api/admin/users", `{"username":"carol","password":"carol-password","isAdmin":true}`, nil)
	do(t, admin, "PATCH", e.srv.URL+"/api/admin/users/"+strconv.FormatInt(e.user.ID, 10), `{"isAdmin":true}`, nil)
	do(t, admin, "DELETE", e.srv.URL+"/api/admin/users/"+strconv.FormatInt(e.admin.ID, 10), "", nil) // refused: not logged
	do(t, admin, "POST", e.srv.URL+"/api/tokens", `{"name":"ci"}`, nil)

	var page struct {
		Entries []store.AuditEntry
		More    bool
	}
	if resp := doJSON(t, admin, "GET", e.srv.URL+"/api/admin/audit", "", &page); resp.StatusCode != http.StatusOK {
		t.Fatalf("audit: %s", resp.Status)
	}
	var got []string
	for _, a := range page.Entries {
		got = append(got, strings.Join(strings.Fields(a.Username+" "+a.Action+" "+a.Target+" "+a.Detail), " "))
	}
	want := []string{
		"admin token.create ci",
		"admin user.update user made administrator",
		"admin user.create carol administrator",
		"admin login password",
		"user login.failed password",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("audit log:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	doJSON(t, admin, "GET", e.srv.URL+"/api/admin/audit?q=carol", "", &page)
	if len(page.Entries) != 1 {
		t.Fatalf("filtered: %+v", page.Entries)
	}
	if resp := do(t, e.client(t, "carol", "carol-password"), "GET", e.srv.URL+"/api/admin/audit", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("new admin reading the log: %s", resp.Status)
	}
	if resp := do(t, e.client(t, "user", "user-password"), "GET", e.srv.URL+"/api/admin/users", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("promoted user: %s", resp.Status)
	}
}

// TestAuditedStream checks that a websocket is logged when it opens, and
// not when the handler reports a failure through it.
func TestAuditedStream(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Server{cfg: &config.Config{}, store: db}
	opened := make(chan struct{})
	h := s.audited("agent.shell", func(w http.ResponseWriter, r *http.Request) {
		auditDetail(r, "interactive")
		if r.URL.Query().Get("fail") != "" {
			skipAudit(r)
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		<-opened
		c.Close(websocket.StatusNormalClosure, "")
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r.WithContext(auth.WithUser(r.Context(), store.User{ID: 7, Username: "alice"})))
	}))
	defer srv.Close()

	ctx := context.Background()
	for _, query := range []string{"", "?fail=1"} {
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/"+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		// Logged while the stream is still open.
		entries, _ := db.Audit(ctx, 0, 10, "")
		if query == "" && (len(entries) != 1 || entries[0].Username != "alice" || entries[0].Detail != "interactive") {
			t.Fatalf("while open: %+v", entries)
		}
		opened <- struct{}{}
		c.CloseNow()
	}
	if entries, _ := db.Audit(ctx, 0, 10, ""); len(entries) != 1 {
		t.Fatalf("a failed stream was logged: %+v", entries)
	}
}
