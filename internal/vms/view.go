package vms

import (
	"context"
	"sync"
	"time"

	"quicksand/internal/console"
	"quicksand/internal/pve"
	"quicksand/internal/store"
)

// View is a deployed VM as the web client sees it.
type View struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	TemplateID   string        `json:"templateId"`
	TemplateName string        `json:"templateName"`
	OS           string        `json:"os"`
	VMID         int           `json:"vmid"`
	Cores        int           `json:"cores"`
	MemoryMiB    int           `json:"memoryMiB"`
	NetworkLabel string        `json:"networkLabel"`
	State        store.VMState `json:"state"`
	Step         string        `json:"step,omitempty"`
	Progress     float64       `json:"progress"`
	Error        string        `json:"error,omitempty"`
	// Power is running, stopped, paused, suspended or unknown.
	Power string `json:"power"`
	// Booting is set while the guest OS starts, until its agent answers.
	Booting bool    `json:"booting"`
	Uptime  int64   `json:"uptime"`
	CPU     float64 `json:"cpu"`
	Mem     int64   `json:"mem"`
	MaxMem  int64   `json:"maxmem"`
	// Action is a power action in progress; ActionError the last one's failure.
	Action          pve.PowerAction    `json:"action,omitempty"`
	ActionError     string             `json:"actionError,omitempty"`
	Consoles        []console.Protocol `json:"consoles"`
	AbsolutePointer bool               `json:"absolutePointer"`
	// Agent is set for VMs with a Quicksand agent.
	Agent     *AgentView `json:"agent,omitempty"`
	Owner     string     `json:"owner,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// consoleInfo caches what a VM's config says about connecting to it.
type consoleInfo struct {
	protocols       []console.Protocol
	absolutePointer bool
	// agent is whether the guest agent is enabled.
	agent bool
}

// Views lists VMs owned by ownerID (0 for everyone's, with owners named).
func (s *Service) Views(ctx context.Context, ownerID int64) ([]View, error) {
	vms, err := s.store.VMs(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var owners map[int64]string
	if ownerID == 0 {
		users, err := s.store.Users(ctx)
		if err != nil {
			return nil, err
		}
		owners = map[int64]string{}
		for _, u := range users {
			owners[u.ID] = u.Username
		}
	}
	views := make([]View, len(vms))
	var wg sync.WaitGroup
	for i, vm := range vms {
		wg.Go(func() {
			views[i] = s.View(ctx, vm)
			views[i].Owner = owners[vm.OwnerID]
		})
	}
	wg.Wait()
	return views, nil
}

// View describes a single VM, with its live power state from Proxmox.
func (s *Service) View(ctx context.Context, vm store.VM) View {
	v := View{
		ID: vm.ID, Name: vm.Name, TemplateID: vm.TemplateID, VMID: vm.VMID,
		Cores: vm.Cores, MemoryMiB: vm.MemoryMiB, State: vm.State, Step: vm.Step,
		Progress: vm.Progress, Error: vm.Error, Power: "unknown", Consoles: []console.Protocol{},
		CreatedAt: vm.CreatedAt,
	}
	if t, ok := s.cfg.Template(vm.TemplateID); ok {
		v.TemplateName, v.OS = t.Name, t.OS
	}
	if n, ok := s.cfg.Network(vm.NetworkID); ok {
		v.NetworkLabel = n.Label
	}
	v.Action, v.ActionError = s.Action(vm.ID)
	if vm.State != store.VMReady {
		return v
	}
	// status/current is live; the cluster overview lags by several seconds.
	if st, err := s.pve.Status(ctx, vm.Node, vm.VMID); err == nil {
		v.Power, v.Uptime, v.CPU, v.Mem, v.MaxMem = st.PowerState(), st.Uptime, st.CPU, st.Mem, st.MaxMem
	}
	v.Booting = v.Power == "running" && s.Booting(vm.ID)
	v.Agent = s.agentView(ctx, vm, v.Power == "running")
	if info, ok := s.consoleInfo(ctx, vm); ok {
		v.Consoles, v.AbsolutePointer = info.protocols, info.absolutePointer
	}
	return v
}

func (s *Service) consoleInfo(ctx context.Context, vm store.VM) (consoleInfo, bool) {
	s.mu.Lock()
	info, ok := s.consoles[vm.ID]
	s.mu.Unlock()
	if ok {
		return info, true
	}
	cfg, err := s.pve.VMConfig(ctx, vm.Node, vm.VMID)
	if err != nil {
		return consoleInfo{}, false
	}
	info = consoleInfo{protocols: []console.Protocol{console.VNC}, absolutePointer: pve.HasTablet(cfg), agent: agentEnabled(cfg)}
	if pve.SupportsSpice(cfg) {
		info.protocols = []console.Protocol{console.Spice, console.VNC}
	}
	s.mu.Lock()
	s.consoles[vm.ID] = info
	s.mu.Unlock()
	return info, true
}
