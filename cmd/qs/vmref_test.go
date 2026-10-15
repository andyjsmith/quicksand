package main

import (
	"strings"
	"testing"
)

func TestVMOfTemplate(t *testing.T) {
	list := []vm{
		{Name: "win10-2e1782", TemplateID: "win10"},
		{Name: "deb-a", TemplateID: "debian"},
		{Name: "deb-b", TemplateID: "debian"},
		{Name: "ubuntu-old", TemplateID: "ubuntu", State: "destroying"},
		{Name: "ubuntu-new", TemplateID: "ubuntu", State: "ready"},
	}
	for ref, want := range map[string]string{"win10": "win10-2e1782", "ubuntu": "ubuntu-new"} {
		if v, err := vmOfTemplate(nil, list, ref, false); err != nil || v.Name != want {
			t.Errorf("%s: got %q, %v; want %q", ref, v.Name, err, want)
		}
	}
	if _, err := vmOfTemplate(nil, list, "debian", false); err == nil || !strings.Contains(err.Error(), "deb-a, deb-b") {
		t.Errorf("debian: got %v, want an error naming both VMs", err)
	}
	if _, err := vmOfTemplate(nil, list, "arch", false); err == nil {
		t.Error("arch: no error")
	}
}
