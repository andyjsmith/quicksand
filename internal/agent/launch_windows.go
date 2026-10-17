package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows the agent runs as a service in session 0, which has no screen,
// so apps are started in the console session as the user signed in there:
// with their token (from WTSQueryUserToken, which needs the agent to run as
// SYSTEM, as its service does), environment and profile directory, on the
// interactive desktop. Each app runs in a job object, so stopping it stops
// what it started too, and so does the agent stopping.

// winDesktop is the console session and the logon signed in to it.
type winDesktop struct {
	session uint32
	id      string
}

func findDesktop(ctx context.Context) (desktop, error) {
	session := windows.WTSGetActiveConsoleSessionId()
	if session == 0xFFFFFFFF {
		return nil, errors.New("the guest has no console session")
	}
	token, err := consoleUserToken(session)
	if err != nil {
		return nil, err
	}
	defer token.Close()
	logon, err := logonID(token)
	if err != nil {
		return nil, err
	}
	return winDesktop{session: session, id: fmt.Sprintf("%d:%x", session, logon)}, nil
}

// consoleUserToken is the primary token of the user signed in to session.
func consoleUserToken(session uint32) (windows.Token, error) {
	var token windows.Token
	if err := windows.WTSQueryUserToken(session, &token); err != nil {
		if errors.Is(err, windows.ERROR_NO_TOKEN) {
			return 0, errors.New("no one is signed in to the guest's screen")
		}
		return 0, fmt.Errorf("the signed-in user's token: %w", err)
	}
	return token, nil
}

// logonID is the token's logon session (its AuthenticationId), which
// changes each time someone signs in.
func logonID(token windows.Token) (uint64, error) {
	// TOKEN_STATISTICS starts with TokenId and AuthenticationId (LUIDs).
	var buf [64]byte
	var n uint32
	if err := windows.GetTokenInformation(token, windows.TokenStatistics, &buf[0], uint32(len(buf)), &n); err != nil {
		return 0, err
	}
	luid := (*windows.LUID)(unsafe.Pointer(&buf[8]))
	return uint64(luid.HighPart)<<32 | uint64(luid.LowPart), nil
}

func (d winDesktop) ID() string { return d.id }

func (d winDesktop) Start(argv []string, stderr io.Writer) (process, error) {
	token, err := consoleUserToken(d.session)
	if err != nil {
		return nil, err
	}
	defer token.Close()
	env, err := token.Environ(false)
	if err != nil {
		return nil, fmt.Errorf("the user's environment: %w", err)
	}
	exe, err := winLookPath(argv[0], winEnv(env, "PATH"), winEnv(env, "PATHEXT"))
	if err != nil {
		return nil, err
	}
	var block *uint16
	if err := windows.CreateEnvironmentBlock(&block, token, false); err != nil {
		return nil, fmt.Errorf("the user's environment: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(block)
	dir, _ := token.GetUserProfileDirectory()

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("job object: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("job object: %w", err)
	}

	app, _ := windows.UTF16PtrFromString(exe)
	cmdLine, _ := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{exe}, argv[1:]...)))
	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	var cwd *uint16
	if dir != "" {
		cwd, _ = windows.UTF16PtrFromString(dir)
	}
	si := windows.StartupInfo{Desktop: desktop}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation
	// Suspended until it's in the job, so nothing it starts escapes it.
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_NEW_PROCESS_GROUP)
	if err := windows.CreateProcessAsUser(token, app, cmdLine, nil, nil, false, flags, block, cwd, &si, &pi); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(pi.Thread)
	if err := windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Process)
		windows.CloseHandle(job)
		return nil, fmt.Errorf("job object: %w", err)
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		windows.TerminateJobObject(job, 1)
		windows.CloseHandle(pi.Process)
		windows.CloseHandle(job)
		return nil, err
	}
	return &winProcess{pid: int(pi.ProcessId), process: pi.Process, job: job}, nil
}

// winEnv looks up an environment variable, ignoring case as Windows does.
func winEnv(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// winProcess is an app and its job object.
type winProcess struct {
	pid     int
	process windows.Handle
	job     windows.Handle
	once    sync.Once
}

func (p *winProcess) Pid() int { return p.pid }

func (p *winProcess) Wait() error {
	windows.WaitForSingleObject(p.process, windows.INFINITE)
	var code uint32
	windows.GetExitCodeProcess(p.process, &code)
	p.close()
	if code != 0 {
		return fmt.Errorf("exit status %d", code)
	}
	return nil
}

func (p *winProcess) Terminate() {
	windows.TerminateJobObject(p.job, 1)
}

// close releases the handles; closing the job ends anything left in it.
func (p *winProcess) close() {
	p.once.Do(func() {
		windows.CloseHandle(p.process)
		windows.CloseHandle(p.job)
	})
}

// winLookPath finds a program like exec.LookPath, but in the given PATH
// and PATHEXT (the signed-in user's, not the service's).
func winLookPath(name, path, pathext string) (string, error) {
	exts := []string{""}
	if filepath.Ext(name) == "" {
		if pathext == "" {
			pathext = ".COM;.EXE;.BAT;.CMD"
		}
		for _, e := range strings.Split(pathext, ";") {
			if e != "" {
				exts = append(exts, strings.ToLower(e))
			}
		}
	}
	try := func(p string) (string, bool) {
		for _, e := range exts {
			if st, err := os.Stat(p + e); err == nil && !st.IsDir() {
				return p + e, true
			}
		}
		return "", false
	}
	if strings.ContainsAny(name, `\/:`) {
		if p, ok := try(name); ok {
			return p, nil
		}
		return "", fmt.Errorf("%s isn't installed", name)
	}
	for _, dir := range filepath.SplitList(path) {
		if p, ok := try(filepath.Join(dir, name)); ok {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s isn't installed (not found in the user's PATH)", name)
}

// watchSessions checks the console session every second and calls changed
// when someone signs in or out, until ctx ends.
func watchSessions(ctx context.Context, changed func()) {
	last := ""
	for sleep(ctx, time.Second) {
		cur := ""
		if d, err := findDesktop(ctx); err == nil {
			cur = d.ID()
		}
		if cur != last {
			last = cur
			changed()
		}
	}
}
