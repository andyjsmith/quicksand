package vms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"quicksand/internal/config"
	"quicksand/internal/store"
)

// ErrCantExtend is returned when a VM's expiry can't be pushed back.
var ErrCantExtend = errors.New("this VM can't be extended")

// expiryCheckInterval is how often expired VMs are looked for.
const expiryCheckInterval = 30 * time.Second

// expiryFor is when a VM deployed now expires: zero if VMs don't expire.
func (s *Service) expiryFor(now time.Time) time.Time {
	if !s.cfg.Expiry.Enabled() {
		return time.Time{}
	}
	return now.Add(time.Duration(s.cfg.Expiry.TimeLimit)).UTC().Truncate(time.Second)
}

// extendsLeft is how many more times vm's owner may extend it: -1 for no
// limit.
func (s *Service) extendsLeft(vm store.VM) int {
	e := s.cfg.Expiry
	if e.MaxExtends == nil {
		return -1
	}
	return max(0, *e.MaxExtends-vm.Extends)
}

// Extend pushes back a VM's expiry by the configured duration, if its owner
// has extensions left; administrators can always extend.
func (s *Service) Extend(ctx context.Context, vm store.VM, admin bool) (store.VM, error) {
	if !s.cfg.Expiry.Enabled() || vm.ExpiresAt.IsZero() {
		return vm, fmt.Errorf("%w: VMs don't expire", ErrCantExtend)
	}
	if vm.State == store.VMDestroying {
		return vm, fmt.Errorf("%w: it's being destroyed", ErrCantExtend)
	}
	if !admin && s.extendsLeft(vm) == 0 {
		return vm, fmt.Errorf("%w: it has been extended as many times as allowed", ErrCantExtend)
	}
	from := vm.ExpiresAt
	if now := time.Now(); now.After(from) {
		from = now
	}
	vm.ExpiresAt = from.Add(time.Duration(s.cfg.Expiry.ExtendDuration)).UTC().Truncate(time.Second)
	vm.Extends++
	if err := s.store.SetVMExpiry(ctx, vm.ID, vm.ExpiresAt, vm.Extends); err != nil {
		return vm, err
	}
	slog.Info("vm extended", "id", vm.ID, "vmid", vm.VMID, "expires", vm.ExpiresAt, "extends", vm.Extends, "admin", admin)
	s.publish(vm)
	return vm, nil
}

// RunExpiry destroys VMs once they expire, until ctx ends. VMs deployed
// while expiry wasn't configured get a full time limit from now.
func (s *Service) RunExpiry(ctx context.Context) {
	if !s.cfg.Expiry.Enabled() {
		return
	}
	if n, err := s.store.SetMissingExpiry(ctx, s.expiryFor(time.Now())); err != nil {
		slog.Error("set missing expiry", "err", err)
	} else if n > 0 {
		slog.Info("gave existing VMs an expiry", "count", n, "limit", time.Duration(s.cfg.Expiry.TimeLimit))
		if vms, err := s.store.VMs(ctx, 0); err == nil {
			for _, vm := range vms {
				s.publish(vm)
			}
		}
	}
	tick := time.NewTicker(expiryCheckInterval)
	defer tick.Stop()
	for {
		s.destroyExpired(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Service) destroyExpired(ctx context.Context) {
	vms, err := s.store.VMs(ctx, 0)
	if err != nil {
		slog.Error("list vms for expiry", "err", err)
		return
	}
	now := time.Now()
	for _, vm := range vms {
		// Deployments in progress expire once they finish (or fail).
		if vm.ExpiresAt.IsZero() || vm.ExpiresAt.After(now) || vm.State == store.VMDeploying || vm.State == store.VMDestroying {
			continue
		}
		slog.Info("vm expired", "id", vm.ID, "vmid", vm.VMID, "name", vm.Name, "expired", vm.ExpiresAt)
		if err := s.Destroy(ctx, vm); err != nil {
			slog.Error("destroy expired vm", "id", vm.ID, "err", err)
		}
	}
}

// quotaLimits are the limits a deployment of tmpl by owner must respect:
// the template's and the instance's, per user and overall.
func (s *Service) quotaLimits(tmpl config.Template, owner store.User) []store.Limit {
	// The template's own limits first: they say the most specific thing.
	var limits []store.Limit
	if q := tmpl.Quota; q != nil {
		if q.User != nil {
			limits = append(limits, store.Limit{TemplateID: tmpl.ID, OwnerID: owner.ID, Max: *q.User,
				Message: fmt.Sprintf("you can have at most %s of %s at a time", plural(*q.User, "VM"), tmpl.Name)})
		}
		if q.Global != nil {
			limits = append(limits, store.Limit{TemplateID: tmpl.ID, Max: *q.Global,
				Message: fmt.Sprintf("there can be at most %s of %s at a time, across all users", plural(*q.Global, "VM"), tmpl.Name)})
		}
	}
	if q := s.cfg.Quota; q != nil {
		if q.User != nil {
			limits = append(limits, store.Limit{OwnerID: owner.ID, Max: *q.User,
				Message: fmt.Sprintf("you can have at most %s at a time", plural(*q.User, "VM"))})
		}
		if q.Global != nil {
			limits = append(limits, store.Limit{Max: *q.Global,
				Message: fmt.Sprintf("Quicksand allows at most %s at a time, across all users", plural(*q.Global, "VM"))})
		}
	}
	return limits
}

// QuotaBlock explains why owner can't deploy tmpl right now ("" if they
// can), for showing before they try.
func (s *Service) QuotaBlock(ctx context.Context, tmpl config.Template, owner store.User) (string, error) {
	for _, l := range s.quotaLimits(tmpl, owner) {
		n, err := s.store.CountVMs(ctx, l.TemplateID, l.OwnerID)
		if err != nil {
			return "", err
		}
		if n >= l.Max {
			return capitalize(l.Message) + ".", nil
		}
	}
	return "", nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func capitalize(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
