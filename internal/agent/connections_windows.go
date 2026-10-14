package agent

import (
	"encoding/binary"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"

	"quicksand/internal/agentproto"
)

var (
	iphlpapi                = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	tcpTableOwnerPIDAll = 5 // TCP_TABLE_OWNER_PID_ALL
	udpTableOwnerPID    = 1 // UDP_TABLE_OWNER_PID
)

func listConnections() ([]agentproto.Connection, error) {
	out := []agentproto.Connection{}
	for _, t := range []struct {
		proto  string
		family uint32
		tcp    bool
	}{
		{"tcp", windows.AF_INET, true}, {"tcp6", windows.AF_INET6, true},
		{"udp", windows.AF_INET, false}, {"udp6", windows.AF_INET6, false},
	} {
		proc, class := procGetExtendedUdpTable, uintptr(udpTableOwnerPID)
		if t.tcp {
			proc, class = procGetExtendedTcpTable, tcpTableOwnerPIDAll
		}
		buf, err := extendedTable(proc, t.family, class)
		if err != nil {
			if t.family == windows.AF_INET {
				return nil, err
			}
			continue
		}
		out = append(out, parseTable(buf, t.proto, t.family == windows.AF_INET6, t.tcp)...)
	}
	return out, nil
}

func extendedTable(proc *windows.LazyProc, family uint32, class uintptr) ([]byte, error) {
	size := uint32(64 << 10)
	for range 5 {
		buf := make([]byte, size)
		r, _, _ := proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0,
			uintptr(family), class, 0)
		switch windows.Errno(r) {
		case 0:
			return buf[:size], nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			size += 16 << 10
			continue
		default:
			return nil, windows.Errno(r)
		}
	}
	return nil, windows.ERROR_INSUFFICIENT_BUFFER
}

// parseTable decodes MIB_{TCP,UDP}{,6}TABLE_OWNER_PID: a uint32 count,
// then fixed-size rows. Ports are in network byte order in the low 16 bits
// of a DWORD.
func parseTable(buf []byte, proto string, v6, tcp bool) []agentproto.Connection {
	if len(buf) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(buf))
	var rowSize int
	switch {
	case tcp && !v6:
		rowSize = 24 // state, laddr, lport, raddr, rport, pid
	case tcp && v6:
		rowSize = 56 // laddr[16], lscope, lport, raddr[16], rscope, rport, state, pid
	case !tcp && !v6:
		rowSize = 12 // laddr, lport, pid
	default:
		rowSize = 28 // laddr[16], lscope, lport, pid
	}
	port := func(b []byte) uint16 { return binary.BigEndian.Uint16(b[:2]) }
	addr4 := func(b []byte) netip.Addr { return netip.AddrFrom4([4]byte(b[:4])) }
	addr6 := func(b []byte) netip.Addr { return netip.AddrFrom16([16]byte(b[:16])) }
	u32 := func(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

	out := make([]agentproto.Connection, 0, n)
	for i := range n {
		off := 4 + i*rowSize
		if off+rowSize > len(buf) {
			break
		}
		row := buf[off : off+rowSize]
		c := agentproto.Connection{Proto: proto}
		switch {
		case tcp && !v6:
			c.State = windowsTCPState(u32(row[0:]))
			c.Local = formatAddrPort(addr4(row[4:]), port(row[8:]))
			c.Remote = formatAddrPort(addr4(row[12:]), port(row[16:]))
			c.PID = int(u32(row[20:]))
		case tcp && v6:
			c.Local = formatAddrPort(addr6(row[0:]), port(row[20:]))
			c.Remote = formatAddrPort(addr6(row[24:]), port(row[44:]))
			c.State = windowsTCPState(u32(row[48:]))
			c.PID = int(u32(row[52:]))
		case !tcp && !v6:
			c.Local = formatAddrPort(addr4(row[0:]), port(row[4:]))
			c.PID = int(u32(row[8:]))
		default:
			c.Local = formatAddrPort(addr6(row[0:]), port(row[20:]))
			c.PID = int(u32(row[24:]))
		}
		if c.State == "LISTEN" {
			c.Remote = ""
		}
		out = append(out, c)
	}
	return out
}

// windowsTCPState maps MIB_TCP_STATE (1 CLOSED .. 12 DELETE_TCB).
func windowsTCPState(s uint32) string {
	names := []string{"", "CLOSE", "LISTEN", "SYN_SENT", "SYN_RECV", "ESTABLISHED", "FIN_WAIT1",
		"FIN_WAIT2", "CLOSE_WAIT", "CLOSING", "LAST_ACK", "TIME_WAIT", "DELETE_TCB"}
	if int(s) < len(names) && s > 0 {
		return names[s]
	}
	return tcpState(int(s))
}
