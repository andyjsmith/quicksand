package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"quicksand/internal/auth"
	"quicksand/internal/pve"
	"quicksand/internal/store"
	"quicksand/internal/vms"
)

// snapshotInterval is how often the event stream resends the full VM list,
// which also picks up power changes made outside Quicksand.
const snapshotInterval = 5 * time.Second

type templateView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	OS           string `json:"os"`
	Cores        int    `json:"cores"`
	MemoryMiB    int    `json:"memoryMiB"`
	MaxCores     int    `json:"maxCores"`
	MaxMemoryMiB int    `json:"maxMemoryMiB"`
	// Agent says deployments can have the Quicksand agent.
	Agent bool `json:"agent"`
	// Error is set when the template can't be read from Proxmox.
	Error string `json:"error,omitempty"`
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	out := make([]templateView, 0, len(s.cfg.Templates))
	for _, t := range s.cfg.Templates {
		v := templateView{
			ID: t.ID, Name: t.Name, Description: t.Description, OS: t.OS,
			MaxCores: t.MaxCores, MaxMemoryMiB: t.MaxMemoryMiB, Agent: t.Agent && s.cfg.Agent != nil,
		}
		d, err := s.vms.TemplateDefaults(r.Context(), t)
		if err != nil {
			slog.Warn("read template defaults", "template", t.ID, "err", err)
			v.Error = "This template can't be read from Proxmox."
		}
		v.Cores, v.MemoryMiB = d.Cores, d.MemoryMiB
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

type networkView struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Default bool   `json:"default"`
}

// handleNetworks lists networks by label; bridges and VLANs stay server-side.
func (s *Server) handleNetworks(w http.ResponseWriter, r *http.Request) {
	out := make([]networkView, len(s.cfg.Networks))
	for i, n := range s.cfg.Networks {
		out[i] = networkView{ID: n.ID, Label: n.Label, Default: n.Default}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleListVMs lists the user's VMs; admins can ask for everyone's with ?all=1.
func (s *Server) handleListVMs(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	owner := u.ID
	if r.URL.Query().Get("all") == "1" {
		if !u.IsAdmin {
			writeError(w, http.StatusForbidden, errors.New("administrators only"))
			return
		}
		owner = 0
	}
	views, err := s.vms.Views(r.Context(), owner)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	var req vms.DeployRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	vm, err := s.vms.Deploy(r.Context(), auth.FromContext(r.Context()), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, s.vms.View(r.Context(), vm))
}

func (s *Server) handleGetVM(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.ownedVM(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.vms.View(r.Context(), vm))
}

func (s *Server) handleDestroy(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.ownedVM(w, r)
	if !ok {
		return
	}
	if err := s.vms.Destroy(r.Context(), vm); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

var powerActions = map[pve.PowerAction]bool{
	pve.PowerStart: true, pve.PowerShutdown: true, pve.PowerStop: true, pve.PowerReboot: true,
	pve.PowerReset: true, pve.PowerSuspend: true, pve.PowerResume: true,
}

func (s *Server) handlePower(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.ownedVM(w, r)
	if !ok {
		return
	}
	var req struct {
		Action pve.PowerAction `json:"action"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !powerActions[req.Action] {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unknown power action %q", req.Action))
		return
	}
	if err := s.vms.Power(r.Context(), vm, req.Action); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleEvents streams changes to the user's VMs as server-sent events:
// "vms" carries the full list (initially and periodically), "vm" one
// changed VM, and "vm-deleted" the ID of a destroyed one.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	u := auth.FromContext(r.Context())
	events, unsubscribe := s.hub.Subscribe(u.ID)
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Stops reverse proxies such as nginx from buffering the stream.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(event string, v any) bool {
		data, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	snapshot := func() bool {
		views, err := s.vms.Views(r.Context(), u.ID)
		if err != nil {
			return r.Context().Err() == nil
		}
		return send("vms", views)
	}

	if !snapshot() {
		return
	}
	ticker := time.NewTicker(snapshotInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !snapshot() {
				return
			}
		case e := <-events:
			if e.Deleted {
				if !send("vm-deleted", map[string]string{"id": e.VMID}) {
					return
				}
				continue
			}
			vm, err := s.store.VM(r.Context(), e.VMID)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil || !send("vm", s.vms.View(r.Context(), vm)) {
				return
			}
		}
	}
}

// ownedVM loads the VM named in the path if the user may manage it: its
// owner, or an administrator.
func (s *Server) ownedVM(w http.ResponseWriter, r *http.Request) (store.VM, bool) {
	vm, err := s.store.VM(r.Context(), r.PathValue("id"))
	u := auth.FromContext(r.Context())
	if err == nil && vm.OwnerID != u.ID && !u.IsAdmin {
		err = store.ErrNotFound
	}
	if err != nil {
		writeServiceError(w, err)
		return store.VM{}, false
	}
	return vm, true
}
