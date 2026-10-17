package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"quicksand/internal/agentproto"
)

// settings is what "qs login" saves, in the user's config directory.
type settings struct {
	Server string `json:"server"`
	// Token is the API token qs signs in with.
	Token string `json:"token"`
	// Fingerprint pins the server certificate's SHA-256 when it isn't
	// trusted by the system (e.g. Quicksand's self-signed certificate).
	Fingerprint string `json:"fingerprint,omitempty"`
}

func settingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "quicksand", "cli.json"), nil
}

func loadSettings() (settings, error) {
	var s settings
	p, err := settingsPath()
	if err != nil {
		return s, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return s, errors.New(`not signed in; run "qs login <server URL>"`)
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

func saveSettings(s settings) error {
	p, err := settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

// client calls the Quicksand API as the signed-in user.
type client struct {
	settings
	http *http.Client
}

func newClient(s settings) *client {
	return &client{settings: s, http: &http.Client{Transport: transport(s.Fingerprint)}}
}

// transport verifies the server certificate against the system roots, or
// against a pinned fingerprint if one is set.
func transport(fingerprint string) *http.Transport {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if fingerprint != "" {
		cfg.InsecureSkipVerify = true
		cfg.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || certFingerprint(raw[0]) != fingerprint {
				return errors.New("the server certificate doesn't match the pinned fingerprint; run \"qs login\" again if it was replaced")
			}
			return nil
		}
	}
	return &http.Transport{
		TLSClientConfig:     cfg,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

func certFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// apiError is an error response from the server.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	if e.Status == http.StatusUnauthorized {
		return `your session has ended; run "qs login" again`
	}
	return e.Message
}

// do sends a JSON request and decodes a JSON response into out (if not nil).
func (c *client) do(method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(c.Server, "/")+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			e.Error = resp.Status
		}
		return &apiError{Status: resp.StatusCode, Message: e.Error}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// authorize adds the signed-in user's token to a request.
func (c *client) authorize(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

// tokenName names the API tokens qs signs in with, so they're recognizable
// in the web client's list.
func tokenName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "qs"
	}
	return "qs on " + host
}

// login signs in with a password and returns a new API token.
func (c *client) login(username, password string) (string, error) {
	b, _ := json.Marshal(map[string]string{"username": username, "password": password, "token": tokenName()})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(c.Server, "/")+"/api/auth/login", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Token string `json:"token"`
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		if out.Error == "" {
			out.Error = resp.Status
		}
		return "", fmt.Errorf("sign in failed: %s", out.Error)
	}
	if out.Token == "" {
		return "", errors.New("the server didn't return a token")
	}
	return out.Token, nil
}

// stream opens a stream (see agentproto.DialStream) to an API path, such
// as a VM's shell.
func (c *client) stream(path string) (net.Conn, error) {
	u, err := url.Parse(strings.TrimRight(c.Server, "/") + path)
	if err != nil {
		return nil, err
	}
	addr := u.Host
	if u.Port() == "" {
		port := "443"
		if u.Scheme == "http" {
			port = "80"
		}
		addr = net.JoinHostPort(u.Hostname(), port)
	}
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "https" {
		cfg := c.http.Transport.(*http.Transport).TLSClientConfig.Clone()
		cfg.ServerName = u.Hostname()
		tc := tls.Client(conn, cfg)
		tc.SetDeadline(time.Now().Add(10 * time.Second))
		if err := tc.Handshake(); err != nil {
			conn.Close()
			return nil, err
		}
		conn = tc
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	c.authorize(req)
	// The server answers once the agent has (agentTimeout there).
	conn.SetDeadline(time.Now().Add(45 * time.Second))
	s, err := agentproto.DialStream(conn, req)
	if err != nil {
		conn.Close()
		var se *agentproto.StatusError
		if errors.As(err, &se) {
			return nil, &apiError{Status: se.Status, Message: se.Message}
		}
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return s, nil
}

// send makes a request with a streamed body (size bytes, -1 if unknown)
// and returns the response, still to be read, if it succeeded.
func (c *client) send(method, path string, body io.Reader, size int64, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, strings.TrimRight(c.Server, "/")+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
		req.Header.Set("Content-Type", contentType)
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			e.Error = resp.Status
		}
		return nil, &apiError{Status: resp.StatusCode, Message: e.Error}
	}
	return resp, nil
}
