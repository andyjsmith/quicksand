package pve

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"testing"
)

func TestSupportsSpice(t *testing.T) {
	for vga, want := range map[string]bool{
		"":                   false,
		"std":                false,
		"qxl":                true,
		"qxl2":               true,
		"qxl,memory=32":      true,
		"type=qxl3":          true,
		"virtio,memory=16":   true,
		"virtio-gl":          true,
		"memory=16,type=std": false,
		"memory=16,type=qxl": true,
	} {
		cfg := map[string]any{}
		if vga != "" {
			cfg["vga"] = vga
		}
		if got := SupportsSpice(cfg); got != want {
			t.Errorf("SupportsSpice(%q) = %v, want %v", vga, got, want)
		}
	}
}

func TestSubjectMatches(t *testing.T) {
	cert := &x509.Certificate{Subject: pkix.Name{
		CommonName:         "violet.home.arpa",
		Organization:       []string{"Proxmox Virtual Environment"},
		OrganizationalUnit: []string{"PVE Cluster Node"},
	}}
	if !subjectMatches(cert, "OU=PVE Cluster Node,O=Proxmox Virtual Environment,CN=violet.home.arpa") {
		t.Error("expected match regardless of attribute order")
	}
	if subjectMatches(cert, "OU=PVE Cluster Node,O=Proxmox Virtual Environment,CN=other") {
		t.Error("matched a different CN")
	}
	if subjectMatches(cert, "CN=violet.home.arpa") {
		t.Error("matched a subject missing attributes")
	}
}

func TestHasTablet(t *testing.T) {
	for _, tc := range []struct {
		cfg  map[string]any
		want bool
	}{
		{map[string]any{}, true},
		{map[string]any{"vga": "std"}, true},
		{map[string]any{"vga": "qxl"}, false},
		{map[string]any{"vga": "qxl", "tablet": float64(1)}, true},
		{map[string]any{"vga": "qxl", "tablet": "1"}, true},
		{map[string]any{"vga": "std", "tablet": float64(0)}, false},
		{map[string]any{"vga": "serial0"}, false},
		{map[string]any{"vga": "virtio"}, true},
	} {
		if got := HasTablet(tc.cfg); got != tc.want {
			t.Errorf("HasTablet(%v) = %v, want %v", tc.cfg, got, tc.want)
		}
	}
}

func TestSetNetBridge(t *testing.T) {
	for _, tc := range []struct {
		in     string
		bridge string
		vlan   int
		want   string
	}{
		{"e1000=BC:24:11:ED:E9:9B,bridge=qsvm,firewall=1", "vmbr1", 0, "e1000=BC:24:11:ED:E9:9B,firewall=1,bridge=vmbr1"},
		{"virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,tag=5", "vmbr2", 105, "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr2,tag=105"},
	} {
		if got := SetNetBridge(tc.in, tc.bridge, tc.vlan); got != tc.want {
			t.Errorf("SetNetBridge(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSetNetOption(t *testing.T) {
	if got := SetNetOption("e1000=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=1", "firewall", "0"); got != "e1000=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=0" {
		t.Errorf("replace: %q", got)
	}
	if got := SetNetOption("virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0", "firewall", "0"); got != "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=0" {
		t.Errorf("append: %q", got)
	}
}

func TestSnapshotJSON(t *testing.T) {
	var list []Snapshot
	err := json.Unmarshal([]byte(`[
		{"name":"quicksand","snaptime":1792000000,"vmstate":1,"description":"x"},
		{"name":"disk-only","snaptime":1792000001},
		{"name":"current","running":1,"digest":"abc"}]`), &list)
	if err != nil || len(list) != 3 {
		t.Fatalf("%v, %d snapshots", err, len(list))
	}
	if !list[0].Memory || list[0].Taken.Unix() != 1792000000 || list[1].Memory || !list[2].Taken.IsZero() {
		t.Errorf("snapshots = %+v", list)
	}
}
