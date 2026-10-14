package agentclient

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"quicksand/internal/agent"
	"quicksand/internal/agentproto"
)

// startAgent runs a real agent over TLS, trusting the returned server key.
func startAgent(t *testing.T) (*httptest.Server, *Client) {
	t.Helper()
	key, err := agentproto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := key.Public().(ed25519.PublicKey)
	a, err := agent.New(agent.Config{AuthorizedKeys: []string{agentproto.FormatPublicKey(pub)}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(a.Handler())
	if srv.TLS, err = a.TLSConfig(); err != nil {
		t.Fatal(err)
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	c, err := New(Options{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	return srv, c
}

func host(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "https://") }

func TestInfoAndExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix commands")
	}
	srv, c := startAgent(t)
	ctx := context.Background()

	info, err := c.Info(ctx, host(srv))
	if err != nil {
		t.Fatal(err)
	}
	if info.OS != runtime.GOOS || info.ProtocolVersion != agentproto.Version || info.Hostname == "" {
		t.Errorf("unexpected info %+v", info)
	}

	res, err := c.Exec(ctx, host(srv), agentproto.ExecRequest{Shell: "echo hello; echo oops >&2; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || strings.TrimSpace(string(res.Stdout)) != "hello" || strings.TrimSpace(string(res.Stderr)) != "oops" {
		t.Errorf("shell exec = %+v (stdout %q stderr %q)", res, res.Stdout, res.Stderr)
	}

	res, err = c.Exec(ctx, host(srv), agentproto.ExecRequest{Argv: []string{"cat"}, Stdin: []byte("piped")})
	if err != nil || string(res.Stdout) != "piped" || res.ExitCode != 0 {
		t.Errorf("argv exec with stdin = %+v, %v", res, err)
	}

	res, err = c.Exec(ctx, host(srv), agentproto.ExecRequest{Argv: []string{"/nonexistent/program"}})
	if err != nil || res.ExitCode != -1 || res.Error == "" {
		t.Errorf("missing program = %+v, %v", res, err)
	}

	start := time.Now()
	res, err = c.Exec(ctx, host(srv), agentproto.ExecRequest{Shell: "sleep 30", TimeoutSeconds: 1})
	if err != nil || !res.TimedOut || time.Since(start) > 10*time.Second {
		t.Errorf("timeout = %+v, %v after %v", res, err, time.Since(start))
	}

	res, err = c.Exec(ctx, host(srv), agentproto.ExecRequest{Shell: "head -c 5000000 /dev/zero"})
	if err != nil || !res.Truncated || len(res.Stdout) != agentproto.MaxExecOutput {
		t.Errorf("large output: truncated=%v len=%d, %v", res.Truncated, len(res.Stdout), err)
	}

	if _, err := c.Exec(ctx, host(srv), agentproto.ExecRequest{}); err == nil {
		t.Error("empty exec request accepted")
	}
}

func TestRejectsUnauthorizedClients(t *testing.T) {
	srv, _ := startAgent(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A client with a different key.
	otherKey, _ := agentproto.GenerateKey()
	other, err := New(Options{Key: otherKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Info(ctx, host(srv)); err == nil {
		t.Error("client with an unauthorized key was accepted")
	}

	// A client with no certificate at all.
	anon := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	if resp, err := anon.Get(srv.URL + "/v1/info"); err == nil {
		resp.Body.Close()
		t.Errorf("client without a certificate got %s", resp.Status)
	}
}
