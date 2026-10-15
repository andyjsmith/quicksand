package vms

import "testing"

func TestSameNIC(t *testing.T) {
	want := "e1000=02:51:54:00:00:03,bridge=vmbr9,tag=101,firewall=0"
	for current, same := range map[string]bool{
		// Proxmox reorders options and may uppercase the MAC.
		"e1000=02:51:54:00:00:03,bridge=vmbr9,firewall=0,tag=101":             true,
		"e1000=02:51:54:00:00:03,bridge=vmbr9,firewall=0,tag=100":             false,
		"e1000=02:51:54:00:00:04,bridge=vmbr9,firewall=0,tag=101":             false,
		"virtio=02:51:54:00:00:03,bridge=vmbr9,firewall=0,tag=101":            false,
		"e1000=02:51:54:00:00:03,bridge=vmbr9,firewall=1,tag=101":             false,
		"e1000=02:51:54:00:00:03,bridge=vmbr9,firewall=0,tag=101,link_down=1": false,
		"": false,
	} {
		if got := sameNIC(current, want); got != same {
			t.Errorf("sameNIC(%q) = %v, want %v", current, got, same)
		}
	}
}
