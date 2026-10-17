// Command quicksand-agent is the Quicksand guest agent. Install it in VM
// templates; see docs/agent.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"quicksand/internal/agent"
	"quicksand/internal/version"
)

func defaultConfigPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramData"), "Quicksand", "agent.json")
	}
	return "/etc/quicksand-agent/config.json"
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			if err := cmdInstall(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "quicksand-agent install:", err)
				os.Exit(1)
			}
			return
		case "set-display":
			// The agent runs itself in the console session to resize the display.
			os.Exit(agent.RunDisplayHelper(os.Args[2:]))
		case "clipboard-get", "clipboard-set":
			// And to reach the clipboard (Windows).
			os.Exit(agent.RunClipboardHelper(os.Args[1:]))
		}
	}
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: quicksand-agent [-config path] [-log path] [-version]")
		fmt.Fprintln(os.Stderr, "       quicksand-agent install -key ed25519:... [-subnet CIDR] [-port N]")
		flag.PrintDefaults()
	}
	configPath := flag.String("config", defaultConfigPath(), "path to the agent configuration file")
	logPath := flag.String("log", "", "append logs to this file instead of standard error")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return
	}
	// A service has no console, so it always logs to a file.
	if *logPath == "" && runtime.GOOS == "windows" && isService() {
		*logPath = filepath.Join(filepath.Dir(*configPath), "agent.log")
	}
	var out io.Writer = os.Stderr
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "quicksand-agent:", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(out, nil)))

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}
	if cfg.StateDir == "" {
		cfg.StateDir = "/var/lib/quicksand-agent"
		if runtime.GOOS == "windows" {
			cfg.StateDir = filepath.Dir(*configPath)
		}
	}
	a, err := agent.New(cfg)
	if err != nil {
		slog.Error("invalid config", "err", err)
		os.Exit(1)
	}
	slog.Info("quicksand agent starting", "version", version.Version)
	if ok, err := runAsService(a.Run); ok || err != nil {
		if err != nil {
			slog.Error("service", "err", err)
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := a.Run(ctx); err != nil {
		slog.Error("agent stopped", "err", err)
		os.Exit(1)
	}
}
