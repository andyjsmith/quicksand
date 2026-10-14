package vms

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"quicksand/internal/agentclient"
	"quicksand/internal/agentproto"
	"quicksand/internal/pve"
	"quicksand/internal/store"
)

const (
	agentProbeTTL     = 10 * time.Second
	agentProbeTimeout = 3 * time.Second
)

// ErrNoAgent is returned for agent operations on VMs without an agent.
var ErrNoAgent = errors.New("this VM has no agent")

// AgentView is an agent's state as the web client sees it.
type AgentView struct {
	IP string `json:"ip"`
	// Status is connected or unreachable.
	Status   string `json:"status"`
	Hostname string `json:"hostname,omitempty"`
	OS       string `json:"os,omitempty"`
	Version  string `json:"version,omitempty"`
	Error    string `json:"error,omitempty"`
}

// agentProbes caches recent agent probes so listing VMs stays cheap.
type agentProbes struct {
	mu      sync.Mutex
	results map[string]agentProbe
}

type agentProbe struct {
	at   time.Time
	info agentproto.Info
	err  error
}

// assignAgentNIC reserves the VM's agent address and returns the NIC
// property for it.
func (s *Service) assignAgentNIC(ctx context.Context, vm *store.VM) (string, error) {
	a := s.cfg.Agent
	network := a.ParsedNetwork()
	server, _ := network.Offset(mustAddr(a.Network.ServerIP))
	ip, err := s.store.AssignAgentIP(ctx, vm.ID, func(i int) (string, bool) {
		// Offsets 1.. in order, skipping Quicksand's own address.
		off := i + 1
		if off >= server {
			off++
		}
		addr, err := network.Address(off)
		return addr.String(), err == nil
	})
	if err != nil {
		return "", err
	}
	vm.AgentIP = ip
	off, _ := network.Offset(mustAddr(ip))
	mac := agentproto.FormatMAC(network.MAC(off))
	return fmt.Sprintf("%s=%s,bridge=%s,firewall=1", a.Network.Model, mac, a.Network.Bridge), nil
}

// configureAgentFirewall locks the agent NIC down: only Quicksand may
// connect, only to the agent port, and the guest can only use its assigned
// address. Other NICs have the firewall disabled, so they are unaffected.
func (s *Service) configureAgentFirewall(ctx context.Context, vm store.VM) error {
	a := s.cfg.Agent
	nic := a.Network.NIC
	if err := s.pve.SetIPFilter(ctx, vm.Node, vm.VMID, nic, vm.AgentIP); err != nil {
		return fmt.Errorf("ip filter: %w", err)
	}
	if err := s.pve.AddFirewallRule(ctx, vm.Node, vm.VMID, pve.FirewallRule{
		Type: "in", Action: "ACCEPT", Iface: nic, Proto: "tcp", Dport: strconv.Itoa(a.Port),
		Source: a.Network.ServerIP, Comment: "Quicksand: agent connections from the Quicksand server",
	}); err != nil {
		return fmt.Errorf("firewall rule: %w", err)
	}
	return s.pve.SetFirewallOptions(ctx, vm.Node, vm.VMID, map[string]string{
		"enable":     "1",
		"policy_in":  "DROP",
		"policy_out": "DROP",
		"ipfilter":   "1",
		"macfilter":  "1",
		"dhcp":       "0",
		"ndp":        "0",
		"radv":       "0",
	})
}

// AgentInfo asks a VM's agent about its guest.
func (s *Service) AgentInfo(ctx context.Context, vm store.VM) (agentproto.Info, error) {
	if vm.AgentIP == "" || s.agents == nil {
		return agentproto.Info{}, ErrNoAgent
	}
	info, err := s.agents.Info(ctx, vm.AgentIP)
	s.recordProbe(vm.ID, info, err)
	return info, err
}

// AgentExec runs a command in a VM through its agent.
func (s *Service) AgentExec(ctx context.Context, vm store.VM, req agentproto.ExecRequest) (agentproto.ExecResponse, error) {
	g, err := s.GuestAgent(vm)
	if err != nil {
		return agentproto.ExecResponse{}, err
	}
	return g.Exec(ctx, g.Host, req)
}

// GuestAgent is a deployed VM's agent, ready to use.
type GuestAgent struct {
	*agentclient.Client
	Host string
	// FilesDir is the template's starting directory for files and shells,
	// empty for the agent's default.
	FilesDir string
}

// GuestAgent returns the agent of a ready VM.
func (s *Service) GuestAgent(vm store.VM) (GuestAgent, error) {
	if vm.AgentIP == "" || s.agents == nil {
		return GuestAgent{}, ErrNoAgent
	}
	if vm.State != store.VMReady {
		return GuestAgent{}, ErrNotReady
	}
	g := GuestAgent{Client: s.agents, Host: vm.AgentIP}
	if t, ok := s.cfg.Template(vm.TemplateID); ok {
		g.FilesDir = t.FilesDir
	}
	return g, nil
}

// File names a guest path for the agent: absolute, or relative to the
// template's starting directory.
func (g GuestAgent) File(path string) agentclient.FileRef {
	return agentclient.FileRef{Base: g.FilesDir, Path: path}
}

// agentView reports a running VM's agent, probing it at most every
// agentProbeTTL.
func (s *Service) agentView(ctx context.Context, vm store.VM, running bool) *AgentView {
	if vm.AgentIP == "" {
		return nil
	}
	v := &AgentView{IP: vm.AgentIP, Status: "unreachable"}
	if !running || s.agents == nil {
		return v
	}
	s.probes.mu.Lock()
	p, ok := s.probes.results[vm.ID]
	s.probes.mu.Unlock()
	if !ok || time.Since(p.at) > agentProbeTTL {
		pctx, cancel := context.WithTimeout(ctx, agentProbeTimeout)
		info, err := s.agents.Info(pctx, vm.AgentIP)
		cancel()
		p = s.recordProbe(vm.ID, info, err)
	}
	if p.err != nil {
		v.Error = p.err.Error()
		return v
	}
	v.Status, v.Hostname, v.OS, v.Version = "connected", p.info.Hostname, p.info.OS, p.info.AgentVersion
	return v
}

func (s *Service) recordProbe(id string, info agentproto.Info, err error) agentProbe {
	p := agentProbe{at: time.Now(), info: info, err: err}
	s.probes.mu.Lock()
	s.probes.results[id] = p
	s.probes.mu.Unlock()
	return p
}

func (s *Service) forgetProbe(id string) {
	s.probes.mu.Lock()
	delete(s.probes.results, id)
	s.probes.mu.Unlock()
}

// agentAnswers reports whether a VM's agent responds, for boot tracking.
func (s *Service) agentAnswers(ctx context.Context, vm store.VM) bool {
	if vm.AgentIP == "" || s.agents == nil {
		return false
	}
	pctx, cancel := context.WithTimeout(ctx, agentProbeTimeout)
	defer cancel()
	info, err := s.agents.Info(pctx, vm.AgentIP)
	s.recordProbe(vm.ID, info, err)
	return err == nil
}

func mustAddr(s string) netip.Addr {
	a, _ := netip.ParseAddr(s)
	return a
}
