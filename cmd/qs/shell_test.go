package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"quicksand/internal/agent"
	"quicksand/internal/agentclient"
	"quicksand/internal/agentproto"
)

// startServer runs a real agent behind a stand-in for the Quicksand
// server's VM list, file and stream endpoints (for one VM, "vm1", whose
// files directory is the returned temporary directory), and returns a
// client signed in to it.
func startServer(t *testing.T) (*client, string) {
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

	guestDir := t.TempDir()
	var zipMu sync.Mutex
	var zipReq struct{ Path, Password string }
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer qst_test" {
			http.Error(w, `{"error":"not signed in"}`, http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		agentHost := strings.TrimPrefix(agentSrv.URL, "https://")
		ref := agentclient.FileRef{Base: guestDir, Path: q.Get("path")}
		fail := func(err error) {
			status := http.StatusBadGateway
			var ae *agentclient.Error
			if errors.As(err, &ae) {
				status = ae.Status
			}
			b, _ := json.Marshal(map[string]string{"error": err.Error()})
			http.Error(w, string(b), status)
		}
		relay := func(resp *http.Response, err error) {
			if err != nil {
				fail(err)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Length", resp.Header.Get("Content-Length"))
			io.Copy(w, resp.Body)
		}
		switch {
		case r.URL.Path == "/api/vms":
			w.Write([]byte(`[{"id":"x","name":"vm1","state":"ready","agent":{"status":"connected","os":"linux"}}]`))
			return
		case strings.HasSuffix(r.URL.Path, "/exec"):
			var req agentproto.ExecRequest
			json.NewDecoder(r.Body).Decode(&req)
			res, err := ac.Exec(r.Context(), agentHost, req)
			if err != nil {
				fail(err)
				return
			}
			json.NewEncoder(w).Encode(res)
			return
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/files"):
			list, err := ac.ListFiles(r.Context(), agentHost, ref)
			if err != nil {
				fail(err)
				return
			}
			json.NewEncoder(w).Encode(list)
			return
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/files/content"):
			relay(ac.Download(r.Context(), agentHost, ref))
			return
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/files/content"):
			e, err := ac.Upload(r.Context(), agentHost, ref, r.Body, r.ContentLength, q.Get("overwrite") == "1")
			if err != nil {
				fail(err)
				return
			}
			json.NewEncoder(w).Encode(e)
			return
		case strings.HasSuffix(r.URL.Path, "/files/zip"):
			var req struct{ Path, Password string }
			json.NewDecoder(r.Body).Decode(&req)
			zipMu.Lock()
			zipReq = req
			zipMu.Unlock()
			w.Write([]byte(`{"url":"/api/downloads/token"}`))
			return
		case r.URL.Path == "/api/downloads/token":
			zipMu.Lock()
			req := zipReq
			zipMu.Unlock()
			relay(ac.DownloadZip(r.Context(), agentHost, agentclient.FileRef{Base: guestDir, Path: req.Path}, req.Password))
			return
		}
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
	s := settings{Server: srv.URL, Token: "qst_test", Fingerprint: certFingerprint(srv.Certificate().Raw)}
	// Commands read the settings from the user's config directory.
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("AppData", home)
	t.Setenv("HOME", home)
	if err := saveSettings(s); err != nil {
		t.Fatal(err)
	}
	return newClient(s), guestDir
}

func TestShellStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a Unix shell")
	}
	c, _ := startServer(t)

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
	bad.Token = "qst_wrong"
	if _, err := bad.stream("/api/vms/x/shell"); err == nil || err.(*apiError).Status != 401 {
		t.Errorf("stream with a bad session: %v", err)
	}
	bad = *newClient(settings{Server: c.Server, Token: c.Token, Fingerprint: strings.Repeat("00", 32)})
	if _, err := bad.stream("/api/vms/x/shell"); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Errorf("stream with the wrong fingerprint: %v", err)
	}
}
