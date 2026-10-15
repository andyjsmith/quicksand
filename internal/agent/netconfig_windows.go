package agent

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
)

// configureAddress gives the interface a static address with no gateway.
func configureAddress(ctx context.Context, iface net.Interface, prefix netip.Prefix) error {
	mask := net.IP(net.CIDRMask(prefix.Bits(), 32)).String()
	args := []string{"interface", "ipv4", "set", "address", "name=" + fmt.Sprint(iface.Index),
		"source=static", "address=" + prefix.Addr().String(), "mask=" + mask, "gateway=none"}
	if out, err := exec.CommandContext(ctx, "netsh", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("netsh %v: %v: %s", args, err, out)
	}
	return nil
}

// unmanageInterface does nothing: a static address set with netsh stays.
func unmanageInterface(ctx context.Context, iface net.Interface) error { return nil }
