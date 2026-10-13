package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"quicksand/internal/auth"
	"quicksand/internal/store"
)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decodeJSON(w, r, &c) {
		return
	}
	u, err := s.sessions.Login(w, r, strings.TrimSpace(c.Username), c.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Logout(w, r); err != nil {
		writeServiceError(w, err)
		return
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
	// Other sessions are signed out; this one signs in again.
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

// setPassword changes a password and signs the user out everywhere.
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
