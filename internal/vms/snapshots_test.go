package vms

import "testing"

func TestParseVMStateProgress(t *testing.T) {
	for line, want := range map[string]int64{
		"329.00 B in 0s":   329,
		"466.57 MiB in 1s": 489234104,
		"2.78 GiB in 8s":   2985002270,
	} {
		if got, ok := parseVMStateProgress(line); !ok || got != want {
			t.Errorf("%q: got %d, %v; want %d", line, got, ok, want)
		}
	}
	for _, line := range []string{
		"saving VM state and RAM using storage 'storage2'",
		"completed saving the VM state in 9s, saved 3.26 GiB",
		"snapshotting 'drive-scsi0' (storage2:vm-222-disk-0)",
	} {
		if _, ok := parseVMStateProgress(line); ok {
			t.Errorf("%q parsed as progress", line)
		}
	}
}
