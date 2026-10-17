package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

const (
	linuxBinary     = "/usr/local/bin/quicksand-agent"
	linuxConfigDir  = "/etc/quicksand-agent"
	linuxConfig     = linuxConfigDir + "/config.json"
	linuxUnit       = "/etc/systemd/system/quicksand-agent.service"
	linuxUnitConfig = `[Unit]
Description=Quicksand guest agent
# Installed by "quicksand-agent install"; see docs/agent.md in Quicksand.
After=network-pre.target
Wants=network-pre.target

[Service]
# Root is needed to configure the agent network interface.
ExecStart=` + linuxBinary + ` -config ` + linuxConfig + `
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`
)

// packagedUnit is installed by the .deb and .rpm packages, which own the
// binary and unit; then install only writes the configuration.
const packagedUnit = "/usr/lib/systemd/system/quicksand-agent.service"

func install(o installOptions) error {
	if os.Geteuid() != 0 {
		return errors.New("run the installer as root")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("the installer needs systemd; see docs/agent.md to install by hand")
	}
	_, err := os.Stat(packagedUnit)
	packaged := err == nil
	if packaged {
		// Drop a by-hand install's binary and unit, which would override the package's.
		for _, f := range []string{linuxUnit, linuxBinary} {
			if err := os.Remove(f); err == nil {
				step("Removed %s (the package provides it)", f)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	} else {
		step("Installing %s", linuxBinary)
		if err := copySelf(linuxBinary, 0o755); err != nil {
			return err
		}
	}
	step("Writing %s", linuxConfig)
	if err := os.MkdirAll(linuxConfigDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(linuxConfigDir, 0o700); err != nil {
		return err
	}
	cfg, err := agentConfig(o)
	if err != nil {
		return err
	}
	if err := os.WriteFile(linuxConfig, cfg, 0o600); err != nil {
		return err
	}
	if !packaged {
		step("Writing %s", linuxUnit)
		if err := os.WriteFile(linuxUnit, []byte(linuxUnitConfig), 0o644); err != nil {
			return err
		}
	}
	// Without a running systemd (e.g. customizing an image offline), the
	// service can only be enabled, and starts on the next boot.
	_, err = os.Stat("/run/systemd/system")
	running := err == nil
	cmds := [][]string{{"enable", "quicksand-agent"}}
	if running {
		cmds = [][]string{{"daemon-reload"}, {"enable", "quicksand-agent"}, {"restart", "quicksand-agent"}}
	}
	for _, args := range cmds {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %v: %v: %s", args, err, out)
		}
	}
	if running {
		step("Installed and started the quicksand-agent service. Logs: journalctl -u quicksand-agent")
	} else {
		step("Installed and enabled the quicksand-agent service; systemd isn't running, so it starts on the next boot.")
	}
	if _, err := os.Stat("/etc/NetworkManager"); err == nil {
		step(`NetworkManager is installed: stop it from running DHCP on the agent NIC
(the second NIC in clones, usually ens19), e.g.
  printf '[keyfile]\nunmanaged-devices=interface-name:ens19\n' > /etc/NetworkManager/conf.d/90-quicksand-agent.conf`)
	}
	return nil
}
