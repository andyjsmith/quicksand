//go:build !linux && !windows

package agent

import (
	"context"
	"errors"
	"net"
	"net/netip"
)

func configureAddress(ctx context.Context, iface net.Interface, prefix netip.Prefix) error {
	return errors.New("configuring the agent interface is not supported on this OS; configure it yourself or set listen")
}

func unmanageInterface(ctx context.Context, iface net.Interface) error { return nil }
