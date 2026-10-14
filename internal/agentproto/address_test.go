package agentproto

import (
	"crypto/ed25519"
	"net/netip"
	"strings"
	"testing"
)

func TestAgentNetworkAddressing(t *testing.T) {
	n, err := ParseAgentNetwork("10.99.0.0/24", "")
	if err != nil {
		t.Fatal(err)
	}
	if n.HostCount() != 254 {
		t.Errorf("HostCount = %d, want 254", n.HostCount())
	}
	addr, err := n.Address(23)
	if err != nil || addr != netip.MustParseAddr("10.99.0.23") {
		t.Fatalf("Address(23) = %v, %v", addr, err)
	}
	mac := n.MAC(23)
	if FormatMAC(mac) != "02:51:53:00:00:17" {
		t.Errorf("MAC(23) = %s", FormatMAC(mac))
	}
	if got, ok := n.AddressForMAC(mac); !ok || got != addr {
		t.Errorf("AddressForMAC(%s) = %v, %v", mac, got, ok)
	}
	if off, err := n.Offset(addr); err != nil || off != 23 {
		t.Errorf("Offset = %d, %v", off, err)
	}

	for _, bad := range []int{0, 255, -1} {
		if _, err := n.Address(bad); err == nil {
			t.Errorf("Address(%d) accepted", bad)
		}
	}
	other := n.MAC(23)
	other[0] = 0x52
	if _, ok := n.AddressForMAC(other); ok {
		t.Error("a MAC without the prefix was accepted")
	}

	wide, _ := ParseAgentNetwork("10.64.0.0/10", "02:aa:bb")
	if a, _ := wide.Address(70000); a != netip.MustParseAddr("10.65.17.112") {
		t.Errorf("wide Address(70000) = %v", a)
	}
	for _, bad := range []string{"10.99.0.0/31", "fd00::/64", "nonsense"} {
		if _, err := ParseAgentNetwork(bad, ""); err == nil {
			t.Errorf("subnet %q accepted", bad)
		}
	}
	if _, err := ParseAgentNetwork("10.0.0.0/24", "01:00:00"); err == nil {
		t.Error("multicast MAC prefix accepted")
	}
}

func TestKeyRoundTrip(t *testing.T) {
	priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	parsed, err := ParsePrivateKey(FormatPrivateKey(priv))
	if err != nil || !parsed.Equal(priv) {
		t.Fatalf("ParsePrivateKey(FormatPrivateKey) = %v", err)
	}
	parsedPub, err := ParsePublicKey(FormatPublicKey(pub))
	if err != nil || !parsedPub.Equal(pub) {
		t.Fatalf("ParsePublicKey(FormatPublicKey) = %v, %v", parsedPub, err)
	}
	for _, bad := range []string{"", "ed25519:", "rsa:AAAA", "ed25519:!!!"} {
		if _, err := ParsePublicKey(bad); err == nil {
			t.Errorf("ParsePublicKey(%q) accepted", bad)
		}
	}
	if _, err := ParsePrivateKey(FormatPublicKey(pub)); err == nil || !strings.Contains(err.Error(), "public key") {
		t.Errorf("a public key given as the private key: %v", err)
	}
	if _, err := ParsePrivateKey("ed25519-private:AAAA"); err == nil {
		t.Error("short private key accepted")
	}
}

func TestErrorMessage(t *testing.T) {
	for body, want := range map[string]string{
		`{"error":"exec: \"nope\": not found"}`: `exec: "nope": not found`,
		`plain text`:                            "plain text",
		``:                                      "502 Bad Gateway",
	} {
		if got := errorMessage([]byte(body), "502 Bad Gateway"); got != want {
			t.Errorf("errorMessage(%q) = %q, want %q", body, got, want)
		}
	}
}
