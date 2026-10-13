package pve

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"strconv"
	"testing"
	"time"

	"quicksand/internal/config"
)

// These tests talk to a real Proxmox server. Run them with:
//
//	QS_TEST_CONFIG=../../config.json QS_TEST_NODE=pve QS_TEST_VMID=100 go test ./internal/pve -run Integration -v
func integrationClient(t *testing.T) (*Client, string, int) {
	t.Helper()
	path, node, vmid := os.Getenv("QS_TEST_CONFIG"), os.Getenv("QS_TEST_NODE"), os.Getenv("QS_TEST_VMID")
	if path == "" || node == "" || vmid == "" {
		t.Skip("QS_TEST_CONFIG, QS_TEST_NODE and QS_TEST_VMID not set")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(cfg.PVE)
	if err != nil {
		t.Fatal(err)
	}
	id, err := strconv.Atoi(vmid)
	if err != nil {
		t.Fatal(err)
	}
	return c, node, id
}

func TestIntegrationSpice(t *testing.T) {
	c, node, vmid := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ticket, err := c.SpiceProxy(ctx, node, vmid)
	if err != nil {
		t.Fatal(err)
	}
	// SPICE opens one connection per channel, all with the same ticket.
	for i := range 3 {
		conn, err := DialSpice(ctx, ticket)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(spiceLinkMess(1)); err != nil {
			t.Fatal(err)
		}
		hdr := make([]byte, 16)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			t.Fatalf("read link reply %d: %v", i, err)
		}
		if !bytes.Equal(hdr[:4], []byte("REDQ")) {
			t.Fatalf("bad magic %q", hdr[:4])
		}
		t.Logf("conn %d: SPICE %d.%d", i, binary.LittleEndian.Uint32(hdr[4:]), binary.LittleEndian.Uint32(hdr[8:]))
		conn.Close()
	}
}

func TestIntegrationVNC(t *testing.T) {
	c, node, vmid := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ticket, err := c.VNCProxy(ctx, node, vmid)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := c.DialVNC(ctx, node, vmid, ticket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	version := make([]byte, 12)
	if _, err := io.ReadFull(conn, version); err != nil {
		t.Fatal(err)
	}
	t.Logf("server version %q", version)
	if !bytes.HasPrefix(version, []byte("RFB ")) {
		t.Fatalf("not an RFB server: %q", version)
	}
}

// spiceLinkMess builds a SpiceLinkHeader + SpiceLinkMess for the given channel type.
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
