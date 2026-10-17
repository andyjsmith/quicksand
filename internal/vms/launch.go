package vms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"quicksand/internal/agentclient"
	"quicksand/internal/agentproto"
	"quicksand/internal/store"
)

// Templates can have the agent launch an app in the guest's desktop
// session (config.Launch). The server tells each agent what to launch,
// along with any URL the VM was deployed to open, once it first answers;
// agents save it, and the server tells them again whenever what an agent
// reports differs from the template (it was changed, or the guest was
// reverted to a snapshot from before).

const (
	launchSyncInterval = 15 * time.Second
	// launchBootAttempts is how often a booting VM's agent is tried, 3s
	// apart, so its app starts as soon as it can.
	launchBootAttempts = 40
)

// ErrAgentTooOld is returned for agents that can't launch apps.
var ErrAgentTooOld = errors.New("this VM's agent is too old to launch apps; update it in the template")

// LaunchView is the app a VM's agent launches, as the web client sees it.
type LaunchView struct {
	// Command is what's launched, e.g. "firefox".
	Command []string `json:"command"`
	// OpensURLs says the VM was able to open a URL when deployed.
	OpensURLs bool `json:"opensURLs"`
	// Running is set while the app runs (as far as the agent last said).
	Running bool `json:"running"`
	// Error says why it isn't running, or why the agent can't launch it.
	Error string `json:"error,omitempty"`
}

// launchView describes a VM's app, from its agent's last probe.
func (s *Service) launchView(vm store.VM, agent *AgentView) *LaunchView {
	t, ok := s.cfg.Template(vm.TemplateID)
	if !ok || t.Launch == nil || vm.AgentIP == "" {
		return nil
	}
	v := &LaunchView{Command: t.Launch.Command, OpensURLs: t.OpensURLs()}
	if agent == nil || agent.Status != "connected" {
		return v
	}
	s.mu.Lock()
	err := s.launchErrors[vm.ID]
	s.mu.Unlock()
	s.probes.mu.Lock()
	p := s.probes.results[vm.ID]
	s.probes.mu.Unlock()
	switch {
	case err != "":
		v.Error = err
	case p.info.Launch != nil && p.info.Launch.Equal(t.Launch.Settings()):
		v.Running, v.Error = p.info.Launch.Running, p.info.Launch.Error
	default:
		v.Error = "telling the agent what to launch"
	}
	return v
}

// launchAsk is what to ask of a VM's agent besides keeping its app in line
// with the template: to start the app if it isn't running, or to open a URL
// in it.
type launchAsk struct {
	start bool
	open  string
}

// syncLaunch tells a VM's agent what to launch (and the URL it was deployed
// to open, if any) when what it reports differs, or when ask asks for more.
// It reports whether the agent answered.
func (s *Service) syncLaunch(ctx context.Context, id string, ask launchAsk) (bool, error) {
	// One at a time per VM, so a URL to open is only sent once.
	s.mu.Lock()
	lock := s.launchLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.launchLocks[id] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	vm, err := s.store.VM(ctx, id)
	if err != nil || vm.State != store.VMReady || vm.AgentIP == "" || s.agents == nil {
		return false, err
	}
	t, _ := s.cfg.Template(vm.TemplateID)
	want := t.Launch.Settings()
	pctx, cancel := context.WithTimeout(ctx, agentProbeTimeout)
	info, err := s.agents.Info(pctx, vm.AgentIP)
	cancel()
	s.recordProbe(vm.ID, info, err)
	if err != nil {
		return false, nil
	}
	var have agentproto.LaunchSettings
	if info.Launch != nil {
		have = info.Launch.LaunchSettings
	}
	// A URL stays pending until the agent has it, unless the template no
	// longer opens URLs.
	pendingURL := vm.OpenURL != "" && len(want.OpenURL) > 0
	if have.Equal(want) && !pendingURL && ask == (launchAsk{}) {
		if vm.OpenURL != "" {
			s.store.ClearVMOpenURL(ctx, vm.ID, vm.OpenURL)
		}
		return true, nil
	}
	req := agentproto.LaunchRequest{LaunchSettings: want, Start: ask.start}
	if pendingURL {
		req.Open = append(req.Open, vm.OpenURL)
	}
	if ask.open != "" {
		req.Open = append(req.Open, ask.open)
	}
	pctx, cancel = context.WithTimeout(ctx, 5*time.Second)
	st, err := s.agents.SetLaunch(pctx, vm.AgentIP, req)
	cancel()
	var ae *agentclient.Error
	switch {
	case errors.As(err, &ae) && ae.Status == 404:
		err = ErrAgentTooOld
	case err == nil:
		info.Launch = &st
		s.recordProbe(vm.ID, info, nil)
		if vm.OpenURL != "" {
			if err := s.store.ClearVMOpenURL(ctx, vm.ID, vm.OpenURL); err != nil {
				slog.Warn("clear open url", "id", vm.ID, "err", err)
			}
		}
		slog.Info("told agent what to launch", "id", vm.ID, "vmid", vm.VMID, "command", want.Command, "open", req.Open)
	}
	s.setLaunchError(vm, err)
	return true, err
}

func (s *Service) setLaunchError(vm store.VM, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	s.mu.Lock()
	old := s.launchErrors[vm.ID]
	if msg == "" {
		delete(s.launchErrors, vm.ID)
	} else {
		s.launchErrors[vm.ID] = msg
	}
	s.mu.Unlock()
	if old != msg {
		s.publish(vm)
	}
}

// syncLaunchAtBoot keeps trying a booting VM's agent until it answers, so
// its app starts right away.
func (s *Service) syncLaunchAtBoot(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), launchBootAttempts*(agentProbeTimeout+3*time.Second))
	defer cancel()
	for i := 0; i < launchBootAttempts; i++ {
		if answered, _ := s.syncLaunch(ctx, id, launchAsk{}); answered || !sleepCtx(ctx, 3*time.Second) {
			return
		}
	}
}

// Relaunch starts a VM's app if it isn't running, e.g. after its user
// closed an app that isn't restarted automatically.
func (s *Service) Relaunch(ctx context.Context, vm store.VM) error {
	if t, ok := s.cfg.Template(vm.TemplateID); !ok || t.Launch == nil {
		return fmt.Errorf("%w: this VM's template doesn't launch an app", ErrBadRequest)
	}
	if _, err := s.GuestAgent(vm); err != nil {
		return err
	}
	answered, err := s.syncLaunch(ctx, vm.ID, launchAsk{start: true})
	if err == nil && !answered {
		err = errors.New("the agent isn't answering")
	}
	return err
}

// OpenURL opens a URL in a running VM's app, for templates that open URLs
// (in a new tab, for browsers).
func (s *Service) OpenURL(ctx context.Context, vm store.VM, url string) error {
	if t, ok := s.cfg.Template(vm.TemplateID); !ok || !t.OpensURLs() {
		return fmt.Errorf("%w: this VM's template doesn't open links", ErrBadRequest)
	}
	if err := agentproto.CheckOpenURL(url); err != nil {
		return fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	if _, err := s.GuestAgent(vm); err != nil {
		return err
	}
	answered, err := s.syncLaunch(ctx, vm.ID, launchAsk{open: url})
	if err == nil && !answered {
		err = errors.New("the agent isn't answering; is the VM running?")
	}
	return err
}

// RunLaunch keeps agents' apps in line with their templates until ctx ends.
func (s *Service) RunLaunch(ctx context.Context) {
	if s.agents == nil {
		return
	}
	for {
		s.syncLaunches(ctx)
		if !sleepCtx(ctx, launchSyncInterval) {
			return
		}
	}
}

func (s *Service) syncLaunches(ctx context.Context) {
	vms, err := s.store.VMs(ctx, 0)
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, vm := range vms {
		// Every agent, even where the template launches nothing: it may
		// have before, and the agent must stop.
		if vm.State != store.VMReady || vm.AgentIP == "" {
			continue
		}
		wg.Go(func() { s.syncLaunch(ctx, vm.ID, launchAsk{}) })
	}
	wg.Wait()
}

// mayLaunch reports whether a VM's agent should, or might, launch an app:
// its template says so, or the agent last said it does.
func (s *Service) mayLaunch(vm store.VM) bool {
	if t, ok := s.cfg.Template(vm.TemplateID); ok && t.Launch != nil {
		return true
	}
	s.probes.mu.Lock()
	defer s.probes.mu.Unlock()
	return s.probes.results[vm.ID].info.Launch != nil
}
