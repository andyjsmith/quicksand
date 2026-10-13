// Package agentclient talks to Quicksand guest agents.
package agentclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"quicksand/internal/agentproto"
)

type Client struct {
	http *http.Client
	port int
}

// Options configures a Client.
type Options struct {
	Key  ed25519.PrivateKey
	Port int
	// Proxy, if set, is a socks5:// URL connections go through, e.g. an SSH
	// dynamic forward into the agent network during development.
	Proxy string
}

func New(opts Options) (*Client, error) {
	tlsConfig, err := agentproto.ClientTLSConfig(opts.Key)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		TLSClientConfig:     tlsConfig,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     60 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	if opts.Proxy != "" {
		u, err := url.Parse(opts.Proxy)
		if err != nil || u.Scheme != "socks5" && u.Scheme != "socks5h" {
			return nil, fmt.Errorf("agent proxy must be a socks5:// URL")
		}
		transport.Proxy = http.ProxyURL(u)
	}
	if opts.Port == 0 {
		opts.Port = agentproto.DefaultPort
	}
	return &Client{http: &http.Client{Transport: transport}, port: opts.Port}, nil
}

// Error is an error response from an agent.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("agent: %d %s", e.Status, e.Message) }

// Info asks the agent at host (an IP, optionally with :port) about its guest.
func (c *Client) Info(ctx context.Context, host string) (agentproto.Info, error) {
	var info agentproto.Info
	err := c.do(ctx, http.MethodGet, host, "/v1/info", nil, &info)
	return info, err
}

// Exec runs a command in the guest and waits for it to finish.
func (c *Client) Exec(ctx context.Context, host string, req agentproto.ExecRequest) (agentproto.ExecResponse, error) {
	var resp agentproto.ExecResponse
	err := c.do(ctx, http.MethodPost, host, "/v1/exec", req, &resp)
	return resp, err
}

func (c *Client) do(ctx context.Context, method, host, path string, body, out any) error {
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, strconv.Itoa(c.port))
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+host+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 3*agentproto.MaxExecOutput))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e agentproto.Error
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			e.Error = resp.Status
		}
		return &Error{Status: resp.StatusCode, Message: e.Error}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("agent: invalid response")
	}
	return nil
}
