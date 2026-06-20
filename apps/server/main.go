// Command skillfleet-server is the SkillFleet control-plane process.
//
// Phase 1-2 scope: load the YAML config, set up structured logging,
// open the SQLite database with WAL + applied migrations, generate
// the admin bootstrap code on first boot, and serve the embedded
// WebUI alongside the /api, /agent, and /health trees. Phase 2 t5
// lit up /agent/enroll; HMAC-guarded /agent/heartbeat etc arrive in
// t7+.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yeluonight/skillfleet/internal/agentapi"
	"github.com/yeluonight/skillfleet/internal/api"
	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/config"
	"github.com/yeluonight/skillfleet/internal/daemon"
	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/draft"
	"github.com/yeluonight/skillfleet/internal/noncepurge"
	"github.com/yeluonight/skillfleet/internal/ratelimit"
	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/scheduler"
	"github.com/yeluonight/skillfleet/internal/serverlog"
	"github.com/yeluonight/skillfleet/internal/setup"
	"github.com/yeluonight/skillfleet/internal/source"
	"github.com/yeluonight/skillfleet/internal/webui"
	"github.com/yeluonight/skillfleet/migrations"
)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "skillfleet-server:", err)
		os.Exit(1)
	}
}

// dispatch interprets argv[1:]. Daemon control subcommands precede flag
// parsing; anything else (including -config/-version flags) falls through
// to run, the foreground server mode.
func dispatch(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "start":
			return runDaemonStart(args[1:])
		case "stop":
			return runDaemonStop(args[1:])
		case "restart":
			return runDaemonRestart(args[1:])
		case "status":
			return runDaemonStatus(args[1:])
		case "help", "-h", "--help":
			printUsage(os.Stderr)
			return nil
		}
	}
	return run(args)
}

func run(args []string) error {
	fl := flag.NewFlagSet("skillfleet-server", flag.ContinueOnError)
	configPath := fl.String("config", config.DefaultPath, "path to server config YAML")
	foreground := fl.Bool("foreground", false, "run in the foreground instead of starting the background service")
	showVer := fl.Bool("version", false, "print version and exit")
	if err := fl.Parse(args); err != nil {
		return err
	}

	if *showVer {
		fmt.Println(version())
		return nil
	}

	cfg, err := loadOrSeedConfig(*configPath)
	if err != nil {
		return err
	}

	log, err := serverlog.New(os.Stderr, cfg.Logging.Level, cfg.Logging.Format)
	if err != nil {
		return err
	}
	slog.SetDefault(log)

	log.Info("skillfleet-server starting",
		slog.String("version", version()),
		slog.String("bind", cfg.Server.Bind),
		slog.String("data_dir", cfg.Server.DataDir),
		slog.String("config", config.ExpandHomeForDisplay(*configPath)),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.Server.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data_dir: %w", err)
	}
	dbPath := filepath.Join(cfg.Server.DataDir, db.DefaultFileName)
	store, err := db.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := store.Close(); cerr != nil {
			log.Warn("db close failed", slog.String("err", cerr.Error()))
		}
	}()

	migResult, err := migrations.Apply(ctx, store, migrations.Embedded())
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	log.Info("database ready",
		slog.String("path", dbPath),
		slog.Int("schema_version", migResult.EndVersion),
		slog.Int("applied_this_run", migResult.AppliedCount),
	)

	setupStatus, statusErr := setup.CurrentStatus(ctx, store)
	if statusErr != nil {
		return fmt.Errorf("setup status: %w", statusErr)
	}
	if !*foreground && daemon.IsTerminal(os.Stdout) && !setupStatus.Required {
		bin, err := daemon.OwnBinaryPath()
		if err == nil {
			if err := daemon.Start(daemon.ServerSpec(bin, *configPath)); err == nil {
				fmt.Println("skillfleet-server started in the background.")
				fmt.Printf("Open http://%s and use `skillfleet-server status` to inspect it.\n", cfg.Server.Bind)
				return nil
			}
			fmt.Fprintln(os.Stderr, "skillfleet-server: falling back to foreground mode")
		}
	}

	// Bootstrap code: regenerated on every boot while users is empty
	// (see migrations/0003_setup_state.sql for the contract).
	code, ok, err := setup.EnsureCode(ctx, store, time.Now())
	if err != nil {
		return fmt.Errorf("setup code: %w", err)
	}
	if ok {
		// Print to stderr explicitly (not the logger) so the code is
		// readable even when logs ship to a structured sink. The banner
		// lines bracket the value to keep it scrubbable.
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  SkillFleet setup code (visit the WebUI to complete bootstrap):")
		fmt.Fprintln(os.Stderr, "  "+code)
		fmt.Fprintln(os.Stderr, "")
		log.Info("setup code generated", slog.String("hint", "see stderr banner above"))
	} else {
		log.Info("setup already completed; admin user exists")
	}

	router, sched, err := buildRouter(cfg, store, log)
	if err != nil {
		return fmt.Errorf("build router: %w", err)
	}
	srv := &http.Server{
		Addr:              cfg.Server.Bind,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Background update-check poller (§2.9 t7). It shares the server's signal
	// context, so SIGINT/SIGTERM cancels mid-sweep; we wait for it to unwind
	// before returning so no check outlives the process. A disabled poller
	// (interval <= 0) makes Run a no-op that returns immediately.
	var schedWG sync.WaitGroup
	schedWG.Add(1)
	go func() {
		defer schedWG.Done()
		if err := sched.Run(ctx); err != nil {
			log.Error("update-check poller exited with error", slog.String("err", err.Error()))
		}
	}()

	// Background nonce pruning (优化改造 §1.5). agent_nonces only grows;
	// rows older than the clock-skew window carry no replay value. 5min
	// interval is far below the table's growth rate and keeps sweeps
	// cheap. Shares ctx so SIGINT/SIGTERM cancels it; schedWG.Wait()
	// below covers it too.
	schedWG.Add(1)
	go func() {
		defer schedWG.Done()
		noncepurge.Run(ctx, store, 5*time.Minute, agentapi.DefaultMaxClockSkew, log)
	}()

	listenErr := make(chan error, 1)
	go func() {
		log.Info("http listener starting", slog.String("addr", cfg.Server.Bind))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
			return
		}
		close(listenErr)
	}()

	select {
	case <-ctx.Done():
		log.Info("skillfleet-server shutting down", slog.String("reason", ctx.Err().Error()))
	case err := <-listenErr:
		if err != nil {
			return fmt.Errorf("http listener: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown error", slog.String("err", err.Error()))
	}
	// ctx is already cancelled on the shutdown path (signal) — wait for the
	// poller to finish its current sweep so we exit cleanly. On the
	// listener-error path ctx may still be live; stop() in the deferred call
	// would cancel it, but we cancel explicitly here to unblock the poller.
	stop()
	schedWG.Wait()
	return nil
}

// buildRouter wires Deps from cfg into the api router. Split out from
// run() to keep the main flow readable and to make the rate-limit /
// session-ttl wiring auditable in one place. It also constructs the
// background update-check poller from the same source/registry stores, so
// the scheduler and the check-updates handler share one set of collaborators.
func buildRouter(cfg config.Config, store *sql.DB, log *slog.Logger) (http.Handler, *scheduler.Scheduler, error) {
	// Rate-limit values are user-supplied via YAML; surface a parse
	// failure at startup rather than silently disabling the limiter
	// or panicking on the first login.
	ipRate, err := ratelimit.ParseRate(cfg.Auth.RateLimit.LoginPerIP)
	if err != nil {
		log.Error("auth.rate_limit.login_per_ip: parse failed; using safe default",
			slog.String("value", cfg.Auth.RateLimit.LoginPerIP),
			slog.String("err", err.Error()),
		)
		ipRate = ratelimit.Rate{Limit: 10, Window: time.Minute}
	}
	userRate, err := ratelimit.ParseRate(cfg.Auth.RateLimit.LoginPerUser)
	if err != nil {
		log.Error("auth.rate_limit.login_per_user: parse failed; using safe default",
			slog.String("value", cfg.Auth.RateLimit.LoginPerUser),
			slog.String("err", err.Error()),
		)
		userRate = ratelimit.Rate{Limit: 5, Window: time.Minute}
	}

	// Registry + drafts share the store/ subdir under the data dir
	// (v1.0 §16.1). registry.New / draft.New create their package and
	// blob directories eagerly.
	storeDir := filepath.Join(cfg.Server.DataDir, "store")
	reg, err := registry.New(store, storeDir)
	if err != nil {
		return nil, nil, fmt.Errorf("registry store: %w", err)
	}
	drafts, err := draft.New(store, reg, storeDir)
	if err != nil {
		return nil, nil, fmt.Errorf("draft store: %w", err)
	}

	// Source bindings (phase 6). The store owns no filesystem state; the
	// fetcher pulls public repos with go-git using its default limits.
	srcStore := source.New(store)
	fetcher := source.NewFetcher()

	// Background update-check poller (phase 6 t7). It reuses the same source
	// store, fetcher, and registry as the manual check-updates handler via a
	// fresh stateless Checker, so both paths apply the identical §8.4 logic
	// (including the moved-commit-but-unchanged-content guard).
	checker := source.NewChecker(srcStore, fetcher, reg)
	sched := scheduler.New(
		srcStore, checker,
		cfg.Scheduler.UpdateCheckInterval, cfg.Scheduler.Concurrency,
		time.Now, log,
	)

	auditLog := audit.New(store, log, time.Now)
	return api.NewRouter(api.Deps{
		DB:         store,
		Logger:     log,
		Now:        time.Now,
		Audit:      auditLog,
		SessionTTL: cfg.Auth.SessionTTL,
		LoginIP:    ipRate,
		LoginUser:  userRate,
		HTTPS:      strings.HasPrefix(strings.ToLower(cfg.Server.ExternalURL), "https://"),
		WebUI:      webui.Handler(),
		Registry:   reg,
		Drafts:     drafts,
		Sources:    srcStore,
		Fetcher:    fetcher,
		Deploy:     deploy.New(store),
		Agent: agentapi.NewRouter(agentapi.Deps{
			DB:       store,
			Logger:   log,
			Now:      time.Now,
			Audit:    auditLog,
			Packages: registryPackageSource{reg},
			Adopter:  registryAdopter{reg},
		}),
	}), sched, nil
}

// loadOrSeedConfig returns a Config, writing the canonical default
// file when the caller's path doesn't exist yet.
func loadOrSeedConfig(path string) (config.Config, error) {
	cfg, err := config.Load(path)
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return config.Config{}, err
	}
	// First launch: seed the file and reload it. Failing to seed is
	// usually a permission problem and should be surfaced verbatim.
	if werr := config.WriteDefault(path); werr != nil {
		return config.Config{}, werr
	}
	fmt.Fprintf(os.Stderr, "skillfleet-server: wrote default config to %s\n", config.ExpandHomeForDisplay(path))
	return config.Load(path)
}

// versionOverride is injected by a release build via
// -ldflags "-X main.versionOverride=v0.10.0" (see Makefile / CI). When
// empty (plain `go build` / `go run`) version() falls back to the VCS
// revision from the build info, then "dev".
var versionOverride string

// version returns the build version embedded by `go build`. Falls back
// to "dev" when the binary was built with `go run` or without VCS info.
func version() string {
	if versionOverride != "" {
		return versionOverride
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				if len(s.Value) > 12 {
					return s.Value[:12]
				}
				return s.Value
			}
		}
	}
	return "dev"
}
