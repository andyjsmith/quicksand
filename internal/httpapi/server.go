// Package httpapi exposes Quicksand's REST and websocket API.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"quicksand/internal/console"
	"quicksand/internal/pve"
)

type Server struct {
	pve      *pve.Client
	consoles *console.Manager
	openers  map[console.Protocol]opener
	mux      *http.ServeMux
}

// opener fetches a ticket for a guest and registers a console session for it.
type opener func(ctx context.Context, node string, vmid int) (*console.Session, error)

// New builds the API server. static, if non-nil, is served for all non-API paths.
func New(client *pve.Client, static fs.FS) *Server {
	s := &Server{
		pve: client,
		// Proxmox tickets are valid for about 30 seconds; new connections
		// for a session are refused after that.
		consoles: console.NewManager(30 * time.Second),
		mux:      http.NewServeMux(),
	}
	s.openers = map[console.Protocol]opener{
		console.Spice: s.openSpice,
		console.VNC:   s.openVNC,
	}

	s.mux.HandleFunc("GET /api/vms", s.handleListVMs)
	s.mux.HandleFunc("GET /api/vms/{node}/{vmid}", s.handleGetVM)
	s.mux.HandleFunc("POST /api/vms/{node}/{vmid}/console", s.handleCreateConsole)
	s.mux.HandleFunc("GET /api/console/{id}/ws", s.handleConsoleWS)
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, errors.New("not found"))
	})
	if static != nil {
		s.mux.Handle("/", spaHandler(static))
	}
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// VM is a guest as presented to the web client.
type VM struct {
	VMID     int                `json:"vmid"`
	Name     string             `json:"name"`
	Node     string             `json:"node"`
	Status   string             `json:"status"`
	Template bool               `json:"template"`
	CPU      float64            `json:"cpu"`
	MaxCPU   int                `json:"maxcpu"`
	Mem      int64              `json:"mem"`
	MaxMem   int64              `json:"maxmem"`
	Uptime   int64              `json:"uptime"`
	Consoles []console.Protocol `json:"consoles"`
	// AbsolutePointer is false when the guest has no USB tablet, so VNC can
	// only drive a relative mouse that drifts from the local cursor.
	AbsolutePointer bool `json:"absolutePointer"`
}

func (s *Server) handleListVMs(w http.ResponseWriter, r *http.Request) {
	vms, err := s.pve.ListVMs(r.Context())
	if err != nil {
		writePVEError(w, err)
		return
	}
	out := make([]VM, len(vms))
	var wg sync.WaitGroup
	for i, vm := range vms {
		wg.Go(func() { out[i] = s.describe(r.Context(), vm) })
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetVM(w http.ResponseWriter, r *http.Request) {
	node, vmid, ok := guestFromPath(w, r)
	if !ok {
		return
	}
	vms, err := s.pve.ListVMs(r.Context())
	if err != nil {
		writePVEError(w, err)
		return
	}
	for _, vm := range vms {
		if vm.Node == node && vm.VMID == vmid {
			writeJSON(w, http.StatusOK, s.describe(r.Context(), vm))
			return
		}
	}
	writeError(w, http.StatusNotFound, errors.New("vm not found"))
}

// describe adds the console protocols a guest supports, preferred first.
func (s *Server) describe(ctx context.Context, vm pve.VM) VM {
	out := VM{
		VMID: vm.VMID, Name: vm.Name, Node: vm.Node, Status: vm.Status,
		Template: vm.Template == 1, CPU: vm.CPU, MaxCPU: vm.MaxCPU,
		Mem: vm.Mem, MaxMem: vm.MaxMem, Uptime: vm.Uptime,
		Consoles: []console.Protocol{console.VNC},
		// Assume the common case if the config can't be read.
		AbsolutePointer: true,
	}
	cfg, err := s.pve.VMConfig(ctx, vm.Node, vm.VMID)
	if err != nil {
		slog.Warn("read vm config", "node", vm.Node, "vmid", vm.VMID, "err", err)
		return out
	}
	if pve.SupportsSpice(cfg) {
		out.Consoles = []console.Protocol{console.Spice, console.VNC}
	}
	out.AbsolutePointer = pve.HasTablet(cfg)
	return out
}

type createConsoleRequest struct {
	Protocol console.Protocol `json:"protocol"`
}

type createConsoleResponse struct {
	ID       string           `json:"id"`
	Protocol console.Protocol `json:"protocol"`
	Password string           `json:"password"`
	Title    string           `json:"title"`
	// Path is the websocket path the browser client connects to.
	Path string `json:"path"`
}

func (s *Server) handleCreateConsole(w http.ResponseWriter, r *http.Request) {
	node, vmid, ok := guestFromPath(w, r)
	if !ok {
		return
	}
	var req createConsoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	open, ok := s.openers[req.Protocol]
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("unsupported console protocol"))
		return
	}
	sess, err := open(r.Context(), node, vmid)
	if err != nil {
		writePVEError(w, err)
		return
	}
	slog.Info("console session created", "node", node, "vmid", vmid, "protocol", sess.Protocol)
	writeJSON(w, http.StatusCreated, createConsoleResponse{
		ID:       sess.ID,
		Protocol: sess.Protocol,
		Password: sess.Password,
		Title:    sess.Title,
		Path:     "/api/console/" + sess.ID + "/ws",
	})
}

func (s *Server) openSpice(ctx context.Context, node string, vmid int) (*console.Session, error) {
	t, err := s.pve.SpiceProxy(ctx, node, vmid)
	if err != nil {
		return nil, err
	}
	dial := func(ctx context.Context) (net.Conn, error) { return pve.DialSpice(ctx, t) }
	return s.consoles.Create(console.Spice, t.Password, t.Title, 0, dial), nil
}

func (s *Server) openVNC(ctx context.Context, node string, vmid int) (*console.Session, error) {
	t, err := s.pve.VNCProxy(ctx, node, vmid)
	if err != nil {
		return nil, err
	}
	dial := func(ctx context.Context) (net.Conn, error) { return s.pve.DialVNC(ctx, node, vmid, t) }
	// vncproxy serves exactly one connection.
	title := "VM " + strconv.Itoa(vmid)
	return s.consoles.Create(console.VNC, t.Password, title, 1, dial), nil
}

func (s *Server) handleConsoleWS(w http.ResponseWriter, r *http.Request) {
	// Dial upstream before accepting so failures surface as HTTP errors.
	dialCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	sess, upstream, err := s.consoles.Open(dialCtx, r.PathValue("id"))
	cancel()
	switch {
	case errors.Is(err, console.ErrNotFound):
		writeError(w, http.StatusNotFound, err)
		return
	case errors.Is(err, console.ErrExhausted):
		writeError(w, http.StatusConflict, err)
		return
	case err != nil:
		slog.Warn("console upstream dial failed", "err", err)
		writeError(w, http.StatusBadGateway, err)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		upstream.Close()
		return
	}
	ws.SetReadLimit(-1)
	started := time.Now()
	console.Relay(websocket.NetConn(r.Context(), ws, websocket.MessageBinary), upstream)
	slog.Debug("console connection closed", "protocol", sess.Protocol, "duration", time.Since(started))
}

func guestFromPath(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	vmid, err := strconv.Atoi(r.PathValue("vmid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid vmid"))
		return "", 0, false
	}
	return r.PathValue("node"), vmid, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writePVEError(w http.ResponseWriter, err error) {
	var pe *pve.Error
	if errors.As(err, &pe) && (pe.Status == http.StatusForbidden || pe.Status == http.StatusNotFound) {
		writeError(w, pe.Status, err)
		return
	}
	slog.Warn("pve request failed", "err", err)
	writeError(w, http.StatusBadGateway, err)
}
