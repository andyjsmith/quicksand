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
