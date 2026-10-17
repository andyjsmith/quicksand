package httpapi

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"quicksand/internal/config"
)

// fakeProvider is a minimal OpenID Connect provider that signs in whoever
// claims says, at once.
type fakeProvider struct {
	*httptest.Server
	key *rsa.PrivateKey

	mu     sync.Mutex
	claims map[string]any
	codes  map[string]fakeCode
}

type fakeCode struct{ nonce, challenge string }

func newFakeProvider(t *testing.T) *fakeProvider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{key: key, codes: map[string]fakeCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.URL,
			"authorization_endpoint":                p.URL + "/auth",
			"token_endpoint":                        p.URL + "/token",
			"jwks_uri":                              p.URL + "/keys",
			"end_session_endpoint":                  p.URL + "/logout",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
		}})
	})
	mux.HandleFunc("GET /auth", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" {
			http.Error(w, "PKCE required", http.StatusBadRequest)
			return
		}
		code := q.Get("state") + "-code"
		p.mu.Lock()
		p.codes[code] = fakeCode{nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
		p.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		p.mu.Lock()
		c, ok := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		claims := map[string]any{}
		for k, v := range p.claims {
			claims[k] = v
		}
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		w.Header().Set("Content-Type", "application/json")
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		now := time.Now()
		claims["iss"], claims["aud"], claims["nonce"] = p.URL, "quicksand", c.nonce
		claims["iat"], claims["exp"] = now.Unix(), now.Add(time.Hour).Unix()
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access", "token_type": "Bearer", "expires_in": 3600,
			"id_token": p.sign(t, claims),
		})
	})
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

func (p *fakeProvider) sign(t *testing.T, claims map[string]any) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(claims)
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	s, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (p *fakeProvider) signInAs(claims map[string]any) {
	p.mu.Lock()
	p.claims = claims
	p.mu.Unlock()
}

// newSSOEnv is a test environment with single sign-on through a fake
// provider.
func newSSOEnv(t *testing.T, oidc config.OIDC) (*testEnv, *fakeProvider) {
	t.Helper()
	p := newFakeProvider(t)
	oidc.Issuer, oidc.ClientID, oidc.ClientSecret = p.URL, "quicksand", "secret"
	// The defaults config.Load fills in.
	oidc.Name, oidc.Scopes, oidc.UsernameClaim, oidc.GroupsClaim = "Single sign-on", []string{"profile", "email"}, "preferred_username", "groups"
	oidc.RedirectURL = "http://placeholder/api/auth/oidc/callback"
	cfg := &config.Config{OIDC: &oidc}
	e := newTestEnvWith(t, cfg)
	// The callback is on the test server, whose address is known only now;
	// the SSO configuration is read at each sign-in.
	oidc.RedirectURL = e.srv.URL + "/api/auth/oidc/callback"
	*cfg.OIDC = oidc
	return e, p
}

// ssoClient is a cookie-keeping client that stops at redirects leaving the
// sign-in flow: to Quicksand's pages, or to qs's loopback listener.
func ssoClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(r *http.Request, _ []*http.Request) error {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Host != "127.0.0.1:4242" || r.URL.Path == "/auth" {
			return nil
		}
		return http.ErrUseLastResponse
	}}
}

// ssoSignIn signs in through the provider and returns the final redirect.
func ssoSignIn(t *testing.T, e *testEnv, c *http.Client, query string) *http.Response {
	t.Helper()
	resp, err := c.Get(e.srv.URL + "/api/auth/oidc/login" + query)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("sign-in ended with %s, want a redirect", resp.Status)
	}
	return resp
}

func me(t *testing.T, e *testEnv, c *http.Client) (map[string]any, int) {
	t.Helper()
	var u map[string]any
	resp := doJSON(t, c, "GET", e.srv.URL+"/api/auth/me", "", &u)
	return u, resp.StatusCode
}

// ssoError returns the message a failed sign-in left for the sign-in page.
func ssoError(t *testing.T, e *testEnv, c *http.Client) string {
	u, _ := url.Parse(e.srv.URL)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == ssoErrorCookie {
			b, _ := base64.RawURLEncoding.DecodeString(ck.Value)
			return string(b)
		}
	}
	return ""
}

func TestSSOSignIn(t *testing.T) {
	e, p := newSSOEnv(t, config.OIDC{AdminGroups: []string{"qs-admins"}, AllowedGroups: []string{"qs-users"}})

	// First sign-in creates the user; Keycloak-style group paths match.
	p.signInAs(map[string]any{"sub": "s-alice", "preferred_username": "alice@example.com", "groups": []string{"/qs-admins"}})
	c := ssoClient()
	if resp := ssoSignIn(t, e, c, "?next=/vm/x"); resp.Header.Get("Location") != "/vm/x" {
		t.Fatalf("redirected to %q, want /vm/x", resp.Header.Get("Location"))
	}
	u, status := me(t, e, c)
	if status != http.StatusOK || u["username"] != "alice@example.com" || u["isAdmin"] != true || u["sso"] != true || u["hasPassword"] != false {
		t.Fatalf("me = %d %v", status, u)
	}

	// Leaving the admin group, and a rename, apply at the next sign-in.
	p.signInAs(map[string]any{"sub": "s-alice", "preferred_username": "alice", "groups": []string{"qs-users"}})
	c = ssoClient()
	ssoSignIn(t, e, c, "")
	if u, _ := me(t, e, c); u["username"] != "alice" || u["isAdmin"] != false {
		t.Fatalf("after rename and demotion, me = %v", u)
	}

	// The admin role of SSO users can't be changed by hand.
	admin := e.client(t, "admin", "admin-password")
	resp := do(t, admin, "PATCH", e.srv.URL+"/api/admin/users/"+jsonNumber(u["id"]), `{"isAdmin":true}`, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("making an SSO user admin by hand: %s, want 400", resp.Status)
	}

	// Outside the allowed groups: refused, with a reason for the sign-in page.
	p.signInAs(map[string]any{"sub": "s-bob", "preferred_username": "bob", "groups": []string{"others"}})
	c = ssoClient()
	if resp := ssoSignIn(t, e, c, ""); resp.Header.Get("Location") != "/login" {
		t.Fatalf("refused sign-in redirected to %q", resp.Header.Get("Location"))
	}
	if _, status := me(t, e, c); status != http.StatusUnauthorized {
		t.Fatalf("refused user is signed in: %d", status)
	}
	if msg := ssoError(t, e, c); !strings.Contains(msg, "isn't in a group allowed") {
		t.Fatalf("error = %q", msg)
	}

	// A password user's name isn't taken over without linkLocalUsers.
	p.signInAs(map[string]any{"sub": "s-user", "preferred_username": "user", "groups": []string{"qs-users"}})
	c = ssoClient()
	ssoSignIn(t, e, c, "")
	if msg := ssoError(t, e, c); !strings.Contains(msg, "already exists") {
		t.Fatalf("name clash error = %q", msg)
	}

	// Signing out returns the provider's sign-out URL.
	c = ssoClient()
	p.signInAs(map[string]any{"sub": "s-alice", "preferred_username": "alice", "groups": []string{"qs-users"}})
	ssoSignIn(t, e, c, "")
	var out struct{ Redirect string }
	doJSON(t, c, "POST", e.srv.URL+"/api/auth/logout", "", &out)
	if !strings.HasPrefix(out.Redirect, p.URL+"/logout?") || !strings.Contains(out.Redirect, "id_token_hint=") {
		t.Fatalf("logout redirect = %q", out.Redirect)
	}
	if _, status := me(t, e, c); status != http.StatusUnauthorized {
		t.Fatalf("still signed in after logout: %d", status)
	}

	// Passwords still work alongside single sign-on.
	if resp := do(t, ssoClient(), "POST", e.srv.URL+"/api/auth/login", `{"username":"user","password":"user-password"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("password login with SSO enabled: %s", resp.Status)
	}
}

func TestSSOLinkLocalUsersAndDisablePasswords(t *testing.T) {
	e, p := newSSOEnv(t, config.OIDC{LinkLocalUsers: true, DisablePasswords: true})
	p.signInAs(map[string]any{"sub": "s-user", "preferred_username": "user"})
	c := ssoClient()
	ssoSignIn(t, e, c, "")
	u, _ := me(t, e, c)
	if u["username"] != "user" || u["sso"] != true || jsonNumber(u["id"]) != jsonNumber(float64(e.user.ID)) {
		t.Fatalf("linked user = %v", u)
	}
	resp := do(t, ssoClient(), "POST", e.srv.URL+"/api/auth/login", `{"username":"admin","password":"admin-password"}`, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("password login with passwords disabled: %s", resp.Status)
	}
	var methods struct {
		Password bool
		SSO      *struct{ Name string }
	}
	doJSON(t, ssoClient(), "GET", e.srv.URL+"/api/auth/methods", "", &methods)
	if methods.Password || methods.SSO == nil || methods.SSO.Name != "Single sign-on" {
		t.Fatalf("methods = %+v", methods)
	}
}

func TestSSOStateBoundToBrowser(t *testing.T) {
	e, p := newSSOEnv(t, config.OIDC{})
	p.signInAs(map[string]any{"sub": "s-eve", "preferred_username": "eve"})
	// Start a sign-in in one browser, finish it in another.
	start := ssoClient()
	start.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp := do(t, start, "GET", e.srv.URL+"/api/auth/oidc/login", "", nil)
	other := ssoClient()
	resp, err := other.Get(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if _, status := me(t, e, other); status != http.StatusUnauthorized {
		t.Fatal("a sign-in finished in another browser signed it in")
	}
	if msg := ssoError(t, e, other); !strings.Contains(msg, "wasn't started in this browser") {
		t.Fatalf("error = %q", msg)
	}
}

func TestSSOForCLI(t *testing.T) {
	e, p := newSSOEnv(t, config.OIDC{})
	p.signInAs(map[string]any{"sub": "s-carol", "preferred_username": "carol"})
	verifier := "a-verifier-only-qs-knows-0123456789abcdefghijk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	browser := ssoClient()
	resp := ssoSignIn(t, e, browser, "?cli_port=4242&cli_challenge="+challenge)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Host != "127.0.0.1:4242" || loc.Path != "/callback" || loc.Query().Get("code") == "" {
		t.Fatalf("handed to %q", loc)
	}
	if _, status := me(t, e, browser); status != http.StatusUnauthorized {
		t.Fatal("a sign-in for qs signed the browser in")
	}
	code := loc.Query().Get("code")

	qs := &http.Client{}
	if resp := do(t, qs, "POST", e.srv.URL+"/api/auth/cli/token", `{"code":"`+code+`","verifier":"wrong","name":"qs"}`, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong verifier: %s", resp.Status)
	}
	// The failed attempt used the code up.
	resp = do(t, qs, "POST", e.srv.URL+"/api/auth/cli/token", `{"code":"`+code+`","verifier":"`+verifier+`","name":"qs"}`, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused code: %s", resp.Status)
	}

	resp = ssoSignIn(t, e, ssoClient(), "?cli_port=4242&cli_challenge="+challenge)
	loc, _ = url.Parse(resp.Header.Get("Location"))
	var out struct {
		Token string
		User  struct{ Username string }
	}
	resp = doJSON(t, qs, "POST", e.srv.URL+"/api/auth/cli/token", `{"code":"`+loc.Query().Get("code")+`","verifier":"`+verifier+`","name":"qs on laptop"}`, &out)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(out.Token, "qst_") || out.User.Username != "carol" {
		t.Fatalf("token: %s %+v", resp.Status, out)
	}
	if resp := do(t, bearer(out.Token), "GET", e.srv.URL+"/api/auth/me", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("qs token: %s", resp.Status)
	}
}

// doJSON is do, decoding the JSON response into out.
func doJSON(t *testing.T, c *http.Client, method, url, body string, out any) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(out)
	return resp
}

func jsonNumber(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
