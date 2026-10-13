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
	// Database is the path of the SQLite database file.
	Database string `json:"database"`
	// SecureCookies marks session cookies Secure. Enable when Quicksand is
	// served over HTTPS (directly or behind a proxy).
	SecureCookies bool `json:"secureCookies"`
	PVE           PVE  `json:"pve"`
	// VMIDs is the pool of Proxmox VM IDs deployments are created with.
	VMIDs     VMIDRange  `json:"vmids"`
	Templates []Template `json:"templates"`
	Networks  []Network  `json:"networks"`
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
	// Pool is the resource pool deployed VMs are created in. Grant the API
	// token its VM privileges on /pool/<pool>: Proxmox deletes a VM's own
	// ACL entries when the VM is destroyed, but pool permissions persist.
	Pool string `json:"pool"`
}

type VMIDRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// Template is a Proxmox template users can deploy.
type Template struct {
	// ID identifies the template in the API and database; it must not change.
	ID          string `json:"id"`
	VMID        int    `json:"vmid"`
	Node        string `json:"node"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// OS is a hint for the icon shown: "windows", "linux" or "other".
	OS string `json:"os"`
	// FullClone copies the template's disks instead of linking to them.
	FullClone bool `json:"fullClone"`
	// Defaults come from the template's own config when zero.
	Cores     int `json:"cores"`
	MemoryMiB int `json:"memoryMiB"`
	// Upper limits offered in the deploy dialog.
	MaxCores     int `json:"maxCores"`
	MaxMemoryMiB int `json:"maxMemoryMiB"`
}

// Network is a bridge (optionally with a VLAN tag) deployments can attach
// to, or no network at all.
type Network struct {
	// ID identifies the network in the API; it must not change.
	ID string `json:"id"`
	// Bridge is a Proxmox bridge or SDN VNet. Null (or omitted) means
	// isolated: the guest gets no network interface at all.
	Bridge *string `json:"bridge"`
	// VLAN, if set, tags the guest's interface on the bridge.
	VLAN int `json:"vlan"`
	// Label is what users see; it defaults to the ID.
	Label   string `json:"label"`
	Default bool   `json:"default"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{Listen: "127.0.0.1:8484", Database: "quicksand.db"}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.PVE.URL == "" || c.PVE.TokenID == "" || c.PVE.TokenSecret == "" {
		return errors.New("pve.url, pve.tokenId and pve.tokenSecret are required")
	}
	if len(c.Templates) > 0 && c.PVE.Pool == "" {
		return errors.New("pve.pool is required to deploy templates")
	}
	if len(c.Templates) > 0 && (c.VMIDs.From <= 0 || c.VMIDs.To < c.VMIDs.From) {
		return errors.New("vmids.from and vmids.to must give a range of VM IDs to deploy into")
	}
	seen := map[string]bool{}
	for i := range c.Templates {
		t := &c.Templates[i]
		if t.ID == "" || t.VMID <= 0 || t.Node == "" {
			return fmt.Errorf("templates[%d]: id, vmid and node are required", i)
		}
		if seen[t.ID] {
			return fmt.Errorf("templates[%d]: duplicate id %q", i, t.ID)
		}
		seen[t.ID] = true
		if t.Name == "" {
			t.Name = t.ID
		}
		if t.MaxCores <= 0 {
			t.MaxCores = 16
		}
		if t.MaxMemoryMiB <= 0 {
			t.MaxMemoryMiB = 32768
		}
	}
	if len(c.Templates) > 0 && len(c.Networks) == 0 {
		return errors.New("at least one network is required to deploy templates")
	}
	seen = map[string]bool{}
	defaults := 0
	for i := range c.Networks {
		n := &c.Networks[i]
		if n.ID == "" {
			return fmt.Errorf("networks[%d]: id is required", i)
		}
		if n.Bridge != nil && *n.Bridge == "" {
			return fmt.Errorf("networks[%d]: bridge must be a bridge name, or null for an isolated network", i)
		}
		if n.Isolated() && n.VLAN != 0 {
			return fmt.Errorf("networks[%d]: an isolated network (bridge null) can't have a VLAN", i)
		}
		if seen[n.ID] {
			return fmt.Errorf("networks[%d]: duplicate id %q", i, n.ID)
		}
		seen[n.ID] = true
		if n.Label == "" {
			n.Label = n.ID
		}
		if n.Default {
			defaults++
		}
	}
	if defaults > 1 {
		return errors.New("only one network can be the default")
	}
	if defaults == 0 && len(c.Networks) > 0 {
		c.Networks[0].Default = true
	}
	return nil
}

// Isolated reports whether the network gives guests no interface at all.
func (n Network) Isolated() bool { return n.Bridge == nil }

func (c *Config) Template(id string) (Template, bool) {
	for _, t := range c.Templates {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}

func (c *Config) Network(id string) (Network, bool) {
	for _, n := range c.Networks {
		if n.ID == id {
			return n, true
		}
	}
	return Network{}, false
}

// DefaultNetwork returns the network used when none is chosen.
func (c *Config) DefaultNetwork() (Network, bool) {
	for _, n := range c.Networks {
		if n.Default {
			return n, true
		}
	}
	return Network{}, false
}
