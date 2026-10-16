package pve

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
)

// FirewallRule is a guest firewall rule.
type FirewallRule struct {
	// Pos is the rule's position in the list (0 is checked first), when
	// listed.
	Pos int `json:"pos"`
	// Type is "in" or "out".
	Type string `json:"type"`
	// Action is ACCEPT, DROP or REJECT.
	Action string `json:"action"`
	// Iface limits the rule to one network device, e.g. "net1".
	Iface   string `json:"iface"`
	Proto   string `json:"proto"`
	Dport   string `json:"dport"`
	Source  string `json:"source"`
	Dest    string `json:"dest"`
	Comment string `json:"comment"`
	Enable  int    `json:"enable"`
}

// Same reports whether two rules match the same traffic the same way
// (positions and comments aside).
func (r FirewallRule) Same(o FirewallRule) bool {
	return r.Type == o.Type && r.Action == o.Action && r.Iface == o.Iface && r.Proto == o.Proto &&
		r.Dport == o.Dport && r.Source == o.Source && r.Dest == o.Dest && r.Enable == o.Enable
}

// FirewallRules lists a guest's firewall rules in order.
func (c *Client) FirewallRules(ctx context.Context, node string, vmid int) ([]FirewallRule, error) {
	var rules []FirewallRule
	if err := c.do(ctx, http.MethodGet, vmPath(node, vmid, "firewall", "rules"), nil, &rules); err != nil {
		return nil, err
	}
	slices.SortFunc(rules, func(a, b FirewallRule) int { return a.Pos - b.Pos })
	return rules, nil
}

// InsertFirewallRule adds an enabled rule at the top of the list (where
// Proxmox always puts new rules).
func (c *Client) InsertFirewallRule(ctx context.Context, node string, vmid int, r FirewallRule) error {
	form := url.Values{"type": {r.Type}, "action": {r.Action}, "enable": {"1"}}
	for k, v := range map[string]string{
		"iface": r.Iface, "proto": r.Proto, "dport": r.Dport, "source": r.Source, "dest": r.Dest, "comment": r.Comment,
	} {
		if v != "" {
			form.Set(k, v)
		}
	}
	return c.do(ctx, http.MethodPost, vmPath(node, vmid, "firewall", "rules"), form, nil)
}

// MoveFirewallRule moves the rule at pos to position to.
func (c *Client) MoveFirewallRule(ctx context.Context, node string, vmid, pos, to int) error {
	return c.do(ctx, http.MethodPut, vmPath(node, vmid, "firewall", "rules", strconv.Itoa(pos)),
		url.Values{"moveto": {strconv.Itoa(to)}}, nil)
}

// DeleteFirewallRule removes the rule at pos.
func (c *Client) DeleteFirewallRule(ctx context.Context, node string, vmid, pos int) error {
	return c.do(ctx, http.MethodDelete, vmPath(node, vmid, "firewall", "rules", strconv.Itoa(pos)), nil, nil)
}

// FirewallOptions returns a guest's firewall options.
func (c *Client) FirewallOptions(ctx context.Context, node string, vmid int) (map[string]any, error) {
	var opts map[string]any
	err := c.do(ctx, http.MethodGet, vmPath(node, vmid, "firewall", "options"), nil, &opts)
	return opts, err
}

// EnsureIPSet makes a guest IP set hold cidrs (among any others), creating
// it if needed.
func (c *Client) EnsureIPSet(ctx context.Context, node string, vmid int, name string, cidrs ...string) error {
	var sets []struct {
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, vmPath(node, vmid, "firewall", "ipset"), nil, &sets); err != nil {
		return err
	}
	if !slices.ContainsFunc(sets, func(s struct {
		Name string `json:"name"`
	}) bool {
		return s.Name == name
	}) {
		if err := c.do(ctx, http.MethodPost, vmPath(node, vmid, "firewall", "ipset"), url.Values{
			"name": {name}, "comment": {"Managed by Quicksand"},
		}, nil); err != nil {
			return err
		}
	}
	var entries []struct {
		CIDR string `json:"cidr"`
	}
	if err := c.do(ctx, http.MethodGet, vmPath(node, vmid, "firewall", "ipset", name), nil, &entries); err != nil {
		return err
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.CIDR] = true
	}
	for _, cidr := range cidrs {
		if !have[cidr] {
			if err := c.do(ctx, http.MethodPost, vmPath(node, vmid, "firewall", "ipset", name), url.Values{"cidr": {cidr}}, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetFirewallOptions sets a guest's firewall options (enable, policy_in, ...).
func (c *Client) SetFirewallOptions(ctx context.Context, node string, vmid int, opts map[string]string) error {
	form := url.Values{}
	for k, v := range opts {
		form.Set(k, v)
	}
	return c.do(ctx, http.MethodPut, vmPath(node, vmid, "firewall", "options"), form, nil)
}

// AddFirewallRule adds a rule at the top of the guest's rule list.
func (c *Client) AddFirewallRule(ctx context.Context, node string, vmid int, r FirewallRule) error {
	form := url.Values{"type": {r.Type}, "action": {r.Action}, "enable": {"1"}, "pos": {"0"}}
	for k, v := range map[string]string{
		"iface": r.Iface, "proto": r.Proto, "dport": r.Dport, "source": r.Source, "comment": r.Comment,
	} {
		if v != "" {
			form.Set(k, v)
		}
	}
	return c.do(ctx, http.MethodPost, vmPath(node, vmid, "firewall", "rules"), form, nil)
}

// SetIPFilter restricts a guest network device to the given addresses by
// creating its ipfilter-<nic> IP set. It takes effect with the ipfilter
// firewall option.
func (c *Client) SetIPFilter(ctx context.Context, node string, vmid int, nic string, cidrs ...string) error {
	name := "ipfilter-" + nic
	err := c.do(ctx, http.MethodPost, vmPath(node, vmid, "firewall", "ipset"), url.Values{
		"name":    {name},
		"comment": {"Managed by Quicksand"},
	}, nil)
	if err != nil {
		return err
	}
	for _, cidr := range cidrs {
		if err := c.do(ctx, http.MethodPost, vmPath(node, vmid, "firewall", "ipset", name), url.Values{
			"cidr": {cidr},
		}, nil); err != nil {
			return err
		}
	}
	return nil
}
