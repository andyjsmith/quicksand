package agent

import "testing"

const xrandrSample = `Screen 0: minimum 320 x 200, current 800 x 600, maximum 8192 x 8192
Virtual-1 connected primary 800x600+0+0 (normal left inverted right x axis y axis) 325mm x 203mm
   1280x800      74.99 +
   1024x768      75.03    60.00
   800x600       75.00*   60.32
Virtual-2 disconnected (normal left inverted right x axis y axis)
   1920x1080     60.00 +
Virtual-3 connected 1024x768+800+0 (normal left inverted right x axis y axis) 0mm x 0mm
   1024x768      60.00*+
`

func TestParseXrandr(t *testing.T) {
	st := parseXrandr([]byte(xrandrSample))
	if st.size != "800x600" {
		t.Errorf("size = %q", st.size)
	}
	want := []xrandrOutput{{"Virtual-1", "1280x800", "800x600"}, {"Virtual-3", "1024x768", "1024x768"}}
	if len(st.outputs) != len(want) {
		t.Fatalf("outputs = %+v", st.outputs)
	}
	for i, o := range st.outputs {
		if o != want[i] {
			t.Errorf("output %d = %+v, want %+v", i, o, want[i])
		}
	}
}

func TestDisplayHold(t *testing.T) {
	st := parseXrandr([]byte(xrandrSample))
	if held(st) {
		t.Fatal("held before any explicit change")
	}
	holdDisplay(st)
	if !held(st) {
		t.Error("not held after an explicit change")
	}
	// The console was resized: its preferred mode changed.
	st.outputs[0].preferred = "1400x752"
	if held(st) {
		t.Error("still held after the preferred mode changed")
	}
	st.outputs[0].preferred = "1280x800"
	if held(st) {
		t.Error("held again without another explicit change")
	}
}
