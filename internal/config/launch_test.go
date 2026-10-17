package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quicksand/internal/agentproto"
)

func TestLaunch(t *testing.T) {
	key, _ := agentproto.GenerateKey()
	load := func(templates string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{
			"pve": {"url": "https://pve:8006", "tokenId": "a@pve!b", "tokenSecret": "s", "pool": "p"},
			"agent": {"privateKey": "` + agentproto.FormatPrivateKey(key) + `",
				"network": {"bridge": "qsagent", "subnet": "10.99.0.0/24", "serverIP": "10.99.0.1"}},
			"vmids": {"from": 200, "to": 209},
			"networks": [{"id": "n", "bridge": "vmbr0"}],
			"templates": ` + templates + `
		}`
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}

	cfg, err := load(`[
		{"id": "chrome", "vmid": 100, "node": "n", "agent": true,
			"launch": {"command": ["google-chrome"], "openURL": ["google-chrome", "{url}"]}},
		{"id": "term", "vmid": 100, "node": "n", "agent": true, "launch": {"command": ["xterm"], "restart": false}},
		{"id": "plain", "vmid": 100, "node": "n", "agent": true}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	chrome, term, plain := cfg.Templates[0], cfg.Templates[1], cfg.Templates[2]
	if s := chrome.Launch.Settings(); !s.Restart || !chrome.OpensURLs() {
		t.Errorf("chrome: %+v, opens URLs %v", s, chrome.OpensURLs())
	}
	if s := term.Launch.Settings(); s.Restart || term.OpensURLs() {
		t.Errorf("term: %+v, opens URLs %v", s, term.OpensURLs())
	}
	if s := plain.Launch.Settings(); len(s.Command) > 0 || plain.OpensURLs() {
		t.Errorf("plain: %+v", s)
	}

	for launch, want := range map[string]string{
		`{"command": ["x"]}, "agent": false`:                  "needs the agent",
		`{"command": []}, "agent": true`:                      "command is required",
		`{"command": ["x"], "openURL": ["x"]}, "agent": true`: "{url}",
	} {
		_, err := load(`[{"id": "t", "vmid": 100, "node": "n", "launch": ` + launch + `}]`)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want an error about %q", launch, err, want)
		}
	}
}
