package agent

import (
	"net/http"
	"net/netip"
	"slices"
	"strconv"

	"quicksand/internal/agentproto"
)

func (a *Agent) handleConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := listConnections()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, agentproto.Error{Error: err.Error()})
		return
	}
	// Name the owning processes.
	if procs, err := listProcesses(); err == nil {
		names := make(map[int]string, len(procs))
		for _, p := range procs {
			names[p.PID] = p.Name
		}
		for i := range conns {
			conns[i].ProcessName = names[conns[i].PID]
		}
	}
	slices.SortStableFunc(conns, func(a, b agentproto.Connection) int {
		if a.Proto != b.Proto {
			if a.Proto < b.Proto {
				return -1
			}
			return 1
		}
		return a.PID - b.PID
	})
	writeJSON(w, http.StatusOK, agentproto.ConnectionList{Connections: conns})
}

func formatAddrPort(ip netip.Addr, port uint16) string {
	return netip.AddrPortFrom(ip.Unmap(), port).String()
}

var tcpStates = map[int]string{
	1: "ESTABLISHED", 2: "SYN_SENT", 3: "SYN_RECV", 4: "FIN_WAIT1", 5: "FIN_WAIT2", 6: "TIME_WAIT",
	7: "CLOSE", 8: "CLOSE_WAIT", 9: "LAST_ACK", 10: "LISTEN", 11: "CLOSING",
}

func tcpState(n int) string {
	if s, ok := tcpStates[n]; ok {
		return s
	}
	return strconv.Itoa(n)
}
