package httpapi

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("::1/128")}
	trusted := func(a netip.Addr) bool {
		for _, p := range proxies {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		name, remote string
		xff          []string
		realIP       string
		want         string
	}{
		{name: "direct", remote: "192.0.2.7:5000", want: "192.0.2.7"},
		{name: "untrusted sender's headers ignored", remote: "192.0.2.7:5000", xff: []string{"198.51.100.1"}, realIP: "198.51.100.2", want: "192.0.2.7"},
		{name: "one proxy", remote: "10.0.0.5:5000", xff: []string{"198.51.100.1"}, want: "198.51.100.1"},
		{name: "spoofed entry left of the real one", remote: "10.0.0.5:5000", xff: []string{"1.2.3.4, 198.51.100.1"}, want: "198.51.100.1"},
		{name: "chain of proxies", remote: "10.0.0.5:5000", xff: []string{"198.51.100.1, 10.0.0.9"}, want: "198.51.100.1"},
		{name: "several headers", remote: "10.0.0.5:5000", xff: []string{"1.2.3.4", "198.51.100.1, 10.0.0.9"}, want: "198.51.100.1"},
		{name: "all trusted", remote: "10.0.0.5:5000", xff: []string{"10.0.0.8, 10.0.0.9"}, want: "10.0.0.8"},
		{name: "garbage", remote: "10.0.0.5:5000", xff: []string{"198.51.100.1, unknown"}, want: "10.0.0.5"},
		{name: "ports and brackets", remote: "[::1]:5000", xff: []string{"[2001:db8::1]:443"}, want: "2001:db8::1"},
		{name: "X-Real-IP", remote: "10.0.0.5:5000", realIP: "198.51.100.3", want: "198.51.100.3"},
		{name: "X-Forwarded-For first", remote: "10.0.0.5:5000", xff: []string{"198.51.100.1"}, realIP: "198.51.100.3", want: "198.51.100.1"},
		{name: "bad X-Real-IP", remote: "10.0.0.5:5000", realIP: "nope", want: "10.0.0.5"},
		{name: "IPv4-mapped proxy", remote: "[::ffff:10.0.0.5]:5000", xff: []string{"198.51.100.1"}, want: "198.51.100.1"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		for _, v := range tc.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if tc.realIP != "" {
			r.Header.Set("X-Real-IP", tc.realIP)
		}
		if got := clientIP(r, trusted); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}
