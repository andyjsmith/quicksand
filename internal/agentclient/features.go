package agentclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"quicksand/internal/agentproto"
)

// Processes lists the guest's processes.
func (c *Client) Processes(ctx context.Context, host string) (agentproto.ProcessList, error) {
	var out agentproto.ProcessList
	err := c.do(ctx, http.MethodGet, host, "/v1/processes", nil, &out)
	return out, err
}

// Kill ends a guest process.
func (c *Client) Kill(ctx context.Context, host string, pid int, force bool) error {
	return c.do(ctx, http.MethodPost, host, "/v1/processes/"+strconv.Itoa(pid)+"/kill",
		agentproto.KillRequest{Force: force}, nil)
}

// Connections lists the guest's TCP and UDP sockets.
func (c *Client) Connections(ctx context.Context, host string) (agentproto.ConnectionList, error) {
	var out agentproto.ConnectionList
	err := c.do(ctx, http.MethodGet, host, "/v1/connections", nil, &out)
	return out, err
}

// FileRef names a guest path: absolute, or relative to Base (the agent's
// default directory when empty).
type FileRef struct {
	Base string
	Path string
}

func (f FileRef) query(extra ...string) string {
	q := url.Values{}
	if f.Base != "" {
		q.Set("base", f.Base)
	}
	q.Set("path", f.Path)
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	return "?" + q.Encode()
}

// ListFiles lists a guest directory.
func (c *Client) ListFiles(ctx context.Context, host string, f FileRef) (agentproto.FileList, error) {
	var out agentproto.FileList
	err := c.do(ctx, http.MethodGet, host, "/v1/files"+f.query(), nil, &out)
	return out, err
}

// HashFile hashes a guest file.
func (c *Client) HashFile(ctx context.Context, host string, f FileRef) (agentproto.FileHashes, error) {
	var out agentproto.FileHashes
	err := c.do(ctx, http.MethodGet, host, "/v1/files/hash"+f.query(), nil, &out)
	return out, err
}

// Mkdir creates a guest directory.
func (c *Client) Mkdir(ctx context.Context, host string, f FileRef) (agentproto.FileEntry, error) {
	var out agentproto.FileEntry
	err := c.do(ctx, http.MethodPost, host, "/v1/files/mkdir"+f.query(), nil, &out)
	return out, err
}

// Remove deletes a guest file or (recursively) directory.
func (c *Client) Remove(ctx context.Context, host string, f FileRef, recursive bool) error {
	r := "0"
	if recursive {
		r = "1"
	}
	return c.do(ctx, http.MethodDelete, host, "/v1/files"+f.query("recursive", r), nil, nil)
}

// Download opens a guest file (or a directory, as a zip archive). The
// caller closes the response body; its headers carry Content-Length,
// Content-Disposition and Last-Modified.
func (c *Client) Download(ctx context.Context, host string, f FileRef) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+c.hostPort(host)+"/v1/files/content"+f.query(), nil)
	if err != nil {
		return nil, err
	}
	return c.send(req)
}

// SetDisplay changes the guest's screen resolution.
func (c *Client) SetDisplay(ctx context.Context, host string, req agentproto.DisplayRequest) (agentproto.DisplayResult, error) {
	var out agentproto.DisplayResult
	err := c.do(ctx, http.MethodPost, host, "/v1/display", req, &out)
	return out, err
}

// SetLinks gives the guest's link-network NICs their addresses (the
// complete set). Agents from before link networks answer 404.
func (c *Client) SetLinks(ctx context.Context, host string, req agentproto.LinkRequest) (agentproto.LinkResult, error) {
	var out agentproto.LinkResult
	err := c.do(ctx, http.MethodPut, host, "/v1/links", req, &out)
	return out, err
}

// DownloadZip streams a guest file or directory as a zip archive encrypted
// with password. The caller closes the response body.
func (c *Client) DownloadZip(ctx context.Context, host string, f FileRef, password string) (*http.Response, error) {
	body, err := json.Marshal(agentproto.ZipRequest{Password: password})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+c.hostPort(host)+"/v1/files/zip"+f.query(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.send(req)
}

// Upload writes body (size bytes, or -1 if unknown) to a guest file.
func (c *Client) Upload(ctx context.Context, host string, f FileRef, body io.Reader, size int64, overwrite bool) (agentproto.FileEntry, error) {
	o := "0"
	if overwrite {
		o = "1"
	}
	var out agentproto.FileEntry
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "https://"+c.hostPort(host)+"/v1/files/content"+f.query("overwrite", o), body)
	if err != nil {
		return out, err
	}
	req.ContentLength = size
	resp, err := c.send(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}

// Shell starts a command in the guest and returns its stream, which
// carries frames (see agentproto.ReadFrame).
func (c *Client) Shell(ctx context.Context, host string, req agentproto.ShellRequest) (net.Conn, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return c.stream(ctx, host, "/v1/shell?"+url.Values{"request": {string(b)}}.Encode())
}

// Connect opens a TCP connection from the guest to addr.
func (c *Client) Connect(ctx context.Context, host, addr string) (net.Conn, error) {
	return c.stream(ctx, host, "/v1/connect?"+url.Values{"addr": {addr}}.Encode())
}

func (c *Client) stream(ctx context.Context, host, path string) (net.Conn, error) {
	addr := c.hostPort(host)
	raw, err := c.dial(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, c.tls)
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+path, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	// Bound the request phase; the stream itself has no deadline.
	if d, ok := ctx.Deadline(); ok {
		conn.SetDeadline(d)
	}
	s, err := agentproto.DialStream(conn, req)
	if err != nil {
		conn.Close()
		if se, ok := err.(*agentproto.StatusError); ok {
			return nil, &Error{Status: se.Status, Message: se.Message}
		}
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return s, nil
}
