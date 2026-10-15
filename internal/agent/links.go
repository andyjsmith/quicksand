package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"quicksand/internal/agentproto"
)

// linkSet holds the link-network addresses the server last asked for. The
// agent keeps them on their NICs: a NIC added while the VM runs appears a
// moment later, and network managers may remove addresses they didn't set.
type linkSet struct {
	mu    sync.Mutex
	links []wantLink
	// managed records the interfaces already released from the guest's
	// network manager.
	managed map[string]bool
}

type wantLink struct {
	mac    net.HardwareAddr
	prefix netip.Prefix
}

func (a *Agent) handleLinks(w http.ResponseWriter, r *http.Request) {
	var req agentproto.LinkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "bad request: " + err.Error()})
		return
	}
	var links []wantLink
	for _, l := range req.Links {
		mac, err := net.ParseMAC(l.MAC)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: fmt.Sprintf("bad MAC %q", l.MAC)})
			return
		}
		prefix, err := netip.ParsePrefix(l.Address)
		if err != nil || !prefix.Addr().Is4() {
			writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: fmt.Sprintf("bad address %q", l.Address)})
			return
		}
		links = append(links, wantLink{mac: mac, prefix: prefix})
	}
	a.links.mu.Lock()
	a.links.links = links
	a.links.mu.Unlock()
	writeJSON(w, http.StatusOK, agentproto.LinkResult{Links: a.applyLinks(r.Context())})
}

// keepLinks re-applies the link addresses every few seconds until ctx ends.
func (a *Agent) keepLinks(ctx context.Context) {
	for sleep(ctx, 5*time.Second) {
		a.applyLinks(ctx)
	}
}

// applyLinks puts each link's address on its NIC, where missing.
func (a *Agent) applyLinks(ctx context.Context) []agentproto.LinkStatus {
	a.links.mu.Lock()
	defer a.links.mu.Unlock()
	out := []agentproto.LinkStatus{}
	if len(a.links.links) == 0 {
		return out
	}
	ifaces, _ := net.Interfaces()
	for _, l := range a.links.links {
		st := agentproto.LinkStatus{MAC: l.mac.String()}
		i := -1
		for j, iface := range ifaces {
			if strings.EqualFold(iface.HardwareAddr.String(), st.MAC) {
				i = j
			}
		}
		if i < 0 {
			st.Error = "no interface with this MAC yet"
			out = append(out, st)
			continue
		}
		iface := ifaces[i]
		st.Interface = iface.Name
		if !a.links.managed[iface.Name] {
			// Keep the guest's network manager from running DHCP on it
			// (and dropping our address when that fails).
			if err := unmanageInterface(ctx, iface); err != nil {
				slog.Debug("unmanage link interface", "interface", iface.Name, "err", err)
			}
			if a.links.managed == nil {
				a.links.managed = map[string]bool{}
			}
			a.links.managed[iface.Name] = true
		}
		if !hasAddress(iface, l.prefix.Addr()) {
			slog.Info("configuring link interface", "interface", iface.Name, "address", l.prefix)
			if err := configureAddress(ctx, iface, l.prefix); err != nil {
				st.Error = err.Error()
				out = append(out, st)
				continue
			}
		}
		st.Configured = true
		out = append(out, st)
	}
	return out
}
