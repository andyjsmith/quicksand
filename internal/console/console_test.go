package console

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func pipeDialer(ctx context.Context) (net.Conn, error) {
	a, _ := net.Pipe()
	return a, nil
}

func TestManagerLimitsConnections(t *testing.T) {
	m := NewManager(time.Minute)
	s := m.Create(VNC, "pw", "title", 1, pipeDialer)

	if _, conn, err := m.Open(context.Background(), s.ID); err != nil {
		t.Fatalf("first open: %v", err)
	} else {
		conn.Close()
	}
	if _, _, err := m.Open(context.Background(), s.ID); !errors.Is(err, ErrExhausted) {
		t.Fatalf("second open: got %v, want ErrExhausted", err)
	}
}

func TestManagerUnlimitedConnections(t *testing.T) {
	m := NewManager(time.Minute)
	s := m.Create(Spice, "pw", "title", 0, pipeDialer)
	for i := range 5 {
		_, conn, err := m.Open(context.Background(), s.ID)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		conn.Close()
	}
}

func TestManagerExpiry(t *testing.T) {
	m := NewManager(time.Millisecond)
	s := m.Create(Spice, "pw", "title", 0, pipeDialer)
	time.Sleep(5 * time.Millisecond)
	if _, _, err := m.Open(context.Background(), s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if _, _, err := m.Open(context.Background(), "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestRelay(t *testing.T) {
	clientA, serverA := net.Pipe()
	clientB, serverB := net.Pipe()
	done := make(chan struct{})
	go func() {
		Relay(serverA, serverB)
		close(done)
	}()

	go clientA.Write([]byte("ping"))
	buf := make([]byte, 4)
	if _, err := clientB.Read(buf); err != nil || string(buf) != "ping" {
		t.Fatalf("got %q, %v", buf, err)
	}
	clientA.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay did not stop after one side closed")
	}
}
