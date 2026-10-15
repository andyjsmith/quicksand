package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quicksand/internal/agentproto"
)

func TestLinks(t *testing.T) {
	key, _ := agentproto.GenerateKey()
	load := func(links string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{
			"pve": {"url": "https://pve:8006", "tokenId": "a@pve!b", "tokenSecret": "s"},
			"agent": {"privateKey": "` + agentproto.FormatPrivateKey(key) + `",
				"network": {"bridge": "qsagent", "subnet": "10.99.0.0/24", "serverIP": "10.99.0.1"}},
			"links": ` + links + `
		}`
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}

	cfg, err := load(`{"bridge": "vmbr9", "vlans": {"from": 100, "to": 199}, "subnet": "10.98.0.0/16"}`)
	if err != nil {
		t.Fatal(err)
	}
	if l := cfg.Links; l.NIC != "net2" || l.Model != "e1000" || l.MACPrefix != DefaultLinkMACPrefix {
		t.Errorf("defaults not applied: %+v", l)
	}
	if n := cfg.Links.ParsedNetwork(); n.Subnet.String() != "10.98.0.0/16" {
		t.Errorf("ParsedNetwork: %+v", n)
	}

	for links, want := range map[string]string{
		`{"vlans": {"from": 100, "to": 199}, "subnet": "10.98.0.0/16"}`:                                             "bridge is required",
		`{"bridge": "vmbr9", "vlans": {"from": 1, "to": 199}, "subnet": "10.98.0.0/16"}`:                            "2-4094",
		`{"bridge": "vmbr9", "vlans": {"from": 200, "to": 100}, "subnet": "10.98.0.0/16"}`:                          "2-4094",
		`{"bridge": "vmbr9", "vlans": {"from": 100, "to": 199}, "subnet": "10.99.0.0/16"}`:                          "overlaps the agent subnet",
		`{"bridge": "vmbr9", "vlans": {"from": 100, "to": 199}, "subnet": "10.98.0.0/16", "nic": "net1"}`:           "agent's NIC",
		`{"bridge": "vmbr9", "vlans": {"from": 100, "to": 199}, "subnet": "10.98.0.0/16", "nic": "net0"}`:           "other than net0",
		`{"bridge": "vmbr9", "vlans": {"from": 100, "to": 199}, "subnet": "10.98.0.0/16", "macPrefix": "02:51:53"}`: "macPrefix must differ",
	} {
		if _, err := load(links); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", links, err, want)
		}
	}
}
