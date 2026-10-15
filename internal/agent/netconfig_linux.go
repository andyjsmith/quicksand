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
	for _, args := range [][]string{
		{"ip", "address", "replace", prefix.String(), "dev", iface.Name},
		{"ip", "link", "set", iface.Name, "up"},
	} {
		if out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%v: %v: %s", args, err, out)
		}
	}
	return nil
}

// unmanageInterface tells NetworkManager, if it runs, to leave the
// interface alone until reboot (when the agent does this again).
func unmanageInterface(ctx context.Context, iface net.Interface) error {
	if _, err := exec.LookPath("nmcli"); err != nil {
		return nil
	}
	if out, err := exec.CommandContext(ctx, "nmcli", "device", "set", iface.Name, "managed", "no").CombinedOutput(); err != nil {
		return fmt.Errorf("nmcli: %v: %s", err, out)
	}
	return nil
}
