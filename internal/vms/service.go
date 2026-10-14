// Package vms deploys, manages and destroys users' VMs.
package vms

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"quicksand/internal/agentclient"
	"quicksand/internal/config"
	"quicksand/internal/pve"
	"quicksand/internal/store"
)

const (
	// bootWatchTimeout is how long a VM shows as booting without its guest
	// agent answering, e.g. when the agent isn't installed.
	bootWatchTimeout = 5 * time.Minute
	bootStartGrace   = 30 * time.Second
	lockWaitTimeout  = 30 * time.Minute
	minMemoryMiB     = 256
)

var (
	ErrBusy       = errors.New("the VM is busy with another operation")
	ErrNotReady   = errors.New("the VM is not ready")
	ErrBadRequest = errors.New("invalid request")
)

// Service owns the lifecycle of deployed VMs. Long-running work (deploys,
// teardowns, power actions) runs in background jobs that report progress
// through the store and the event hub.
type Service struct {
	cfg   *config.Config
	pve   *pve.Client
	store *store.Store
	hub   *Hub

	mu   sync.Mutex
	jobs map[string]*job
	// actions holds power actions in flight and the last one's error, per VM.
	actions map[string]actionState
	// consoles caches each ready VM's console capabilities.
	consoles map[string]consoleInfo
	// boots holds the boot watch of VMs whose guest OS is starting.
	boots map[string]*bootWatch

	// agents is nil when no agent is configured.
	agents *agentclient.Client
	probes agentProbes
}

type bootWatch struct{ cancel context.CancelFunc }

type job struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type actionState struct {
	Action pve.PowerAction
	Error  string
}

// NewService creates the VM service. agents may be nil if no agent is
// configured.
func NewService(cfg *config.Config, client *pve.Client, s *store.Store, hub *Hub, agents *agentclient.Client) *Service {
	return &Service{cfg: cfg, pve: client, store: s, hub: hub, jobs: map[string]*job{}, actions: map[string]actionState{},
		consoles: map[string]consoleInfo{}, boots: map[string]*bootWatch{},
		agents: agents, probes: agentProbes{results: map[string]agentProbe{}}}
}

// Recover handles jobs interrupted by a restart: unfinished deployments are
// marked failed (the user can destroy them) and teardowns are resumed.
func (s *Service) Recover(ctx context.Context) error {
	vms, err := s.store.VMs(ctx, 0)
	if err != nil {
		return err
	}
	for _, vm := range vms {
		switch vm.State {
		case store.VMDeploying:
			s.fail(vm, errors.New("interrupted by a server restart"))
		case store.VMDestroying:
			s.startJob(vm.ID, func(ctx context.Context) { s.runDestroy(ctx, vm) })
		}
	}
	return nil
}

// Shutdown cancels running jobs and waits for them to stop.
func (s *Service) Shutdown() {
	s.mu.Lock()
	jobs := make([]*job, 0, len(s.jobs))
	for _, j := range s.jobs {
		j.cancel()
		jobs = append(jobs, j)
	}
	s.mu.Unlock()
	for _, j := range jobs {
		<-j.done
	}
}

// DeployRequest is what a user chooses in the deploy dialog.
type DeployRequest struct {
	TemplateID string `json:"templateId"`
	Name       string `json:"name"`
	Cores      int    `json:"cores"`
	MemoryMiB  int    `json:"memoryMiB"`
	NetworkID  string `json:"networkId"`
	// Agent, for templates with the agent, connects the VM to the agent
	// network (default true). False deploys it without the agent NIC.
	Agent *bool `json:"agent"`
}

var nameInvalid = regexp.MustCompile(`[^a-z0-9-]+`)

// Deploy validates a request, reserves a VM ID and starts the deployment.
func (s *Service) Deploy(ctx context.Context, owner store.User, req DeployRequest) (store.VM, error) {
	tmpl, ok := s.cfg.Template(req.TemplateID)
	if !ok {
		return store.VM{}, fmt.Errorf("%w: unknown template", ErrBadRequest)
	}
	network, ok := s.cfg.Network(req.NetworkID)
	if req.NetworkID == "" {
		network, ok = s.cfg.DefaultNetwork()
	}
	if !ok {
		return store.VM{}, fmt.Errorf("%w: unknown network", ErrBadRequest)
	}
	defaults, err := s.TemplateDefaults(ctx, tmpl)
	if err != nil {
		return store.VM{}, err
	}
	if req.Cores == 0 {
		req.Cores = defaults.Cores
	}
	if req.MemoryMiB == 0 {
		req.MemoryMiB = defaults.MemoryMiB
	}
	if req.Cores < 1 || req.Cores > tmpl.MaxCores {
		return store.VM{}, fmt.Errorf("%w: CPUs must be between 1 and %d", ErrBadRequest, tmpl.MaxCores)
	}
	if req.MemoryMiB < minMemoryMiB || req.MemoryMiB > tmpl.MaxMemoryMiB {
		return store.VM{}, fmt.Errorf("%w: memory must be between %d and %d MiB", ErrBadRequest, minMemoryMiB, tmpl.MaxMemoryMiB)
	}

	id := newID()
	// Proxmox names must be valid DNS labels.
	name := strings.Trim(nameInvalid.ReplaceAllString(strings.ToLower(req.Name), "-"), "-")
	if name == "" {
		name = tmpl.ID + "-" + id[:6]
	}
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}

	// VM IDs Proxmox already uses (that we can see) are skipped.
	existing := map[int]bool{}
	if list, err := s.pve.ListVMs(ctx); err == nil {
		for _, v := range list {
			existing[v.VMID] = true
		}
	}
	vm, err := s.store.CreateVM(ctx, store.VM{
		ID: id, OwnerID: owner.ID, Name: name, TemplateID: tmpl.ID, Node: tmpl.Node,
		Cores: req.Cores, MemoryMiB: req.MemoryMiB, NetworkID: network.ID,
		State: store.VMDeploying, Step: "Queued", ExpiresAt: s.expiryFor(time.Now()),
	}, s.cfg.VMIDs.From, s.cfg.VMIDs.To, existing, s.quotaLimits(tmpl, owner)...)
	if err != nil {
		return store.VM{}, err
	}
	s.publish(vm)
	slog.Info("deploying vm", "id", vm.ID, "vmid", vm.VMID, "template", tmpl.ID, "owner", owner.Username)
	withAgent := tmpl.Agent && s.cfg.Agent != nil && (req.Agent == nil || *req.Agent)
	s.startJob(vm.ID, func(ctx context.Context) { s.runDeploy(ctx, vm, tmpl, network, owner, withAgent) })
	return vm, nil
}

// TemplateDefaults are the resources a deployment gets unless the user
// chooses otherwise: the config's values, else the template's own.
type TemplateDefaults struct {
	Cores     int
	MemoryMiB int
}

func (s *Service) TemplateDefaults(ctx context.Context, tmpl config.Template) (TemplateDefaults, error) {
	d := TemplateDefaults{Cores: tmpl.Cores, MemoryMiB: tmpl.MemoryMiB}
	if d.Cores > 0 && d.MemoryMiB > 0 {
		return d, nil
	}
	cfg, err := s.pve.VMConfig(ctx, tmpl.Node, tmpl.VMID)
	if err != nil {
		return d, fmt.Errorf("read template %s: %w", tmpl.ID, err)
	}
	if d.Cores == 0 {
		d.Cores = max(1, intValue(cfg["cores"]))
	}
	if d.MemoryMiB == 0 {
		// Proxmox's default when memory is unset.
		d.MemoryMiB = 512
		if m := intValue(cfg["memory"]); m > 0 {
			d.MemoryMiB = m
		}
	}
	d.Cores = min(d.Cores, tmpl.MaxCores)
	d.MemoryMiB = min(d.MemoryMiB, tmpl.MaxMemoryMiB)
	return d, nil
}

func (s *Service) runDeploy(ctx context.Context, vm store.VM, tmpl config.Template, network config.Network, owner store.User, hasAgent bool) {
	progress := func(step string, p float64) { s.progress(vm, store.VMDeploying, step, p) }

	// 1. Clone (0-70%). Full clones log per-disk progress; linked clones are quick.
	progress("Cloning template", 0.02)
	upid, err := s.pve.Clone(ctx, tmpl.Node, tmpl.VMID, pve.CloneOptions{
		NewID:       vm.VMID,
		Name:        vm.Name,
		Full:        tmpl.FullClone,
		Pool:        s.cfg.PVE.Pool,
		Description: fmt.Sprintf("Deployed by Quicksand for %s from %s.", owner.Username, tmpl.Name),
	})
	if err == nil {
		err = s.pve.WaitTask(ctx, tmpl.Node, upid, func(line string) {
			if pct, ok := parseCloneProgress(line); ok {
				progress("Cloning template", 0.02+0.68*pct)
			}
		})
	}
	if err != nil {
		s.fail(vm, fmt.Errorf("clone: %w", err))
		return
	}

	// 2. Configure (70-85%).
	progress("Configuring hardware", 0.7)
	cfg, err := s.pve.VMConfig(ctx, vm.Node, vm.VMID)
	if err != nil {
		s.fail(vm, fmt.Errorf("read config: %w", err))
		return
	}
	set := map[string]string{
		"cores":  strconv.Itoa(vm.Cores),
		"memory": strconv.Itoa(vm.MemoryMiB),
		"tags":   "quicksand",
	}
	// Ballooning would let the guest drop below what the user asked for.
	var remove []string
	if _, ok := cfg["balloon"]; ok {
		remove = append(remove, "balloon")
	}
	agentNIC := ""
	if hasAgent {
		agentNIC = s.cfg.Agent.Network.NIC
		nic, err := s.assignAgentNIC(ctx, &vm)
		if err != nil {
			s.fail(vm, fmt.Errorf("agent network: %w", err))
			return
		}
		set[agentNIC] = nic
	}
	if _, ok := cfg[tmpl.Serial]; tmpl.Serial != "" && !ok {
		set[tmpl.Serial] = "socket"
	}
	if network.Isolated() {
		// A NIC without a bridge would get QEMU user-mode networking (NAT
		// to the host's network), so isolation means no NIC at all.
		for key := range cfg {
			if isNetKey(key) && key != agentNIC {
				remove = append(remove, key)
			}
		}
	} else {
		net0, _ := cfg["net0"].(string)
		if net0 == "" {
			s.fail(vm, errors.New("the template has no network interface (net0)"))
			return
		}
		set["net0"] = pve.SetNetBridge(net0, *network.Bridge, network.VLAN)
	}
	if hasAgent {
		// Only the agent NIC goes through the VM firewall Quicksand turns on
		// below; the others stay as unfiltered as they were.
		for key, value := range cfg {
			if !isNetKey(key) || key == agentNIC || slices.Contains(remove, key) {
				continue
			}
			current, _ := value.(string)
			if v, ok := set[key]; ok {
				current = v
			}
			set[key] = pve.SetNetOption(current, "firewall", "0")
		}
	}
	if err := s.pve.UpdateConfig(ctx, vm.Node, vm.VMID, set, remove); err != nil {
		s.fail(vm, fmt.Errorf("configure: %w", err))
		return
	}
	if hasAgent {
		progress("Configuring the agent firewall", 0.8)
		if err := s.configureAgentFirewall(ctx, vm); err != nil {
			s.fail(vm, fmt.Errorf("agent firewall: %w", err))
			return
		}
	}

	// 3. Power on (85-100%). The VM is usable from here; the guest OS
	// booting is tracked separately (see watchBoot) so the console can be
	// opened during boot and guests without an agent never block.
	progress("Powering on", 0.85)
	if upid, err = s.pve.Power(ctx, vm.Node, vm.VMID, pve.PowerStart); err == nil {
		err = s.pve.WaitTask(ctx, vm.Node, upid, nil)
	}
	if err != nil {
		s.fail(vm, fmt.Errorf("power on: %w", err))
		return
	}
	s.progress(vm, store.VMReady, "", 1)
	slog.Info("vm ready", "id", vm.ID, "vmid", vm.VMID, "agentIP", vm.AgentIP)
	if agentEnabled(cfg) || vm.AgentIP != "" {
		s.watchBoot(vm, agentEnabled(cfg))
	}
}

// watchBoot marks a VM as booting until an agent answers (the QEMU guest
// agent if qemuAgent, or the Quicksand agent if the VM has one), giving up
// after bootWatchTimeout or when the VM stops.
func (s *Service) watchBoot(vm store.VM, qemuAgent bool) {
	ctx, cancel := context.WithTimeout(context.Background(), bootWatchTimeout)
	w := &bootWatch{cancel: cancel}
	s.mu.Lock()
	if prev := s.boots[vm.ID]; prev != nil {
		prev.cancel()
	}
	s.boots[vm.ID] = w
	s.mu.Unlock()
	s.publish(vm)

	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			// A newer watch (e.g. after a reboot) may have replaced this one.
			if s.boots[vm.ID] == w {
				delete(s.boots, vm.ID)
			}
			s.mu.Unlock()
			s.publish(vm)
		}()
		// A reboot task finishes while the VM is still stopped, before
		// Proxmox starts it again; only a VM seen running (or still down
		// after the grace period) has really stopped.
		started, seenRunning := time.Now(), false
		for {
			if qemuAgent && s.pve.AgentPing(ctx, vm.Node, vm.VMID) == nil || s.agentAnswers(ctx, vm) {
				return
			}
			if st, err := s.pve.Status(ctx, vm.Node, vm.VMID); err == nil {
				running := st.PowerState() == "running"
				seenRunning = seenRunning || running
				if !running && (seenRunning || time.Since(started) > bootStartGrace) {
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}()
}

// Booting reports whether a VM's guest OS is still starting up.
func (s *Service) Booting(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.boots[id] != nil
}

func (s *Service) stopBootWatch(id string) {
	s.mu.Lock()
	if w := s.boots[id]; w != nil {
		w.cancel()
		delete(s.boots, id)
	}
	s.mu.Unlock()
}

// Destroy stops and deletes a VM, cancelling its deployment if one is running.
func (s *Service) Destroy(ctx context.Context, vm store.VM) error {
	if vm.State == store.VMDestroying {
		return nil
	}
	s.cancelJob(vm.ID)
	s.stopBootWatch(vm.ID)
	s.progress(vm, store.VMDestroying, "Stopping", 0)
	slog.Info("destroying vm", "id", vm.ID, "vmid", vm.VMID)
	s.startJob(vm.ID, func(ctx context.Context) { s.runDestroy(ctx, vm) })
	return nil
}

func (s *Service) runDestroy(ctx context.Context, vm store.VM) {
	// A VM the token can't see doesn't exist (e.g. its clone failed):
	// Proxmox answers 403 rather than 404 for VM IDs outside our pool.
	if list, err := s.pve.ListVMs(ctx); err == nil {
		exists := false
		for _, v := range list {
			exists = exists || v.VMID == vm.VMID
		}
		if !exists {
			s.remove(vm)
			return
		}
	}
	status, err := s.pve.Status(ctx, vm.Node, vm.VMID)
	if pve.IsNotFound(err) {
		s.remove(vm)
		return
	}
	// A cancelled deployment can leave a clone task running on Proxmox,
	// which keeps the VM locked until it finishes.
	for deadline := time.Now().Add(lockWaitTimeout); err == nil && status.Lock != "" && time.Now().Before(deadline); {
		s.progress(vm, store.VMDestroying, "Waiting for Proxmox ("+status.Lock+")", 0.1)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		status, err = s.pve.Status(ctx, vm.Node, vm.VMID)
	}
	if err == nil && status.Status != "stopped" {
		s.progress(vm, store.VMDestroying, "Stopping", 0.2)
		var upid pve.UPID
		if upid, err = s.pve.Power(ctx, vm.Node, vm.VMID, pve.PowerStop); err == nil {
			err = s.pve.WaitTask(ctx, vm.Node, upid, nil)
		}
	}
	if err == nil {
		s.progress(vm, store.VMDestroying, "Deleting", 0.6)
		var upid pve.UPID
		if upid, err = s.pve.Destroy(ctx, vm.Node, vm.VMID); err == nil {
			err = s.pve.WaitTask(ctx, vm.Node, upid, nil)
		}
	}
	if err != nil && !pve.IsNotFound(err) {
		if ctx.Err() != nil {
			return
		}
		slog.Warn("destroy failed", "id", vm.ID, "vmid", vm.VMID, "err", err)
		s.progress(vm, store.VMFailed, "", 0, fmt.Sprintf("destroy: %v", err))
		return
	}
	s.remove(vm)
}

// Power runs a power action on a ready VM. It returns once Proxmox has
// accepted the action; completion is reported through events.
func (s *Service) Power(ctx context.Context, vm store.VM, action pve.PowerAction) error {
	if vm.State != store.VMReady {
		return ErrNotReady
	}
	s.mu.Lock()
	if _, busy := s.jobs[vm.ID]; busy || s.actions[vm.ID].Action != "" {
		s.mu.Unlock()
		return ErrBusy
	}
	s.actions[vm.ID] = actionState{Action: action}
	s.mu.Unlock()

	upid, err := s.pve.Power(ctx, vm.Node, vm.VMID, action)
	if err != nil {
		s.setAction(vm, actionState{Error: err.Error()})
		return err
	}
	s.publish(vm)
	s.startJob(vm.ID, func(ctx context.Context) {
		err := s.pve.WaitTask(ctx, vm.Node, upid, nil)
		state := actionState{}
		if err != nil && ctx.Err() == nil {
			state.Error = fmt.Sprintf("%s: %v", action, err)
		}
		bootsOS := action == pve.PowerStart || action == pve.PowerReboot || action == pve.PowerReset
		if err == nil && bootsOS {
			if info, ok := s.consoleInfo(ctx, vm); ok && (info.agent || vm.AgentIP != "") {
				s.watchBoot(vm, info.agent)
			}
		}
		s.setAction(vm, state)
	})
	return nil
}

// Action returns the power action in flight for a VM and the last error.
func (s *Service) Action(id string) (pve.PowerAction, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.actions[id]
	return a.Action, a.Error
}

func (s *Service) setAction(vm store.VM, a actionState) {
	s.mu.Lock()
	if a == (actionState{}) {
		delete(s.actions, vm.ID)
	} else {
		s.actions[vm.ID] = a
	}
	s.mu.Unlock()
	s.publish(vm)
}

func (s *Service) progress(vm store.VM, state store.VMState, step string, p float64, errMsg ...string) {
	msg := strings.Join(errMsg, "")
	if err := s.store.UpdateVMProgress(context.Background(), vm.ID, state, step, p, msg); err != nil {
		slog.Error("record vm progress", "id", vm.ID, "err", err)
	}
	s.publish(vm)
}

func (s *Service) fail(vm store.VM, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	slog.Warn("deployment failed", "id", vm.ID, "vmid", vm.VMID, "err", err)
	s.progress(vm, store.VMFailed, "", 0, err.Error())
}

func (s *Service) remove(vm store.VM) {
	if err := s.store.DeleteVM(context.Background(), vm.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.Error("delete vm record", "id", vm.ID, "err", err)
	}
	s.mu.Lock()
	delete(s.actions, vm.ID)
	delete(s.consoles, vm.ID)
	s.mu.Unlock()
	s.forgetProbe(vm.ID)
	slog.Info("vm destroyed", "id", vm.ID, "vmid", vm.VMID)
	s.hub.Publish(Event{VMID: vm.ID, OwnerID: vm.OwnerID, Deleted: true})
}

func (s *Service) publish(vm store.VM) {
	s.hub.Publish(Event{VMID: vm.ID, OwnerID: vm.OwnerID})
}

// startJob runs fn in the background as the VM's current job.
func (s *Service) startJob(id string, fn func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.jobs[id] = j
	s.mu.Unlock()
	go func() {
		defer close(j.done)
		defer cancel()
		fn(ctx)
		s.mu.Lock()
		if s.jobs[id] == j {
			delete(s.jobs, id)
		}
		s.mu.Unlock()
	}()
}

// cancelJob stops a VM's running job, if any, and waits for it to exit.
func (s *Service) cancelJob(id string) {
	s.mu.Lock()
	j := s.jobs[id]
	s.mu.Unlock()
	if j != nil {
		j.cancel()
		<-j.done
	}
}

// Clone task logs report progress like
// "drive-scsi0: transferred 1.2 GiB of 32.0 GiB (3.75%)".
var cloneProgress = regexp.MustCompile(`\((\d+(?:\.\d+)?)%\)`)

func parseCloneProgress(line string) (float64, bool) {
	m := cloneProgress.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	pct, err := strconv.ParseFloat(m[1], 64)
	return pct / 100, err == nil
}

// isNetKey matches network interface properties: net0, net1, ...
func isNetKey(key string) bool {
	n, ok := strings.CutPrefix(key, "net")
	if !ok || n == "" {
		return false
	}
	_, err := strconv.Atoi(n)
	return err == nil
}

func agentEnabled(cfg map[string]any) bool {
	v, _ := cfg["agent"].(string)
	first, _, _ := strings.Cut(v, ",")
	return first == "1" || first == "enabled=1"
}

func intValue(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
