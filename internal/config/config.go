// Package config loads Quicksand's server configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type Config struct {
	// Listen is the address the HTTP server binds to.
	Listen string `json:"listen"`
	PVE    PVE    `json:"pve"`
}

type PVE struct {
	// URL is the base URL of the Proxmox API, e.g. https://pve.example:8006.
	URL         string `json:"url"`
	TokenID     string `json:"tokenId"`
	TokenSecret string `json:"tokenSecret"`
	// Fingerprint pins the SHA-256 fingerprint of the API certificate
	// (hex, colons optional). Leave empty to use the system trust store.
	Fingerprint string `json:"fingerprint"`
	// InsecureSkipVerify disables API certificate verification entirely.
	InsecureSkipVerify bool `json:"insecureSkipVerify"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{Listen: "127.0.0.1:8484"}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.PVE.URL == "" || cfg.PVE.TokenID == "" || cfg.PVE.TokenSecret == "" {
		return nil, errors.New("pve.url, pve.tokenId and pve.tokenSecret are required")
	}
	return cfg, nil
}
