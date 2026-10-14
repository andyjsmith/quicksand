package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"quicksand/internal/agentproto"
)

// session is a started command attached to a shell stream.
type session interface {
	// Stdin feeds the command; closing it signals end of input.
	Stdin() io.WriteCloser
	// Outputs are the command's output streams, read until EOF.
	Stdout() io.Reader
	Stderr() io.Reader // nil with a terminal
	Resize(cols, rows int) error
	// Wait waits for the command to exit and returns its exit code.
	Wait() (int, error)
	// Kill ends the command (and, with a terminal, its process group).
	Kill()
	// Close releases the session once its output has been read.
	Close()
}

func (a *Agent) handleShell(w http.ResponseWriter, r *http.Request) {
	var req agentproto.ShellRequest
	if err := json.Unmarshal([]byte(r.URL.Query().Get("request")), &req); err != nil {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "invalid shell request"})
		return
	}
	if !agentproto.IsStreamRequest(r) {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "shells need a stream request"})
		return
	}
	if req.Dir == "" {
		req.Dir = defaultFilesDir()
	}
	if info, err := os.Stat(req.Dir); err != nil || !info.IsDir() {
		req.Dir = ""
	}
	cmd := shellCommandFor(req)
	if cmd.Err != nil && req.Shell != "" {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: fmt.Sprintf("there's no shell %q in the guest", req.Shell)})
		return
	}
	cmd.Dir = req.Dir
	cmd.Env = shellEnv(req)

	var s session
	var err error
	if req.PTY {
		s, err = startPTY(cmd, max(req.Cols, 20), max(req.Rows, 5))
	}
	if !req.PTY || errors.Is(err, errNoPTY) {
		s, err = startPipes(cmd)
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, agentproto.Error{Error: "start " + cmd.Path + ": " + err.Error()})
		return
	}
	slog.Info("shell started", "command", req.Command, "pty", req.PTY, "from", r.RemoteAddr)
	conn, err := agentproto.AcceptStream(w, r)
	if err != nil {
		s.Kill()
		s.Wait()
		s.Close()
		return
	}
	code, err := runSession(conn, s)
	slog.Info("shell ended", "code", code, "err", err)
}

// runSession relays frames between conn and s until the command exits,
// then sends its exit status.
func runSession(conn net.Conn, s session) (int, error) {
	defer conn.Close()
	fw := agentproto.NewFrameWriter(conn)

	// Input: frames from the client, read only here. A closed connection
	// kills the command if it's still running.
	var mu sync.Mutex
	exited := false
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		stdin := s.Stdin()
		for {
			t, payload, err := agentproto.ReadFrame(conn)
			if err != nil {
				mu.Lock()
				if !exited {
					s.Kill()
				}
				mu.Unlock()
				return
			}
			switch t {
			case agentproto.FrameStdin:
				stdin.Write(payload)
			case agentproto.FrameStdinEOF:
				stdin.Close()
			case agentproto.FrameResize:
				var rs agentproto.Resize
				if json.Unmarshal(payload, &rs) == nil && rs.Cols > 0 && rs.Rows > 0 {
					s.Resize(rs.Cols, rs.Rows)
				}
			}
		}
	}()

	var outputs sync.WaitGroup
	copyOut := func(t agentproto.FrameType, r io.Reader) {
		if r == nil {
			return
		}
		outputs.Go(func() { io.Copy(fw.StreamWriter(t), r) })
	}
	copyOut(agentproto.FrameStdout, s.Stdout())
	copyOut(agentproto.FrameStderr, s.Stderr())

	code, err := s.Wait()
	mu.Lock()
	exited = true
	mu.Unlock()
	// Let output written just before exit arrive, but don't wait forever on
	// background processes that inherited it.
	drained := make(chan struct{})
	go func() { outputs.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
	}
	s.Close()
	exit := agentproto.ShellExit{Code: code}
	if err != nil {
		exit.Code, exit.Error = -1, err.Error()
	}
	fw.WriteJSON(agentproto.FrameExit, exit)
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
	// Give the client a moment to read the exit frame and close its end,
	// which ends the input loop.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	<-inputDone
	return code, err
}

// ptyInput ignores Close: a terminal's input ends with ^D (^Z on Windows),
// not EOF.
type ptyInput struct{ f *os.File }

func (p ptyInput) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p ptyInput) Close() error                { return nil }

// pipeSession runs a command with pipes, keeping stdout and stderr apart.
type pipeSession struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stdout, stderr io.ReadCloser
}

func startPipes(cmd *exec.Cmd) (session, error) {
	s := &pipeSession{cmd: cmd}
	var err error
	if s.stdin, err = cmd.StdinPipe(); err != nil {
		return nil, err
	}
	// os.Pipe rather than cmd.StdoutPipe: Wait must not close the read
	// ends before runSession has drained them.
	var stdoutW, stderrW *os.File
	if s.stdout, stdoutW, err = osPipe(); err != nil {
		return nil, err
	}
	if s.stderr, stderrW, err = osPipe(); err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = stdoutW, stderrW
	err = cmd.Start()
	stdoutW.Close()
	stderrW.Close()
	if err != nil {
		s.stdout.Close()
		s.stderr.Close()
		return nil, err
	}
	return s, nil
}

func osPipe() (io.ReadCloser, *os.File, error) {
	r, w, err := os.Pipe()
	return r, w, err
}

func (s *pipeSession) Stdin() io.WriteCloser { return s.stdin }
func (s *pipeSession) Stdout() io.Reader     { return s.stdout }
func (s *pipeSession) Stderr() io.Reader     { return s.stderr }
func (s *pipeSession) Resize(int, int) error { return nil }
func (s *pipeSession) Kill()                 { s.cmd.Process.Kill() }
func (s *pipeSession) Close()                { s.stdout.Close(); s.stderr.Close() }
func (s *pipeSession) Wait() (int, error)    { return exitStatus(s.cmd.Wait()) }

func exitStatus(err error) (int, error) {
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), nil
	}
	return -1, err
}

// handleConnect opens a TCP connection from the guest to addr and joins it
// to the stream, for port forwarding.
func (a *Agent) handleConnect(w http.ResponseWriter, r *http.Request) {
	addr := r.URL.Query().Get("addr")
	if _, _, err := net.SplitHostPort(addr); err != nil {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "addr must be host:port"})
		return
	}
	if !agentproto.IsStreamRequest(r) {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "connect needs a stream request"})
		return
	}
	target, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, agentproto.Error{Error: err.Error()})
		return
	}
	conn, err := agentproto.AcceptStream(w, r)
	if err != nil {
		target.Close()
		return
	}
	slog.Debug("forwarding", "addr", addr, "from", r.RemoteAddr)
	agentproto.Join(conn, target)
}
