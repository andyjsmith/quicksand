package agent

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"quicksand/internal/agentproto"
)

func listConnections() ([]agentproto.Connection, error) {
	owners := socketOwners()
	out := []agentproto.Connection{}
	for _, proto := range []string{"tcp", "tcp6", "udp", "udp6"} {
		f, err := os.Open("/proc/net/" + proto)
		if err != nil {
			continue // e.g. IPv6 disabled
		}
		sc := bufio.NewScanner(f)
		sc.Scan() // header
		for sc.Scan() {
			// sl local rem st tx:rx tr:when retrnsmt uid timeout inode
			fields := strings.Fields(sc.Text())
			if len(fields) < 10 {
				continue
			}
			local, ok1 := parseProcAddr(fields[1])
			remote, ok2 := parseProcAddr(fields[2])
			if !ok1 || !ok2 {
				continue
			}
			c := agentproto.Connection{Proto: proto, Local: local, PID: owners[fields[9]]}
			st, _ := strconv.ParseInt(fields[3], 16, 32)
			if strings.HasPrefix(proto, "tcp") {
				c.State = tcpState(int(st))
				if c.State != "LISTEN" {
					c.Remote = remote
				}
			} else if st == 1 { // connected UDP socket
				c.Remote = remote
			}
			out = append(out, c)
		}
		f.Close()
	}
	return out, nil
}

// parseProcAddr decodes "0100007F:0016": the address as host-order 32-bit
// words in hex, then the port.
func parseProcAddr(s string) (string, bool) {
	hexIP, hexPort, ok := strings.Cut(s, ":")
	if !ok {
		return "", false
	}
	raw, err := hex.DecodeString(hexIP)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return "", false
	}
	for i := 0; i < len(raw); i += 4 {
		binary.BigEndian.PutUint32(raw[i:], binary.LittleEndian.Uint32(raw[i:]))
	}
	ip, _ := netip.AddrFromSlice(raw)
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return "", false
	}
	return formatAddrPort(ip, uint16(port)), true
}

// socketOwners maps socket inodes to the PIDs holding them.
func socketOwners() map[string]int {
	owners := map[string]int{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return owners
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		dir := "/proc/" + e.Name() + "/fd/"
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(dir + fd.Name())
			if err != nil {
				continue
			}
			if inode, ok := strings.CutPrefix(link, "socket:["); ok {
				owners[strings.TrimSuffix(inode, "]")] = pid
			}
		}
	}
	return owners
}
