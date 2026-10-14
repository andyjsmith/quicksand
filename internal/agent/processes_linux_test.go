package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemdService(t *testing.T) {
	for cgroup, want := range map[string]string{
		"0::/system.slice/ssh.service\n": "ssh.service",
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/org.gnome.Shell@wayland.service\n": "org.gnome.Shell@wayland.service",
		"0::/user.slice/user-1000.slice/session-2.scope\n":                                             "",
		"12:cpu:/\n0::/init.scope\n":                                                                   "",
	} {
		dir := t.TempDir() + "/"
		os.WriteFile(filepath.Join(dir, "cgroup"), []byte(cgroup), 0o644)
		if got := systemdService(dir); got != want {
			t.Errorf("%q: got %q, want %q", cgroup, got, want)
		}
	}
	if got := systemdService("/nonexistent/"); got != "" {
		t.Errorf("missing file: %q", got)
	}
}
