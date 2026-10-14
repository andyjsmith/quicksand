package agentproto

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// StreamProtocol is the HTTP Upgrade token switching a request to a raw
// byte stream. Shells and port forwards use streams from the client
// (browser-less) through the Quicksand server to the agent; each hop is an
// HTTP/1.1 request upgraded with it.
const StreamProtocol = "quicksand-stream"

// StatusError is a refused stream request.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string { return fmt.Sprintf("%d %s", e.Status, e.Message) }

// IsStreamRequest reports whether r asks for a stream.
func IsStreamRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), StreamProtocol) &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// AcceptStream switches an HTTP/1.1 request to a stream. Errors before
// this are reported as ordinary HTTP responses.
func AcceptStream(w http.ResponseWriter, r *http.Request) (net.Conn, error) {
	if !IsStreamRequest(r) {
		return nil, errors.New("not a stream request")
	}
	conn, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + StreamProtocol + "\r\n\r\n")
	if err := brw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	return &bufferedConn{Conn: conn, r: brw.Reader}, nil
}

// DialStream sends req (without a body) over conn, an HTTP/1.1
// connection, and returns the stream once the server switches to it.
func DialStream(conn net.Conn, req *http.Request) (net.Conn, error) {
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", StreamProtocol)
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, &StatusError{Status: resp.StatusCode, Message: errorMessage(body, resp.Status)}
	}
	return &bufferedConn{Conn: conn, r: br}, nil
}

// errorMessage extracts {"error": ...} from a response body.
func errorMessage(body []byte, fallback string) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	if s := strings.TrimSpace(string(body)); s != "" {
		return s
	}
	return fallback
}

// bufferedConn reads through the buffer that may hold the first stream
// bytes read along with the HTTP headers.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (c *bufferedConn) CloseWrite() error { return closeWrite(c.Conn) }

func closeWrite(c net.Conn) error {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Close()
}

// Join copies between a and b until both directions end, passing on
// half-closes, then closes both.
func Join(a, b net.Conn) {
	var wg sync.WaitGroup
	var once sync.Once
	closeBoth := func() { a.Close(); b.Close() }
	pipe := func(dst, src net.Conn) {
		defer wg.Done()
		if _, err := io.Copy(dst, src); err != nil {
			// A broken connection ends both directions.
			once.Do(closeBoth)
			return
		}
		closeWrite(dst)
	}
	wg.Add(2)
	go pipe(a, b)
	go pipe(b, a)
	wg.Wait()
	once.Do(closeBoth)
}
