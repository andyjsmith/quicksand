// Package httpapi exposes Quicksand's REST, event stream and websocket API.
package httpapi

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"quicksand/internal/auth"
	"quicksand/internal/config"
	"quicksand/internal/console"
	"quicksand/internal/pve"
	"quicksand/internal/store"
	"quicksand/internal/vms"
)

type Server struct {
	cfg      *config.Config
	pve      *pve.Client
	store    *store.Store
	sessions *auth.Sessions
	vms      *vms.Service
	hub      *vms.Hub
	consoles *console.Manager
	mux      *http.ServeMux

	agentPublicKey string
}

type Deps struct {
	Config   *config.Config
	PVE      *pve.Client
	Store    *store.Store
	Sessions *auth.Sessions
	VMs      *vms.Service
	Hub      *vms.Hub
	// Static, if non-nil, is the built web client served for non-API paths.
	Static fs.FS
	// AgentPublicKey is the key agents must trust; empty without an agent.
	AgentPublicKey string
}

func New(d Deps) *Server {
	s := &Server{
		cfg: d.Config, pve: d.PVE, store: d.Store, sessions: d.Sessions, vms: d.VMs, hub: d.Hub,
		agentPublicKey: d.AgentPublicKey,
		// Proxmox console tickets are valid for about 30 seconds; new
		// connections for a session are refused after that.
		consoles: console.NewManager(30 * time.Second),
		mux:      http.NewServeMux(),
	}
	user := func(h http.HandlerFunc) http.Handler { return s.requireUser(h) }
	admin := func(h http.HandlerFunc) http.Handler { return s.requireUser(s.requireAdmin(h)) }

	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	s.mux.Handle("GET /api/auth/me", user(s.handleMe))
	s.mux.Handle("POST /api/auth/password", user(s.handleChangePassword))

	s.mux.Handle("GET /api/templates", user(s.handleTemplates))
	s.mux.Handle("GET /api/networks", user(s.handleNetworks))
	s.mux.Handle("GET /api/vms", user(s.handleListVMs))
	s.mux.Handle("POST /api/vms", user(s.handleDeploy))
	s.mux.Handle("GET /api/vms/{id}", user(s.handleGetVM))
	s.mux.Handle("DELETE /api/vms/{id}", user(s.handleDestroy))
	s.mux.Handle("POST /api/vms/{id}/power", user(s.handlePower))
	s.mux.Handle("POST /api/vms/{id}/console", user(s.handleCreateConsole))
	s.mux.Handle("GET /api/vms/{id}/agent", user(s.handleAgentInfo))
	s.mux.Handle("POST /api/vms/{id}/exec", user(s.handleAgentExec))
	s.mux.Handle("GET /api/console/{id}/ws", user(s.handleConsoleWS))
	s.mux.Handle("GET /api/events", user(s.handleEvents))

	s.mux.Handle("GET /api/admin/agent", admin(s.handleAgentSettings))
	s.mux.Handle("GET /api/admin/users", admin(s.handleListUsers))
	s.mux.Handle("POST /api/admin/users", admin(s.handleCreateUser))
	s.mux.Handle("PATCH /api/admin/users/{id}", admin(s.handleUpdateUser))
	s.mux.Handle("DELETE /api/admin/users/{id}", admin(s.handleDeleteUser))

	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, errors.New("not found"))
	})
	if d.Static != nil {
		s.mux.Handle("/", spaHandler(d.Static))
	}
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, errors.New("cross-origin request refused"))
		return
	}
	s.mux.ServeHTTP(w, r)
}

// sameOrigin refuses state-changing requests from other origins. Session
// cookies are SameSite=Strict as well; this is defence in depth.
func sameOrigin(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.sessions.User(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, errors.New("not signed in"))
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), u)))
	})
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth.FromContext(r.Context()).IsAdmin {
			writeError(w, http.StatusForbidden, errors.New("administrators only"))
			return
		}
		next(w, r)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid JSON body"))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// writeServiceError maps errors from Proxmox, the store and the VM service
// to HTTP responses.
func writeServiceError(w http.ResponseWriter, err error) {
	var pe *pve.Error
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, errors.New("not found"))
	case errors.Is(err, store.ErrNoVMID):
		writeError(w, http.StatusServiceUnavailable, errors.New("no free VM IDs; destroy a VM first"))
	case errors.Is(err, vms.ErrBadRequest):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, vms.ErrBusy), errors.Is(err, vms.ErrNotReady), errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err)
	case errors.As(err, &pe) && pe.Status == http.StatusForbidden:
		writeError(w, http.StatusForbidden, err)
	case errors.As(err, &pe):
		slog.Warn("proxmox request failed", "err", err)
		writeError(w, http.StatusBadGateway, err)
	default:
		slog.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, errors.New("internal error"))
	}
}
