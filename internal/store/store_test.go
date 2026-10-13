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
