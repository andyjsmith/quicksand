package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"quicksand/internal/auth"
	"quicksand/internal/store"
	"quicksand/internal/version"
)

// maxTokenLifetime bounds the expiry of tokens created by hand.
const maxTokenLifetime = 5 * 365 * 24 * time.Hour

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.Tokens(r.Context(), auth.FromContext(r.Context()).ID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

// handleCreateToken makes an API token for the user and returns its
// secret, shown this once.
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		// ExpiresInDays is 0 for a token that expires only when unused.
		ExpiresInDays int `json:"expiresInDays"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	lifetime := time.Duration(req.ExpiresInDays) * 24 * time.Hour
	if req.ExpiresInDays < 0 || lifetime > maxTokenLifetime {
		writeError(w, http.StatusBadRequest, errors.New("expiresInDays must be 0 (none) to 1825"))
		return
	}
	var expires time.Time
	if lifetime > 0 {
		expires = time.Now().Add(lifetime)
	}
	t, secret, err := s.sessions.CreateToken(r.Context(), auth.FromContext(r.Context()).ID, req.Name, expires)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	auditTarget(r, t.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"token": t, "secret": secret})
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	u := auth.FromContext(r.Context())
	tokens, err := s.store.Tokens(r.Context(), u.ID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	for _, t := range tokens {
		if t.ID == id {
			auditTarget(r, t.Name)
		}
	}
	if err := s.store.DeleteToken(r.Context(), u.ID, id); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAudit returns a page of the audit log, newest first: entries
// older than "before" (an entry ID) that contain "q".
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	const page = 100
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	entries, err := s.store.Audit(r.Context(), before, page+1, q.Get("q"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	more := len(entries) > page
	if more {
		entries = entries[:page]
	}
	writeJSON(w, http.StatusOK, struct {
		Entries []store.AuditEntry `json:"entries"`
		More    bool               `json:"more"`
	}{entries, more})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": version.Version})
}
