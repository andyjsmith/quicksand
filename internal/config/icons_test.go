package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A 1x1 PNG.
var tinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

func TestTemplateIcons(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, IconsDir), 0o755)
	icons := map[string][]byte{
		"edge.png":  tinyPNG,
		"logo.svg":  []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"/>`),
		"fake.png":  []byte("not an image"),
		"fake.svg":  []byte("<html></html>"),
		"large.png": append(append([]byte{}, tinyPNG...), make([]byte, MaxIconSize)...),
	}
	for name, data := range icons {
		os.WriteFile(filepath.Join(dir, IconsDir, name), data, 0o644)
	}
	load := func(icon string) (*Config, error) {
		path := filepath.Join(dir, "config.json")
		data := `{
			"pve": {"url": "https://pve:8006", "tokenId": "a@pve!b", "tokenSecret": "s", "pool": "p"},
			"vmids": {"from": 200, "to": 209},
			"networks": [{"id": "n", "bridge": "vmbr0"}],
			"templates": [{"id": "t", "vmid": 100, "node": "n", "icon": "` + icon + `"}]
		}`
		os.WriteFile(path, []byte(data), 0o600)
		return Load(path)
	}

	for icon, typ := range map[string]string{"edge.png": "image/png", "logo.svg": "image/svg+xml", "": ""} {
		cfg, err := load(icon)
		if err != nil {
			t.Fatalf("%q: %v", icon, err)
		}
		if got := cfg.Templates[0].IconType; got != typ {
			t.Errorf("%q: type %q, want %q", icon, got, typ)
		}
		if icon != "" && string(cfg.Templates[0].IconData) != string(icons[icon]) {
			t.Errorf("%q: wrong data", icon)
		}
	}
	for icon, want := range map[string]string{
		"missing.png":    "no such file",
		"fake.png":       "isn't a PNG",
		"fake.svg":       "isn't a PNG",
		"large.png":      "larger than",
		"../config.json": "not a path",
		`sub\\edge.png`:  "not a path",
		"/etc/passwd":    "not a path",
	} {
		if _, err := load(icon); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want an error about %q", icon, err, want)
		}
	}
}
