package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"quicksand/internal/config"
	"quicksand/internal/store"
)

const (
	// ssoLoginTTL is how long a sign-in at the provider may take.
	ssoLoginTTL = 10 * time.Minute
	// cliCodeTTL is how long qs has to redeem the code it is handed.
	cliCodeTTL = 2 * time.Minute
	// maxPendingLogins bounds sign-ins in progress, which anyone can start.
	maxPendingLogins = 10_000
)

// ErrSSO is returned, wrapped, for sign-ins the user should be told about:
// refused accounts, username clashes and errors from the provider.
var ErrSSO = errors.New("single sign-on failed")

// SSO signs users in with an OpenID Connect provider, using the
// authorization code flow with PKCE.
type SSO struct {
	cfg    *config.OIDC
	store  *store.Store
	client *http.Client

	mu         sync.Mutex
	provider   *oidc.Provider // discovered on first use, so Quicksand starts while the provider is down
	endSession string
	pending    map[string]*PendingLogin // by state
	cliCodes   map[string]*cliCode      // by code
}

// PendingLogin is a sign-in waiting for the provider to redirect back.
type PendingLogin struct {
	verifier, nonce string
	expires         time.Time
	// Next is where the browser goes after signing in.
	Next string
	// CLIPort, if set, is the loopback port qs is waiting on; the sign-in
	// is handed to qs (see IssueCLICode) instead of the browser.
	CLIPort      int
	cliChallenge string
}

type cliCode struct {
	userID    int64
	idToken   string
	challenge string
	expires   time.Time
}

func NewSSO(cfg *config.OIDC, s *store.Store) *SSO {
	return &SSO{
		cfg: cfg, store: s,
		client:   &http.Client{Timeout: 15 * time.Second},
		pending:  map[string]*PendingLogin{},
		cliCodes: map[string]*cliCode{},
	}
}

// Name labels the sign-in button.
func (s *SSO) Name() string { return s.cfg.Name }

// PasswordsDisabled reports whether single sign-on is the only way in.
func (s *SSO) PasswordsDisabled() bool { return s.cfg.DisablePasswords }

// AdminFromGroups reports whether SSO users' administrator role follows
// their groups at the provider.
func (s *SSO) AdminFromGroups() bool { return len(s.cfg.AdminGroups) > 0 }

// clientContext carries the HTTP client for go-oidc and oauth2. It is never
// cancelled: the provider keeps it for fetching signing keys later.
func (s *SSO) clientContext() context.Context {
	return oidc.ClientContext(context.Background(), s.client)
}

// Discover fetches the provider's configuration, if it hasn't been yet.
func (s *SSO) Discover() (*oidc.Provider, error) {
	s.mu.Lock()
	p := s.provider
	s.mu.Unlock()
	if p != nil {
		return p, nil
	}
	p, err := oidc.NewProvider(s.clientContext(), s.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("%w: can't reach the identity provider: %v", ErrSSO, err)
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	p.Claims(&meta)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider == nil {
		s.provider, s.endSession = p, meta.EndSession
	}
	return s.provider, nil
}

func (s *SSO) oauth2Config(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     s.cfg.ClientID,
		ClientSecret: s.cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  s.cfg.RedirectURL,
		Scopes:       append([]string{oidc.ScopeOpenID}, s.cfg.Scopes...),
	}
}

// Begin starts a sign-in and returns the provider URL to send the browser
// to, and the state to bind to the browser. For qs, cliPort and
// cliChallenge (base64url SHA-256 of qs's verifier) are set.
func (s *SSO) Begin(next string, cliPort int, cliChallenge string) (authURL, state string, err error) {
	p, err := s.Discover()
	if err != nil {
		return "", "", err
	}
	login := &PendingLogin{
		verifier: oauth2.GenerateVerifier(), nonce: randomString(),
		expires: time.Now().Add(ssoLoginTTL),
		Next:    next, CLIPort: cliPort, cliChallenge: cliChallenge,
	}
	state = randomString()
	s.mu.Lock()
	s.sweep()
	if len(s.pending) >= maxPendingLogins {
		s.mu.Unlock()
		return "", "", fmt.Errorf("%w: too many sign-ins in progress; try again shortly", ErrSSO)
	}
	s.pending[state] = login
	s.mu.Unlock()
	return s.oauth2Config(p).AuthCodeURL(state, oidc.Nonce(login.nonce), oauth2.S256ChallengeOption(login.verifier)), state, nil
}

// Take removes and returns the sign-in a provider redirect is for.
func (s *SSO) Take(state string) (*PendingLogin, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	login, ok := s.pending[state]
	delete(s.pending, state)
	return login, ok && time.Now().Before(login.expires)
}

// Finish exchanges the provider's code and returns the signed-in user,
// created or updated from the ID token's claims, and the raw ID token.
func (s *SSO) Finish(ctx context.Context, login *PendingLogin, code string) (store.User, string, error) {
	p, err := s.Discover()
	if err != nil {
		return store.User{}, "", err
	}
	token, err := s.oauth2Config(p).Exchange(oidc.ClientContext(ctx, s.client), code, oauth2.VerifierOption(login.verifier))
	if err != nil {
		return store.User{}, "", fmt.Errorf("%w: the identity provider refused the sign-in: %v", ErrSSO, err)
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return store.User{}, "", fmt.Errorf("%w: the identity provider sent no ID token", ErrSSO)
	}
	idToken, err := p.Verifier(&oidc.Config{ClientID: s.cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		return store.User{}, "", fmt.Errorf("%w: invalid ID token: %v", ErrSSO, err)
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(login.nonce)) != 1 {
		return store.User{}, "", fmt.Errorf("%w: the ID token is for another sign-in", ErrSSO)
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return store.User{}, "", fmt.Errorf("%w: %v", ErrSSO, err)
	}
	username, _ := claims[s.cfg.UsernameClaim].(string)
	u, err := s.resolveUser(ctx, idToken.Subject, strings.TrimSpace(username), stringList(claims[s.cfg.GroupsClaim]))
	return u, raw, err
}

// resolveUser finds the user with subject, or creates (or links) one, and
// brings their name and, with admin groups, their role up to date.
func (s *SSO) resolveUser(ctx context.Context, subject, username string, groups []string) (store.User, error) {
	if username == "" {
		return store.User{}, fmt.Errorf("%w: the identity provider sent no %q claim to name you by", ErrSSO, s.cfg.UsernameClaim)
	}
	if !ValidUsername(username) {
		return store.User{}, fmt.Errorf("%w: your username %q isn't usable here (1-64 letters, digits, '.', '_', '-', '@' or '+')", ErrSSO, username)
	}
	admin := inGroups(groups, s.cfg.AdminGroups)
	if len(s.cfg.AllowedGroups) > 0 && !admin && !inGroups(groups, s.cfg.AllowedGroups) {
		return store.User{}, fmt.Errorf("%w: your account isn't in a group allowed to use Quicksand", ErrSSO)
	}

	u, err := s.store.UserBySubject(ctx, subject)
	switch {
	case err == nil:
		if u.Username != username {
			if err := s.store.SetUsername(ctx, u.ID, username); errors.Is(err, store.ErrConflict) {
				return store.User{}, fmt.Errorf("%w: another Quicksand user is already named %q", ErrSSO, username)
			} else if err != nil {
				return store.User{}, err
			}
		}
	case errors.Is(err, store.ErrNotFound):
		local, _, err := s.store.UserCredentials(ctx, username)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return s.store.CreateSSOUser(ctx, username, subject, s.AdminFromGroups() && admin)
		case err != nil:
			return store.User{}, err
		case local.SSO || !s.cfg.LinkLocalUsers:
			return store.User{}, fmt.Errorf("%w: a Quicksand user named %q already exists; ask an administrator", ErrSSO, username)
		}
		if err := s.store.LinkSubject(ctx, local.ID, subject); err != nil {
			return store.User{}, err
		}
		u = local
	default:
		return store.User{}, err
	}
	if s.AdminFromGroups() && u.IsAdmin != admin {
		if err := s.store.SetAdmin(ctx, u.ID, admin); err != nil {
			return store.User{}, err
		}
	}
	return s.store.User(ctx, u.ID)
}

// IssueCLICode hands a finished sign-in to qs: it redeems the returned code
// with RedeemCLICode and the verifier matching the login's challenge.
func (s *SSO) IssueCLICode(login *PendingLogin, userID int64, idToken string) string {
	code := randomString()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	s.cliCodes[code] = &cliCode{userID: userID, idToken: idToken, challenge: login.cliChallenge, expires: time.Now().Add(cliCodeTTL)}
	return code
}

// RedeemCLICode returns the user and ID token a qs code was issued for,
// once, if verifier matches the challenge qs started the sign-in with.
func (s *SSO) RedeemCLICode(code, verifier string) (userID int64, idToken string, ok bool) {
	s.mu.Lock()
	c, found := s.cliCodes[code]
	delete(s.cliCodes, code)
	s.mu.Unlock()
	if !found || time.Now().After(c.expires) {
		return 0, "", false
	}
	sum := sha256.Sum256([]byte(verifier))
	if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(c.challenge)) != 1 {
		return 0, "", false
	}
	return c.userID, c.idToken, true
}

// LogoutURL is where to send the browser to sign out at the provider too,
// or "" if it doesn't support that. The provider returns it to
// postLogoutURL, which must be registered there.
func (s *SSO) LogoutURL(idToken, postLogoutURL string) string {
	if _, err := s.Discover(); err != nil {
		return ""
	}
	s.mu.Lock()
	end := s.endSession
	s.mu.Unlock()
	if end == "" {
		return ""
	}
	q := url.Values{"client_id": {s.cfg.ClientID}, "post_logout_redirect_uri": {postLogoutURL}}
	if idToken != "" {
		q.Set("id_token_hint", idToken)
	}
	sep := "?"
	if strings.Contains(end, "?") {
		sep = "&"
	}
	return end + sep + q.Encode()
}

// sweep drops expired sign-ins and codes; s.mu must be held.
func (s *SSO) sweep() {
	now := time.Now()
	for k, v := range s.pending {
		if now.After(v.expires) {
			delete(s.pending, k)
		}
	}
	for k, v := range s.cliCodes {
		if now.After(v.expires) {
			delete(s.cliCodes, k)
		}
	}
}

// inGroups reports whether any of groups is one of want. Keycloak sends
// full group paths ("/admins"), so a leading "/" is ignored on both sides.
func inGroups(groups, want []string) bool {
	for _, g := range groups {
		if slices.ContainsFunc(want, func(w string) bool {
			return strings.TrimPrefix(w, "/") == strings.TrimPrefix(g, "/")
		}) {
			return true
		}
	}
	return false
}

// stringList reads a claim that is a list of strings, or a single string.
func stringList(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func randomString() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
