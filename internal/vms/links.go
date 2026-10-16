package vms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"quicksand/internal/agentclient"
	"quicksand/internal/agentproto"
	"quicksand/internal/pve"
	"quicksand/internal/store"
)

// Link networks are private networks users join their VMs into. Each is a
// VLAN (on the configured bridge, which has no uplink); a linked VM gets an
// extra NIC tagged with its network's VLAN, unfiltered, so members reach
// each other as if on one switch, and nothing else. A VM keeps its address
// on link networks for life, so merging networks only retags NICs.
//
// The database is the source of truth. NICs and agent addresses are brought
// in line with it after every change, and again periodically, which also
// repairs what a snapshot revert or guest reboot undoes.

// ErrLinksDisabled is returned when the server has no links configuration.
var ErrLinksDisabled = errors.New("link networks aren't configured on this server")

// linkSyncInterval is how often link NICs and agent addresses are checked.
const linkSyncInterval = 30 * time.Second

// LinkView is a VM's place on a link network, for the web client.
type LinkView struct {
	Network int64  `json:"network"`
	Name    string `json:"name"`
	// Address is the VM's address with its prefix length, e.g. "10.98.0.5/16".
	Address string `json:"address"`
	// MAC identifies the link NIC in the guest, for setting Address by hand.
	MAC string `json:"mac"`
	// Status is "configured" (the agent set Address), "pending" (being set
	// up) or "manual" (no agent to set it: the user must).
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type linkState struct {
	status, err string
}

// LinksInfo describes link networks to the web client.
type LinksInfo struct {
	Enabled bool   `json:"enabled"`
	Subnet  string `json:"subnet,omitempty"`
}

func (s *Service) LinksInfo() LinksInfo {
	if s.cfg.Links == nil {
		return LinksInfo{}
	}
	return LinksInfo{Enabled: true, Subnet: s.cfg.Links.ParsedNetwork().Subnet.String()}
}

// CheckLinks verifies that link VLANs can't reach the agent network: when
// the agent's VNet is on the links bridge, its tag must be outside the
// links range. It needs SDN.Audit; without it, only a warning is logged.
func (s *Service) CheckLinks(ctx context.Context) error {
	l, a := s.cfg.Links, s.cfg.Agent
	if l == nil || a == nil {
		return nil
	}
	if a.Network.Bridge == l.Bridge {
		return fmt.Errorf("links.bridge %s is the agent bridge itself; use the bridge the agent VNet is on", l.Bridge)
	}
	vnet, err := s.pve.VNet(ctx, a.Network.Bridge)
	if pve.IsNotFound(err) {
		return nil // A plain bridge, not an SDN VNet.
	}
	if err == nil {
		var zone pve.Zone
		if zone, err = s.pve.Zone(ctx, vnet.Zone); err == nil {
			if zone.Bridge == l.Bridge && vnet.Tag >= l.VLANs.From && vnet.Tag <= l.VLANs.To {
				return fmt.Errorf("links.vlans %d-%d includes the agent network's VLAN %d on %s; linked VMs would join the agent network",
					l.VLANs.From, l.VLANs.To, vnet.Tag, l.Bridge)
			}
			return nil
		}
	}
	slog.Warn("can't check links.vlans against the agent network's VLAN (the API token needs SDN.Audit); make sure they don't overlap", "err", err)
	return nil
}

// Link joins vm and others into one link network: the one any of them is
// in, a new one if none is, or (with merge) the oldest of several, whose
// members all move into it.
func (s *Service) Link(ctx context.Context, vm store.VM, others []store.VM, merge bool) (store.LinkNetwork, error) {
	l := s.cfg.Links
	if l == nil {
		return store.LinkNetwork{}, ErrLinksDisabled
	}
	ids := []string{vm.ID}
	for _, o := range others {
		if !slices.Contains(ids, o.ID) {
			ids = append(ids, o.ID)
		}
	}
	if len(ids) < 2 {
		return store.LinkNetwork{}, fmt.Errorf("%w: choose at least one VM to link with", ErrBadRequest)
	}
	for _, v := range append([]store.VM{vm}, others...) {
		if v.State != store.VMReady {
			return store.LinkNetwork{}, fmt.Errorf("%s: %w", v.Name, ErrNotReady)
		}
	}
	network := l.ParsedNetwork()
	alloc := store.LinkAllocator{VLANFrom: l.VLANs.From, VLANTo: l.VLANs.To, NextIP: func(i int) (string, bool) {
		addr, err := network.Address(i + 1)
		return addr.String(), err == nil
	}}

	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	change, err := s.store.Link(ctx, ids, merge, alloc)
	if err != nil {
		return store.LinkNetwork{}, err
	}
	slog.Info("linked vms", "network", change.Network.ID, "vlan", change.Network.VLAN, "vms", ids, "moved", len(change.Changed))
	return change.Network, s.applyLinkChange(ctx, change)
}

// Unlink takes a VM out of its link network; a network left with a single
// VM is removed, and that VM unlinked too.
func (s *Service) Unlink(ctx context.Context, vm store.VM) error {
	if s.cfg.Links == nil {
		return ErrLinksDisabled
	}
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	change, err := s.store.Unlink(ctx, vm.ID)
	if err != nil {
		return err
	}
	slog.Info("unlinked vm", "id", vm.ID, "unlinked", len(change.Changed))
	return s.applyLinkChange(ctx, change)
}

// leaveLinks unlinks a VM being destroyed, so its network updates (or
// dissolves) right away. Its own NIC goes with it.
func (s *Service) leaveLinks(ctx context.Context, vm store.VM) {
	if vm.LinkID == 0 {
		return
	}
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	change, err := s.store.Unlink(ctx, vm.ID)
	if err != nil {
		slog.Warn("unlink destroyed vm", "id", vm.ID, "err", err)
		return
	}
	change.Changed = slices.DeleteFunc(change.Changed, func(v store.VM) bool { return v.ID == vm.ID })
	if err := s.applyLinkChange(ctx, change); err != nil {
		slog.Warn("unlink destroyed vm", "id", vm.ID, "err", err)
	}
}

// RenameLinkNetwork names a link network the user has a VM in (any, for
// administrators).
func (s *Service) RenameLinkNetwork(ctx context.Context, user store.User, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 40 {
		return fmt.Errorf("%w: a network name is 1 to 40 characters", ErrBadRequest)
	}
	members, err := s.linkMembers(ctx, id)
	if err != nil {
		return err
	}
	if !user.IsAdmin && !slices.ContainsFunc(members, func(v store.VM) bool { return v.OwnerID == user.ID }) {
		return store.ErrNotFound
	}
	if err := s.store.RenameLinkNetwork(ctx, id, name); err != nil {
		return err
	}
	for _, v := range members {
		s.publish(v)
	}
	return nil
}

func (s *Service) linkMembers(ctx context.Context, id int64) ([]store.VM, error) {
	all, err := s.store.VMs(ctx, 0)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(v store.VM) bool { return v.LinkID != id }), nil
}

// applyLinkChange brings the NICs of the VMs a link operation moved in
// line, then has their agents address them. The caller holds linkMu.
func (s *Service) applyLinkChange(ctx context.Context, change store.LinkChange) error {
	var errs []error
	for _, v := range change.Changed {
		if err := s.syncLinkNIC(ctx, v, change.Network.VLAN); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", v.Name, err))
		}
		s.setLinkState(v, s.initialLinkState(v))
		go s.pushLinks(v.ID, 10)
		// The link rules come and go with the link.
		if err := s.syncFirewall(ctx, v); err != nil {
			errs = append(errs, fmt.Errorf("%s firewall: %w", v.Name, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		slog.Warn("configure link NICs", "err", err)
		return fmt.Errorf("linked, but setting up the network adapter failed (it's retried shortly): %w", err)
	}
	return nil
}

// linkNIC is the link NIC property for a VM on a VLAN. It goes through
// the firewall only when the VM's link network has rules (see firewall.go).
func (s *Service) linkNIC(ctx context.Context, vm store.VM, vlan int) (string, error) {
	l := s.cfg.Links
	network := l.ParsedNetwork()
	addr, err := netip.ParseAddr(vm.LinkIP)
	if err != nil {
		return "", fmt.Errorf("link address %q: %w", vm.LinkIP, err)
	}
	off, err := network.Offset(addr)
	if err != nil {
		return "", err
	}
	mac := agentproto.FormatMAC(network.MAC(off))
	firewall := 0
	if s.linkFiltered(ctx, vm) {
		firewall = 1
	}
	return fmt.Sprintf("%s=%s,bridge=%s,tag=%d,firewall=%d", l.Model, mac, l.Bridge, vlan, firewall), nil
}

// syncLinkNIC gives a VM its link NIC on vlan, or removes it for 0, unless
// it's already that way. Proxmox hot-plugs and retags NICs of running VMs.
func (s *Service) syncLinkNIC(ctx context.Context, vm store.VM, vlan int) error {
	l := s.cfg.Links
	cfg, err := s.pve.VMConfig(ctx, vm.Node, vm.VMID)
	if err != nil {
		return err
	}
	current, _ := cfg[l.NIC].(string)
	if vlan == 0 {
		if current == "" {
			return nil
		}
		return s.pve.UpdateConfig(ctx, vm.Node, vm.VMID, nil, []string{l.NIC})
	}
	want, err := s.linkNIC(ctx, vm, vlan)
	if err != nil {
		return err
	}
	if sameNIC(current, want) {
		return nil
	}
	return s.pve.UpdateConfig(ctx, vm.Node, vm.VMID, map[string]string{l.NIC: want}, nil)
}

// nicOptions are the netX options other than the model (whose value is
// the MAC address).
var nicOptions = map[string]bool{"bridge": true, "tag": true, "firewall": true, "link_down": true,
	"mtu": true, "queues": true, "rate": true, "trunks": true, "macaddr": true}

// sameNIC compares the parts of two netX properties that matter for links:
// model and MAC, bridge, VLAN tag and firewall, and that it's connected.
func sameNIC(a, b string) bool {
	oa, ob := pve.NetOptions(a), pve.NetOptions(b)
	for _, k := range []string{"bridge", "tag", "firewall", "link_down"} {
		if oa[k] != ob[k] {
			return false
		}
	}
	return strings.EqualFold(nicModel(oa), nicModel(ob))
}

func nicModel(opts map[string]string) string {
	for k, v := range opts {
		if !nicOptions[k] {
			return k + "=" + v
		}
	}
	return ""
}

func (s *Service) initialLinkState(vm store.VM) linkState {
	if vm.AgentIP == "" || s.agents == nil {
		return linkState{status: "manual"}
	}
	return linkState{status: "pending"}
}

func (s *Service) setLinkState(vm store.VM, st linkState) {
	s.mu.Lock()
	old, known := s.linkStates[vm.ID]
	s.linkStates[vm.ID] = st
	s.mu.Unlock()
	if !known || old != st {
		s.publish(vm)
	}
}

// pushLinks tells a VM's agent its link addresses (none when unlinked),
// trying up to attempts times while the NIC appears or the agent starts.
func (s *Service) pushLinks(id string, attempts int) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(attempts)*5*time.Second)
	defer cancel()
	for i := 0; i < attempts; i++ {
		if i > 0 && !sleepCtx(ctx, 3*time.Second) {
			return
		}
		vm, err := s.store.VM(ctx, id)
		if err != nil {
			return
		}
		if vm.AgentIP == "" || s.agents == nil {
			if vm.LinkID != 0 {
				s.setLinkState(vm, linkState{status: "manual"})
			}
			return
		}
		req := agentproto.LinkRequest{Links: []agentproto.Link{}}
		if vm.LinkID != 0 {
			addr, mac, err := s.linkAddress(vm)
			if err != nil {
				s.setLinkState(vm, linkState{status: "manual", err: err.Error()})
				return
			}
			req.Links = append(req.Links, agentproto.Link{MAC: mac, Address: addr})
		}
		pctx, pcancel := context.WithTimeout(ctx, 5*time.Second)
		res, err := s.agents.SetLinks(pctx, vm.AgentIP, req)
		pcancel()
		var ae *agentclient.Error
		switch {
		case errors.As(err, &ae) && ae.Status == 404:
			if vm.LinkID != 0 {
				s.setLinkState(vm, linkState{status: "manual", err: "this VM's agent is too old to set the address"})
			}
			return
		case err != nil:
			if vm.LinkID != 0 {
				s.setLinkState(vm, linkState{status: "pending", err: "waiting for the agent"})
			}
			continue
		}
		if vm.LinkID == 0 {
			return
		}
		st := linkState{status: "configured"}
		for _, l := range res.Links {
			if !l.Configured {
				st = linkState{status: "pending", err: l.Error}
			}
		}
		s.setLinkState(vm, st)
		if st.status == "configured" {
			return
		}
	}
}

// linkAddress returns a linked VM's address (with prefix length) and NIC MAC.
func (s *Service) linkAddress(vm store.VM) (string, string, error) {
	network := s.cfg.Links.ParsedNetwork()
	addr, err := netip.ParseAddr(vm.LinkIP)
	if err != nil {
		return "", "", err
	}
	off, err := network.Offset(addr)
	if err != nil {
		return "", "", err
	}
	return netip.PrefixFrom(addr, network.Subnet.Bits()).String(), network.MAC(off).String(), nil
}

// RunLinks periodically brings link NICs and agent addresses in line with
// the database, until ctx ends.
func (s *Service) RunLinks(ctx context.Context) {
	if s.cfg.Links == nil {
		return
	}
	for {
		s.syncLinks(ctx)
		if !sleepCtx(ctx, linkSyncInterval) {
			return
		}
	}
}

func (s *Service) syncLinks(ctx context.Context) {
	vms, err := s.store.VMs(ctx, 0)
	if err != nil {
		return
	}
	for _, vm := range vms {
		// Only VMs that were ever linked can have a link NIC.
		if vm.State == store.VMReady && vm.LinkIP != "" {
			s.syncLink(ctx, vm.ID)
		}
	}
}

// syncLink repairs one VM's link NIC and re-sends its agent addresses
// (which an agent forgets when it restarts). The VM and its network are
// read under linkMu: a link change may have happened since the caller
// looked.
func (s *Service) syncLink(ctx context.Context, id string) {
	s.linkMu.Lock()
	vm, err := s.store.VM(ctx, id)
	if err != nil || vm.State != store.VMReady {
		s.linkMu.Unlock()
		return
	}
	vlan := 0
	if vm.LinkID != 0 {
		networks, err := s.store.LinkNetworks(ctx)
		if err != nil {
			s.linkMu.Unlock()
			return
		}
		if i := slices.IndexFunc(networks, func(n store.LinkNetwork) bool { return n.ID == vm.LinkID }); i >= 0 {
			vlan = networks[i].VLAN
		}
	}
	err = s.syncLinkNIC(ctx, vm, vlan)
	s.linkMu.Unlock()
	if err != nil && ctx.Err() == nil {
		slog.Warn("sync link NIC", "id", vm.ID, "vmid", vm.VMID, "err", err)
	}
	if vm.LinkID != 0 {
		s.pushLinks(vm.ID, 1)
	}
}

// resyncLink re-applies a VM's link setup now, e.g. after a snapshot
// revert restored an older NIC configuration.
func (s *Service) resyncLink(ctx context.Context, id string) {
	if s.cfg.Links == nil {
		return
	}
	s.syncLink(ctx, id)
	go s.pushLinks(id, 10)
}

// linkView describes a VM's link network, if it's in one.
func (s *Service) linkView(vm store.VM, networks []store.LinkNetwork) *LinkView {
	if vm.LinkID == 0 || s.cfg.Links == nil {
		return nil
	}
	v := &LinkView{Network: vm.LinkID}
	if i := slices.IndexFunc(networks, func(n store.LinkNetwork) bool { return n.ID == vm.LinkID }); i >= 0 {
		v.Name = networks[i].Name
	} else {
		v.Name = "Network " + strconv.FormatInt(vm.LinkID, 10)
	}
	v.Address, v.MAC, _ = s.linkAddress(vm)
	s.mu.Lock()
	st, ok := s.linkStates[vm.ID]
	s.mu.Unlock()
	if !ok {
		st = s.initialLinkState(vm)
	}
	v.Status, v.Error = st.status, st.err
	return v
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
