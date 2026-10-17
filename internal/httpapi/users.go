package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"quicksand/internal/auth"
	"quicksand/internal/store"
)

var errPasswordsDisabled = errors.New("password sign-in is turned off; use single sign-on")

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.passwordsEnabled() {
		writeError(w, http.StatusForbidden, errPasswordsDisabled)
		return
	}
	var req struct {
		credentials
		// Token, if set, names an API token to create (and return) instead
		// of starting a session, as qs does.
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	username := strings.TrimSpace(req.Username)
	u, err := s.sessions.CheckPassword(r.Context(), username, req.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		s.audit(r, store.AuditEntry{Username: username, Action: "login.failed", Detail: "password"})
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if req.Token != "" {
		t, secret, err := s.sessions.CreateToken(r.Context(), u.ID, req.Token, time.Time{})
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		s.audit(r, store.AuditEntry{UserID: u.ID, Username: u.Username, Action: "login", Detail: "password, token " + t.Name})
		writeJSON(w, http.StatusOK, map[string]any{"user": u, "token": secret})
		return
	}
	if err := s.sessions.Start(w, r, u.ID, ""); err != nil {
		writeServiceError(w, err)
		return
	}
	s.audit(r, store.AuditEntry{UserID: u.ID, Username: u.Username, Action: "login", Detail: "password"})
	writeJSON(w, http.StatusOK, u)
}

// handleLogout ends the session. For single sign-on sessions it returns
// where to send the browser to sign out at the provider too.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	u, tokenID, signedIn := s.sessions.Authenticate(r)
	idToken, err := s.sessions.Logout(w, r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if signedIn {
		detail := ""
		if tokenID != 0 {
			detail = "revoked its token"
		}
		s.audit(r, store.AuditEntry{UserID: u.ID, Username: u.Username, Action: "logout", Detail: detail})
	}
	if s.sso != nil && idToken != "" {
		if u := s.sso.LogoutURL(idToken, s.postLogoutURL()); u != "" {
			writeJSON(w, http.StatusOK, map[string]string{"redirect": u})
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, auth.FromContext(r.Context()))
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	u := auth.FromContext(r.Context())
	if !s.passwordsEnabled() {
		writeError(w, http.StatusForbidden, errPasswordsDisabled)
		return
	}
	if !u.HasPassword {
		writeError(w, http.StatusBadRequest, errors.New("you sign in with single sign-on and have no password"))
		return
	}
	_, hash, err := s.store.UserCredentials(r.Context(), u.Username)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if !auth.CheckPassword(hash, req.Current) {
		writeError(w, http.StatusForbidden, errors.New("current password is incorrect"))
		return
	}
	if err := s.setPassword(r, u.ID, req.New); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// Other sessions and tokens are signed out; this one signs in again.
	if _, err := s.sessions.Login(w, r, u.Username, req.New); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.Users(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		credentials
		IsAdmin bool `json:"isAdmin"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.passwordsEnabled() {
		writeError(w, http.StatusBadRequest, errors.New("password sign-in is turned off; users are added when they first sign in"))
		return
	}
	auditTarget(r, strings.TrimSpace(req.Username))
	if req.IsAdmin {
		auditDetail(r, "administrator")
	}
	u, err := auth.CreateUser(r.Context(), s.store, req.Username, req.Password, req.IsAdmin)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, errors.New("that username is taken"))
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if !ok {
		return
	}
	var req struct {
		Password *string `json:"password"`
		IsAdmin  *bool   `json:"isAdmin"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	target, err := s.store.User(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if req.IsAdmin != nil && target.SSO && s.sso != nil && s.sso.AdminFromGroups() {
		writeError(w, http.StatusBadRequest, errors.New("single sign-on users' administrator role follows their groups at the identity provider"))
		return
	}
	if req.Password != nil && !s.passwordsEnabled() {
		writeError(w, http.StatusBadRequest, errPasswordsDisabled)
		return
	}
	var changes []string
	switch {
	case req.IsAdmin != nil && *req.IsAdmin:
		changes = append(changes, "made administrator")
	case req.IsAdmin != nil:
		changes = append(changes, "removed administrator")
	}
	if req.Password != nil {
		changes = append(changes, "set password")
	}
	auditDetail(r, "%s", strings.Join(changes, ", "))
	if req.IsAdmin != nil {
		if !*req.IsAdmin && id == auth.FromContext(r.Context()).ID {
			writeError(w, http.StatusBadRequest, errors.New("you can't remove your own administrator role"))
			return
		}
		if err := s.store.SetAdmin(r.Context(), id, *req.IsAdmin); err != nil {
			writeServiceError(w, err)
			return
		}
	}
	if req.Password != nil {
		if err := s.setPassword(r, id, *req.Password); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	u, err := s.store.User(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if !ok {
		return
	}
	if id == auth.FromContext(r.Context()).ID {
		writeError(w, http.StatusBadRequest, errors.New("you can't delete your own account"))
		return
	}
	err := s.store.DeleteUser(r.Context(), id)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, errors.New("the user still has VMs; destroy them first"))
		return
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setPassword changes a password and signs the user out everywhere,
// revoking their API tokens too.
func (s *Server) setPassword(r *http.Request, id int64, password string) error {
	if err := auth.ValidatePassword(password); err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.store.SetPassword(r.Context(), id, hash); err != nil {
		return err
	}
	if err := s.store.DeleteUserTokens(r.Context(), id); err != nil {
		return err
	}
	return s.store.DeleteUserSessions(r.Context(), id)
}

func userID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid user id"))
		return 0, false
	}
	return id, true
}
