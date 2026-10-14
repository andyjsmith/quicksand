package agent

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"quicksand/internal/agentproto"
)

// errDisplayUnsupported is returned where the agent can't resize displays.
var errDisplayUnsupported = errors.New("resizing the display isn't supported on this guest")

// Display sizes accepted; widths are rounded down to a multiple of 8, which
// display drivers generally want.
const (
	minDisplayWidth, minDisplayHeight = 640, 480
	maxDisplayWidth, maxDisplayHeight = 8192, 8192
)

// displayMu serializes resolution changes.
var displayMu sync.Mutex

func (a *Agent) handleDisplay(w http.ResponseWriter, r *http.Request) {
	var req agentproto.DisplayRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "invalid display request"})
		return
	}
	width := min(max(req.Width, minDisplayWidth), maxDisplayWidth) &^ 7
	height := min(max(req.Height, minDisplayHeight), maxDisplayHeight)
	displayMu.Lock()
	res, err := setDisplay(r.Context(), width, height)
	displayMu.Unlock()
	switch {
	case errors.Is(err, errDisplayUnsupported):
		writeJSON(w, http.StatusNotImplemented, agentproto.Error{Error: err.Error()})
		return
	case err != nil:
		slog.Warn("set display", "width", width, "height", height, "err", err)
		writeJSON(w, http.StatusInternalServerError, agentproto.Error{Error: err.Error()})
		return
	}
	slog.Info("display resized", "width", res.Width, "height", res.Height, "adapter", res.Adapter)
	writeJSON(w, http.StatusOK, res)
}
