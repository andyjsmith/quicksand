package agent

import (
	"os"
	"testing"
)

// testdata/gnome-state.txt is GetCurrentState from an Ubuntu 26.04 guest
// (GNOME 50, one VirtIO GPU monitor at 1696x1048, a size the host set).
func TestParseGnomeState(t *testing.T) {
	data, err := os.ReadFile("testdata/gnome-state.txt")
	if err != nil {
		t.Fatal(err)
	}
	st, err := parseGnomeState(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if st.connector != "Virtual-1" || st.serial == "" {
		t.Errorf("connector %q serial %q", st.connector, st.serial)
	}
	if len(st.modes) != 24 {
		t.Errorf("%d modes, want 24", len(st.modes))
	}
	// x, y, scale, transform, primary.
	if got := st.logical[1:]; got[0] != "0" || got[1] != "0" || got[2] != "1.0" || got[3] != "0" || got[4] != "true" {
		t.Errorf("logical monitor %q", got)
	}
	for _, tc := range []struct {
		w, h         int
		wantW, wantH int
		wantExact    bool
	}{
		{1920, 1080, 1920, 1080, true},
		{1700, 1050, 1696, 1048, false},
		{1600, 1100, 1400, 1050, false},
		{1000, 700, 800, 600, false},
		{300, 200, 800, 600, false},
	} {
		m, exact, ok := pickMode(st.modes, tc.w, tc.h)
		if !ok || m.width != tc.wantW || m.height != tc.wantH || exact != tc.wantExact {
			t.Errorf("pickMode(%dx%d) = %s exact=%v", tc.w, tc.h, m.id, exact)
		}
		if ok && len(m.scales) == 0 {
			t.Errorf("%s has no scales", m.id)
		}
	}
}
