// Package agent is the Quicksand guest agent: a small HTTPS server inside a
// VM that the Quicksand server connects to. It only accepts clients holding
// one of its authorized keys (see agentproto).
package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"time"

	"quicksand/internal/agentproto"
)

// Version is the agent build's version, reported to the server.
var Version = "dev"

// Config is the agent's configuration file.
type Config struct {
	// Port is the TCP port to listen on (default 7070).
	Port int `json:"port"`
	// AuthorizedKeys are the Quicksand server public keys ("ed25519:...")
	// allowed to connect. More than one allows key rotation.
	AuthorizedKeys []string `json:"authorizedKeys"`
	// Network, if set, makes the agent find its NIC by MAC prefix, give it
	// the address encoded in the MAC (no gateway) and listen only there.
	Network *NetworkConfig `json:"network,omitempty"`
	// Listen overrides the listen address, e.g. "127.0.0.1:7070" for testing.
	Listen string `json:"listen,omitempty"`
}

type NetworkConfig struct {
	Subnet    string `json:"subnet"`
	MACPrefix string `json:"macPrefix,omitempty"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	// Windows PowerShell 5.1 writes UTF-8 files with a byte order mark.
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Port == 0 {
		c.Port = agentproto.DefaultPort
	}
	return c, nil
}

// Agent serves the agent API.
type Agent struct {
	cfg     Config
	keys    []ed25519.PublicKey
	network *agentproto.AgentNetwork
	links   linkSet
	handler http.Handler
}

func New(cfg Config) (*Agent, error) {
	a := &Agent{cfg: cfg}
	for _, k := range cfg.AuthorizedKeys {
		pub, err := agentproto.ParsePublicKey(k)
		if err != nil {
			return nil, fmt.Errorf("authorizedKeys: %w", err)
		}
		a.keys = append(a.keys, pub)
	}
	if len(a.keys) == 0 {
		return nil, errors.New("authorizedKeys must list the Quicksand server's public key")
	}
	if cfg.Network != nil {
		n, err := agentproto.ParseAgentNetwork(cfg.Network.Subnet, cfg.Network.MACPrefix)
		if err != nil {
			return nil, err
		}
		a.network = &n
	}
	a.handler = a.routes()
	return a, nil
}

// TLSConfig returns the server TLS configuration enforcing client keys.
func (a *Agent) TLSConfig() (*tls.Config, error) {
	return agentproto.ServerTLSConfig(a.keys)
}

// Handler returns the HTTP API (without TLS), for embedding and tests.
func (a *Agent) Handler() http.Handler { return a.handler }

// Run serves until ctx is cancelled. With a Network configured it keeps the
// agent NIC configured and follows its address.
func (a *Agent) Run(ctx context.Context) error {
	tlsConfig, err := a.TLSConfig()
	if err != nil {
		return err
	}
	go a.keepLinks(ctx)
	for ctx.Err() == nil {
		addr, err := a.listenAddress(ctx)
		if err != nil {
			slog.Warn("agent network not ready", "err", err)
			if !sleep(ctx, 5*time.Second) {
				break
			}
			continue
		}
		if err := a.serveOn(ctx, addr, tlsConfig); err != nil {
			slog.Warn("serving stopped", "addr", addr, "err", err)
			if !sleep(ctx, 2*time.Second) {
				break
			}
		}
	}
	return nil
}

// serveOn listens on addr until ctx ends, the listener fails, or (with a
// managed network) the address disappears from the NIC.
func (a *Agent) serveOn(ctx context.Context, addr string, tlsConfig *tls.Config) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelDebug),
	}
	slog.Info("listening", "addr", addr)

	done := make(chan error, 1)
	go func() { done <- srv.Serve(tls.NewListener(ln, tlsConfig)) }()

	watch := time.NewTicker(30 * time.Second)
	defer watch.Stop()
	for {
		select {
		case <-ctx.Done():
			srv.Close()
			return nil
		case err := <-done:
			return err
		case <-watch.C:
			if a.network == nil {
				continue
			}
			// DHCP clients or network managers may remove our address;
			// put it back, and rebind if it changed.
			if now, err := a.listenAddress(ctx); err != nil || now != addr {
				srv.Close()
				return fmt.Errorf("agent address changed (now %q, %v)", now, err)
			}
		}
	}
}

// listenAddress returns where to listen, configuring the agent NIC first
// when a managed network is configured.
func (a *Agent) listenAddress(ctx context.Context) (string, error) {
	port := strconv.Itoa(a.cfg.Port)
	if a.cfg.Listen != "" {
		return a.cfg.Listen, nil
	}
	if a.network == nil {
		return ":" + port, nil
	}
	iface, addr, err := a.findAgentInterface()
	if err != nil {
		return "", err
	}
	prefix := netip.PrefixFrom(addr, a.network.Subnet.Bits())
	if !hasAddress(iface, addr) {
		slog.Info("configuring agent interface", "interface", iface.Name, "address", prefix)
		if err := configureAddress(ctx, iface, prefix); err != nil {
			return "", fmt.Errorf("configure %s on %s: %w", prefix, iface.Name, err)
		}
		// Windows takes a moment (duplicate address detection) before the
		// address is usable.
		for i := 0; i < 20 && !hasAddress(iface, addr); i++ {
			if !sleep(ctx, 500*time.Millisecond) {
				return "", ctx.Err()
			}
		}
		if !hasAddress(iface, addr) {
			return "", fmt.Errorf("%s did not appear on %s", addr, iface.Name)
		}
	}
	return net.JoinHostPort(addr.String(), port), nil
}

func (a *Agent) findAgentInterface() (net.Interface, netip.Addr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return net.Interface{}, netip.Addr{}, err
	}
	for _, iface := range ifaces {
		if addr, ok := a.network.AddressForMAC(iface.HardwareAddr); ok {
			return iface, addr, nil
		}
	}
	return net.Interface{}, netip.Addr{}, errors.New("no interface with the agent MAC prefix")
}

func hasAddress(iface net.Interface, want netip.Addr) bool {
	// Re-read the interface; its addresses change underneath us.
	fresh, err := net.InterfaceByIndex(iface.Index)
	if err != nil {
		return false
	}
	addrs, err := fresh.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(ipnet.IP); ok && ip.Unmap() == want {
				return true
			}
		}
	}
	return false
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
