package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"quicksand/internal/agentclient"
	"quicksand/internal/agentproto"
	"quicksand/internal/config"
)

func newAgentClient(a *config.Agent) (*agentclient.Client, ed25519.PublicKey, error) {
	key := a.Key()
	c, err := agentclient.New(agentclient.Options{Key: key, Port: a.Port, Proxy: a.Proxy})
	return c, key.Public().(ed25519.PublicKey), err
}

func agentCommand(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("missing agent subcommand")
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("agent "+sub, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	configPath := fs.String("config", "config.json", "path to the configuration file")

	if sub == "keygen" {
		fs.Parse(args)
		key, err := agentproto.GenerateKey()
		if err != nil {
			return err
		}
		fmt.Printf(`Add the private key to the agent section of config.json:

  "agent": {
    "privateKey": %q,
    ...
  }

Agents must trust its public key (their "authorizedKeys"; it's also shown on
the Admin page and by "quicksand agent pubkey"):

  %s
`, agentproto.FormatPrivateKey(key), agentproto.FormatPublicKey(key.Public().(ed25519.PublicKey)))
		return nil
	}

	timeout := fs.Int("timeout", 60, "command timeout in seconds (exec)")
	shell := fs.String("shell", "", "run this command line through the guest's shell (exec)")
	fs.Parse(args)
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if cfg.Agent == nil {
		return errors.New("config has no agent section")
	}

	switch sub {
	case "pubkey":
		fmt.Println(agentproto.FormatPublicKey(cfg.Agent.Key().Public().(ed25519.PublicKey)))
		return nil
	case "info", "exec":
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown agent subcommand %q", sub)
	}

	if fs.NArg() < 1 {
		fs.Usage()
		return errors.New("expected an agent address")
	}
	host := fs.Arg(0)
	client, _, err := newAgentClient(cfg.Agent)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout+15)*time.Second)
	defer cancel()

	if sub == "info" {
		info, err := client.Info(ctx, host)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	}

	req := agentproto.ExecRequest{Shell: *shell, TimeoutSeconds: *timeout}
	if req.Shell == "" {
		req.Argv = fs.Args()[1:]
		if len(req.Argv) > 0 && req.Argv[0] == "--" {
			req.Argv = req.Argv[1:]
		}
		if len(req.Argv) == 0 {
			return errors.New("expected a command after the address, or -shell")
		}
	}
	res, err := client.Exec(ctx, host, req)
	if err != nil {
		return err
	}
	os.Stdout.Write(res.Stdout)
	os.Stderr.Write(res.Stderr)
	switch {
	case res.Error != "":
		return errors.New(res.Error)
	case res.TimedOut:
		return errors.New("command timed out")
	case res.ExitCode != 0:
		os.Exit(res.ExitCode)
	}
	return nil
}
