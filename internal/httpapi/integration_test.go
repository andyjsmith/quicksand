package httpapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"quicksand/internal/config"
	"quicksand/internal/pve"
)

// These tests relay real console traffic through the API. Run them with:
//
//	QS_TEST_CONFIG=../../config.json QS_TEST_NODE=pve QS_TEST_VMID=100 go test ./internal/httpapi -run Integration -v
func integrationServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	path, node, vmid := os.Getenv("QS_TEST_CONFIG"), os.Getenv("QS_TEST_NODE"), os.Getenv("QS_TEST_VMID")
	if path == "" || node == "" || vmid == "" {
		t.Skip("QS_TEST_CONFIG, QS_TEST_NODE and QS_TEST_VMID not set")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	client, err := pve.New(cfg.PVE)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(client, nil))
	t.Cleanup(srv.Close)
	return srv, "/api/vms/" + node + "/" + vmid
}

func createConsole(t *testing.T, srv *httptest.Server, vmPath, protocol string) createConsoleResponse {
	t.Helper()
	resp, err := http.Post(srv.URL+vmPath+"/console", "application/json", strings.NewReader(`{"protocol":"`+protocol+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create console: %s %s", resp.Status, b)
	}
	var out createConsoleResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func dialConsole(ctx context.Context, t *testing.T, srv *httptest.Server, path string) *websocket.Conn {
	t.Helper()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+path, &websocket.DialOptions{
		Subprotocols: []string{"binary"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestIntegrationSpiceRelay(t *testing.T) {
	srv, vmPath := integrationServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sess := createConsole(t, srv, vmPath, "spice")
	// spice-html5 opens one websocket per channel against the same URL.
	for _, channel := range []byte{1, 2, 3, 4} {
		ws := dialConsole(ctx, t, srv, sess.Path)
		if ws.Subprotocol() != "binary" {
			t.Errorf("subprotocol %q", ws.Subprotocol())
		}
		if err := ws.Write(ctx, websocket.MessageBinary, spiceLinkMess(channel)); err != nil {
			t.Fatal(err)
		}
		_, msg, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("channel %d: %v", channel, err)
		}
		if !bytes.HasPrefix(msg, []byte("REDQ")) {
			t.Fatalf("channel %d: bad reply %q", channel, msg[:min(len(msg), 16)])
		}
		ws.Close(websocket.StatusNormalClosure, "")
	}
}

func TestIntegrationVNCRelay(t *testing.T) {
	srv, vmPath := integrationServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sess := createConsole(t, srv, vmPath, "vnc")
	ws := dialConsole(ctx, t, srv, sess.Path)
	defer ws.Close(websocket.StatusNormalClosure, "")
	_, msg, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(msg, []byte("RFB ")) {
		t.Fatalf("bad greeting %q", msg)
	}

	// vncproxy serves a single connection, so a second one is refused.
	resp, err := http.Get(srv.URL + sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second connection: got %s, want 409", resp.Status)
	}
}

func spiceLinkMess(channelType byte) []byte {
	var body bytes.Buffer
	binary.Write(&body, binary.LittleEndian, uint32(0)) // connection_id
	body.WriteByte(channelType)
	body.WriteByte(0)                                    // channel_id
	binary.Write(&body, binary.LittleEndian, uint32(0))  // num_common_caps
	binary.Write(&body, binary.LittleEndian, uint32(0))  // num_channel_caps
	binary.Write(&body, binary.LittleEndian, uint32(18)) // caps_offset

	var msg bytes.Buffer
	msg.WriteString("REDQ")
	binary.Write(&msg, binary.LittleEndian, uint32(2)) // major
	binary.Write(&msg, binary.LittleEndian, uint32(2)) // minor
	binary.Write(&msg, binary.LittleEndian, uint32(body.Len()))
	msg.Write(body.Bytes())
	return msg.Bytes()
}
