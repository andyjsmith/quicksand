package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"quicksand/internal/auth"
	"quicksand/internal/console"
	"quicksand/internal/pve"
	"quicksand/internal/store"
)

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
	vm, ok := s.ownedVM(w, r)
	if !ok {
		return
	}
	var req createConsoleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	auditDetail(r, "%s", req.Protocol)
	if vm.State != store.VMReady {
		writeError(w, http.StatusConflict, errors.New("the VM is not ready"))
		return
	}
	view := s.vms.View(r.Context(), vm)
	if view.Power != "running" {
		writeError(w, http.StatusConflict, errors.New("the VM is not running"))
		return
	}
	supported := false
	for _, p := range view.Consoles {
		supported = supported || p == req.Protocol
	}
	if !supported {
		writeError(w, http.StatusBadRequest, errors.New("this VM doesn't support that console protocol"))
		return
	}

	owner := auth.FromContext(r.Context()).ID
	var sess *console.Session
	switch req.Protocol {
	case console.Spice:
		t, err := s.pve.SpiceProxy(r.Context(), vm.Node, vm.VMID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		dial := func(ctx context.Context) (net.Conn, error) { return pve.DialSpice(ctx, t) }
		sess = s.consoles.Create(owner, console.Spice, t.Password, vm.Name, 0, dial)
	case console.VNC:
		t, err := s.pve.VNCProxy(r.Context(), vm.Node, vm.VMID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		node, vmid := vm.Node, vm.VMID
		dial := func(ctx context.Context) (net.Conn, error) { return s.pve.DialVNC(ctx, node, vmid, t) }
		// vncproxy serves exactly one connection.
		sess = s.consoles.Create(owner, console.VNC, t.Password, vm.Name, 1, dial)
	}
	slog.Info("console session created", "vm", vm.ID, "vmid", strconv.Itoa(vm.VMID), "protocol", req.Protocol)
	writeJSON(w, http.StatusCreated, createConsoleResponse{
		ID:       sess.ID,
		Protocol: sess.Protocol,
		Password: sess.Password,
		Title:    sess.Title,
		Path:     "/api/console/" + sess.ID + "/ws",
	})
}

func (s *Server) handleConsoleWS(w http.ResponseWriter, r *http.Request) {
	// Dial upstream before accepting so failures surface as HTTP errors.
	dialCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	sess, upstream, err := s.consoles.Open(dialCtx, r.PathValue("id"), auth.FromContext(r.Context()).ID)
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
