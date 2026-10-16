package vms

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"quicksand/internal/pve"
	"quicksand/internal/store"
)

// Users filter their VM's traffic per network: Egress (the user network,
// net0) and Link (the link network's NIC). Each has a default per
// direction and an ordered list of rules, which Quicksand turns into
// Proxmox firewall rules scoped to that NIC (iface), so they can never
// touch the agent NIC. A NIC only goes through the firewall while its
// network filters anything; otherwise it stays as unfiltered as before.
//
// The settings live in the database; the Proxmox rules Quicksand writes are
// marked by their comment and brought back in line after every change and
// periodically (firewallSyncInterval), which also undoes edits made in
// Proxmox directly.

const (
	firewallMark         = "Quicksand firewall:"
	firewallSyncInterval = time.Minute
	maxFirewallRules     = 100
)

// ErrNotApplied wraps a failure to apply settings that were saved (and
// that the sync loop keeps trying to apply).
var ErrNotApplied = errors.New("saved, but applying it failed (it's retried shortly)")

// FirewallRule is one rule, as users write it.
type FirewallRule struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	// Direction is "out" (traffic the VM sends) or "in".
	Direction string `json:"direction"`
	// Action is "allow" or "block".
	Action string `json:"action"`
	// Protocol is "any", "tcp", "udp" or "icmp".
	Protocol string `json:"protocol"`
	// Remote is "any", "private" (privateRanges) or addresses and CIDRs
	// separated by commas.
	Remote string `json:"remote"`
	// Ports, for TCP and UDP: "443", "8000-8080", "80,443"; empty for all.
	Ports   string `json:"ports"`
	Comment string `json:"comment"`
}

// NetworkFirewall is one network's settings: the rules, checked in order,
// then the default for the direction ("allow" or "block").
type NetworkFirewall struct {
	Outbound string         `json:"outbound"`
	Inbound  string         `json:"inbound"`
	Rules    []FirewallRule `json:"rules"`
}

// Firewall is a VM's firewall settings.
type Firewall struct {
	Egress NetworkFirewall `json:"egress"`
	Link   NetworkFirewall `json:"link"`
}

func defaultFirewall() Firewall {
	return Firewall{
		Egress: NetworkFirewall{Outbound: "allow", Inbound: "allow", Rules: []FirewallRule{}},
		Link:   NetworkFirewall{Outbound: "allow", Inbound: "allow", Rules: []FirewallRule{}},
	}
}

// filters reports whether the network filters anything at all.
func (n NetworkFirewall) filters() bool {
	return n.Outbound == "block" || n.Inbound == "block" ||
		slices.ContainsFunc(n.Rules, func(r FirewallRule) bool { return r.Enabled })
}

var (
	anyAddress = []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"}
	privateV4  = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}
	privateV6  = []string{"fc00::/7", "fe80::/10"}
	portsRe    = regexp.MustCompile(`^\d{1,5}(-\d{1,5})?(,\d{1,5}(-\d{1,5})?)*$`)
)

// normalize checks the settings and tidies them: trimmed values, canonical
// addresses, and an ID for each rule.
func (f *Firewall) normalize() error {
	if err := f.Egress.normalize("egress"); err != nil {
		return err
	}
	return f.Link.normalize("link network")
}

func (n *NetworkFirewall) normalize(name string) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s: %s", ErrBadRequest, name, fmt.Sprintf(format, args...))
	}
	for _, d := range []*string{&n.Outbound, &n.Inbound} {
		if *d == "" {
			*d = "allow"
		}
		if *d != "allow" && *d != "block" {
			return bad("the default must be allow or block")
		}
	}
	if len(n.Rules) > maxFirewallRules {
		return bad("at most %d rules", maxFirewallRules)
	}
	if n.Rules == nil {
		n.Rules = []FirewallRule{}
	}
	ids := map[string]bool{}
	for i := range n.Rules {
		r := &n.Rules[i]
		if r.ID == "" || ids[r.ID] || len(r.ID) > 40 {
			r.ID = newRuleID()
		}
		ids[r.ID] = true
		if r.Direction != "out" && r.Direction != "in" {
			return bad("rule %d: direction must be out or in", i+1)
		}
		if r.Action != "allow" && r.Action != "block" {
			return bad("rule %d: action must be allow or block", i+1)
		}
		if !slices.Contains([]string{"any", "tcp", "udp", "icmp"}, r.Protocol) {
			return bad("rule %d: protocol must be any, tcp, udp or icmp", i+1)
		}
		remote, err := normalizeRemote(r.Remote)
		if err != nil {
			return bad("rule %d: %v", i+1, err)
		}
		r.Remote = remote
		r.Ports = strings.ReplaceAll(r.Ports, " ", "")
		if r.Ports != "" {
			if r.Protocol != "tcp" && r.Protocol != "udp" {
				return bad("rule %d: ports need TCP or UDP", i+1)
			}
			if err := checkPorts(r.Ports); err != nil {
				return bad("rule %d: %v", i+1, err)
			}
		}
		r.Comment = strings.TrimSpace(r.Comment)
		if len([]rune(r.Comment)) > 60 {
			return bad("rule %d: the note is longer than 60 characters", i+1)
		}
	}
	return nil
}

func normalizeRemote(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	if remote == "" || remote == "any" || remote == "private" {
		return cmp(remote, "any"), nil
	}
	var parts []string
	for p := range strings.SplitSeq(remote, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(p); err == nil {
			parts = append(parts, prefix.Masked().String())
		} else if addr, err := netip.ParseAddr(p); err == nil && addr.Zone() == "" {
			parts = append(parts, addr.Unmap().String())
		} else {
			return "", fmt.Errorf("%q isn't an IP address or CIDR range", p)
		}
	}
	if len(parts) == 0 || len(parts) > 20 {
		return "", errors.New("give 1 to 20 addresses or ranges")
	}
	return strings.Join(parts, ", "), nil
}

func checkPorts(ports string) error {
	if !portsRe.MatchString(ports) {
		return fmt.Errorf("ports %q: use a port, a range like 8000-8080, or a list like 80,443", ports)
	}
	for p := range strings.SplitSeq(ports, ",") {
		lo, hi, isRange := strings.Cut(p, "-")
		a, _ := strconv.Atoi(lo)
		b := a
		if isRange {
			b, _ = strconv.Atoi(hi)
		}
		if a < 1 || b > 65535 || b < a {
			return fmt.Errorf("ports %q: ports are 1-65535, ranges low-high", ports)
		}
	}
	return nil
}

func cmp(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func newRuleID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Firewall returns a VM's firewall settings.
func (s *Service) Firewall(ctx context.Context, vm store.VM) (Firewall, error) {
	raw, err := s.store.VMFirewall(ctx, vm.ID)
	if err != nil {
		return Firewall{}, err
	}
	return parseFirewall(raw), nil
}

func parseFirewall(raw string) Firewall {
	f := defaultFirewall()
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			slog.Error("stored firewall settings are invalid", "err", err)
			return defaultFirewall()
		}
		f.normalize()
	}
	return f
}

// SetFirewall saves a VM's firewall settings and applies them. When saving
// worked but applying didn't, the settings are returned with ErrNotApplied.
func (s *Service) SetFirewall(ctx context.Context, vm store.VM, f Firewall) (Firewall, error) {
	if vm.State != store.VMReady {
		return Firewall{}, ErrNotReady
	}
	if err := f.normalize(); err != nil {
		return Firewall{}, err
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return Firewall{}, err
	}
	if err := s.store.SetVMFirewall(ctx, vm.ID, string(raw)); err != nil {
		return Firewall{}, err
	}
	slog.Info("firewall changed", "id", vm.ID, "vmid", vm.VMID,
		"egress", len(f.Egress.Rules), "link", len(f.Link.Rules))
	if err := s.applyFirewall(ctx, vm.ID); err != nil {
		slog.Warn("apply firewall", "id", vm.ID, "err", err)
		return f, fmt.Errorf("%w: %v", ErrNotApplied, err)
	}
	return f, nil
}

// linkFiltered reports whether a VM's link NIC goes through the firewall.
func (s *Service) linkFiltered(ctx context.Context, vm store.VM) bool {
	raw, err := s.store.VMFirewall(ctx, vm.ID)
	return err == nil && raw != "" && parseFirewall(raw).Link.filters()
}

// applyFirewall brings a VM's NICs and firewall in line with its settings.
// A NIC starts filtering only once its rules are in place, and stops
// before they're removed, so its traffic isn't briefly dropped.
func (s *Service) applyFirewall(ctx context.Context, id string) error {
	vm, err := s.store.VM(ctx, id)
	if err != nil {
		return err
	}
	linkOff := false
	if l := s.cfg.Links; l != nil && vm.LinkIP != "" {
		if cfg, err := s.pve.VMConfig(ctx, vm.Node, vm.VMID); err == nil {
			cur, _ := cfg[l.NIC].(string)
			linkOff = pve.NetOptions(cur)["firewall"] == "1" && !s.linkFiltered(ctx, vm)
		}
	}
	if linkOff {
		s.syncLink(ctx, vm.ID)
	}
	if err := s.syncFirewall(ctx, vm); err != nil {
		return err
	}
	if !linkOff && s.cfg.Links != nil && vm.LinkIP != "" {
		s.syncLink(ctx, vm.ID)
	}
	return nil
}

// syncFirewall writes a VM's firewall settings to Proxmox: the egress
// NIC's firewall flag, the options and IP sets filtering needs, and the
// rules. (The link NIC's flag belongs to the link sync: see linkNIC.)
func (s *Service) syncFirewall(ctx context.Context, vm store.VM) error {
	raw, err := s.store.VMFirewall(ctx, vm.ID)
	if err != nil || raw == "" {
		return err // Never set: Quicksand leaves the firewall alone.
	}
	if vm.State != store.VMReady {
		return nil
	}
	f := parseFirewall(raw)
	s.fwMu.Lock()
	defer s.fwMu.Unlock()

	cfg, err := s.pve.VMConfig(ctx, vm.Node, vm.VMID)
	if err != nil {
		return err
	}
	net0, hasEgress := cfg["net0"].(string)
	egressOn := hasEgress && f.Egress.filters()
	linkNIC := ""
	if s.cfg.Links != nil {
		linkNIC = s.cfg.Links.NIC
	}
	_, hasLinkNIC := cfg[linkNIC].(string)
	linkOn := linkNIC != "" && vm.LinkID != 0 && hasLinkNIC && f.Link.filters()

	setEgressFlag := func(on bool) error {
		want := map[bool]string{true: "1", false: "0"}[on]
		if !hasEgress || pve.NetOptions(net0)["firewall"] == want {
			return nil
		}
		return s.pve.UpdateConfig(ctx, vm.Node, vm.VMID, map[string]string{"net0": pve.SetNetOption(net0, "firewall", want)}, nil)
	}
	if !egressOn {
		if err := setEgressFlag(false); err != nil {
			return fmt.Errorf("egress NIC: %w", err)
		}
	}

	hasAgent := vm.AgentIP != ""
	anyOn := egressOn || linkOn
	if hasAgent || anyOn {
		// Filtered NICs need DHCP and IPv6 neighbour discovery let through.
		// The agent NIC's options stay as configureAgentFirewall set them.
		on := map[bool]string{true: "1", false: "0"}[anyOn]
		opts := map[string]string{"dhcp": on, "ndp": on}
		if !hasAgent {
			opts["enable"], opts["policy_in"], opts["policy_out"] = "1", "DROP", "DROP"
			opts["macfilter"], opts["ipfilter"], opts["radv"] = "1", "0", "0"
		}
		if err := s.pve.SetFirewallOptions(ctx, vm.Node, vm.VMID, opts); err != nil {
			return fmt.Errorf("firewall options: %w", err)
		}
	}
	if hasAgent {
		// The IP filter pins the agent NIC to its address; users' NICs may
		// use any address (DHCP, static, IPv6). An empty set would allow
		// only link-local addresses, and Proxmox refuses /0 entries, so
		// "any" is each family in two halves.
		for nic, on := range map[string]bool{"net0": egressOn, linkNIC: linkOn} {
			if on {
				if err := s.pve.EnsureIPSet(ctx, vm.Node, vm.VMID, "ipfilter-"+nic, anyAddress...); err != nil {
					return fmt.Errorf("IP set for %s: %w", nic, err)
				}
			}
		}
	}

	var want []pve.FirewallRule
	if egressOn {
		want = append(want, proxmoxRules("net0", "egress", f.Egress)...)
	}
	if linkOn {
		want = append(want, proxmoxRules(linkNIC, "link", f.Link)...)
	}
	if err := s.syncFirewallRules(ctx, vm, want); err != nil {
		return fmt.Errorf("rules: %w", err)
	}

	if egressOn {
		if err := setEgressFlag(true); err != nil {
			return fmt.Errorf("egress NIC: %w", err)
		}
	}
	return nil
}

// syncFirewallRules makes Quicksand's firewall rules (those marked
// firewallMark) exactly want, in order, keeping every other rule (the
// agent's) at the top of the list.
func (s *Service) syncFirewallRules(ctx context.Context, vm store.VM, want []pve.FirewallRule) error {
	rules, err := s.pve.FirewallRules(ctx, vm.Node, vm.VMID)
	if err != nil {
		return err
	}
	var ours, others []pve.FirewallRule
	for _, r := range rules {
		if strings.HasPrefix(r.Comment, firewallMark) {
			ours = append(ours, r)
		} else {
			others = append(others, r)
		}
	}
	othersFirst := true
	for i, r := range rules {
		if i < len(others) && strings.HasPrefix(r.Comment, firewallMark) {
			othersFirst = false
		}
	}
	if othersFirst && slices.EqualFunc(ours, want, func(a, b pve.FirewallRule) bool { return a.Same(b) }) {
		return nil
	}
	// New rules go in at the top (Proxmox puts them there), above the old
	// ones, which then go; then the others move back above them all.
	for i := len(want) - 1; i >= 0; i-- {
		if err := s.pve.InsertFirewallRule(ctx, vm.Node, vm.VMID, want[i]); err != nil {
			return err
		}
	}
	for i := len(ours) - 1; i >= 0; i-- {
		if err := s.pve.DeleteFirewallRule(ctx, vm.Node, vm.VMID, ours[i].Pos+len(want)); err != nil {
			return err
		}
	}
	// The list is now want, then the others in their old order; moving the
	// last rule to the top len(others) times puts the others first.
	last := len(want) + len(others) - 1
	for range others {
		if err := s.pve.MoveFirewallRule(ctx, vm.Node, vm.VMID, last, 0); err != nil {
			return err
		}
	}
	return nil
}

// proxmoxRules turns a network's settings into Proxmox rules for its NIC:
// each enabled rule (one per address family and ICMP flavour it needs),
// then the defaults. Blocked outbound traffic is rejected, so programs
// fail fast; blocked inbound traffic is dropped.
func proxmoxRules(nic, label string, n NetworkFirewall) []pve.FirewallRule {
	var out []pve.FirewallRule
	for i, r := range n.Rules {
		if !r.Enabled {
			continue
		}
		comment := fmt.Sprintf("%s %s rule %d", firewallMark, label, i+1)
		if r.Comment != "" {
			comment += " (" + r.Comment + ")"
		}
		for _, target := range ruleTargets(r.Remote) {
			for _, proto := range ruleProtocols(r.Protocol, target.family) {
				pr := pve.FirewallRule{Type: r.Direction, Action: proxmoxAction(r.Action, r.Direction),
					Iface: nic, Proto: proto, Comment: comment, Enable: 1}
				if r.Protocol == "tcp" || r.Protocol == "udp" {
					pr.Dport = strings.ReplaceAll(r.Ports, "-", ":")
				}
				if r.Direction == "out" {
					pr.Dest = target.list
				} else {
					pr.Source = target.list
				}
				out = append(out, pr)
			}
		}
	}
	for _, d := range []struct{ dir, action string }{{"out", n.Outbound}, {"in", n.Inbound}} {
		out = append(out, pve.FirewallRule{Type: d.dir, Action: proxmoxAction(d.action, d.dir), Iface: nic,
			Comment: fmt.Sprintf("%s %s default %sbound", firewallMark, label, d.dir), Enable: 1})
	}
	return out
}

func proxmoxAction(action, dir string) string {
	switch {
	case action == "allow":
		return "ACCEPT"
	case dir == "out":
		return "REJECT"
	default:
		return "DROP"
	}
}

// ruleTarget is the addresses of one family a rule matches ("" for any);
// Proxmox lists can't mix IPv4 and IPv6.
type ruleTarget struct {
	family int // 4, 6, or 0 for any
	list   string
}

func ruleTargets(remote string) []ruleTarget {
	switch remote {
	case "any":
		return []ruleTarget{{}}
	case "private":
		return []ruleTarget{{4, strings.Join(privateV4, ",")}, {6, strings.Join(privateV6, ",")}}
	}
	var v4, v6 []string
	for p := range strings.SplitSeq(remote, ",") {
		p = strings.TrimSpace(p)
		if strings.Contains(p, ":") {
			v6 = append(v6, p)
		} else if p != "" {
			v4 = append(v4, p)
		}
	}
	var out []ruleTarget
	if len(v4) > 0 {
		out = append(out, ruleTarget{4, strings.Join(v4, ",")})
	}
	if len(v6) > 0 {
		out = append(out, ruleTarget{6, strings.Join(v6, ",")})
	}
	return out
}

// ruleProtocols is the Proxmox protocols a rule needs for a family: ICMP
// is "icmp" for IPv4 and "ipv6-icmp" for IPv6.
func ruleProtocols(protocol string, family int) []string {
	switch {
	case protocol == "any":
		return []string{""}
	case protocol != "icmp":
		return []string{protocol}
	case family == 4:
		return []string{"icmp"}
	case family == 6:
		return []string{"ipv6-icmp"}
	default:
		return []string{"icmp", "ipv6-icmp"}
	}
}

// RunFirewall periodically re-applies firewall settings, undoing drift
// (edits in Proxmox, a snapshot revert's NIC flags), until ctx ends.
func (s *Service) RunFirewall(ctx context.Context) {
	for sleepCtx(ctx, firewallSyncInterval) {
		vms, err := s.store.VMs(ctx, 0)
		if err != nil {
			continue
		}
		for _, vm := range vms {
			if vm.State != store.VMReady {
				continue
			}
			if err := s.syncFirewall(ctx, vm); err != nil && ctx.Err() == nil {
				slog.Warn("sync firewall", "id", vm.ID, "vmid", vm.VMID, "err", err)
			}
		}
	}
}
