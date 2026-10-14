package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"quicksand/internal/agentproto"
)

// cmdExec runs one command in the guest and prints its output, like the
// web UI's "Run a command": no terminal or stream, just the result. It
// exits with the command's exit code (124 if it timed out, like timeout(1)).
func cmdExec(args []string) error {
	code, err := runExec(args)
	if err != nil {
		return err
	}
	os.Exit(code)
	return nil
}

func runExec(args []string) (int, error) {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	timeout := fs.Int("timeout", 60, "give up after this many `seconds` (at most 1800)")
	stdin := fs.Bool("i", false, "send standard input to the command (up to 4 MiB)")
	argv := fs.Bool("argv", false, "run the program directly with these arguments, not through the guest's shell")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: qs exec [-timeout seconds] [-i] [-argv] <vm> <command...>")
		fs.PrintDefaults()
	}
	// Flags come before the VM; everything after it is the command.
	fs.Parse(args)
	if fs.NArg() < 2 {
		fs.Usage()
		os.Exit(2)
	}
	if *timeout < 1 || *timeout > int(agentproto.MaxExecTimeout.Seconds()) {
		return 0, fmt.Errorf("-timeout must be 1 to %d seconds", int(agentproto.MaxExecTimeout.Seconds()))
	}
	req := agentproto.ExecRequest{TimeoutSeconds: *timeout}
	if *argv {
		req.Argv = fs.Args()[1:]
	} else {
		req.Shell = strings.Join(fs.Args()[1:], " ")
	}
	if *stdin {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, agentproto.MaxExecOutput+1))
		if err != nil {
			return 0, err
		}
		if len(data) > agentproto.MaxExecOutput {
			return 0, errors.New("standard input is over 4 MiB; use qs upload or qs shell instead")
		}
		req.Stdin = data
	}
	c, err := signedIn()
	if err != nil {
		return 0, err
	}
	v, err := findVM(c, fs.Arg(0))
	if err != nil {
		return 0, err
	}
	var res agentproto.ExecResponse
	if err := c.do("POST", "/api/vms/"+url.PathEscape(v.ID)+"/exec", req, &res); err != nil {
		return 0, err
	}
	os.Stdout.Write(res.Stdout)
	os.Stderr.Write(res.Stderr)
	switch {
	case res.TimedOut:
		fmt.Fprintf(os.Stderr, "qs: the command timed out after %d seconds and was killed\n", *timeout)
		return 124, nil
	case res.Error != "":
		return 0, errors.New(res.Error)
	}
	if res.Truncated {
		fmt.Fprintln(os.Stderr, "qs: the output was truncated (over 4 MiB per stream)")
	}
	return res.ExitCode, nil
}
