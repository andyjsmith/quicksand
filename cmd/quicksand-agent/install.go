package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"quicksand/internal/agent"
	"quicksand/internal/agentproto"
)

// installOptions are the "install" command's settings.
type installOptions struct {
	key, subnet string
	port        int
}

// cmdInstall installs (or upgrades) this binary as the agent service.
func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	key := fs.String("key", "", "the Quicksand server's public `key` (\"ed25519:...\", from the Admin page or \"quicksand agent pubkey\")")
	subnet := fs.String("subnet", "10.99.0.0/24", "the agent network, as in Quicksand's config")
	port := fs.Int("port", agentproto.DefaultPort, "the TCP port to listen on")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: quicksand-agent install -key ed25519:... [-subnet CIDR] [-port N]")
		fmt.Fprintln(os.Stderr, "Installs (or upgrades) this binary as the agent service; run as root or an administrator.")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if fs.NArg() != 0 || *key == "" {
		fs.Usage()
		os.Exit(2)
	}
	if _, err := agentproto.ParsePublicKey(*key); err != nil {
		return fmt.Errorf("invalid -key: %w", err)
	}
	if _, err := netip.ParsePrefix(*subnet); err != nil {
		return fmt.Errorf("invalid -subnet: %w", err)
	}
	if *port <= 0 || *port > 65535 {
		return errors.New("invalid -port")
	}
	return install(installOptions{key: *key, subnet: *subnet, port: *port})
}

// agentConfig is the configuration file the installer writes.
func agentConfig(o installOptions) ([]byte, error) {
	b, err := json.MarshalIndent(agent.Config{
		Port:           o.port,
		AuthorizedKeys: []string{o.key},
		Network:        &agent.NetworkConfig{Subnet: o.subnet},
	}, "", "  ")
	return append(b, '\n'), err
}

// copySelf installs the running binary at dst, unless it's already that
// file, through a temporary file so a failure leaves the old one intact.
func copySelf(dst string, mode os.FileMode) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if same, _ := sameFile(self, dst); same {
		return nil
	}
	in, err := os.Open(self)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("install %s: %w", dst, err)
	}
	return nil
}

func sameFile(a, b string) (bool, error) {
	ia, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ia, ib), nil
}

// step prints what the installer is doing.
func step(format string, args ...any) {
	fmt.Println(strings.TrimSpace(fmt.Sprintf(format, args...)))
}
