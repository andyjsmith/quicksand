package vms

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"quicksand/internal/pve"
	"quicksand/internal/store"
)

// suspendCheckInterval is how often idle VMs are looked for.
const suspendCheckInterval = 30 * time.Second

// usage is how much a VM is being used: open is how many sessions
// (consoles, shells, requests to its agent, ...) it has open now, and last
// when one last closed or the VM was last started.
type usage struct {
	open int
	last time.Time
}

// Use records that someone is using a VM, until done is called. Idle VMs
// are suspended, if configured to be.
func (s *Service) Use(id string) (done func()) {
	s.mu.Lock()
	u := s.usageLocked(id)
	u.open++
	s.mu.Unlock()
	return sync.OnceFunc(func() {
		s.mu.Lock()
		u.open--
		u.last = time.Now()
		s.mu.Unlock()
	})
}

// resetIdle restarts a VM's idle clock, e.g. when it's started.
func (s *Service) resetIdle(id string) {
	s.mu.Lock()
	s.usageLocked(id).last = time.Now()
	s.mu.Unlock()
}

// usageLocked returns a VM's usage, starting its idle clock now if it has
// none yet (it's new, or the server just started). s.mu must be held.
func (s *Service) usageLocked(id string) *usage {
	u := s.usage[id]
	if u == nil {
		u = &usage{last: time.Now()}
		s.usage[id] = u
	}
	return u
}

// suspendAt is when a VM will be suspended if it goes on being unused:
// zero if it's in use or VMs aren't suspended.
func (s *Service) suspendAt(id string) time.Time {
	if !s.cfg.Suspend.Enabled() {
		return time.Time{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.usageLocked(id)
	if u.open > 0 {
		return time.Time{}
	}
	return u.last.Add(time.Duration(s.cfg.Suspend.IdleTime)).UTC().Truncate(time.Second)
}

// RunSuspend hibernates running VMs that have been idle for the configured
// time, until ctx ends.
func (s *Service) RunSuspend(ctx context.Context) {
	if !s.cfg.Suspend.Enabled() {
		return
	}
	tick := time.NewTicker(suspendCheckInterval)
	defer tick.Stop()
	for {
		s.suspendIdle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Service) suspendIdle(ctx context.Context) {
	vms, err := s.store.VMs(ctx, 0)
	if err != nil {
		slog.Error("list vms for suspension", "err", err)
		return
	}
	now := time.Now()
	for _, vm := range vms {
		if vm.State != store.VMReady {
			continue
		}
		if at := s.suspendAt(vm.ID); at.IsZero() || at.After(now) {
			continue
		}
		st, err := s.pve.Status(ctx, vm.Node, vm.VMID)
		if err != nil || st.PowerState() != "running" {
			continue
		}
		slog.Info("vm idle, hibernating", "id", vm.ID, "vmid", vm.VMID, "name", vm.Name,
			"idle", time.Duration(s.cfg.Suspend.IdleTime))
		// Should this fail, it's tried again after another idle period.
		s.resetIdle(vm.ID)
		if err := s.Power(ctx, vm, pve.PowerHibernate); err != nil && !errors.Is(err, ErrBusy) {
			slog.Error("hibernate idle vm", "id", vm.ID, "err", err)
		}
	}
}
