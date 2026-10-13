// Package console brokers browser console connections to guest displays.
//
// A console session is created through the API, which fetches a short-lived
// ticket from Proxmox and returns an opaque session ID. The browser then
// opens one or more websockets for that ID; each is relayed byte-for-byte to
// a fresh upstream connection. The browser speaks the display protocol
// (SPICE, RFB) itself; the server only provides transport.
package console

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

type Protocol string

const (
	Spice Protocol = "spice"
	VNC   Protocol = "vnc"
)

// Dialer opens a new upstream connection for a session.
type Dialer func(ctx context.Context) (net.Conn, error)

type Session struct {
	ID       string
	Protocol Protocol
	// Password is handed to the browser client to authenticate the
	// display protocol itself. It is single-use and short-lived upstream.
	Password string
	Title    string
	// Owner is the user the session was issued to; only they may open it.
	Owner int64

	dial    Dialer
	expires time.Time
	// maxConns limits websockets per session; 0 means unlimited (SPICE
	// opens one per channel).
	maxConns int
	conns    int
}

var (
	ErrNotFound  = errors.New("console session not found or expired")
	ErrExhausted = errors.New("console session already in use")
)

// Manager tracks pending sessions. A session only gates the opening of new
// connections; established relays outlive it.
type Manager struct {
	ttl      time.Duration
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager(ttl time.Duration) *Manager {
	return &Manager{ttl: ttl, sessions: map[string]*Session{}}
}

func (m *Manager) Create(owner int64, protocol Protocol, password, title string, maxConns int, dial Dialer) *Session {
	s := &Session{
		ID:       randomID(),
		Protocol: protocol,
		Password: password,
		Title:    title,
		Owner:    owner,
		dial:     dial,
		expires:  time.Now().Add(m.ttl),
		maxConns: maxConns,
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweep()
	m.sessions[s.ID] = s
	return s
}

// Open dials a new upstream connection for the session with the given ID,
// which must have been issued to owner.
func (m *Manager) Open(ctx context.Context, id string, owner int64) (*Session, net.Conn, error) {
	m.mu.Lock()
	m.sweep()
	s, ok := m.sessions[id]
	if !ok || s.Owner != owner {
		m.mu.Unlock()
		return nil, nil, ErrNotFound
	}
	if s.maxConns > 0 && s.conns >= s.maxConns {
		m.mu.Unlock()
		return nil, nil, ErrExhausted
	}
	s.conns++
	m.mu.Unlock()

	conn, err := s.dial(ctx)
	return s, conn, err
}

func (m *Manager) sweep() {
	now := time.Now()
	for id, s := range m.sessions {
		if now.After(s.expires) {
			delete(m.sessions, id)
		}
	}
}

// Relay copies data between a and b until either side closes.
func Relay(a, b net.Conn) {
	var once sync.Once
	closeBoth := func() {
		a.Close()
		b.Close()
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(a, b)
		once.Do(closeBoth)
	}()
	go func() {
		defer wg.Done()
		io.Copy(b, a)
		once.Do(closeBoth)
	}()
	wg.Wait()
}

func randomID() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
