// Package agentclient talks to Quicksand guest agents.
package agentclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/net/proxy"

	"quicksand/internal/agentproto"
)

type Client struct {
	http *http.Client
	port int
	// tls and dial open stream connections, which bypass the HTTP client.
	tls  *tls.Config
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
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
	dial := transport.DialContext
	if opts.Proxy != "" {
		u, err := url.Parse(opts.Proxy)
		if err != nil || u.Scheme != "socks5" && u.Scheme != "socks5h" {
			return nil, fmt.Errorf("agent proxy must be a socks5:// URL")
		}
		transport.Proxy = http.ProxyURL(u)
		d, err := proxy.FromURL(u, &net.Dialer{Timeout: 5 * time.Second})
		if err != nil {
			return nil, err
		}
		dial = d.(proxy.ContextDialer).DialContext
	}
	if opts.Port == 0 {
		opts.Port = agentproto.DefaultPort
	}
	streamTLS := tlsConfig.Clone()
	streamTLS.NextProtos = []string{"http/1.1"}
	return &Client{http: &http.Client{Transport: transport}, port: opts.Port, tls: streamTLS, dial: dial}, nil
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

func (c *Client) hostPort(host string) string {
	if _, _, err := net.SplitHostPort(host); err != nil {
		return net.JoinHostPort(host, strconv.Itoa(c.port))
	}
	return host
}

func (c *Client) do(ctx context.Context, method, host, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+c.hostPort(host)+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.send(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 3*agentproto.MaxExecOutput))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("agent: invalid response")
	}
	return nil
}

// send performs req, turning error statuses into *Error.
func (c *Client) send(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var e agentproto.Error
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			e.Error = resp.Status
		}
		return nil, &Error{Status: resp.StatusCode, Message: e.Error}
	}
	return resp, nil
}
