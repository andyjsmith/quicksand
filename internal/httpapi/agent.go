package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"quicksand/internal/agentproto"
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

func writeAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, vms.ErrNoAgent):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, vms.ErrNotReady):
		writeError(w, http.StatusConflict, err)
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
