package main

import (
	"crypto/ed25519"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"quicksand/internal/agent"
	"quicksand/internal/agentclient"
	"quicksand/internal/agentproto"
)

// startServer runs a real agent behind a stand-in for the Quicksand
// server's shell endpoint, and returns a client signed in to it.
func startServer(t *testing.T) *client {
	t.Helper()
	key, err := agentproto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	a, err := agent.New(agent.Config{AuthorizedKeys: []string{agentproto.FormatPublicKey(key.Public().(ed25519.PublicKey))}})
	if err != nil {
		t.Fatal(err)
	}
	agentSrv := httptest.NewUnstartedServer(a.Handler())
	if agentSrv.TLS, err = a.TLSConfig(); err != nil {
		t.Fatal(err)
	}
	agentSrv.StartTLS()
	t.Cleanup(agentSrv.Close)
	ac, err := agentclient.New(agentclient.Options{Key: key})
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ck, err := r.Cookie("quicksand_session"); err != nil || ck.Value != "s3cret" {
			http.Error(w, `{"error":"not signed in"}`, http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		if strings.HasSuffix(r.URL.Path, "/connect") {
			conn, err := ac.Connect(r.Context(), strings.TrimPrefix(agentSrv.URL, "https://"), q.Get("addr"))
			if err != nil {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
				return
			}
			client, err := agentproto.AcceptStream(w, r)
			if err != nil {
				conn.Close()
				return
			}
			agentproto.Join(client, conn)
			return
		}
		req := agentproto.ShellRequest{Command: q.Get("command"), PTY: q.Get("pty") == "1", Term: q.Get("term")}
		req.Cols, _ = strconv.Atoi(q.Get("cols"))
		req.Rows, _ = strconv.Atoi(q.Get("rows"))
		conn, err := ac.Shell(r.Context(), strings.TrimPrefix(agentSrv.URL, "https://"), req)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		client, err := agentproto.AcceptStream(w, r)
		if err != nil {
			conn.Close()
			return
		}
		agentproto.Join(client, conn)
	}))
	t.Cleanup(srv.Close)
	return newClient(settings{Server: srv.URL, Session: "s3cret", Fingerprint: certFingerprint(srv.Certificate().Raw)})
}

func TestShellStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a Unix shell")
	}
	c := startServer(t)

	// Swap stdio for pipes for runShell.
	stdin, stdinW, _ := os.Pipe()
	stdoutR, stdout, _ := os.Pipe()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdin, stdout
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	stdinW.WriteString("hello\n")
	stdinW.Close()

	conn, err := c.stream("/api/vms/x/shell?command=" + "tr+a-z+A-Z%3B+exit+5")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	code, err := runShell(conn, false, false, false)
	stdout.Close()
	out, _ := io.ReadAll(stdoutR)
	if err != nil || code != 5 || string(out) != "HELLO\n" {
		t.Errorf("runShell = %d, %v; output %q", code, err, out)
	}

	bad := *c
	bad.Session = "wrong"
	if _, err := bad.stream("/api/vms/x/shell"); err == nil || err.(*apiError).Status != 401 {
		t.Errorf("stream with a bad session: %v", err)
	}
	bad = *newClient(settings{Server: c.Server, Session: c.Session, Fingerprint: strings.Repeat("00", 32)})
	if _, err := bad.stream("/api/vms/x/shell"); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Errorf("stream with the wrong fingerprint: %v", err)
	}
}
