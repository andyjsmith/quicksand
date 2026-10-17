// Package httpapi exposes Quicksand's REST, event stream and websocket API.
package httpapi

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
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
	sso      *auth.SSO // nil without single sign-on
	vms      *vms.Service
	hub      *vms.Hub
	consoles *console.Manager
	mux      *http.ServeMux

	agentPublicKey string
	downloads      downloads
}

type Deps struct {
	Config   *config.Config
	PVE      *pve.Client
	Store    *store.Store
	Sessions *auth.Sessions
	// SSO, if non-nil, signs users in with an OpenID Connect provider.
	SSO *auth.SSO
	VMs *vms.Service
	Hub *vms.Hub
	// Static, if non-nil, is the built web client served for non-API paths.
	Static fs.FS
	// DevWeb, if set, is a web client dev server (Vite) that non-API paths
	// are proxied to instead, so development uses a single origin.
	DevWeb *url.URL
	// AgentPublicKey is the key agents must trust; empty without an agent.
	AgentPublicKey string
}

func New(d Deps) *Server {
	s := &Server{
		cfg: d.Config, pve: d.PVE, store: d.Store, sessions: d.Sessions, sso: d.SSO, vms: d.VMs, hub: d.Hub,
		agentPublicKey: d.AgentPublicKey,
		// Proxmox console tickets are valid for about 30 seconds; new
		// connections for a session are refused after that.
		consoles: console.NewManager(30 * time.Second),
		mux:      http.NewServeMux(),
	}
	user := func(h http.HandlerFunc) http.Handler { return s.requireUser(h) }
	admin := func(h http.HandlerFunc) http.Handler { return s.requireUser(s.requireAdmin(h)) }

	// audit records the action when the handler succeeds.
	audit := s.audited
	// use counts the request as use of the VM, so it isn't suspended.
	use := s.using

	s.mux.HandleFunc("GET /api/auth/methods", s.handleAuthMethods)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/auth/oidc/login", s.handleSSOLogin)
	s.mux.HandleFunc("GET /api/auth/oidc/callback", s.handleSSOCallback)
	s.mux.HandleFunc("POST /api/auth/cli/token", s.handleCLIToken)
	s.mux.Handle("GET /api/auth/me", user(s.handleMe))
	s.mux.Handle("POST /api/auth/password", user(audit("password.change", s.handleChangePassword)))
	s.mux.Handle("GET /api/version", user(s.handleVersion))

	s.mux.Handle("GET /api/tokens", user(s.handleListTokens))
	s.mux.Handle("POST /api/tokens", user(audit("token.create", s.handleCreateToken)))
	s.mux.Handle("DELETE /api/tokens/{id}", user(audit("token.revoke", s.handleDeleteToken)))

	s.mux.Handle("GET /api/templates", user(s.handleTemplates))
	s.mux.Handle("GET /api/networks", user(s.handleNetworks))
	s.mux.Handle("GET /api/vms", user(s.handleListVMs))
	s.mux.Handle("POST /api/vms", user(audit("vm.deploy", s.handleDeploy)))
	s.mux.Handle("GET /api/vms/{id}", user(s.handleGetVM))
	s.mux.Handle("DELETE /api/vms/{id}", user(audit("vm.destroy", s.handleDestroy)))
	s.mux.Handle("POST /api/vms/{id}/power", user(audit("vm.power", s.handlePower)))
	s.mux.Handle("POST /api/vms/{id}/console", user(audit("vm.console", s.handleCreateConsole)))
	s.mux.Handle("GET /api/vms/{id}/agent", user(s.handleAgentInfo))
	s.mux.Handle("POST /api/vms/{id}/exec", user(use(audit("agent.exec", s.handleAgentExec))))
	s.mux.Handle("GET /api/vms/{id}/shell", user(use(audit("agent.shell", s.handleShell))))
	s.mux.Handle("GET /api/vms/{id}/connect", user(use(audit("agent.forward", s.handleConnect))))
	s.mux.Handle("GET /api/vms/{id}/serial", user(use(audit("vm.serial", s.handleSerial))))
	s.mux.Handle("POST /api/vms/{id}/display", user(use(s.handleDisplay)))
	s.mux.Handle("GET /api/vms/{id}/clipboard", user(use(s.handleGetClipboard)))
	s.mux.Handle("PUT /api/vms/{id}/clipboard", user(use(s.handleSetClipboard)))
	s.mux.Handle("POST /api/vms/{id}/open", user(use(audit("vm.open", s.handleOpenURL))))
	s.mux.Handle("POST /api/vms/{id}/relaunch", user(use(audit("vm.relaunch", s.handleRelaunch))))
	s.mux.Handle("POST /api/vms/{id}/extend", user(audit("vm.extend", s.handleExtend)))
	s.mux.Handle("POST /api/vms/{id}/snapshot", user(audit("vm.snapshot", s.handleSnapshot)))
	s.mux.Handle("POST /api/vms/{id}/snapshot/revert", user(audit("vm.snapshot.revert", s.handleRevertSnapshot)))
	s.mux.Handle("DELETE /api/vms/{id}/snapshot", user(audit("vm.snapshot.delete", s.handleDeleteSnapshot)))
	s.mux.Handle("GET /api/vms/{id}/firewall", user(s.handleGetFirewall))
	s.mux.Handle("PUT /api/vms/{id}/firewall", user(audit("vm.firewall", s.handleSetFirewall)))
	s.mux.Handle("POST /api/vms/{id}/link", user(audit("vm.link", s.handleLink)))
	s.mux.Handle("POST /api/vms/{id}/unlink", user(audit("vm.unlink", s.handleUnlink)))
	s.mux.Handle("GET /api/links", user(s.handleLinksInfo))
	s.mux.Handle("PATCH /api/links/{id}", user(audit("link.rename", s.handleRenameLink)))
	s.mux.Handle("GET /api/vms/{id}/processes", user(use(s.handleProcesses)))
	s.mux.Handle("POST /api/vms/{id}/processes/{pid}/kill", user(use(audit("agent.kill", s.handleKill))))
	s.mux.Handle("GET /api/vms/{id}/connections", user(use(s.handleConnections)))
	s.mux.Handle("GET /api/vms/{id}/files", user(use(s.handleListFiles)))
	s.mux.Handle("DELETE /api/vms/{id}/files", user(use(audit("agent.file.delete", s.handleDeleteFile))))
	s.mux.Handle("POST /api/vms/{id}/files/mkdir", user(use(audit("agent.file.mkdir", s.handleMkdir))))
	s.mux.Handle("GET /api/vms/{id}/files/content", user(use(audit("agent.file.download", s.handleDownload))))
	s.mux.Handle("PUT /api/vms/{id}/files/content", user(use(audit("agent.file.upload", s.handleUpload))))
	s.mux.Handle("POST /api/vms/{id}/files/zip", user(use(audit("agent.file.download", s.handlePrepareZip))))
	s.mux.Handle("GET /api/downloads/{token}", user(s.handlePreparedDownload))
	s.mux.Handle("GET /api/vms/{id}/files/hash", user(use(s.handleHashFile)))
	s.mux.Handle("GET /api/console/{id}/ws", user(s.handleConsoleWS))
	s.mux.Handle("GET /api/events", user(s.handleEvents))

	s.mux.Handle("GET /api/admin/agent", admin(s.handleAgentSettings))
	s.mux.Handle("GET /api/admin/audit", admin(s.handleAudit))
	s.mux.Handle("GET /api/admin/users", admin(s.handleListUsers))
	s.mux.Handle("POST /api/admin/users", admin(audit("user.create", s.handleCreateUser)))
	s.mux.Handle("PATCH /api/admin/users/{id}", admin(audit("user.update", s.handleUpdateUser)))
	s.mux.Handle("DELETE /api/admin/users/{id}", admin(audit("user.delete", s.handleDeleteUser)))

	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, errors.New("not found"))
	})
	switch {
	case d.DevWeb != nil:
		s.mux.Handle("/", devWebProxy(d.DevWeb))
	case d.Static != nil:
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
		u, _, ok := s.sessions.Authenticate(r)
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

// detail turns "<sentinel>: what happened" into "What happened.", for
// errors whose detail is meant for the user.
func detail(err, sentinel error) error {
	msg := strings.TrimPrefix(err.Error(), sentinel.Error()+": ")
	if msg != "" && msg[0] >= 'a' && msg[0] <= 'z' {
		msg = string(msg[0]-'a'+'A') + msg[1:]
	}
	return errors.New(strings.TrimSuffix(msg, ".") + ".")
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
	case errors.Is(err, store.ErrLimit):
		writeError(w, http.StatusConflict, detail(err, store.ErrLimit))
	case errors.Is(err, vms.ErrLinksDisabled):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrNoLinkVLAN), errors.Is(err, store.ErrNoLinkIP):
		writeError(w, http.StatusServiceUnavailable, err)
	case errors.Is(err, vms.ErrNoSnapshot):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, vms.ErrCantExtend):
		writeError(w, http.StatusConflict, detail(err, vms.ErrCantExtend))
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
