package httpapi

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"quicksand/internal/auth"
)

const (
	// ssoStateCookie binds a sign-in at the provider to the browser that
	// started it. Lax, as the provider's redirect back is cross-site.
	ssoStateCookie = "quicksand_sso"
	// ssoErrorCookie carries a failed sign-in's message to the sign-in page,
	// which reads and clears it. A cookie, unlike a query parameter, can't be
	// set by a link from elsewhere.
	ssoErrorCookie = "quicksand_sso_error"
)

// handleAuthMethods tells sign-in pages and qs how users can sign in.
func (s *Server) handleAuthMethods(w http.ResponseWriter, r *http.Request) {
	type sso struct {
		Name string `json:"name"`
		// AdminFromGroups is set when SSO users' administrator role follows
		// their groups at the provider.
		AdminFromGroups bool `json:"adminFromGroups"`
	}
	resp := struct {
		Password bool `json:"password"`
		SSO      *sso `json:"sso"`
	}{Password: s.passwordsEnabled()}
	if s.sso != nil {
		resp.SSO = &sso{Name: s.sso.Name(), AdminFromGroups: s.sso.AdminFromGroups()}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) passwordsEnabled() bool {
	return s.sso == nil || !s.sso.PasswordsDisabled()
}

// handleSSOLogin sends the browser to the provider. With cli_port and
// cli_challenge (from qs), the finished sign-in is handed to qs's loopback
// listener instead of signing the browser in.
func (s *Server) handleSSOLogin(w http.ResponseWriter, r *http.Request) {
	if s.sso == nil {
		writeError(w, http.StatusNotFound, errors.New("single sign-on isn't configured"))
		return
	}
	q := r.URL.Query()
	var port int
	challenge := q.Get("cli_challenge")
	if p := q.Get("cli_port"); p != "" {
		var err error
		port, err = strconv.Atoi(p)
		if err != nil || port < 1024 || port > 65535 || len(challenge) != 43 {
			writeError(w, http.StatusBadRequest, errors.New("invalid cli_port or cli_challenge"))
			return
		}
	}
	authURL, state, err := s.sso.Begin(localPath(q.Get("next")), port, challenge)
	if err != nil {
		s.ssoFailed(w, r, port, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: ssoStateCookie, Value: state, Path: "/api/auth/oidc/", MaxAge: 600,
		HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleSSOCallback is where the provider sends the browser back.
func (s *Server) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	if s.sso == nil {
		writeError(w, http.StatusNotFound, errors.New("single sign-on isn't configured"))
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	c, err := r.Cookie(ssoStateCookie)
	http.SetCookie(w, &http.Cookie{
		Name: ssoStateCookie, Value: "", Path: "/api/auth/oidc/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
	if err != nil || state == "" || c.Value != state {
		s.ssoFailed(w, r, 0, fmt.Errorf("%w: this sign-in wasn't started in this browser, or took too long; try again", auth.ErrSSO))
		return
	}
	login, ok := s.sso.Take(state)
	if !ok {
		s.ssoFailed(w, r, 0, fmt.Errorf("%w: the sign-in took too long; try again", auth.ErrSSO))
		return
	}
	if e := q.Get("error"); e != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = e
		}
		s.ssoFailed(w, r, login.CLIPort, fmt.Errorf("%w: %s", auth.ErrSSO, msg))
		return
	}
	u, idToken, err := s.sso.Finish(r.Context(), login, q.Get("code"))
	if err != nil {
		s.ssoFailed(w, r, login.CLIPort, err)
		return
	}
	slog.Info("single sign-on", "user", u.Username, "cli", login.CLIPort != 0)
	if login.CLIPort != 0 {
		code := s.sso.IssueCLICode(login, u.ID, idToken)
		http.Redirect(w, r, cliCallback(login.CLIPort, url.Values{"code": {code}}), http.StatusFound)
		return
	}
	if err := s.sessions.Start(w, r, u.ID, idToken); err != nil {
		s.ssoFailed(w, r, 0, err)
		return
	}
	next := login.Next
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

// handleCLIToken trades the code qs was handed, and the verifier only qs
// knows, for a session.
func (s *Server) handleCLIToken(w http.ResponseWriter, r *http.Request) {
	if s.sso == nil {
		writeError(w, http.StatusNotFound, errors.New("single sign-on isn't configured"))
		return
	}
	var req struct {
		Code     string `json:"code"`
		Verifier string `json:"verifier"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	userID, idToken, ok := s.sso.RedeemCLICode(req.Code, req.Verifier)
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("invalid or expired sign-in code"))
		return
	}
	session, _, err := s.sessions.Create(r.Context(), userID, idToken)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	u, err := s.store.User(r.Context(), userID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session, "user": u})
}

// ssoFailed reports a failed sign-in: to qs if it started it, otherwise on
// the sign-in page.
func (s *Server) ssoFailed(w http.ResponseWriter, r *http.Request, cliPort int, err error) {
	msg := "Single sign-on failed."
	if errors.Is(err, auth.ErrSSO) {
		msg = strings.TrimPrefix(err.Error(), auth.ErrSSO.Error()+": ")
		slog.Warn("single sign-on failed", "err", err)
	} else {
		slog.Error("single sign-on failed", "err", err)
	}
	if cliPort != 0 {
		http.Redirect(w, r, cliCallback(cliPort, url.Values{"error": {msg}}), http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: ssoErrorCookie, Value: base64.RawURLEncoding.EncodeToString([]byte(msg)), Path: "/", MaxAge: 60,
		Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func cliCallback(port int, q url.Values) string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback?%s", port, q.Encode())
}

// localPath returns p if it is a path on this site, else "".
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return ""
	}
	return p
}

// postLogoutURL is where the provider sends the browser after signing out:
// the sign-in page, at the address Quicksand is registered with.
func (s *Server) postLogoutURL() string {
	u, _ := url.Parse(s.cfg.OIDC.RedirectURL)
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/login"}).String()
}
