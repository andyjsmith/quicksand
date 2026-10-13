// Package pve is a minimal Proxmox VE API client covering what Quicksand needs.
package pve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"quicksand/internal/config"
)

type Client struct {
	base      *url.URL
	auth      string
	tlsConfig *tls.Config
	http      *http.Client
	// wsHTTP has no overall timeout, which would cut off long-lived websockets.
	wsHTTP *http.Client
}

func New(cfg config.PVE) (*Client, error) {
	base, err := url.Parse(strings.TrimSuffix(cfg.URL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse pve url: %w", err)
	}
	tlsConfig, err := apiTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	return &Client{
		base:      base,
		auth:      fmt.Sprintf("PVEAPIToken=%s=%s", cfg.TokenID, cfg.TokenSecret),
		tlsConfig: tlsConfig,
		http:      &http.Client{Transport: transport, Timeout: 30 * time.Second},
		wsHTTP:    &http.Client{Transport: transport},
	}, nil
}

func apiTLSConfig(cfg config.PVE) (*tls.Config, error) {
	switch {
	case cfg.InsecureSkipVerify:
		return &tls.Config{InsecureSkipVerify: true}, nil
	case cfg.Fingerprint != "":
		want, err := hex.DecodeString(strings.ReplaceAll(cfg.Fingerprint, ":", ""))
		if err != nil || len(want) != sha256.Size {
			return nil, errors.New("pve.fingerprint must be a hex SHA-256 fingerprint")
		}
		return &tls.Config{
			// Verification is replaced by the fingerprint pin below.
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				if len(rawCerts) == 0 {
					return errors.New("pve: no server certificate")
				}
				got := sha256.Sum256(rawCerts[0])
				if !bytes.Equal(got[:], want) {
					return fmt.Errorf("pve: certificate fingerprint mismatch (got %X)", got)
				}
				return nil
			},
		}, nil
	default:
		return &tls.Config{}, nil
	}
}

// Error is an error response from the Proxmox API.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("pve: %d %s", e.Status, e.Message)
}

// do performs an API call and decodes the "data" member of the response into out.
func (c *Client) do(ctx context.Context, method, path string, form url.Values, out any) error {
	u := c.base.JoinPath("/api2/json", path)
	var body io.Reader
	if method == http.MethodGet || method == http.MethodDelete {
		u.RawQuery = form.Encode()
	} else if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.auth)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var envelope struct {
		Data   json.RawMessage   `json:"data"`
		Errors map[string]string `json:"errors"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		msg := resp.Status
		if json.Unmarshal(raw, &envelope) == nil && len(envelope.Errors) > 0 {
			msg = fmt.Sprintf("%s %v", msg, envelope.Errors)
		}
		return &Error{Status: resp.StatusCode, Message: msg}
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("pve: decode %s: %w", path, err)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Data, out)
}
