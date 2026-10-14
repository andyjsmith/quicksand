package agent

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"quicksand/internal/agentproto"
)

func (a *Agent) handleProcesses(w http.ResponseWriter, r *http.Request) {
	procs, err := listProcesses()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, agentproto.Error{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, agentproto.ProcessList{
		Processes: procs, Time: time.Now().UTC(), NumCPU: runtime.NumCPU(), MemoryTotal: memoryTotal(),
	})
}

func (a *Agent) handleKill(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.Atoi(r.PathValue("pid"))
	if err != nil || pid <= 0 {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "invalid pid"})
		return
	}
	if pid == os.Getpid() {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "that is the agent itself"})
		return
	}
	var req agentproto.KillRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "invalid JSON body"})
			return
		}
	}
	slog.Info("kill", "pid", pid, "force", req.Force, "from", r.RemoteAddr)
	if err := killProcess(pid, req.Force); err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, os.ErrProcessDone), errors.Is(err, os.ErrNotExist):
			status = http.StatusNotFound
		case errors.Is(err, os.ErrPermission):
			status = http.StatusForbidden
		}
		writeJSON(w, status, agentproto.Error{Error: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
