package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"quicksand/internal/auth"
	"quicksand/internal/store"
)

type linkNetworkJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (s *Server) handleLinksInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.vms.LinksInfo())
}

// handleLink joins the VM and those listed in "with" into one link network.
// VMs in different networks need "merge": without it, the answer is 409
// with mergeNeeded and the networks involved.
func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.ownedVM(w, r)
	if !ok {
		return
	}
	var req struct {
		With  []string `json:"with"`
		Merge bool     `json:"merge"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	u := auth.FromContext(r.Context())
	var others []store.VM
	for _, id := range req.With {
		o, err := s.store.VM(r.Context(), id)
		// Users link only their own VMs; administrators any.
		if err == nil && o.OwnerID != u.ID && !u.IsAdmin {
			err = store.ErrNotFound
		}
		if err != nil {
			writeServiceError(w, err)
			return
		}
		others = append(others, o)
	}
	network, err := s.vms.Link(r.Context(), vm, others, req.Merge)
	var merge *store.MergeNeededError
	switch {
	case errors.As(err, &merge):
		nets := []linkNetworkJSON{}
		for _, n := range merge.Networks {
			nets = append(nets, linkNetworkJSON{ID: n.ID, Name: n.Name})
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "mergeNeeded": true, "networks": nets})
		return
	case err != nil && network.ID == 0:
		writeServiceError(w, err)
		return
	}
	resp := map[string]any{"network": linkNetworkJSON{ID: network.ID, Name: network.Name}}
	if err != nil {
		// Linked, but a NIC change failed; the sync loop retries it.
		resp["warning"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUnlink(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.ownedVM(w, r)
	if !ok {
		return
	}
	if err := s.vms.Unlink(r.Context(), vm); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRenameLink(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	auditDetail(r, "renamed to %q", req.Name)
	if err := s.vms.RenameLinkNetwork(r.Context(), auth.FromContext(r.Context()), id, req.Name); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
