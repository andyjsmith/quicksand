// Command quicksand serves the Quicksand web application and manages its users.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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
	"quicksand/internal/version"
	"quicksand/internal/vms"
	"quicksand/web"
)

const usage = `Usage:
  quicksand [serve] [-config path] [-debug] [-dev-web URL]
  quicksand version
  quicksand user add [-config path] [-admin] <username>
  quicksand user passwd [-config path] <username>
  quicksand user delete [-config path] <username>
  quicksand user admin [-config path] [-revoke] <username>
  quicksand user list [-config path]
  quicksand agent keygen
  quicksand agent pubkey [-config path]
  quicksand agent info [-config path] <host[:port]>
  quicksand agent exec [-config path] [-timeout 60] <host[:port]> -- <command> [args...]
  quicksand agent exec [-config path] [-timeout 60] -shell '<command line>' <host[:port]>

Without -config, the configuration is config.json in the current directory,
or /etc/quicksand/config.json if there is none.
`

// defaultConfig is the configuration file used without -config: config.json
// in the current directory if there is one, otherwise the packaged location.
func defaultConfig() string {
	if _, err := os.Stat("config.json"); err == nil {
		return "config.json"
	}
	return "/etc/quicksand/config.json"
}

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && (args[0] == "serve" || args[0] == "user" || args[0] == "agent" || args[0] == "version") {
		cmd, args = args[0], args[1:]
	}
	if len(args) > 0 && (args[0] == "-version" || args[0] == "--version") {
		cmd = "version"
	}
	var err error
	switch cmd {
	case "version":
		fmt.Println("quicksand", version.Version)
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
	configPath := fs.String("config", defaultConfig(), "path to the configuration file")
	debug := fs.Bool("debug", false, "enable debug logging")
	devWeb := fs.String("dev-web", "", "for development: proxy the web client to this dev server `URL` (e.g. http://localhost:5173)")
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
	var sso *auth.SSO
	if cfg.OIDC != nil {
		sso = auth.NewSSO(cfg.OIDC, db)
		// Discovered now to report problems early; sign-ins retry it.
		go func() {
			if _, err := sso.Discover(); err != nil {
				slog.Warn("single sign-on", "err", err)
			}
		}()
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
	var devWebURL *url.URL
	if *devWeb != "" {
		if devWebURL, err = url.Parse(*devWeb); err != nil || devWebURL.Host == "" {
			return fmt.Errorf("invalid -dev-web URL %q", *devWeb)
		}
		slog.Info("proxying the web client to its dev server", "url", devWebURL)
	} else if static == nil {
		slog.Info("web client not embedded; serving API only (use the Vite dev server, or -dev-web)")
	}
	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: httpapi.New(httpapi.Deps{
			Config: cfg, PVE: client, Store: db, Sessions: auth.NewSessions(db, cfg.SecureCookies), SSO: sso,
			VMs: service, Hub: hub, Static: static, DevWeb: devWebURL, AgentPublicKey: agentPublicKey,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go expireSessions(ctx, db, cfg.AuditRetention())
	go service.RunExpiry(ctx)
	go service.RunSuspend(ctx)
	go service.RunLaunch(ctx)
	go service.RunLinks(ctx)
	go service.RunFirewall(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	if cfg.TLS != nil {
		slog.Info("listening (HTTPS)", "addr", cfg.Listen, "version", version.Version)
		err = srv.ListenAndServeTLS(cfg.TLS.Cert, cfg.TLS.Key)
	} else {
		slog.Info("listening (HTTP)", "addr", cfg.Listen, "version", version.Version)
		err = srv.ListenAndServe()
	}
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// expireSessions hourly removes expired sessions and API tokens, and audit
// log entries past their retention (cfg.AuditRetention).
func expireSessions(ctx context.Context, db *store.Store, auditRetention time.Duration) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		for name, clean := range map[string]func(context.Context) error{
			"sessions": db.DeleteExpiredSessions, "tokens": db.DeleteDeadTokens,
			"audit log": func(ctx context.Context) error { return db.DeleteOldAudit(ctx, auditRetention) },
		} {
			if err := clean(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("clean up "+name, "err", err)
			}
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
	configPath := fs.String("config", defaultConfig(), "path to the configuration file")
	admin := fs.Bool("admin", false, "make the user an administrator")
	revoke := fs.Bool("revoke", false, "with admin: remove the administrator role")
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
			role, sign := "user", "password"
			if u.IsAdmin {
				role = "admin"
			}
			if u.SSO {
				sign = "sso"
			}
			fmt.Printf("%-24s %-6s %-8s created %s\n", u.Username, role, sign, u.CreatedAt.Format(time.DateOnly))
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
	case "admin":
		if fs.NArg() != 1 {
			fs.Usage()
			return errors.New("expected one username")
		}
		u, _, err := db.UserCredentials(ctx, fs.Arg(0))
		if err != nil {
			return fmt.Errorf("user %q: %w", fs.Arg(0), err)
		}
		if err := db.SetAdmin(ctx, u.ID, !*revoke); err != nil {
			return err
		}
		if *revoke {
			fmt.Printf("%s is no longer an administrator.\n", u.Username)
		} else {
			fmt.Printf("%s is now an administrator.\n", u.Username)
		}
		if u.SSO && cfg.OIDC != nil && len(cfg.OIDC.AdminGroups) > 0 {
			fmt.Println("Their groups at the identity provider decide this again at their next sign-in (oidc.adminGroups).")
		}
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
