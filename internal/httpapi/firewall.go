package httpapi

import (
	"errors"
	"net/http"

	"quicksand/internal/vms"
)

func (s *Server) handleGetFirewall(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.ownedVM(w, r)
	if !ok {
		return
	}
	f, err := s.vms.Firewall(r.Context(), vm)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// handleSetFirewall replaces a VM's firewall settings and applies them. If
// they were saved but applying failed, the answer still has the settings,
// with a warning.
func (s *Server) handleSetFirewall(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.ownedVM(w, r)
	if !ok {
		return
	}
	var req vms.Firewall
	if !decodeJSON(w, r, &req) {
		return
	}
	f, err := s.vms.SetFirewall(r.Context(), vm, req)
	if err != nil && !errors.Is(err, vms.ErrNotApplied) {
		writeServiceError(w, err)
		return
	}
	resp := map[string]any{"firewall": f}
	if err != nil {
		resp["warning"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}
