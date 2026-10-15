package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestLinks(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	u, _ := s.CreateUser(ctx, "dave", "hash", false)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if _, err := s.CreateVM(ctx, VM{ID: id, OwnerID: u.ID, Name: id, TemplateID: "t", Node: "n",
			Cores: 1, MemoryMiB: 512, NetworkID: "net", State: VMReady}, 100, 110, nil); err != nil {
			t.Fatal(err)
		}
	}
	alloc := LinkAllocator{VLANFrom: 100, VLANTo: 101, NextIP: func(i int) (string, bool) {
		return fmt.Sprintf("10.98.0.%d", i+1), i < 250
	}}
	vm := func(id string) VM {
		v, err := s.VM(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	// a+b: a new network.
	ch, err := s.Link(ctx, []string{"a", "b"}, false, alloc)
	if err != nil || ch.Network.VLAN != 100 || ch.Network.Name != "Network 1" || len(ch.Changed) != 2 {
		t.Fatalf("link a b: %+v, %v", ch, err)
	}
	n1 := ch.Network.ID
	if a, b := vm("a"), vm("b"); a.LinkID != n1 || b.LinkID != n1 || a.LinkIP != "10.98.0.1" || b.LinkIP != "10.98.0.2" {
		t.Fatalf("a %+v b %+v", a, b)
	}
	// c joins a's network.
	if ch, err = s.Link(ctx, []string{"c", "a"}, false, alloc); err != nil || ch.Network.ID != n1 || len(ch.Changed) != 1 {
		t.Fatalf("link c a: %+v, %v", ch, err)
	}
	// d+e: a second network.
	ch, err = s.Link(ctx, []string{"d", "e"}, false, alloc)
	if err != nil || ch.Network.VLAN != 101 || ch.Network.Name != "Network 2" {
		t.Fatalf("link d e: %+v, %v", ch, err)
	}
	// No more VLANs for a third.
	if err := s.RenameLinkNetwork(ctx, n1, "Lab"); err != nil {
		t.Fatal(err)
	}
	// Linking across networks needs a merge; merging moves all of d's network.
	var merge *MergeNeededError
	if _, err := s.Link(ctx, []string{"a", "d"}, false, alloc); !errors.As(err, &merge) || len(merge.Networks) != 2 {
		t.Fatalf("link a d: %v", err)
	}
	if ch, err = s.Link(ctx, []string{"a", "d"}, true, alloc); err != nil || ch.Network.ID != n1 || ch.Network.Name != "Lab" || len(ch.Changed) != 2 {
		t.Fatalf("merge: %+v, %v", ch, err)
	}
	if nets, _ := s.LinkNetworks(ctx); len(nets) != 1 {
		t.Fatalf("networks after merge: %+v", nets)
	}
	if e := vm("e"); e.LinkID != n1 || e.LinkIP != "10.98.0.5" {
		t.Fatalf("e after merge: %+v", e)
	}

	// Unlinking: the network stays while two remain, and goes with the last.
	for _, id := range []string{"a", "b", "c"} {
		if ch, err = s.Unlink(ctx, id); err != nil || len(ch.Changed) != 1 {
			t.Fatalf("unlink %s: %+v, %v", id, ch, err)
		}
	}
	if ch, err = s.Unlink(ctx, "d"); err != nil || len(ch.Changed) != 2 {
		t.Fatalf("unlink d: %+v, %v", ch, err)
	}
	if nets, _ := s.LinkNetworks(ctx); len(nets) != 0 {
		t.Fatalf("networks left: %+v", nets)
	}
	// Addresses stay with their VMs.
	if a := vm("a"); a.LinkID != 0 || a.LinkIP != "10.98.0.1" {
		t.Fatalf("a after unlink: %+v", a)
	}
	// Unlinking an unlinked VM changes nothing.
	if ch, err = s.Unlink(ctx, "a"); err != nil || len(ch.Changed) != 0 {
		t.Fatalf("unlink again: %+v, %v", ch, err)
	}
}
