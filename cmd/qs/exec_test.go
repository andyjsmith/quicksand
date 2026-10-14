package main

import (
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

// capture runs fn with stdout and stderr (and optionally stdin) swapped
// for pipes, returning what it wrote.
func capture(t *testing.T, stdin string, fn func()) (string, string) {
	t.Helper()
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	oldIn, oldOut, oldErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = inR, outW, errW
	defer func() { os.Stdin, os.Stdout, os.Stderr = oldIn, oldOut, oldErr }()
	go func() { inW.WriteString(stdin); inW.Close() }()
	stdout, stderr := make(chan string), make(chan string)
	go func() { b, _ := io.ReadAll(outR); stdout <- string(b) }()
	go func() { b, _ := io.ReadAll(errR); stderr <- string(b) }()
	fn()
	outW.Close()
	errW.Close()
	return <-stdout, <-stderr
}

func TestExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a Unix shell")
	}
	startServer(t)
	var code int
	var err error
	out, errOut := capture(t, "", func() { code, err = runExec([]string{"vm1", "echo", "hi;", "echo", "oops", ">&2;", "exit", "3"}) })
	if err != nil || code != 3 || out != "hi\n" || errOut != "oops\n" {
		t.Errorf("shell: code %d, %v, stdout %q, stderr %q", code, err, out, errOut)
	}
	// -i sends stdin; -argv skips the shell, so ; is just an argument.
	out, _ = capture(t, "from stdin\n", func() { code, err = runExec([]string{"-i", "vm1", "cat"}) })
	if err != nil || code != 0 || out != "from stdin\n" {
		t.Errorf("-i: code %d, %v, stdout %q", code, err, out)
	}
	out, _ = capture(t, "", func() { code, err = runExec([]string{"-argv", "vm1", "echo", "a;b"}) })
	if err != nil || out != "a;b\n" {
		t.Errorf("-argv: %v, stdout %q", err, out)
	}
	_, errOut = capture(t, "", func() { code, err = runExec([]string{"-timeout", "1", "vm1", "sleep 5"}) })
	if err != nil || code != 124 || !strings.Contains(errOut, "timed out") {
		t.Errorf("timeout: code %d, %v, stderr %q", code, err, errOut)
	}
	if _, err := runExec([]string{"-argv", "vm1", "no-such-program"}); err == nil {
		t.Error("running a missing program succeeded")
	}
}

func TestDownloadPasswordFromStdin(t *testing.T) {
	_, guest := startServer(t)
	os.WriteFile(guest+"/a.txt", []byte("alpha"), 0o644)
	out := t.TempDir()
	var err error
	capture(t, "hunter2\n", func() { err = cmdDownload([]string{"-e", "-", "vm1", "a.txt", out}) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out + "/a.txt.zip"); err != nil {
		t.Error(err)
	}
	capture(t, "", func() { err = cmdDownload([]string{"-e", "-", "vm1", "a.txt", out}) })
	if err == nil {
		t.Error("an empty password from stdin was accepted")
	}
}
