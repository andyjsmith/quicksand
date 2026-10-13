// Command quicksand serves the Quicksand web application.
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"quicksand/internal/config"
	"quicksand/internal/httpapi"
	"quicksand/internal/pve"
	"quicksand/web"
)

func main() {
	configPath := flag.String("config", "config.json", "path to the configuration file")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	if *debug {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}
	client, err := pve.New(cfg.PVE)
	if err != nil {
		slog.Error("create pve client", "err", err)
		os.Exit(1)
	}

	static := web.Dist()
	if static == nil {
		slog.Info("web client not embedded; serving API only (use the Vite dev server)")
	}
	srv := httpapi.New(client, static)

	slog.Info("listening", "addr", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, srv); err != nil {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}
