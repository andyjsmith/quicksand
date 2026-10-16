package vms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"quicksand/internal/pve"
	"quicksand/internal/store"
)

// Each VM has at most one snapshot, named snapshotName: taking a new one
// replaces it, and reverting keeps it.
const snapshotName = "quicksand"

// Snapshot actions, tracked like power actions (see Action).
const (
	ActionSnapshot       pve.PowerAction = "snapshot"
	ActionRevert         pve.PowerAction = "revert"
	ActionDeleteSnapshot pve.PowerAction = "delete-snapshot"
)

// ErrNoSnapshot is returned when reverting to or deleting a snapshot a VM
// doesn't have.
var ErrNoSnapshot = errors.New("this VM has no snapshot")

// SnapshotInfo describes a VM's snapshot.
type SnapshotInfo struct {
	TakenAt time.Time `json:"takenAt"`
	// Memory says it includes the VM's RAM: reverting resumes the VM as it
	// was, running programs and all.
	Memory bool `json:"memory"`
}

// ActionProgress is how far along a snapshot action is.
type ActionProgress struct {
	Step string `json:"step"`
	// Done and Total are bytes, while saving memory. Total is the VM's RAM:
	// QEMU skips pages that are all zeros, so it saves less than that, but
	// how much less isn't known until it's done.
	Done  int64 `json:"done,omitempty"`
	Total int64 `json:"total,omitempty"`
}

// setProgress updates the progress of a VM's running action.
func (s *Service) setProgress(vm store.VM, p ActionProgress) {
	s.mu.Lock()
	a, ok := s.actions[vm.ID]
	if ok && a.Action != "" {
		a.Progress = &p
		s.actions[vm.ID] = a
	}
	s.mu.Unlock()
	if ok {
		s.publish(vm)
	}
}

// Lines of a snapshot task's log while it saves the VM's memory:
// "saving VM state and RAM using storage 'local-lvm'", then "1.33 GiB in 4s"
// every second, then "completed saving the VM state in 9s, saved 3.26 GiB".
var (
	vmStateSaved = regexp.MustCompile(`^(\d+(?:\.\d+)?) (B|KiB|MiB|GiB|TiB) in \d+s$`)
	byteUnits    = map[string]float64{"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}
)

// parseVMStateProgress returns the bytes of memory a snapshot task's log
// line says it has saved.
func parseVMStateProgress(line string) (int64, bool) {
	m := vmStateSaved.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return int64(n * byteUnits[m[2]]), true
}

// snapshot returns a VM's snapshot, nil if it has none. It's read from
// Proxmox once, then kept up to date by the snapshot actions.
func (s *Service) snapshot(ctx context.Context, vm store.VM) (*SnapshotInfo, error) {
	s.mu.Lock()
	info, known := s.snapshots[vm.ID]
	s.mu.Unlock()
	if known {
		return info, nil
	}
	list, err := s.pve.Snapshots(ctx, vm.Node, vm.VMID)
	if err != nil {
		return nil, err
	}
	info = nil
	for _, snap := range list {
		if snap.Name == snapshotName {
			info = &SnapshotInfo{TakenAt: snap.Taken, Memory: snap.Memory}
		}
	}
	s.mu.Lock()
	s.snapshots[vm.ID] = info
	s.mu.Unlock()
	return info, nil
}

func (s *Service) setSnapshot(id string, info *SnapshotInfo) {
	s.mu.Lock()
	s.snapshots[id] = info
	s.mu.Unlock()
}

// beginAction claims a VM for an action, failing if one is already running.
func (s *Service) beginAction(vm store.VM, action pve.PowerAction) error {
	if vm.State != store.VMReady {
		return ErrNotReady
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.jobs[vm.ID]; busy || s.actions[vm.ID].Action != "" {
		return ErrBusy
	}
	s.actions[vm.ID] = actionState{Action: action}
	return nil
}

// TakeSnapshot snapshots a VM, with its memory if memory is set, replacing
// the snapshot it had.
func (s *Service) TakeSnapshot(ctx context.Context, vm store.VM, memory bool) error {
	existing, err := s.snapshot(ctx, vm)
	if err != nil {
		return err
	}
	if err := s.beginAction(vm, ActionSnapshot); err != nil {
		return err
	}
	s.publish(vm)
	slog.Info("taking snapshot", "id", vm.ID, "vmid", vm.VMID, "memory", memory, "replacing", existing != nil)
	s.startJob(vm.ID, func(ctx context.Context) {
		err := s.runTakeSnapshot(ctx, vm, existing != nil, memory)
		state := actionState{}
		if err != nil && ctx.Err() == nil {
			state.Error = "snapshot: " + err.Error()
			slog.Warn("snapshot failed", "id", vm.ID, "err", err)
		}
		s.setAction(vm, state)
	})
	return nil
}

func (s *Service) runTakeSnapshot(ctx context.Context, vm store.VM, replace, memory bool) error {
	// Proxmox can't overwrite a snapshot: delete the old one first.
	if replace {
		s.setProgress(vm, ActionProgress{Step: "Removing the previous snapshot"})
		err := s.runTask(ctx, vm, func() (pve.UPID, error) {
			return s.pve.DeleteSnapshot(ctx, vm.Node, vm.VMID, snapshotName)
		}, nil)
		if err != nil {
			return fmt.Errorf("remove the previous snapshot: %w", err)
		}
		s.setSnapshot(vm.ID, nil)
	}
	saving := ActionProgress{Step: "Saving memory", Total: int64(vm.MemoryMiB) << 20}
	if st, err := s.pve.Status(ctx, vm.Node, vm.VMID); err == nil && st.MaxMem > 0 {
		saving.Total = st.MaxMem
	}
	disks := ActionProgress{Step: "Snapshotting disks"}
	if memory {
		s.setProgress(vm, saving)
	} else {
		s.setProgress(vm, disks)
	}
	taken := time.Now().UTC().Truncate(time.Second)
	err := s.runTask(ctx, vm, func() (pve.UPID, error) {
		return s.pve.CreateSnapshot(ctx, vm.Node, vm.VMID, snapshotName, "Taken in Quicksand", memory)
	}, func(line string) {
		if n, ok := parseVMStateProgress(line); ok && memory {
			saving.Done = min(n, saving.Total)
			s.setProgress(vm, saving)
		} else if strings.HasPrefix(line, "completed saving the VM state") {
			// Shown full for a moment before moving on: the bytes saved
			// usually fall short of the RAM (see ActionProgress).
			saving.Done = saving.Total
			s.setProgress(vm, saving)
		} else if strings.HasPrefix(line, "snapshotting ") && memory {
			s.setProgress(vm, disks)
		}
	})
	if err != nil {
		return err
	}
	// The VM may have been stopped, in which case there's no memory to save.
	info := &SnapshotInfo{TakenAt: taken, Memory: memory}
	if list, err := s.pve.Snapshots(ctx, vm.Node, vm.VMID); err == nil {
		for _, snap := range list {
			if snap.Name == snapshotName {
				info = &SnapshotInfo{TakenAt: snap.Taken, Memory: snap.Memory}
			}
		}
	}
	s.setSnapshot(vm.ID, info)
	return nil
}

// RevertSnapshot returns a VM to its snapshot, which it keeps. A snapshot
// with memory resumes the VM as it was; one without leaves it stopped.
// With start, a VM whose snapshot has no memory is started afterwards too.
func (s *Service) RevertSnapshot(ctx context.Context, vm store.VM, start bool) error {
	return s.snapshotAction(ctx, vm, ActionRevert, func(ctx context.Context) error {
		err := s.runTask(ctx, vm, func() (pve.UPID, error) {
			return s.pve.RollbackSnapshot(ctx, vm.Node, vm.VMID, snapshotName, start)
		}, nil)
		if err == nil {
			// The rollback restores the VM's config as it was, link NIC and
			// all; links may have changed since.
			s.resyncLink(ctx, vm.ID)
			if err := s.syncFirewall(ctx, vm); err != nil {
				slog.Warn("firewall after revert", "id", vm.ID, "err", err)
			}
		}
		return err
	})
}

// DeleteSnapshot removes a VM's snapshot.
func (s *Service) DeleteSnapshot(ctx context.Context, vm store.VM) error {
	return s.snapshotAction(ctx, vm, ActionDeleteSnapshot, func(ctx context.Context) error {
		err := s.runTask(ctx, vm, func() (pve.UPID, error) {
			return s.pve.DeleteSnapshot(ctx, vm.Node, vm.VMID, snapshotName)
		}, nil)
		if err == nil {
			s.setSnapshot(vm.ID, nil)
		}
		return err
	})
}

// runTask starts a Proxmox task and waits for it, retrying a few times when
// the VM's config is briefly locked by something else ("can't lock file
// ... got timeout"), which Proxmox gives up on after 10 seconds. onLog,
// if set, gets the task's log lines.
func (s *Service) runTask(ctx context.Context, vm store.VM, start func() (pve.UPID, error), onLog func(line string)) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			slog.Info("vm locked; retrying", "id", vm.ID, "vmid", vm.VMID, "err", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
		var upid pve.UPID
		if upid, err = start(); err == nil {
			err = s.pve.WaitTask(ctx, vm.Node, upid, onLog)
		}
		if err == nil || !strings.Contains(err.Error(), "can't lock file") {
			return err
		}
	}
	return err
}

// snapshotAction runs run as action on a VM that has a snapshot.
func (s *Service) snapshotAction(ctx context.Context, vm store.VM, action pve.PowerAction, run func(context.Context) error) error {
	info, err := s.snapshot(ctx, vm)
	if err != nil {
		return err
	}
	if info == nil {
		return ErrNoSnapshot
	}
	if err := s.beginAction(vm, action); err != nil {
		return err
	}
	s.publish(vm)
	slog.Info("snapshot action", "action", action, "id", vm.ID, "vmid", vm.VMID)
	s.startJob(vm.ID, func(ctx context.Context) {
		state := actionState{}
		if err := run(ctx); err != nil && ctx.Err() == nil {
			state.Error = fmt.Sprintf("%s: %v", action, err)
			slog.Warn("snapshot action failed", "action", action, "id", vm.ID, "err", err)
		}
		s.setAction(vm, state)
	})
	return nil
}
