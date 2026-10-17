package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"quicksand/internal/agentproto"

	"github.com/urfave/cli/v3"
)

// cmdExec runs one command in the guest and prints its output, like the
// web UI's "Run a command": no terminal or stream, just the result. It
// exits with the command's exit code (124 if it timed out, like timeout(1)).
func cmdExec(ctx context.Context, cmd *cli.Command) error {
	code, err := runExec(cmd)
	if err != nil {
		return err
	}
	exit(code)
	return nil
}

// exit is os.Exit, replaced in tests.
var exit = os.Exit

func runExec(cmd *cli.Command) (int, error) {
	// Flags come before the VM; everything after it is the command.
	args := cmd.Args().Slice()
	if len(args) < 2 {
		return 0, errors.New("usage: qs exec [--timeout seconds] [-i] [--argv] <vm> <command...>")
	}
	timeout := cmd.Int("timeout")
	if timeout < 1 || timeout > int(agentproto.MaxExecTimeout.Seconds()) {
		return 0, fmt.Errorf("--timeout must be 1 to %d seconds", int(agentproto.MaxExecTimeout.Seconds()))
	}
	req := agentproto.ExecRequest{TimeoutSeconds: timeout}
	if cmd.Bool("argv") {
		req.Argv = args[1:]
	} else {
		req.Shell = strings.Join(args[1:], " ")
	}
	if cmd.Bool("i") {
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
	v, err := findVM(c, args[0])
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
		fmt.Fprintf(os.Stderr, "qs: the command timed out after %d seconds and was killed\n", timeout)
		return 124, nil
	case res.Error != "":
		return 0, errors.New(res.Error)
	}
	if res.Truncated {
		fmt.Fprintln(os.Stderr, "qs: the output was truncated (over 4 MiB per stream)")
	}
	return res.ExitCode, nil
}
