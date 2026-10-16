package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"quicksand/internal/agentproto"
)

// fakeClipboard stands in for the OS clipboard.
type fakeClipboard struct {
	mu   sync.Mutex
	text string
	err  error
}

func (f *fakeClipboard) read(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text, f.err
}

func (f *fakeClipboard) write(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.text = text
	return nil
}

func TestClipboard(t *testing.T) {
	fake := &fakeClipboard{text: "first"}
	clipboardRead, clipboardWrite = fake.read, fake.write
	clipboardPollInterval = 20 * time.Millisecond
	t.Cleanup(func() {
		clipboardRead, clipboardWrite = readClipboard, writeClipboard
		clipboardPollInterval = time.Second
	})

	key, _ := agentproto.GenerateKey()
	a, err := New(Config{AuthorizedKeys: []string{agentproto.FormatPublicKey(key.Public().(ed25519.PublicKey))}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	get := func(query string) (agentproto.Clipboard, int) {
		t.Helper()
		resp, err := http.Get(srv.URL + "/v1/clipboard" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var c agentproto.Clipboard
		json.NewDecoder(resp.Body).Decode(&c)
		return c, resp.StatusCode
	}
	set := func(text string) int {
		t.Helper()
		body := fmt.Sprintf(`{"text": %q}`, text)
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/clipboard", strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// The current text, at once.
	c, status := get("")
	if status != 200 || c.Text != "first" || c.Seq == 0 {
		t.Fatalf("get: %d %+v", status, c)
	}
	first := c.Seq

	// Unchanged: waiting returns the same state when the wait runs out.
	start := time.Now()
	if c, _ = get(fmt.Sprintf("?after=%d&wait=1", first)); c.Seq != first || time.Since(start) < 900*time.Millisecond {
		t.Fatalf("unchanged wait: %+v after %v", c, time.Since(start))
	}

	// A copy in the guest ends the wait early, with a new seq.
	go func() {
		time.Sleep(100 * time.Millisecond)
		fake.write(context.Background(), "copied in the guest")
	}()
	start = time.Now()
	c, _ = get(fmt.Sprintf("?after=%d&wait=10", first))
	if c.Text != "copied in the guest" || c.Seq == first || time.Since(start) > 2*time.Second {
		t.Fatalf("change: %+v after %v", c, time.Since(start))
	}

	// Pasting sets the clipboard, which then reads back as a change.
	if status := set("pasted"); status != http.StatusNoContent {
		t.Fatalf("set: %d", status)
	}
	if c2, _ := get(fmt.Sprintf("?after=%d", c.Seq)); c2.Text != "pasted" || c2.Seq == c.Seq {
		t.Fatalf("after set: %+v", c2)
	}

	// No clipboard to reach: 501, so clients stop asking.
	fake.err = fmt.Errorf("%w: no one is at the guest's screen", errClipboardUnsupported)
	if _, status := get(""); status != http.StatusNotImplemented {
		t.Errorf("unsupported get: %d", status)
	}
	if status := set("x"); status != http.StatusNotImplemented {
		t.Errorf("unsupported set: %d", status)
	}
}
