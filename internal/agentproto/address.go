package agentproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// Agent NICs carry their IP address in their MAC address, so the agent can
// configure its interface without DHCP and the server knows the address in
// advance. The MAC is a locally administered prefix followed by the IP's
// offset in the agent subnet:
//
//	prefix 02:51:53, subnet 10.99.0.0/24, MAC 02:51:53:00:00:17 -> 10.99.0.23
const DefaultMACPrefix = "02:51:53"

// AgentNetwork describes the agent subnet, as both sides configure it.
type AgentNetwork struct {
	Subnet    netip.Prefix
	MACPrefix [3]byte
}

func ParseAgentNetwork(subnet, macPrefix string) (AgentNetwork, error) {
	p, err := netip.ParsePrefix(subnet)
	if err != nil || !p.Addr().Is4() {
		return AgentNetwork{}, fmt.Errorf("agent subnet %q must be an IPv4 prefix like 10.99.0.0/24", subnet)
	}
	if p.Bits() < 8 || p.Bits() > 30 {
		return AgentNetwork{}, errors.New("agent subnet must be between /8 and /30")
	}
	if macPrefix == "" {
		macPrefix = DefaultMACPrefix
	}
	hw, err := net.ParseMAC(macPrefix + ":00:00:00")
	if err != nil || len(hw) != 6 {
		return AgentNetwork{}, fmt.Errorf("MAC prefix %q must be three bytes like 02:51:53", macPrefix)
	}
	if hw[0]&0x01 != 0 {
		return AgentNetwork{}, errors.New("MAC prefix must not be multicast")
	}
	n := AgentNetwork{Subnet: p.Masked()}
	copy(n.MACPrefix[:], hw[:3])
	return n, nil
}

// HostCount is the number of usable host offsets (1 .. HostCount).
func (n AgentNetwork) HostCount() int {
	return 1<<(32-n.Subnet.Bits()) - 2
}

// Address returns the IP for a host offset in the subnet.
func (n AgentNetwork) Address(offset int) (netip.Addr, error) {
	if offset < 1 || offset > n.HostCount() {
		return netip.Addr{}, fmt.Errorf("host offset %d is outside %s", offset, n.Subnet)
	}
	base := binary.BigEndian.Uint32(n.Subnet.Addr().AsSlice())
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], base+uint32(offset))
	return netip.AddrFrom4(b), nil
}

// Offset returns the host offset of addr in the subnet.
func (n AgentNetwork) Offset(addr netip.Addr) (int, error) {
	if !n.Subnet.Contains(addr) {
		return 0, fmt.Errorf("%s is outside %s", addr, n.Subnet)
	}
	off := int(binary.BigEndian.Uint32(addr.AsSlice()) - binary.BigEndian.Uint32(n.Subnet.Addr().AsSlice()))
	if off < 1 || off > n.HostCount() {
		return 0, fmt.Errorf("%s is the network or broadcast address", addr)
	}
	return off, nil
}

// MAC returns the agent NIC MAC for a host offset.
func (n AgentNetwork) MAC(offset int) net.HardwareAddr {
	return net.HardwareAddr{n.MACPrefix[0], n.MACPrefix[1], n.MACPrefix[2],
		byte(offset >> 16), byte(offset >> 8), byte(offset)}
}

// AddressForMAC returns the IP encoded in an agent NIC's MAC, or false if
// the MAC doesn't carry the prefix.
func (n AgentNetwork) AddressForMAC(mac net.HardwareAddr) (netip.Addr, bool) {
	if len(mac) != 6 || mac[0] != n.MACPrefix[0] || mac[1] != n.MACPrefix[1] || mac[2] != n.MACPrefix[2] {
		return netip.Addr{}, false
	}
	addr, err := n.Address(int(mac[3])<<16 | int(mac[4])<<8 | int(mac[5]))
	return addr, err == nil
}

// FormatMAC renders a MAC the way Proxmox expects it in netX properties.
func FormatMAC(mac net.HardwareAddr) string {
	return strings.ToUpper(mac.String())
}
