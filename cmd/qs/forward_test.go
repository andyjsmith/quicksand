package main

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestParseForward(t *testing.T) {
	for _, tc := range []struct{ spec, listen, target string }{
		{"8080:localhost:80", "127.0.0.1:8080", "localhost:80"},
		{"0.0.0.0:8080:10.0.0.5:80", "0.0.0.0:8080", "10.0.0.5:80"},
		{"*:8080:db:5432", ":8080", "db:5432"},
		{"[::1]:8080:[fe80::1]:22", "[::1]:8080", "[fe80::1]:22"},
		{"9999:8.8.8.8:53", "127.0.0.1:9999", "8.8.8.8:53"},
	} {
		f, err := parseForward(tc.spec)
		if err != nil || f.listen != tc.listen || f.target != tc.target {
			t.Errorf("parseForward(%q) = %+v, %v", tc.spec, f, err)
		}
	}
	for _, bad := range []string{"8080", "8080:host", "x:host:80", "8080:host:0", "8080::80", "8.8.8.8:127.0.0.1:9999", "[::1:8080:h:1"} {
		if f, err := parseForward(bad); err == nil {
			t.Errorf("parseForward(%q) = %+v, want an error", bad, f)
		}
	}
}

func TestForward(t *testing.T) {
	c := startServer(t)
	// A guest-side service: echoes, then says goodbye on half-close.
	svc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	go func() {
		for {
			conn, err := svc.Accept()
			if err != nil {
				return
			}
			go func() {
				io.Copy(conn, conn)
				conn.Write([]byte("bye"))
				conn.Close()
			}()
		}
	}()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go serveForward(c, vm{ID: "x"}, ln, svc.Addr().String())

	// Two connections at once, each its own stream.
	for range 2 {
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		conn.Write([]byte("ping"))
		conn.(*net.TCPConn).CloseWrite()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		got, err := io.ReadAll(conn)
		conn.Close()
		if string(got) != "pingbye" {
			t.Errorf("got %q, %v", got, err)
		}
	}
}
