package config

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quicksand/internal/agentproto"
)

func loadWithAgentKey(t *testing.T, key string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{
		"pve": {"url": "https://pve:8006", "tokenId": "a@pve!b", "tokenSecret": "s"},
		"agent": {"privateKey": "` + key + `", "network": {"bridge": "qsagent", "subnet": "10.99.0.0/24", "serverIP": "10.99.0.1"}}
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestAgentPrivateKey(t *testing.T) {
	key, _ := agentproto.GenerateKey()
	cfg, err := loadWithAgentKey(t, agentproto.FormatPrivateKey(key))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Agent.Key().Equal(key) {
		t.Error("Key() doesn't return the configured key")
	}
	if cfg.Agent.Port != agentproto.DefaultPort || cfg.Agent.Network.NIC != "net1" {
		t.Errorf("defaults not applied: %+v", cfg.Agent)
	}

	if _, err := loadWithAgentKey(t, ""); err == nil || !strings.Contains(err.Error(), "quicksand agent keygen") {
		t.Errorf("missing key: %v", err)
	}
	pub := agentproto.FormatPublicKey(key.Public().(ed25519.PublicKey))
	if _, err := loadWithAgentKey(t, pub); err == nil || !strings.Contains(err.Error(), "public key") {
		t.Errorf("public key as private key: %v", err)
	}
}
