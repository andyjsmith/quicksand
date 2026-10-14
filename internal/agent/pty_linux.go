package agent

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// ptySession runs a command on a new pseudo-terminal.
type ptySession struct {
	cmd    *exec.Cmd
	master *os.File
}

// errNoPTY is never returned here: Linux always has terminals.
var errNoPTY = errors.New("no terminal support")

func startPTY(cmd *exec.Cmd, cols, rows int) (session, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	// Use the raw descriptor through SyscallConn: Fd() would switch the
	// file to blocking mode and break deadlines.
	var n int
	err = control(master, func(fd int) error {
		if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
			return err
		}
		var err error
		n, err = unix.IoctlGetInt(fd, unix.TIOCGPTN)
		return err
	})
	if err != nil {
		master.Close()
		return nil, err
	}
	tty, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, err
	}
	defer tty.Close()
	s := &ptySession{cmd: cmd, master: master}
	s.Resize(cols, rows)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, err
	}
	return s, nil
}

func control(f *os.File, fn func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		return err
	}
	return ferr
}

func (s *ptySession) Stdin() io.WriteCloser { return ptyInput{s.master} }
func (s *ptySession) Stdout() io.Reader     { return ptyOutput{s.master} }
func (s *ptySession) Stderr() io.Reader     { return nil }

func (s *ptySession) Resize(cols, rows int) error {
	return control(s.master, func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(rows), Col: uint16(cols)})
	})
}

func (s *ptySession) Wait() (int, error) { return exitStatus(s.cmd.Wait()) }

func (s *ptySession) Kill() {
	// The shell leads its own session; take its process group with it.
	syscall.Kill(-s.cmd.Process.Pid, syscall.SIGHUP)
	s.cmd.Process.Kill()
}

func (s *ptySession) Close() { s.master.Close() }

// ptyOutput reports EIO, which the master returns once the terminal has
// no processes left, as EOF.
type ptyOutput struct{ f *os.File }

func (p ptyOutput) Read(b []byte) (int, error) {
	n, err := p.f.Read(b)
	if errors.Is(err, syscall.EIO) {
		err = io.EOF
	}
	return n, err
}
