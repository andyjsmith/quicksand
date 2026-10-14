package agent

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// errNoPTY makes shells fall back to pipes on Windows releases without
// pseudo-consoles (before 10 1809).
var errNoPTY = errors.New("this Windows has no pseudo-console support")

func hasPTY() bool { return kernel32.NewProc("CreatePseudoConsole").Find() == nil }

// conptySession runs a command on a pseudo-console (ConPTY), which turns
// the console into a VT byte stream like a Unix terminal's.
type conptySession struct {
	process windows.Handle
	// job holds the command and its children so Kill can end them all;
	// zero if the process couldn't be assigned to one.
	job     windows.Handle
	in, out *os.File

	mu      sync.Mutex
	console windows.Handle // zero once closed
}

func startPTY(cmd *exec.Cmd, cols, rows int) (session, error) {
	if !hasPTY() {
		return nil, errNoPTY
	}
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	// The console reads its input from inR and writes its output to outW.
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return nil, err
	}
	s := &conptySession{in: os.NewFile(uintptr(inW), "conpty-in"), out: os.NewFile(uintptr(outR), "conpty-out")}
	err := windows.CreatePseudoConsole(coord(cols, rows), inR, outW, 0, &s.console)
	// The console keeps its own copies of its ends.
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)
	if err != nil {
		s.in.Close()
		s.out.Close()
		return nil, err
	}
	if err := s.start(cmd); err != nil {
		s.closeConsole()
		s.in.Close()
		s.out.Close()
		return nil, err
	}
	return s, nil
}

func (s *conptySession) start(cmd *exec.Cmd) error {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	// The attribute's value is the console handle itself, not a pointer to it.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		*(*unsafe.Pointer)(unsafe.Pointer(&s.console)), unsafe.Sizeof(s.console)); err != nil {
		return err
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	// Empty standard handles: otherwise the command would inherit the
	// agent's own instead of using the console.
	si.Flags = windows.STARTF_USESTDHANDLES

	app, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return err
	}
	line := windows.ComposeCommandLine(cmd.Args)
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.CmdLine != "" {
		line = cmd.SysProcAttr.CmdLine
	}
	lineW, err := windows.UTF16PtrFromString(line)
	if err != nil {
		return err
	}
	var dir *uint16
	if cmd.Dir != "" {
		if dir, err = windows.UTF16PtrFromString(cmd.Dir); err != nil {
			return err
		}
	}
	var pi windows.ProcessInformation
	// Suspended until it's in the job, so children it starts are too.
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(app, lineW, nil, nil, false, flags, envBlock(cmd.Env), dir, &si.StartupInfo, &pi); err != nil {
		return err
	}
	defer windows.CloseHandle(pi.Thread)
	s.process = pi.Process
	if job, err := windows.CreateJobObject(nil, nil); err == nil {
		if windows.AssignProcessToJobObject(job, pi.Process) == nil {
			s.job = job
		} else {
			windows.CloseHandle(job)
		}
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		s.Kill()
		windows.CloseHandle(s.process)
		if s.job != 0 {
			windows.CloseHandle(s.job)
		}
		return err
	}
	return nil
}

func coord(cols, rows int) windows.Coord {
	return windows.Coord{X: int16(min(cols, 32767)), Y: int16(min(rows, 32767))}
}

// envBlock builds a CreateProcess environment block; nil inherits the
// agent's environment.
func envBlock(env []string) *uint16 {
	if len(env) == 0 {
		return nil
	}
	var b []uint16
	for _, kv := range env {
		if strings.ContainsRune(kv, 0) {
			continue
		}
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	b = append(b, 0)
	return &b[0]
}

func (s *conptySession) Stdin() io.WriteCloser { return ptyInput{s.in} }
func (s *conptySession) Stdout() io.Reader     { return s.out }
func (s *conptySession) Stderr() io.Reader     { return nil }

func (s *conptySession) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.console == 0 {
		return nil
	}
	return windows.ResizePseudoConsole(s.console, coord(cols, rows))
}

func (s *conptySession) Wait() (int, error) {
	if _, err := windows.WaitForSingleObject(s.process, windows.INFINITE); err != nil {
		return -1, err
	}
	var code uint32
	err := windows.GetExitCodeProcess(s.process, &code)
	// The console's output only ends when the console is closed, and
	// closing it can wait until what's left has been read: do it while
	// runSession drains the output.
	go s.closeConsole()
	if err != nil {
		return -1, err
	}
	return int(code), nil
}

func (s *conptySession) closeConsole() {
	s.mu.Lock()
	h := s.console
	s.console = 0
	s.mu.Unlock()
	if h != 0 {
		windows.ClosePseudoConsole(h)
	}
}

func (s *conptySession) Kill() {
	if s.job != 0 && windows.TerminateJobObject(s.job, 1) == nil {
		return
	}
	windows.TerminateProcess(s.process, 1)
}

func (s *conptySession) Close() {
	// Closing the output first unblocks a ClosePseudoConsole waiting for
	// output nobody will read.
	s.out.Close()
	s.closeConsole()
	s.in.Close()
	windows.CloseHandle(s.process)
	if s.job != 0 {
		windows.CloseHandle(s.job)
	}
}
