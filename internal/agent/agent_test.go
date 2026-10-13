package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigWithBOM(t *testing.T) {
	// As written by Windows PowerShell 5.1's Set-Content -Encoding UTF8.
	path := filepath.Join(t.TempDir(), "agent.json")
	data := "\xef\xbb\xbf" + `{"authorizedKeys": ["ed25519:AAAAC3NzaC1lZDI1NTE5AAAAIEXAMPLEKEYEXAMPLEKEYEXA="], "network": {"subnet": "10.99.0.0/24"}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 7070 || cfg.Network == nil || cfg.Network.Subnet != "10.99.0.0/24" || len(cfg.AuthorizedKeys) != 1 {
		t.Errorf("unexpected config %+v", cfg)
	}
}
