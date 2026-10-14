package main

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"quicksand/internal/agent"
	"quicksand/internal/agentproto"
)

func TestAgentConfig(t *testing.T) {
	key, err := agentproto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := agentproto.FormatPublicKey(key.Public().(ed25519.PublicKey))
	b, err := agentConfig(installOptions{key: pub, subnet: "10.99.0.0/24", port: 7070})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, b, 0o600)
	cfg, err := agent.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 7070 || len(cfg.AuthorizedKeys) != 1 || cfg.AuthorizedKeys[0] != pub || cfg.Network == nil || cfg.Network.Subnet != "10.99.0.0/24" {
		t.Errorf("config = %+v", cfg)
	}
	if _, err := agent.New(cfg); err != nil {
		t.Errorf("the agent rejects the config: %v", err)
	}
}
