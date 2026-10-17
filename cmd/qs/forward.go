package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"quicksand/internal/agentproto"

	"github.com/urfave/cli/v3"
)

// forward is a local port forwarded to an address as the guest sees it.
type forward struct {
	listen string // host:port on this machine
	target string // host:port, resolved in the guest
}

// parseForward parses ssh's -L syntax: [bind_address:]port:host:hostport.
// IPv6 addresses go in brackets. Without a bind address, the port listens
// on localhost only; "*" listens on all interfaces.
func parseForward(spec string) (forward, error) {
	parts, err := splitAddr(spec)
	if err != nil {
		return forward{}, fmt.Errorf("invalid forward %q: %w", spec, err)
	}
	bind := "127.0.0.1"
	switch len(parts) {
	case 3:
	case 4:
		bind, parts = parts[0], parts[1:]
		if bind == "*" {
			bind = ""
		}
	default:
		return forward{}, fmt.Errorf("invalid forward %q: want [bind_address:]port:host:hostport", spec)
	}
	port, host, hostPort := parts[0], parts[1], parts[2]
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return forward{}, fmt.Errorf("invalid forward %q: bad local port %q", spec, port)
	}
	if n, err := strconv.Atoi(hostPort); err != nil || n <= 0 || n > 65535 {
		return forward{}, fmt.Errorf("invalid forward %q: bad remote port %q", spec, hostPort)
	}
	if host == "" {
		return forward{}, fmt.Errorf("invalid forward %q: no remote host", spec)
	}
	return forward{listen: net.JoinHostPort(bind, port), target: net.JoinHostPort(host, hostPort)}, nil
}

// splitAddr splits on colons outside brackets, dropping the brackets.
func splitAddr(s string) ([]string, error) {
	var parts []string
	for {
		var part string
		if strings.HasPrefix(s, "[") {
			end := strings.IndexByte(s, ']')
			if end < 0 {
				return nil, errors.New("unclosed [")
			}
			part, s = s[1:end], s[end+1:]
			if s != "" && s[0] != ':' {
				return nil, errors.New("expected : after ]")
			}
		} else if i := strings.IndexByte(s, ':'); i >= 0 {
			part, s = s[:i], s[i:]
		} else {
			part, s = s, ""
		}
		parts = append(parts, part)
		if s == "" {
			return parts, nil
		}
		s = s[1:] // the colon
	}
}

// cmdForward forwards local ports to addresses reached from the guest, like
// ssh -L (local forwards only), until interrupted.
func cmdForward(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()
	if len(args) < 2 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: qs forward <vm> [bind_address:]port:host:hostport...")
	}
	var forwards []forward
	for _, s := range args[1:] {
		f, err := parseForward(s)
		if err != nil {
			return err
		}
		forwards = append(forwards, f)
	}
	c, err := signedIn()
	if err != nil {
		return err
	}
	v, err := findVM(c, args[0])
	if err != nil {
		return err
	}
	if v.Agent == nil {
		return fmt.Errorf("%s has no agent; forwarding needs one", v.Name)
	}

	var listeners []net.Listener
	defer func() {
		for _, ln := range listeners {
			ln.Close()
		}
	}()
	for _, f := range forwards {
		ln, err := net.Listen("tcp", f.listen)
		if err != nil {
			return err
		}
		listeners = append(listeners, ln)
		fmt.Fprintf(os.Stderr, "Forwarding %s to %s in %s.\n", ln.Addr(), f.target, v.Name)
		go serveForward(c, v, ln, f.target)
	}
	fmt.Fprintln(os.Stderr, "Press Ctrl-C to stop.")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	return nil
}

func serveForward(c *client, v vm, ln net.Listener, target string) {
	path := "/api/vms/" + url.PathEscape(v.ID) + "/connect?" + url.Values{"addr": {target}}.Encode()
	for {
		local, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			remote, err := c.stream(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "qs: forward %s to %s: %v\n", local.RemoteAddr(), target, err)
				local.Close()
				return
			}
			agentproto.Join(local, remote)
		}()
	}
}
