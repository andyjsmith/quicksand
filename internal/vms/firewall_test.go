package vms

import (
	"errors"
	"strings"
	"testing"

	"quicksand/internal/pve"
)

func TestFirewallNormalize(t *testing.T) {
	f := Firewall{Egress: NetworkFirewall{Rules: []FirewallRule{
		{Enabled: true, Direction: "out", Action: "block", Protocol: "tcp", Remote: " 10.1.2.3 , 192.168.1.7/16, 2001:db8::1 ", Ports: "80, 8000-8080"},
	}}}
	if err := f.normalize(); err != nil {
		t.Fatal(err)
	}
	r := f.Egress.Rules[0]
	if f.Egress.Outbound != "allow" || f.Link.Inbound != "allow" || r.ID == "" {
		t.Errorf("defaults and ID: %+v", f)
	}
	if r.Remote != "10.1.2.3, 192.168.0.0/16, 2001:db8::1" || r.Ports != "80,8000-8080" {
		t.Errorf("normalized: %q %q", r.Remote, r.Ports)
	}

	for _, bad := range []FirewallRule{
		{Direction: "up", Action: "block", Protocol: "any", Remote: "any"},
		{Direction: "out", Action: "maybe", Protocol: "any", Remote: "any"},
		{Direction: "out", Action: "block", Protocol: "gre", Remote: "any"},
		{Direction: "out", Action: "block", Protocol: "any", Remote: "any", Ports: "80"},
		{Direction: "out", Action: "block", Protocol: "tcp", Remote: "any", Ports: "0"},
		{Direction: "out", Action: "block", Protocol: "tcp", Remote: "any", Ports: "90-80"},
		{Direction: "out", Action: "block", Protocol: "tcp", Remote: "example.com"},
		{Direction: "out", Action: "block", Protocol: "tcp", Remote: "fe80::1%eth0"},
	} {
		f := Firewall{Link: NetworkFirewall{Rules: []FirewallRule{bad}}}
		if err := f.normalize(); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%+v: got %v, want a bad request", bad, err)
		}
	}
}

func TestProxmoxRules(t *testing.T) {
	n := NetworkFirewall{Outbound: "allow", Inbound: "block", Rules: []FirewallRule{
		{Enabled: true, Direction: "out", Action: "allow", Protocol: "udp", Remote: "any", Ports: "53", Comment: "DNS"},
		{Enabled: false, Direction: "out", Action: "block", Protocol: "tcp", Remote: "any", Ports: "25"},
		{Enabled: true, Direction: "out", Action: "block", Protocol: "any", Remote: "private"},
		{Enabled: true, Direction: "in", Action: "allow", Protocol: "icmp", Remote: "any"},
		{Enabled: true, Direction: "in", Action: "allow", Protocol: "tcp", Remote: "203.0.113.0/24, 2001:db8::/32", Ports: "8000-8080"},
	}}
	got := proxmoxRules("net0", "egress", n)
	type r = pve.FirewallRule
	want := []r{
		{Type: "out", Action: "ACCEPT", Iface: "net0", Proto: "udp", Dport: "53"},
		{Type: "out", Action: "REJECT", Iface: "net0", Dest: strings.Join(privateV4, ",")},
		{Type: "out", Action: "REJECT", Iface: "net0", Dest: strings.Join(privateV6, ",")},
		{Type: "in", Action: "ACCEPT", Iface: "net0", Proto: "icmp"},
		{Type: "in", Action: "ACCEPT", Iface: "net0", Proto: "ipv6-icmp"},
		{Type: "in", Action: "ACCEPT", Iface: "net0", Proto: "tcp", Dport: "8000:8080", Source: "203.0.113.0/24"},
		{Type: "in", Action: "ACCEPT", Iface: "net0", Proto: "tcp", Dport: "8000:8080", Source: "2001:db8::/32"},
		{Type: "out", Action: "ACCEPT", Iface: "net0"},
		{Type: "in", Action: "DROP", Iface: "net0"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rules, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		want[i].Enable = 1
		if !got[i].Same(want[i]) || !strings.HasPrefix(got[i].Comment, firewallMark) {
			t.Errorf("rule %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	if !strings.Contains(got[0].Comment, "egress rule 1 (DNS)") || !strings.Contains(got[1].Comment, "rule 3") {
		t.Errorf("comments: %q, %q", got[0].Comment, got[1].Comment)
	}
}
