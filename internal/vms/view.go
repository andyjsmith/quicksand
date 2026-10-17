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
	Action      pve.PowerAction `json:"action,omitempty"`
	ActionError string          `json:"actionError,omitempty"`
	// ActionProgress is how far along the action is, for those that say.
	ActionProgress *ActionProgress `json:"actionProgress,omitempty"`
	// Link is the VM's link network, if it's in one.
	Link            *LinkView          `json:"link,omitempty"`
	Consoles        []console.Protocol `json:"consoles"`
	AbsolutePointer bool               `json:"absolutePointer"`
	// ExpiresAt is when the VM is destroyed, unless extended; unset if it
	// never is. ExtendsLeft is how many more times its owner may extend it
	// (unset: no limit), and ExpiryWarningSeconds how long before expiring
	// it should be shown as about to.
	ExpiresAt            *time.Time `json:"expiresAt,omitempty"`
	ExtendsLeft          *int       `json:"extendsLeft,omitempty"`
	ExtendSeconds        int64      `json:"extendSeconds,omitempty"`
	ExpiryWarningSeconds int64      `json:"expiryWarningSeconds,omitempty"`
	// SuspendAt is when the running VM is hibernated if nobody uses it
	// before then; unset if it's in use or VMs aren't suspended.
	// SuspendWarningSeconds is how long before then to say so.
	SuspendAt             *time.Time `json:"suspendAt,omitempty"`
	SuspendWarningSeconds int64      `json:"suspendWarningSeconds,omitempty"`
	// Snapshot is the VM's snapshot, if it has one.
	Snapshot *SnapshotInfo `json:"snapshot,omitempty"`
	// Serial is set when shells can use the VM's serial console.
	Serial bool `json:"serial"`
	// Agent is set for VMs with a Quicksand agent.
	Agent *AgentView `json:"agent,omitempty"`
	// Launch is the app the VM's agent launches, if its template has one.
	Launch    *LaunchView `json:"launch,omitempty"`
	Owner     string      `json:"owner,omitempty"`
	CreatedAt time.Time   `json:"createdAt"`
}

// consoleInfo caches what a VM's config says about connecting to it.
type consoleInfo struct {
	protocols       []console.Protocol
	absolutePointer bool
	// agent is whether the guest agent is enabled.
	agent bool
	// serial lists the VM's serial ports (serial0 ...).
	serial []string
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
	if s.cfg.Expiry.Enabled() && !vm.ExpiresAt.IsZero() {
		at := vm.ExpiresAt
		v.ExpiresAt = &at
		if left := s.extendsLeft(vm); left >= 0 {
			v.ExtendsLeft = &left
		}
		v.ExtendSeconds = int64(time.Duration(s.cfg.Expiry.ExtendDuration).Seconds())
		// A day's notice, or a quarter of the time limit if that's shorter.
		v.ExpiryWarningSeconds = int64(min(24*time.Hour, time.Duration(s.cfg.Expiry.TimeLimit)/4).Seconds())
	}
	v.Action, v.ActionError, v.ActionProgress = s.Action(vm.ID)
	if vm.LinkID != 0 {
		if networks, err := s.store.LinkNetworks(ctx); err == nil {
			v.Link = s.linkView(vm, networks)
		}
	}
	if vm.State != store.VMReady {
		return v
	}
	// status/current is live; the cluster overview lags by several seconds.
	if st, err := s.pve.Status(ctx, vm.Node, vm.VMID); err == nil {
		v.Power, v.Uptime, v.CPU, v.Mem, v.MaxMem = st.PowerState(), st.Uptime, st.CPU, st.Mem, st.MaxMem
	}
	v.Booting = v.Power == "running" && s.Booting(vm.ID)
	if at := s.suspendAt(vm.ID); v.Power == "running" && !at.IsZero() {
		v.SuspendAt = &at
		// A quarter of an hour's notice, or a quarter of the idle time if
		// that's shorter.
		v.SuspendWarningSeconds = int64(min(15*time.Minute, time.Duration(s.cfg.Suspend.IdleTime)/4).Seconds())
	}
	if snap, err := s.snapshot(ctx, vm); err == nil {
		v.Snapshot = snap
	}
	v.Agent = s.agentView(ctx, vm, v.Power == "running")
	v.Launch = s.launchView(vm, v.Agent)
	if info, ok := s.consoleInfo(ctx, vm); ok {
		v.Consoles, v.AbsolutePointer = info.protocols, info.absolutePointer
		_, err := s.serialPort(ctx, vm)
		v.Serial = err == nil
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
	for _, port := range []string{"serial0", "serial1", "serial2", "serial3"} {
		if _, ok := cfg[port]; ok {
			info.serial = append(info.serial, port)
		}
	}
	if pve.SupportsSpice(cfg) {
		info.protocols = []console.Protocol{console.Spice, console.VNC}
	}
	s.mu.Lock()
	s.consoles[vm.ID] = info
	s.mu.Unlock()
	return info, true
}
