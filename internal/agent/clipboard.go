package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"quicksand/internal/agentproto"
)

// The guest clipboard, for consoles whose protocol can't share it (VNC, or
// SPICE without spice-vdagent). The text lives in the desktop session, so
// each OS reads and writes it there (readClipboard and writeClipboard).
// Nothing is watched until a client asks: it then polls while it waits.

// errClipboardUnsupported means the guest's clipboard can't be reached:
// no one is logged in, or (on Linux) no clipboard tool is installed.
var errClipboardUnsupported = errors.New("clipboard unavailable")

var clipboardPollInterval = time.Second

// The OS clipboard; tests replace these.
var (
	clipboardRead  = readClipboard
	clipboardWrite = writeClipboard
)

// clipboardState numbers the clipboard's contents as they change.
type clipboardState struct {
	mu   sync.Mutex
	seq  uint64
	text string
}

// update records text read from the clipboard and returns the current state.
func (c *clipboardState) update(text string) agentproto.Clipboard {
	c.mu.Lock()
	defer c.mu.Unlock()
	if text != c.text || c.seq == 0 {
		c.seq++
		c.text = text
	}
	return agentproto.Clipboard{Seq: c.seq, Text: c.text}
}

func (a *Agent) handleGetClipboard(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	deadline := time.Now().Add(min(time.Duration(max(wait, 0))*time.Second, agentproto.MaxClipboardWait))
	for {
		text, err := clipboardRead(r.Context())
		if err != nil {
			writeClipboardError(w, err)
			return
		}
		st := a.clipboard.update(text)
		if st.Seq != after || !time.Now().Before(deadline) {
			writeJSON(w, http.StatusOK, st)
			return
		}
		if !sleep(r.Context(), clipboardPollInterval) {
			return
		}
	}
}

func (a *Agent) handleSetClipboard(w http.ResponseWriter, r *http.Request) {
	var req agentproto.ClipboardSet
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*agentproto.MaxClipboardText)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "bad request: " + err.Error()})
		return
	}
	if len(req.Text) > agentproto.MaxClipboardText {
		writeJSON(w, http.StatusRequestEntityTooLarge, agentproto.Error{Error: "the text is too long for the clipboard"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := clipboardWrite(ctx, req.Text); err != nil {
		writeClipboardError(w, err)
		return
	}
	// The watcher sees this text as a change, like any copy in the guest.
	w.WriteHeader(http.StatusNoContent)
}

func writeClipboardError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, errClipboardUnsupported) {
		status = http.StatusNotImplemented
	}
	writeJSON(w, status, agentproto.Error{Error: err.Error()})
}
