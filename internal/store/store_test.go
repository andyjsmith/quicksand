package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUsersAndSessions(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	u, err := s.CreateUser(ctx, "Alice", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "alice", "hash", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username (case-insensitive): got %v, want ErrConflict", err)
	}
	got, hash, err := s.UserCredentials(ctx, "ALICE")
	if err != nil || got.ID != u.ID || hash != "hash" || !got.IsAdmin {
		t.Fatalf("UserCredentials = %+v, %q, %v", got, hash, err)
	}

	token := []byte("token-hash")
	if err := s.CreateSession(ctx, token, u.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if su, err := s.SessionUser(ctx, token); err != nil || su.ID != u.ID {
		t.Fatalf("SessionUser = %+v, %v", su, err)
	}
	expired := []byte("expired")
	if err := s.CreateSession(ctx, expired, u.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, expired); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session: got %v, want ErrNotFound", err)
	}
}

func TestVMIDAllocation(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	u, err := s.CreateUser(ctx, "bob", "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	newVM := func(id string, exclude map[int]bool) (VM, error) {
		return s.CreateVM(ctx, VM{ID: id, OwnerID: u.ID, Name: id, TemplateID: "t", Node: "n",
			Cores: 1, MemoryMiB: 512, NetworkID: "net", State: VMDeploying}, 100, 102, exclude)
	}

	a, err := newVM("a", map[int]bool{100: true})
	if err != nil || a.VMID != 101 {
		t.Fatalf("first VM: vmid %d, %v; want 101 (100 is taken in Proxmox)", a.VMID, err)
	}
	b, err := newVM("b", nil)
	if err != nil || b.VMID != 100 {
		t.Fatalf("second VM: vmid %d, %v; want 100", b.VMID, err)
	}
	if _, err := newVM("c", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := newVM("d", nil); !errors.Is(err, ErrNoVMID) {
		t.Fatalf("pool exhausted: got %v, want ErrNoVMID", err)
	}

	if err := s.DeleteUser(ctx, u.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleting a user who owns VMs: got %v, want ErrConflict", err)
	}
	if err := s.DeleteVM(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if again, err := newVM("e", nil); err != nil || again.VMID != 101 {
		t.Fatalf("after delete: vmid %d, %v; want 101 reused", again.VMID, err)
	}
	vms, err := s.VMs(ctx, u.ID)
	if err != nil || len(vms) != 3 {
		t.Fatalf("VMs = %d, %v", len(vms), err)
	}
}

func TestAgentIPAllocation(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	u, _ := s.CreateUser(ctx, "carol", "hash", false)
	for _, id := range []string{"a", "b", "c"} {
		if _, err := s.CreateVM(ctx, VM{ID: id, OwnerID: u.ID, Name: id, TemplateID: "t", Node: "n",
			Cores: 1, MemoryMiB: 512, NetworkID: "net", State: VMDeploying}, 100, 110, nil); err != nil {
			t.Fatal(err)
		}
	}
	pool := []string{"10.0.0.2", "10.0.0.3"}
	next := func(i int) (string, bool) {
		if i >= len(pool) {
			return "", false
		}
		return pool[i], true
	}
	if ip, err := s.AssignAgentIP(ctx, "a", next); err != nil || ip != "10.0.0.2" {
		t.Fatalf("a: %q, %v", ip, err)
	}
	if ip, err := s.AssignAgentIP(ctx, "b", next); err != nil || ip != "10.0.0.3" {
		t.Fatalf("b: %q, %v", ip, err)
	}
	if _, err := s.AssignAgentIP(ctx, "c", next); !errors.Is(err, ErrNoAgentIP) {
		t.Fatalf("c: got %v, want ErrNoAgentIP", err)
	}
	if v, _ := s.VM(ctx, "a"); v.AgentIP != "10.0.0.2" {
		t.Errorf("stored agent IP = %q", v.AgentIP)
	}
	// Destroying a VM frees its address.
	s.DeleteVM(ctx, "a")
	if ip, err := s.AssignAgentIP(ctx, "c", next); err != nil || ip != "10.0.0.2" {
		t.Fatalf("c after delete: %q, %v", ip, err)
	}
}

func TestLimitsAndExpiry(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	alice, _ := s.CreateUser(ctx, "alice", "hash", false)
	bob, _ := s.CreateUser(ctx, "bob", "hash", false)
	n := 0
	create := func(owner int64, tmpl string, limits ...Limit) (VM, error) {
		n++
		id := string(rune('a' + n))
		return s.CreateVM(ctx, VM{ID: id, OwnerID: owner, Name: id, TemplateID: tmpl, Node: "n",
			Cores: 1, MemoryMiB: 512, NetworkID: "net", State: VMReady}, 100, 199, nil, limits...)
	}
	perUser := func(owner int64) Limit { return Limit{TemplateID: "win", OwnerID: owner, Max: 1, Message: "one each"} }
	global := Limit{TemplateID: "win", Max: 2, Message: "two in all"}

	if _, err := create(alice.ID, "win", perUser(alice.ID), global); err != nil {
		t.Fatal(err)
	}
	if _, err := create(alice.ID, "win", perUser(alice.ID), global); !errors.Is(err, ErrLimit) {
		t.Errorf("alice's second: %v", err)
	}
	// Other templates don't count against the template's limits.
	if _, err := create(alice.ID, "linux", Limit{TemplateID: "linux", OwnerID: alice.ID, Max: 1}); err != nil {
		t.Errorf("alice's linux VM: %v", err)
	}
	b1, err := create(bob.ID, "win", perUser(bob.ID), global)
	if err != nil {
		t.Fatal(err)
	}
	// A third user would exceed the global limit.
	carol, _ := s.CreateUser(ctx, "carol", "hash", false)
	if _, err := create(carol.ID, "win", perUser(carol.ID), global); !errors.Is(err, ErrLimit) {
		t.Errorf("carol over the global limit: %v", err)
	}
	// VMs being destroyed don't count.
	s.UpdateVMProgress(ctx, b1.ID, VMDestroying, "", 0, "")
	if _, err := create(carol.ID, "win", perUser(carol.ID), global); err != nil {
		t.Errorf("carol after bob's is destroyed: %v", err)
	}
	if c, _ := s.CountVMs(ctx, "", alice.ID); c != 2 {
		t.Errorf("alice has %d VMs, want 2", c)
	}

	// Expiry: set, read back, and backfill those without one.
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err := s.SetVMExpiry(ctx, b1.ID, at, 2); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.VM(ctx, b1.ID); !v.ExpiresAt.Equal(at) || v.Extends != 2 {
		t.Errorf("expiry = %v, %d extends", v.ExpiresAt, v.Extends)
	}
	if changed, err := s.SetMissingExpiry(ctx, at.Add(time.Hour)); err != nil || changed != 3 {
		t.Errorf("backfilled %d, %v; want 3", changed, err)
	}
	if v, _ := s.VM(ctx, b1.ID); !v.ExpiresAt.Equal(at) {
		t.Errorf("backfill changed an existing expiry to %v", v.ExpiresAt)
	}
}
