package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func install(o installOptions) error {
	binDir := filepath.Join(os.Getenv("ProgramFiles"), "Quicksand")
	dataDir := filepath.Join(os.Getenv("ProgramData"), "Quicksand")
	exe := filepath.Join(binDir, "quicksand-agent.exe")
	config := filepath.Join(dataDir, "agent.json")
	logFile := filepath.Join(dataDir, "agent.log")

	m, err := mgr.Connect()
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return errors.New("run the installer as an administrator (an elevated prompt)")
		}
		return fmt.Errorf("connect to the service manager: %w", err)
	}
	defer m.Disconnect()

	// Windows locks a running executable: stop an installed agent first.
	existing, err := m.OpenService(serviceName)
	if err == nil {
		defer existing.Close()
		step("Stopping the %s service", serviceName)
		if err := stopService(existing); err != nil {
			return err
		}
	}

	step("Installing %s", exe)
	if err := copySelf(exe, 0o755); err != nil {
		return err
	}

	// Config and logs: SYSTEM and Administrators only.
	step("Writing %s", config)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	if err := restrictToAdmins(dataDir); err != nil {
		return fmt.Errorf("restrict %s: %w", dataDir, err)
	}
	cfg, err := agentConfig(o)
	if err != nil {
		return err
	}
	if err := os.WriteFile(config, cfg, 0o600); err != nil {
		return err
	}

	// Windows Firewall blocks inbound connections by default. The Proxmox VM
	// firewall already limits who can reach this port to the Quicksand server.
	step("Allowing TCP %d through Windows Firewall", o.port)
	netsh := filepath.Join(os.Getenv("SystemRoot"), "System32", "netsh.exe")
	exec.Command(netsh, "advfirewall", "firewall", "delete", "rule", "name=Quicksand agent").Run()
	if out, err := exec.Command(netsh, "advfirewall", "firewall", "add", "rule", "name=Quicksand agent", "dir=in", "action=allow",
		"protocol=TCP", "localport="+strconv.Itoa(o.port), "program="+exe, "profile=any").CombinedOutput(); err != nil {
		return fmt.Errorf("add the firewall rule: %v: %s", err, out)
	}

	// A service running as LocalSystem (needed to configure the agent
	// interface), started automatically and restarted if it fails.
	args := []string{"-config", config, "-log", logFile}
	s := existing
	if s == nil {
		step("Creating the %s service", serviceName)
		s, err = m.CreateService(serviceName, exe, mgr.Config{
			DisplayName: "Quicksand agent",
			Description: "Quicksand guest agent (see docs/agent.md).",
			StartType:   mgr.StartAutomatic,
		}, args...)
		if err != nil {
			return fmt.Errorf("create the service: %w", err)
		}
		defer s.Close()
	} else {
		c, err := s.Config()
		if err != nil {
			return err
		}
		c.BinaryPathName = syscall.EscapeArg(exe)
		for _, a := range args {
			c.BinaryPathName += " " + syscall.EscapeArg(a)
		}
		c.StartType = mgr.StartAutomatic
		if err := s.UpdateConfig(c); err != nil {
			return fmt.Errorf("update the service: %w", err)
		}
	}
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}, 86400); err != nil {
		return fmt.Errorf("set the service's recovery actions: %w", err)
	}
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return err
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("start the service: %w", err)
	}
	step("Installed and started the %s service. Log: %s", serviceName, logFile)
	return nil
}

func stopService(s *mgr.Service) error {
	st, err := s.Query()
	if err != nil {
		return err
	}
	if st.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err != nil && st.State != svc.StopPending {
			return fmt.Errorf("stop the service: %w", err)
		}
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		if st, err = s.Query(); err != nil {
			return err
		}
		if st.State == svc.Stopped {
			// The process may take a moment to let go of the executable.
			time.Sleep(time.Second)
			return nil
		}
	}
	return errors.New("the service didn't stop within 30 seconds")
}

// restrictToAdmins gives SYSTEM and Administrators full control of dir
// (and what it contains), and no one else access, replacing inherited ACLs.
func restrictToAdmins(dir string) error {
	sd, err := windows.SecurityDescriptorFromString("D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
