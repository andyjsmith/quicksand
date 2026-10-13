package pve

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// VM is a QEMU guest as reported by /cluster/resources.
type VM struct {
	VMID     int     `json:"vmid"`
	Name     string  `json:"name"`
	Node     string  `json:"node"`
	Status   string  `json:"status"`
	Template int     `json:"template"`
	CPU      float64 `json:"cpu"`
	MaxCPU   int     `json:"maxcpu"`
	Mem      int64   `json:"mem"`
	MaxMem   int64   `json:"maxmem"`
	Uptime   int64   `json:"uptime"`
	Type     string  `json:"type"`
}

func (c *Client) ListVMs(ctx context.Context) ([]VM, error) {
	var all []VM
	if err := c.do(ctx, http.MethodGet, "/cluster/resources", url.Values{"type": {"vm"}}, &all); err != nil {
		return nil, err
	}
	vms := all[:0]
	for _, vm := range all {
		if vm.Type == "qemu" {
			vms = append(vms, vm)
		}
	}
	return vms, nil
}

// VMConfig returns the current configuration of a QEMU guest as raw key/values.
func (c *Client) VMConfig(ctx context.Context, node string, vmid int) (map[string]any, error) {
	var cfg map[string]any
	err := c.do(ctx, http.MethodGet, vmPath(node, vmid, "config"), nil, &cfg)
	return cfg, err
}

// displayType returns the display adapter type from a guest's vga property,
// which is either the keyless element or "type=", e.g. "qxl2,memory=32" or
// "memory=32,type=virtio". Proxmox's default (no vga set) is "std".
func displayType(cfg map[string]any) string {
	vga, _ := cfg["vga"].(string)
	for part := range strings.SplitSeq(vga, ",") {
		key, value, hasKey := strings.Cut(part, "=")
		if !hasKey {
			return key
		} else if key == "type" {
			return value
		}
	}
	return "std"
}

// SupportsSpice reports whether a guest's display adapter exposes a SPICE
// server. Proxmox enables SPICE for qxl and virtio displays.
func SupportsSpice(cfg map[string]any) bool {
	typ := displayType(cfg)
	return strings.HasPrefix(typ, "qxl") || strings.HasPrefix(typ, "virtio")
}

// HasTablet reports whether a guest has a USB tablet, which gives VNC an
// absolute pointer. Without one, VNC falls back to a relative PS/2 mouse that
// drifts from the local cursor. Proxmox omits the tablet by default on qxl
// guests (not virtio ones), as the SPICE agent positions the pointer there.
func HasTablet(cfg map[string]any) bool {
	switch v := cfg["tablet"].(type) {
	case float64:
		return v != 0
	case string:
		return v != "0"
	}
	typ := displayType(cfg)
	return !strings.HasPrefix(typ, "qxl") && !strings.HasPrefix(typ, "serial")
}

func vmPath(node string, vmid int, rest ...string) string {
	return "/" + strings.Join(append([]string{"nodes", node, "qemu", strconv.Itoa(vmid)}, rest...), "/")
}
