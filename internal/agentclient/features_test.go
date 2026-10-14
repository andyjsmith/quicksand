package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"quicksand/internal/agentproto"
)

func TestFiles(t *testing.T) {
	srv, c := startAgent(t)
	ctx := context.Background()
	dir := t.TempDir()
	ref := func(p string) FileRef { return FileRef{Base: dir, Path: p} }

	if _, err := c.Mkdir(ctx, host(srv), ref("sub")); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("quicksand"), 100000)
	e, err := c.Upload(ctx, host(srv), ref("sub/a.bin"), bytes.NewReader(data), int64(len(data)), false)
	if err != nil || e.Size != int64(len(data)) || e.Path != filepath.Join(dir, "sub", "a.bin") {
		t.Fatalf("upload = %+v, %v", e, err)
	}
	if _, err := c.Upload(ctx, host(srv), ref("sub/a.bin"), strings.NewReader("x"), 1, false); !isStatus(err, 409) {
		t.Errorf("upload over existing file without overwrite: %v", err)
	}
	if _, err := c.Upload(ctx, host(srv), ref("sub/a.bin"), bytes.NewReader(data), int64(len(data)), true); err != nil {
		t.Errorf("overwrite: %v", err)
	}

	list, err := c.ListFiles(ctx, host(srv), ref("sub"))
	if err != nil || len(list.Entries) != 1 || list.Entries[0].Name != "a.bin" || list.Parent != dir {
		t.Fatalf("list = %+v, %v", list, err)
	}
	// Absolute paths ignore the base.
	if list, err := c.ListFiles(ctx, host(srv), FileRef{Base: "/nonexistent", Path: dir}); err != nil || list.Path != dir {
		t.Errorf("absolute list = %+v, %v", list, err)
	}

	resp, err := c.Download(ctx, host(srv), ref("sub/a.bin"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, data) || !strings.Contains(resp.Header.Get("Content-Disposition"), "a.bin") {
		t.Errorf("download: %d bytes, %q", len(got), resp.Header.Get("Content-Disposition"))
	}
	resp, err = c.Download(ctx, host(srv), ref("sub"))
	if err != nil || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("directory download: %v", err)
	}
	resp.Body.Close()

	h, err := c.HashFile(ctx, host(srv), ref("sub/a.bin"))
	if err != nil || len(h.SHA256) != 64 || h.Size != int64(len(data)) {
		t.Errorf("hash = %+v, %v", h, err)
	}

	if err := c.Remove(ctx, host(srv), ref("sub"), false); err == nil {
		t.Error("removed a non-empty directory without recursive")
	}
	if err := c.Remove(ctx, host(srv), ref("sub"), true); err != nil {
		t.Error(err)
	}
	if _, err := c.ListFiles(ctx, host(srv), ref("sub")); !isStatus(err, 404) {
		t.Errorf("list removed directory: %v", err)
	}
	if err := c.Remove(ctx, host(srv), FileRef{Path: "/"}, true); !isStatus(err, 400) {
		t.Errorf("removing / gave %v", err)
	}
}

func TestProcessesAndConnections(t *testing.T) {
	srv, c := startAgent(t)
	ctx := context.Background()

	list, err := c.Processes(ctx, host(srv))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range list.Processes {
		if p.PID == os.Getpid() {
			found = p.Memory > 0 && p.Name != ""
		}
	}
	if !found || list.NumCPU == 0 || list.MemoryTotal == 0 {
		t.Errorf("this process missing or incomplete in %d processes", len(list.Processes))
	}

	cmd := startSleep(t)
	if err := c.Kill(ctx, host(srv), cmd.Process.Pid, false); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	if err := c.Kill(ctx, host(srv), os.Getpid(), true); !isStatus(err, 400) {
		t.Errorf("killing the agent itself: %v", err)
	}

	conns, err := c.Connections(ctx, host(srv))
	if err != nil {
		t.Fatal(err)
	}
	port := ":" + strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port)
	listening := false
	for _, conn := range conns.Connections {
		if conn.State == "LISTEN" && strings.HasSuffix(conn.Local, port) {
			listening = conn.PID == os.Getpid()
		}
	}
	if !listening {
		t.Errorf("the agent's own listener isn't in %d connections", len(conns.Connections))
	}
}

func TestConnect(t *testing.T) {
	srv, c := startAgent(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Echo until the client half-closes, then say goodbye.
		io.Copy(conn, conn)
		conn.Write([]byte("bye"))
		conn.Close()
	}()
	conn, err := c.Connect(context.Background(), host(srv), ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("echo me"))
	conn.(interface{ CloseWrite() error }).CloseWrite()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	got, err := io.ReadAll(conn)
	if string(got) != "echo mebye" {
		t.Errorf("got %q, %v", got, err)
	}

	if _, err := c.Connect(context.Background(), host(srv), "127.0.0.1:1"); !isStatus(err, 502) {
		t.Errorf("connect to a closed port: %v", err)
	}
}

func readShell(t *testing.T, conn net.Conn) (string, string, agentproto.ShellExit) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var stdout, stderr strings.Builder
	for {
		typ, payload, err := agentproto.ReadFrame(conn)
		if err != nil {
			t.Fatalf("read frame: %v (stdout so far %q)", err, stdout.String())
		}
		switch typ {
		case agentproto.FrameStdout:
			stdout.Write(payload)
		case agentproto.FrameStderr:
			stderr.Write(payload)
		case agentproto.FrameExit:
			var exit agentproto.ShellExit
			json.Unmarshal(payload, &exit)
			return stdout.String(), stderr.String(), exit
		}
	}
}

func isStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}
