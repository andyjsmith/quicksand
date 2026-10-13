package pve

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// SpiceTicket is the connection info returned by the spiceproxy endpoint, in
// the shape remote-viewer expects (see virt-viewer's .vv file format).
type SpiceTicket struct {
	// Proxy is the HTTP CONNECT proxy (the node's spiceproxy, port 3128).
	Proxy string `json:"proxy"`
	// Host is the opaque "pvespiceproxy:..." ticket used as the CONNECT host.
	Host        string `json:"host"`
	TLSPort     int    `json:"tls-port"`
	Password    string `json:"password"`
	CA          string `json:"ca"`
	HostSubject string `json:"host-subject"`
	Title       string `json:"title"`
}

func (c *Client) SpiceProxy(ctx context.Context, node string, vmid int) (*SpiceTicket, error) {
	var t SpiceTicket
	if err := c.do(ctx, http.MethodPost, vmPath(node, vmid, "spiceproxy"), url.Values{}, &t); err != nil {
		return nil, err
	}
	// The CA comes back with its newlines escaped, as it would be in a .vv file.
	t.CA = strings.ReplaceAll(t.CA, `\n`, "\n")
	return &t, nil
}

// DialSpice opens a TLS connection to the guest's SPICE server through the
// node's spiceproxy. Each SPICE channel needs its own connection.
func DialSpice(ctx context.Context, t *SpiceTicket) (net.Conn, error) {
	proxy, err := url.Parse(t.Proxy)
	if err != nil {
		return nil, fmt.Errorf("spice: parse proxy: %w", err)
	}
	proxyAddr := proxy.Host
	if proxy.Port() == "" {
		proxyAddr = net.JoinHostPort(proxy.Hostname(), "3128")
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("spice: dial proxy: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			conn.Close()
		}
	}()
	if deadline, has := ctx.Deadline(); has {
		conn.SetDeadline(deadline)
	}

	target := t.Host + ":" + strconv.Itoa(t.TLSPort)
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.0\r\nHost: %s\r\n\r\n", target, target); err != nil {
		return nil, fmt.Errorf("spice: send CONNECT: %w", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, fmt.Errorf("spice: read CONNECT response: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spice: proxy refused CONNECT: %s", resp.Status)
	}
	// The SPICE server only speaks after our TLS ClientHello, so nothing
	// beyond the response headers can have been buffered.
	if br.Buffered() > 0 {
		return nil, errors.New("spice: unexpected data after CONNECT response")
	}

	tlsConn := tls.Client(conn, spiceTLSConfig(t))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("spice: tls handshake: %w", err)
	}
	conn.SetDeadline(time.Time{})
	ok = true
	return tlsConn, nil
}

// spiceTLSConfig verifies the SPICE server the way remote-viewer does: the
// chain must lead to the cluster CA and the subject must equal host-subject.
// The certificate's names are not checked, as the CONNECT host is a ticket.
func spiceTLSConfig(t *SpiceTicket) *tls.Config {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(t.CA))
	return &tls.Config{
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no server certificate")
			}
			leaf := cs.PeerCertificates[0]
			inter := x509.NewCertPool()
			for _, cert := range cs.PeerCertificates[1:] {
				inter.AddCert(cert)
			}
			if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter}); err != nil {
				return err
			}
			if t.HostSubject != "" && !subjectMatches(leaf, t.HostSubject) {
				return fmt.Errorf("certificate subject %q does not match %q", leaf.Subject, t.HostSubject)
			}
			return nil
		},
	}
}

// subjectMatches compares a certificate subject against a "K=V,K=V" string
// without depending on attribute order.
func subjectMatches(cert *x509.Certificate, subject string) bool {
	want := map[string]bool{}
	for _, part := range strings.Split(subject, ",") {
		want[strings.TrimSpace(part)] = true
	}
	got := map[string]bool{}
	for _, rdn := range cert.Subject.ToRDNSequence() {
		for _, atv := range rdn {
			key, ok := attrNames[atv.Type.String()]
			if !ok {
				key = atv.Type.String()
			}
			got[fmt.Sprintf("%s=%v", key, atv.Value)] = true
		}
	}
	if len(got) != len(want) {
		return false
	}
	for k := range want {
		if !got[k] {
			return false
		}
	}
	return true
}

var attrNames = map[string]string{
	"2.5.4.3":  "CN",
	"2.5.4.6":  "C",
	"2.5.4.7":  "L",
	"2.5.4.8":  "ST",
	"2.5.4.10": "O",
	"2.5.4.11": "OU",
}

// VNCTicket is the result of the vncproxy endpoint in websocket mode.
type VNCTicket struct {
	Port   string `json:"port"`
	Ticket string `json:"ticket"`
	User   string `json:"user"`
	// Password is the VNC password to authenticate the RFB session with.
	Password string `json:"password"`
}

func (c *Client) VNCProxy(ctx context.Context, node string, vmid int) (*VNCTicket, error) {
	var t VNCTicket
	form := url.Values{"websocket": {"1"}}
	if err := c.do(ctx, http.MethodPost, vmPath(node, vmid, "vncproxy"), form, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// DialVNC connects to the vncwebsocket endpoint and returns the RFB stream.
func (c *Client) DialVNC(ctx context.Context, node string, vmid int, t *VNCTicket) (net.Conn, error) {
	u := c.base.JoinPath("/api2/json", vmPath(node, vmid, "vncwebsocket"))
	u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	u.RawQuery = url.Values{"port": {t.Port}, "vncticket": {t.Ticket}}.Encode()

	ws, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{
		HTTPClient:   c.wsHTTP,
		HTTPHeader:   http.Header{"Authorization": {c.auth}},
		Subprotocols: []string{"binary"},
	})
	if err != nil {
		return nil, fmt.Errorf("vnc: dial websocket: %w", err)
	}
	ws.SetReadLimit(-1)
	// The returned conn outlives ctx, which only bounds the dial.
	return websocket.NetConn(context.Background(), ws, websocket.MessageBinary), nil
}
