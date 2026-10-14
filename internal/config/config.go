// Package config loads Quicksand's server configuration.
package config

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"strconv"
	"time"

	"quicksand/internal/agentproto"
)

type Config struct {
	// Listen is the address the HTTP server binds to.
	Listen string `json:"listen"`
	// Database is the path of the SQLite database file.
	Database string `json:"database"`
	// SecureCookies marks session cookies Secure. Enable when Quicksand is
	// served over HTTPS (directly or behind a proxy).
	SecureCookies bool `json:"secureCookies"`
	// TLS, if set, serves HTTPS with this certificate. Browsers only allow
	// clipboard access and audio decoding on secure origins, so serve over
	// HTTPS (here or via a reverse proxy) anywhere but localhost.
	TLS *TLS `json:"tls,omitempty"`
	PVE PVE  `json:"pve"`
	// VMIDs is the pool of Proxmox VM IDs deployments are created with.
	VMIDs     VMIDRange  `json:"vmids"`
	Templates []Template `json:"templates"`
	Networks  []Network  `json:"networks"`
	// Agent configures the optional in-guest agent. Required when any
	// template has agent set.
	Agent *Agent `json:"agent,omitempty"`
	// Expiry, if set with a time limit, destroys VMs that long after they
	// are deployed unless their owners extend them. Without it, VMs never
	// expire.
	Expiry *Expiry `json:"expiry,omitempty"`
	// Quota limits how many VMs exist at once, across all templates; each
	// template can have its own as well. Without it, there is no limit.
	Quota *Quota `json:"quota,omitempty"`
}

// Expiry configures how long deployed VMs live.
type Expiry struct {
	// TimeLimit is how long after deploying a VM is destroyed, e.g. "72h" or
	// "7d". Zero or unset: VMs don't expire.
	TimeLimit Duration `json:"timeLimit"`
	// ExtendDuration is how much each extension adds (default 24h).
	ExtendDuration Duration `json:"extendDuration"`
	// MaxExtends is how many times an owner may extend a VM: unset is
	// unlimited, 0 is never. Administrators can always extend.
	MaxExtends *int `json:"maxExtends"`
}

// Enabled reports whether VMs expire at all.
func (e *Expiry) Enabled() bool { return e != nil && e.TimeLimit > 0 }

// Quota limits how many VMs exist at once; unset limits are unlimited.
type Quota struct {
	// User is the most VMs one user may have.
	User *int `json:"user"`
	// Global is the most VMs there may be across all users.
	Global *int `json:"global"`
}

func (q *Quota) validate(where string) error {
	if q == nil {
		return nil
	}
	if q.User != nil && *q.User < 0 || q.Global != nil && *q.Global < 0 {
		return fmt.Errorf("%s: limits can't be negative", where)
	}
	return nil
}

// Duration is a time.Duration written as a string: Go's syntax ("90m",
// "36h"), plus days ("7d", "1d12h").
type Duration time.Duration

var dayPrefix = regexp.MustCompile(`^(\d+)d`)

func ParseDuration(s string) (time.Duration, error) {
	var days time.Duration
	if m := dayPrefix.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		days = time.Duration(n) * 24 * time.Hour
		if s = s[len(m[0]):]; s == "" {
			return days, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (use e.g. \"36h\" or \"7d\")", s)
	}
	return days + d, nil
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.New("durations are strings, e.g. \"36h\" or \"7d\"")
	}
	if s == "" {
		*d = 0
		return nil
	}
	v, err := ParseDuration(s)
	if err != nil {
		return err
	}
	if v < 0 {
		return fmt.Errorf("negative duration %q", s)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// Agent configures how Quicksand reaches agents in deployed VMs. See
// docs/proxmox-setup.md for the network this describes.
type Agent struct {
	// PrivateKey is the key Quicksand authenticates to agents with
	// ("ed25519-private:..."; create one with "quicksand agent keygen").
	// Agents trust its public key, so keep it: a new key means updating
	// every template.
	PrivateKey string `json:"privateKey"`
	// Port agents listen on (default 7070).
	Port int `json:"port"`
	// Proxy, if set, is a socks5:// URL agent connections go through, e.g.
	// "ssh -D" into the Quicksand host while developing elsewhere.
	Proxy   string       `json:"proxy"`
	Network AgentNetwork `json:"network"`
}

type AgentNetwork struct {
	// Bridge is the SDN VNet (with "Isolate Ports") agent NICs attach to.
	Bridge string `json:"bridge"`
	// Subnet agent NICs are addressed from, e.g. "10.99.0.0/24".
	Subnet string `json:"subnet"`
	// ServerIP is Quicksand's own address on the agent network. Agents
	// only accept connections from it (enforced by the VM firewall).
	ServerIP string `json:"serverIP"`
	// MACPrefix is the first three bytes of agent NIC MACs (default 02:51:53).
	MACPrefix string `json:"macPrefix"`
	// NIC is the VM network device used for the agent (default "net1").
	NIC string `json:"nic"`
	// Model is the NIC model (default "e1000", which needs no guest drivers).
	Model string `json:"model"`
}

type TLS struct {
	// Cert and Key are PEM file paths; Cert may include intermediates.
	Cert string `json:"cert"`
	Key  string `json:"key"`
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
	// Agent says the template has the Quicksand agent installed; clones
	// get a NIC on the agent network.
	Agent bool `json:"agent"`
	// FilesDir is the guest's default path: where file browsing, shells and
	// relative paths (e.g. qs upload/download) start, e.g. "/home/user" or
	// "C:\\Users\\user\\Desktop". Empty means the root of the filesystem
	// ("/", or the system drive on Windows).
	FilesDir string `json:"filesDir"`
	// Quota limits how many VMs of this template exist at once.
	Quota *Quota `json:"quota,omitempty"`
	// Serial enables shells over a serial console: the Proxmox serial
	// port to use, "serial0" to "serial3". Deploys add it (as a socket)
	// when the template lacks it. The guest must run a login prompt on
	// the port (e.g. serial-getty@ttyS0 on Linux, or console=ttyS0).
	Serial string `json:"serial"`
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
	if err := c.Quota.validate("quota"); err != nil {
		return err
	}
	if e := c.Expiry; e != nil {
		if e.MaxExtends != nil && *e.MaxExtends < 0 {
			return errors.New("expiry.maxExtends can't be negative")
		}
		if e.ExtendDuration == 0 {
			e.ExtendDuration = Duration(24 * time.Hour)
		}
	}
	if c.TLS != nil && (c.TLS.Cert == "" || c.TLS.Key == "") {
		return errors.New("tls.cert and tls.key are both required")
	}
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
		if err := t.Quota.validate(fmt.Sprintf("templates[%d].quota", i)); err != nil {
			return err
		}
		if t.Serial != "" && !slices.Contains([]string{"serial0", "serial1", "serial2", "serial3"}, t.Serial) {
			return fmt.Errorf("templates[%d]: serial must be serial0 to serial3", i)
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
	for _, t := range c.Templates {
		if t.Agent && c.Agent == nil {
			return fmt.Errorf("template %q has agent set, but there is no agent section", t.ID)
		}
	}
	if c.Agent != nil {
		if err := c.Agent.validate(); err != nil {
			return fmt.Errorf("agent: %w", err)
		}
	}
	if defaults == 0 && len(c.Networks) > 0 {
		c.Networks[0].Default = true
	}
	return nil
}

var netKey = regexp.MustCompile(`^net\d+$`)

func (a *Agent) validate() error {
	if a.PrivateKey == "" {
		return errors.New(`privateKey is required; generate one with "quicksand agent keygen"`)
	}
	if _, err := agentproto.ParsePrivateKey(a.PrivateKey); err != nil {
		return fmt.Errorf("privateKey: %w", err)
	}
	if a.Port == 0 {
		a.Port = agentproto.DefaultPort
	}
	n := &a.Network
	if n.Bridge == "" {
		return errors.New("network.bridge is required")
	}
	if n.NIC == "" {
		n.NIC = "net1"
	}
	if !netKey.MatchString(n.NIC) || n.NIC == "net0" {
		return errors.New(`network.nic must be a network device other than net0, e.g. "net1"`)
	}
	if n.Model == "" {
		n.Model = "e1000"
	}
	if n.MACPrefix == "" {
		n.MACPrefix = agentproto.DefaultMACPrefix
	}
	an, err := agentproto.ParseAgentNetwork(n.Subnet, n.MACPrefix)
	if err != nil {
		return fmt.Errorf("network: %w", err)
	}
	server, err := netip.ParseAddr(n.ServerIP)
	if err != nil {
		return errors.New("network.serverIP must be Quicksand's IP address on the agent network")
	}
	if _, err := an.Offset(server); err != nil {
		return fmt.Errorf("network.serverIP: %w", err)
	}
	return nil
}

// Key returns the parsed private key (valid after Load).
func (a *Agent) Key() ed25519.PrivateKey {
	k, _ := agentproto.ParsePrivateKey(a.PrivateKey)
	return k
}

// ParsedNetwork returns the agent addressing scheme (valid after Load).
func (a *Agent) ParsedNetwork() agentproto.AgentNetwork {
	n, _ := agentproto.ParseAgentNetwork(a.Network.Subnet, a.Network.MACPrefix)
	return n
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
