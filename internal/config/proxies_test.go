package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustedProxies(t *testing.T) {
	load := func(proxies string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{
			"pve": {"url": "https://pve:8006", "tokenId": "a@pve!b", "tokenSecret": "s"},
			"trustedProxies": ` + proxies + `
		}`
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}

	cfg, err := load(`["127.0.0.1", "::1", "10.1.0.0/16"]`)
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string]bool{
		"127.0.0.1":        true,
		"::ffff:127.0.0.1": true,
		"::1":              true,
		"10.1.200.3":       true,
		"10.2.0.1":         false,
		"192.0.2.1":        false,
	} {
		if got := cfg.TrustedProxy(netip.MustParseAddr(addr)); got != want {
			t.Errorf("TrustedProxy(%s) = %v, want %v", addr, got, want)
		}
	}

	if _, err := load(`["proxy.example"]`); err == nil || !strings.Contains(err.Error(), "trustedProxies[0]") {
		t.Errorf("a host name: got %v", err)
	}
}
