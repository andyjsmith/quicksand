package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"time"

	"quicksand/internal/agentproto"
)

func (a *Agent) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/info", a.handleInfo)
	mux.HandleFunc("POST /v1/exec", a.handleExec)
	mux.HandleFunc("GET /v1/shell", a.handleShell)
	mux.HandleFunc("GET /v1/connect", a.handleConnect)
	mux.HandleFunc("GET /v1/processes", a.handleProcesses)
	mux.HandleFunc("POST /v1/processes/{pid}/kill", a.handleKill)
	mux.HandleFunc("GET /v1/connections", a.handleConnections)
	mux.HandleFunc("GET /v1/files", a.handleListFiles)
	mux.HandleFunc("DELETE /v1/files", a.handleDeleteFile)
	mux.HandleFunc("POST /v1/files/mkdir", a.handleMkdir)
	mux.HandleFunc("GET /v1/files/content", a.handleDownload)
	mux.HandleFunc("PUT /v1/files/content", a.handleUpload)
	mux.HandleFunc("GET /v1/files/hash", a.handleHash)
	mux.HandleFunc("POST /v1/files/zip", a.handleZip)
	mux.HandleFunc("POST /v1/display", a.handleDisplay)
	mux.HandleFunc("PUT /v1/links", a.handleLinks)
	mux.HandleFunc("GET /v1/clipboard", a.handleGetClipboard)
	mux.HandleFunc("PUT /v1/clipboard", a.handleSetClipboard)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, agentproto.Error{Error: "not found"})
	})
	return mux
}

func (a *Agent) handleInfo(w http.ResponseWriter, r *http.Request) {
	info := agentproto.Info{
		ProtocolVersion: agentproto.Version,
		AgentVersion:    Version,
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		Time:            time.Now().UTC(),
		Interfaces:      []agentproto.Interface{},
	}
	info.Hostname, _ = os.Hostname()
	info.FilesDir, info.PathSeparator = defaultFilesDir(), string(filepath.Separator)
	if u, err := user.Current(); err == nil {
		info.User = u.Username
	}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			ai := agentproto.Interface{Name: iface.Name, MAC: iface.HardwareAddr.String(), Addresses: []string{}}
			if addrs, err := iface.Addrs(); err == nil {
				for _, addr := range addrs {
					ai.Addresses = append(ai.Addresses, addr.String())
				}
			}
			info.Interfaces = append(info.Interfaces, ai)
		}
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *Agent) handleExec(w http.ResponseWriter, r *http.Request) {
	var req agentproto.ExecRequest
	r.Body = http.MaxBytesReader(w, r.Body, 2*agentproto.MaxExecOutput)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "invalid JSON body"})
		return
	}
	if (len(req.Argv) == 0) == (req.Shell == "") {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "set exactly one of argv or shell"})
		return
	}
	timeout := agentproto.DefaultExecTimeout
	if req.TimeoutSeconds > 0 {
		timeout = min(time.Duration(req.TimeoutSeconds)*time.Second, agentproto.MaxExecTimeout)
	}
	slog.Info("exec", "argv", req.Argv, "shell", req.Shell, "from", r.RemoteAddr)
	writeJSON(w, http.StatusOK, runCommand(r.Context(), req, timeout))
}

func runCommand(ctx context.Context, req agentproto.ExecRequest, timeout time.Duration) agentproto.ExecResponse {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if req.Shell != "" {
		cmd = shellCommand(ctx, req.Shell)
	} else {
		cmd = exec.CommandContext(ctx, req.Argv[0], req.Argv[1:]...)
	}
	stdout := &cappedBuffer{limit: agentproto.MaxExecOutput}
	stderr := &cappedBuffer{limit: agentproto.MaxExecOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.Stdin = bytes.NewReader(req.Stdin)
	// Don't hang on grandchildren still holding stdout after a kill.
	cmd.WaitDelay = 2 * time.Second

	start := time.Now()
	err := cmd.Run()
	resp := agentproto.ExecResponse{
		ExitCode:   -1,
		Stdout:     stdout.Bytes(),
		Stderr:     stderr.Bytes(),
		Truncated:  stdout.truncated || stderr.truncated,
		TimedOut:   errors.Is(ctx.Err(), context.DeadlineExceeded),
		DurationMs: time.Since(start).Milliseconds(),
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		resp.ExitCode = 0
	case errors.As(err, &exitErr) && !resp.TimedOut:
		resp.ExitCode = exitErr.ExitCode()
	default:
		resp.Error = err.Error()
	}
	return resp
}

// cappedBuffer keeps the first limit bytes written and discards the rest.
// It must not embed bytes.Buffer: io.Copy would use the promoted ReadFrom
// and bypass the limit.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room < len(p) {
		b.truncated = true
		if room > 0 {
			b.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *cappedBuffer) Bytes() []byte { return b.buf.Bytes() }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
