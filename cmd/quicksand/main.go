// Command quicksand serves the Quicksand web application and manages its users.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"quicksand/internal/agentclient"
	"quicksand/internal/agentproto"
	"quicksand/internal/auth"
	"quicksand/internal/config"
	"quicksand/internal/httpapi"
	"quicksand/internal/pve"
	"quicksand/internal/store"
	"quicksand/internal/vms"
	"quicksand/web"
)

const usage = `Usage:
  quicksand [serve] [-config config.json] [-debug]
  quicksand user add [-config config.json] [-admin] <username>
  quicksand user passwd [-config config.json] <username>
  quicksand user delete [-config config.json] <username>
  quicksand user list [-config config.json]
  quicksand agent keygen
  quicksand agent pubkey [-config config.json]
  quicksand agent info [-config config.json] <host[:port]>
  quicksand agent exec [-config config.json] [-timeout 60] <host[:port]> -- <command> [args...]
  quicksand agent exec [-config config.json] [-timeout 60] -shell '<command line>' <host[:port]>
`

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && (args[0] == "serve" || args[0] == "user" || args[0] == "agent") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "user":
		err = userCommand(args)
	case "agent":
		err = agentCommand(args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "quicksand:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "config.json", "path to the configuration file")
	debug := fs.Bool("debug", false, "enable debug logging")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.Parse(args)

	if *debug {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if n, err := db.CountAdmins(context.Background()); err == nil && n == 0 {
		slog.Warn("no administrator exists yet; create one with: quicksand user add -admin <username>")
	}
	client, err := pve.New(cfg.PVE)
	if err != nil {
		return fmt.Errorf("create pve client: %w", err)
	}

	var agents *agentclient.Client
	var agentPublicKey string
	if cfg.Agent != nil {
		client, pub, err := newAgentClient(cfg.Agent)
		if err != nil {
			return err
		}
		agents, agentPublicKey = client, agentproto.FormatPublicKey(pub)
	}
	hub := vms.NewHub()
	service := vms.NewService(cfg, client, db, hub, agents)
	if err := service.Recover(context.Background()); err != nil {
		return fmt.Errorf("recover jobs: %w", err)
	}
	if err := service.CheckLinks(context.Background()); err != nil {
		return err
	}
	defer service.Shutdown()

	static := web.Dist()
	if static == nil {
		slog.Info("web client not embedded; serving API only (use the Vite dev server)")
	}
	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: httpapi.New(httpapi.Deps{
			Config: cfg, PVE: client, Store: db, Sessions: auth.NewSessions(db, cfg.SecureCookies),
			VMs: service, Hub: hub, Static: static, AgentPublicKey: agentPublicKey,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go expireSessions(ctx, db)
	go service.RunExpiry(ctx)
	go service.RunLinks(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	if cfg.TLS != nil {
		slog.Info("listening (HTTPS)", "addr", cfg.Listen)
		err = srv.ListenAndServeTLS(cfg.TLS.Cert, cfg.TLS.Key)
	} else {
		slog.Info("listening (HTTP)", "addr", cfg.Listen)
		err = srv.ListenAndServe()
	}
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func expireSessions(ctx context.Context, db *store.Store) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := db.DeleteExpiredSessions(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("delete expired sessions", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func userCommand(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("missing user subcommand")
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("user "+sub, flag.ExitOnError)
	configPath := fs.String("config", "config.json", "path to the configuration file")
	admin := fs.Bool("admin", false, "make the user an administrator")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	ctx := context.Background()

	switch sub {
	case "list":
		users, err := db.Users(ctx)
		if err != nil {
			return err
		}
		for _, u := range users {
			role := "user"
			if u.IsAdmin {
				role = "admin"
			}
			fmt.Printf("%-24s %-6s created %s\n", u.Username, role, u.CreatedAt.Format(time.DateOnly))
		}
		return nil
	case "delete":
		if fs.NArg() != 1 {
			fs.Usage()
			return errors.New("expected one username")
		}
		u, _, err := db.UserCredentials(ctx, fs.Arg(0))
		if err != nil {
			return fmt.Errorf("user %q: %w", fs.Arg(0), err)
		}
		if err := db.DeleteUser(ctx, u.ID); errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("user %q still owns VMs; destroy them first", u.Username)
		} else if err != nil {
			return err
		}
		fmt.Printf("Deleted user %s.\n", u.Username)
		return nil
	case "add", "passwd":
		if fs.NArg() != 1 {
			fs.Usage()
			return errors.New("expected one username")
		}
		username := fs.Arg(0)
		password, err := readNewPassword()
		if err != nil {
			return err
		}
		if sub == "add" {
			u, err := auth.CreateUser(ctx, db, username, password, *admin)
			if errors.Is(err, store.ErrConflict) {
				return fmt.Errorf("user %q already exists", username)
			}
			if err != nil {
				return err
			}
			fmt.Printf("Created user %s.\n", u.Username)
			return nil
		}
		u, _, err := db.UserCredentials(ctx, username)
		if err != nil {
			return fmt.Errorf("user %q: %w", username, err)
		}
		if err := auth.ValidatePassword(password); err != nil {
			return err
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			return err
		}
		if err := db.SetPassword(ctx, u.ID, hash); err != nil {
			return err
		}
		if err := db.DeleteUserSessions(ctx, u.ID); err != nil {
			return err
		}
		fmt.Printf("Changed the password for %s and signed them out everywhere.\n", u.Username)
		return nil
	}
	fmt.Fprint(os.Stderr, usage)
	return fmt.Errorf("unknown user subcommand %q", sub)
}
