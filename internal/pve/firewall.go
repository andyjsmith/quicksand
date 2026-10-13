package pve

import (
	"context"
	"net/http"
	"net/url"
)

// FirewallRule is a guest firewall rule.
type FirewallRule struct {
	// Type is "in" or "out".
	Type string
	// Action is ACCEPT, DROP or REJECT.
	Action string
	// Iface limits the rule to one network device, e.g. "net1".
	Iface   string
	Proto   string
	Dport   string
	Source  string
	Comment string
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
