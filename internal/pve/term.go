package pve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// TermTicket is the result of the termproxy endpoint.
type TermTicket struct {
	Port   json.Number `json:"port"`
	Ticket string      `json:"ticket"`
	User   string      `json:"user"`
}

// TermProxy starts a terminal proxy for a VM's serial port (serial0..3).
func (c *Client) TermProxy(ctx context.Context, node string, vmid int, serial string) (*TermTicket, error) {
	var t TermTicket
	if err := c.do(ctx, http.MethodPost, vmPath(node, vmid, "termproxy"), url.Values{"serial": {serial}}, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// TermConn is a serial console connection. Proxmox's terminal protocol
// (as its xterm.js page speaks it): after "user:ticket\n" and the reply
// "OK", output arrives as raw bytes; input is sent as "0:<length>:<data>",
// resizes as "1:<cols>:<rows>:" and keepalives as "2".
type TermConn struct {
	ws      *websocket.Conn
	pending []byte // output received with the "OK"
	mu      sync.Mutex
	done    chan struct{}
	once    sync.Once
}

// DialTerm connects to a terminal proxy started by TermProxy.
func (c *Client) DialTerm(ctx context.Context, node string, vmid int, t *TermTicket) (*TermConn, error) {
	u := c.base.JoinPath("/api2/json", vmPath(node, vmid, "vncwebsocket"))
	u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	u.RawQuery = url.Values{"port": {t.Port.String()}, "vncticket": {t.Ticket}}.Encode()
	ws, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{
		HTTPClient:   c.wsHTTP,
		HTTPHeader:   http.Header{"Authorization": {c.auth}},
		Subprotocols: []string{"binary"},
	})
	if err != nil {
		return nil, fmt.Errorf("serial: dial websocket: %w", err)
	}
	ws.SetReadLimit(-1)
	if err := ws.Write(ctx, websocket.MessageBinary, []byte(t.User+":"+t.Ticket+"\n")); err != nil {
		ws.CloseNow()
		return nil, fmt.Errorf("serial: authenticate: %w", err)
	}
	_, reply, err := ws.Read(ctx)
	if err != nil {
		ws.CloseNow()
		return nil, fmt.Errorf("serial: authenticate: %w", err)
	}
	if !strings.HasPrefix(string(reply), "OK") {
		ws.CloseNow()
		return nil, fmt.Errorf("serial: the terminal proxy refused the ticket: %q", reply)
	}
	tc := &TermConn{ws: ws, pending: reply[2:], done: make(chan struct{})}
	go tc.keepalive()
	return tc, nil
}

// keepalive pings like Proxmox's own client; termproxy times out idle
// connections.
func (t *TermConn) keepalive() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-tick.C:
			t.send([]byte("2"))
		}
	}
}

func (t *TermConn) send(msg []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return t.ws.Write(ctx, websocket.MessageBinary, msg)
}

// ReadOutput returns the next piece of console output.
func (t *TermConn) ReadOutput() ([]byte, error) {
	if len(t.pending) > 0 {
		p := t.pending
		t.pending = nil
		return p, nil
	}
	_, msg, err := t.ws.Read(context.Background())
	if err != nil {
		var ce websocket.CloseError
		if errors.As(err, &ce) {
			return nil, errors.New("the serial console closed")
		}
	}
	return msg, err
}

// Input sends keyboard input.
func (t *TermConn) Input(p []byte) error {
	msg := append([]byte("0:"+strconv.Itoa(len(p))+":"), p...)
	return t.send(msg)
}

// Resize tells the terminal its size.
func (t *TermConn) Resize(cols, rows int) error {
	return t.send([]byte("1:" + strconv.Itoa(cols) + ":" + strconv.Itoa(rows) + ":"))
}

func (t *TermConn) Close() error {
	t.once.Do(func() { close(t.done) })
	return t.ws.CloseNow()
}
