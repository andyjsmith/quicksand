package pve

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// VNet is an SDN virtual network.
type VNet struct {
	Name string `json:"vnet"`
	Zone string `json:"zone"`
	Tag  int    `json:"tag"`
}

// Zone is an SDN zone; Bridge is the bridge VLAN zones put their VNets on.
type Zone struct {
	Name   string `json:"zone"`
	Type   string `json:"type"`
	Bridge string `json:"bridge"`
}

// VNet returns an SDN VNet's configuration (needs SDN.Audit).
func (c *Client) VNet(ctx context.Context, name string) (VNet, error) {
	var v VNet
	err := c.do(ctx, http.MethodGet, "/cluster/sdn/vnets/"+url.PathEscape(name), nil, &v)
	return v, err
}

// Zone returns an SDN zone's configuration (needs SDN.Audit). It's found
// in the list of zones: reading one zone directly needs SDN.Allocate.
func (c *Client) Zone(ctx context.Context, name string) (Zone, error) {
	var zones []Zone
	if err := c.do(ctx, http.MethodGet, "/cluster/sdn/zones", nil, &zones); err != nil {
		return Zone{}, err
	}
	for _, z := range zones {
		if z.Name == name {
			return z, nil
		}
	}
	return Zone{}, &Error{Status: http.StatusNotFound, Message: "no SDN zone " + name}
}

// NetOptions parses a netX property ("e1000=AA:..,bridge=vmbr9,tag=100")
// into its options; the model's key is the model and its value the MAC.
func NetOptions(net string) map[string]string {
	opts := map[string]string{}
	for p := range strings.SplitSeq(net, ",") {
		if k, v, ok := strings.Cut(p, "="); ok {
			opts[k] = v
		}
	}
	return opts
}
