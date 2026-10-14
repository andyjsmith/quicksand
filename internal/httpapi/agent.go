package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"quicksand/internal/agentclient"
	"quicksand/internal/agentproto"
	"quicksand/internal/pve"
	"quicksand/internal/vms"
)

func (s *Server) handleAgentInfo(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.ownedVM(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	info, err := s.vms.AgentInfo(ctx, vm)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleAgentExec runs a command in the guest and returns its result once
// it finishes (bounded by the request's timeout).
func (s *Server) handleAgentExec(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.ownedVM(w, r)
	if !ok {
		return
	}
	var req agentproto.ExecRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if (len(req.Argv) == 0) == (req.Shell == "") {
		writeError(w, http.StatusBadRequest, errors.New("set exactly one of argv or shell"))
		return
	}
	timeout := agentproto.DefaultExecTimeout
	if req.TimeoutSeconds > 0 {
		timeout = min(time.Duration(req.TimeoutSeconds)*time.Second, agentproto.MaxExecTimeout)
	}
	// Leave the agent time to report a timed-out command.
	ctx, cancel := context.WithTimeout(r.Context(), timeout+15*time.Second)
	defer cancel()
	res, err := s.vms.AgentExec(ctx, vm, req)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// guestAgent loads the VM in the path and its agent, writing the error
// response if either is unavailable.
func (s *Server) guestAgent(w http.ResponseWriter, r *http.Request) (vms.GuestAgent, bool) {
	vm, ok := s.ownedVM(w, r)
	if !ok {
		return vms.GuestAgent{}, false
	}
	g, err := s.vms.GuestAgent(vm)
	if err != nil {
		writeAgentError(w, err)
		return vms.GuestAgent{}, false
	}
	return g, true
}

// agentTimeout bounds quick agent requests (listings and the like).
const agentTimeout = 30 * time.Second

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
	defer cancel()
	list, err := g.Processes(ctx, g.Host)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleKill(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	pid, err := strconv.Atoi(r.PathValue("pid"))
	if err != nil || pid <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("invalid pid"))
		return
	}
	var req agentproto.KillRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
	defer cancel()
	if err := g.Kill(ctx, g.Host, pid, req.Force); err != nil {
		writeAgentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
	defer cancel()
	list, err := g.Connections(ctx, g.Host)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
	defer cancel()
	list, err := g.ListFiles(ctx, g.Host, g.File(r.URL.Query().Get("path")))
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleHashFile(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	// Hashing reads the whole file.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	h, err := g.HashFile(ctx, g.Host, g.File(r.URL.Query().Get("path")))
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
	defer cancel()
	e, err := g.Mkdir(ctx, g.Host, g.File(r.URL.Query().Get("path")))
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	q := r.URL.Query()
	if err := g.Remove(ctx, g.Host, g.File(q.Get("path")), q.Get("recursive") == "1"); err != nil {
		writeAgentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDownload streams a guest file (or a directory as a zip) to the
// client.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	resp, err := g.Download(r.Context(), g.Host, g.File(r.URL.Query().Get("path")))
	if err != nil {
		writeAgentError(w, err)
		return
	}
	relayDownload(w, resp)
}

// handleUpload streams the request body to a guest file.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	e, err := g.Upload(r.Context(), g.Host, g.File(q.Get("path")), r.Body, r.ContentLength, q.Get("overwrite") == "1")
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// handleDisplay changes the guest's screen resolution through the agent,
// for consoles whose protocol can't (VNC, or SPICE without its agent).
func (s *Server) handleDisplay(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	var req agentproto.DisplayRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Width <= 0 || req.Height <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("width and height are required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
	defer cancel()
	res, err := g.SetDisplay(ctx, g.Host, req)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleShell runs a command (or an interactive shell) in the guest over
// a stream; see agentproto.ShellRequest. Query: command, shell, pty=1,
// cols, rows, term.
func (s *Server) handleShell(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	req := agentproto.ShellRequest{Command: q.Get("command"), Shell: q.Get("shell"), PTY: q.Get("pty") == "1", Term: q.Get("term"), Dir: g.FilesDir}
	req.Cols, _ = strconv.Atoi(q.Get("cols"))
	req.Rows, _ = strconv.Atoi(q.Get("rows"))
	s.relayStream(w, r, func(ctx context.Context) (net.Conn, error) { return g.Shell(ctx, g.Host, req) }, writeAgentError)
}

// handleConnect opens a TCP connection from the guest to addr (host:port,
// as the guest resolves it) over a stream, for port forwarding.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	addr := r.URL.Query().Get("addr")
	if _, _, err := net.SplitHostPort(addr); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("addr must be host:port"))
		return
	}
	s.relayStream(w, r, func(ctx context.Context) (net.Conn, error) { return g.Connect(ctx, g.Host, addr) }, writeAgentError)
}

// handleSerial opens a shell on the VM's serial console, for templates
// that enable it, over a stream or websocket carrying the same frames as
// handleShell. Query: cols, rows.
func (s *Server) handleSerial(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.ownedVM(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	cols, _ := strconv.Atoi(q.Get("cols"))
	rows, _ := strconv.Atoi(q.Get("rows"))
	s.relayStream(w, r, func(ctx context.Context) (net.Conn, error) { return s.vms.SerialShell(ctx, vm, cols, rows) }, writeSerialError)
}

// relayStream opens the agent's stream first, so failures are ordinary
// HTTP errors, then switches the client to a stream and joins the two.
// Browsers, which can't make stream requests, use a websocket instead:
// its binary messages carry the stream's bytes.
func (s *Server) relayStream(w http.ResponseWriter, r *http.Request, open func(context.Context) (net.Conn, error), writeErr func(http.ResponseWriter, error)) {
	isWS := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	if !isWS && !agentproto.IsStreamRequest(r) {
		writeError(w, http.StatusBadRequest, errors.New("this endpoint needs an Upgrade: "+agentproto.StreamProtocol+" (or websocket) request"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
	agent, err := open(ctx)
	cancel()
	if err != nil && isWS {
		// Browsers can't read a refused websocket's response; accept it
		// and report the error as the session's end instead.
		rec := httptest.NewRecorder()
		writeErr(rec, err)
		var body struct {
			Error string `json:"error"`
		}
		json.Unmarshal(rec.Body.Bytes(), &body)
		if ws, err := websocket.Accept(w, r, nil); err == nil {
			fw := agentproto.NewFrameWriter(websocket.NetConn(r.Context(), ws, websocket.MessageBinary))
			fw.WriteJSON(agentproto.FrameExit, agentproto.ShellExit{Code: -1, Error: body.Error})
			ws.Close(websocket.StatusNormalClosure, "")
		}
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	var client net.Conn
	if isWS {
		var ws *websocket.Conn
		if ws, err = websocket.Accept(w, r, nil); err == nil {
			ws.SetReadLimit(agentproto.MaxFrame + 64<<10)
			// Not r.Context(): that ends when the handler returns.
			client = websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
		}
	} else {
		client, err = agentproto.AcceptStream(w, r)
	}
	if err != nil {
		agent.Close()
		slog.Warn("accept stream", "err", err)
		return
	}
	agentproto.Join(client, agent)
}

func writeSerialError(w http.ResponseWriter, err error) {
	var pe *pve.Error
	switch {
	case errors.Is(err, vms.ErrNoSerial):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, vms.ErrNotReady), errors.As(err, &pe):
		writeServiceError(w, err)
	default:
		writeError(w, http.StatusBadGateway, err)
	}
}

func writeAgentError(w http.ResponseWriter, err error) {
	var ae *agentclient.Error
	switch {
	case errors.Is(err, vms.ErrNoAgent), errors.Is(err, vms.ErrNoSerial):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, vms.ErrNotReady):
		writeError(w, http.StatusConflict, err)
	case errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500:
		// The agent understood and refused (no such file, exists, ...).
		writeError(w, ae.Status, errors.New(ae.Message))
	case errors.As(err, &ae):
		writeError(w, http.StatusBadGateway, errors.New(ae.Message))
	default:
		writeError(w, http.StatusBadGateway, errors.New("the agent did not respond: "+err.Error()))
	}
}

// handleAgentSettings tells administrators what to put in agent configs.
func (s *Server) handleAgentSettings(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"enabled": s.cfg.Agent != nil}
	if s.cfg.Agent != nil {
		out["publicKey"] = s.agentPublicKey
		out["port"] = s.cfg.Agent.Port
		out["subnet"] = s.cfg.Agent.Network.Subnet
		out["macPrefix"] = s.cfg.Agent.Network.MACPrefix
	}
	writeJSON(w, http.StatusOK, out)
}
