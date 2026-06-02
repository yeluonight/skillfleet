// Command skillfleet-agent is the SkillFleet edge process.
//
// Phase 2 t6 scope: agent.json load/save plus a working `enroll`
// subcommand that hits POST /agent/enroll, persists the returned
// device_id / device_secret to agent.json, and refuses to overwrite
// an existing config. HMAC-signed heartbeat / inventory arrive in t8.
//
// Invocation forms:
//
//	skillfleet-agent                              # run loop (refuses without agent.json)
//	skillfleet-agent enroll [-name NAME] <server-url> <token>
//	skillfleet-agent -version
//
// Configuration lives at ~/.skillfleet/agent/agent.json (v1.0 §8.2)
// and is created on successful enrolment with 0o600 permissions.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/yeluonight/skillfleet/internal/agentcandidates"
	"github.com/yeluonight/skillfleet/internal/agentcfg"
	"github.com/yeluonight/skillfleet/internal/agentclient"
	"github.com/yeluonight/skillfleet/internal/agentscan"
	"github.com/yeluonight/skillfleet/internal/enrollclient"
	"github.com/yeluonight/skillfleet/internal/serverlog"
)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "skillfleet-agent:", err)
		os.Exit(1)
	}
}

// dispatch interprets argv[1:] and delegates to the matching command.
// Top-level subcommands precede flag parsing so `enroll` can take
// positional args without colliding with the run loop's flags.
func dispatch(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "enroll":
			return runEnroll(args[1:])
		case "roots":
			return runRoots(args[1:])
		case "-version", "--version":
			fmt.Println(version())
			return nil
		case "help", "-h", "--help":
			printUsage(os.Stderr)
			return nil
		}
	}
	return runLoop(args)
}

// runLoop is the agent's normal operating mode: load the configured
// agent.json, set up logging, then start the heartbeat loop. Refuses
// to start without an existing config — the operator must run `enroll`
// first. Inventory + jobs loops arrive in Phase 3+.
func runLoop(args []string) error {
	fl := flag.NewFlagSet("skillfleet-agent", flag.ContinueOnError)
	configPath := fl.String("config", agentcfg.DefaultPath, "path to agent JSON config")
	showVer := fl.Bool("version", false, "print version and exit")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *showVer {
		fmt.Println(version())
		return nil
	}

	cfg, err := agentcfg.Load(*configPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf(
				"no agent config at %s; run `skillfleet-agent enroll <url> <token>` first",
				agentcfg.ExpandHomeForDisplay(*configPath),
			)
		}
		return err
	}

	log, err := serverlog.New(os.Stderr, "info", "text")
	if err != nil {
		return err
	}
	slog.SetDefault(log)

	log.Info("skillfleet-agent starting",
		slog.String("version", version()),
		slog.String("server_url", cfg.ServerURL),
		slog.String("device_id", cfg.DeviceID),
		slog.Int("heartbeat_sec", cfg.HeartbeatIntSec),
		slog.Int("inventory_sec", cfg.InventoryIntSec),
		slog.String("config", agentcfg.ExpandHomeForDisplay(*configPath)),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := agentclient.New(agentclient.Config{
		ServerURL:    cfg.ServerURL,
		DeviceID:     cfg.DeviceID,
		DeviceSecret: cfg.DeviceSecret,
	})
	if err != nil {
		return err
	}

	hbInterval := time.Duration(cfg.HeartbeatIntSec) * time.Second
	invInterval := time.Duration(cfg.InventoryIntSec) * time.Second
	jobsInterval := time.Duration(cfg.JobsIntSec) * time.Second

	// Heartbeat, inventory, and job polling run on independent tickers so
	// a slow scan or install never delays liveness reporting. All stop
	// when ctx is cancelled.
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		runHeartbeat(ctx, log, client, hbInterval, version())
	}()
	go func() {
		defer wg.Done()
		runInventory(ctx, log, client, invInterval, version(), *configPath)
	}()
	go func() {
		defer wg.Done()
		runJobs(ctx, log, client, jobsInterval, *configPath)
	}()
	wg.Wait()

	log.Info("skillfleet-agent shutting down", slog.String("reason", ctx.Err().Error()))
	return nil
}

// runHeartbeat ticks at the configured interval and POSTs /agent/heartbeat
// until ctx is done. A first heartbeat fires immediately on startup so
// last_seen_at updates without waiting a full interval. Transient
// errors are logged with the agent backing off, but the loop itself
// never aborts — operator approval can flip the device status at any
// moment and we want the next tick to discover it.
//
// State-transition logging: subsequent identical outcomes are silent
// (DEBUG) so a 5-second cadence doesn't flood stderr; whenever the
// outcome changes (e.g. "pending → ok" after the operator approves)
// we emit one INFO line so the operator sees the system come alive.
func runHeartbeat(ctx context.Context, log *slog.Logger, client *agentclient.Client, interval time.Duration, agentVer string) {
	const (
		stateInit         = ""
		stateOK           = "ok"
		stateNotApproved  = "not_approved"
		stateUnauthorized = "unauthorized"
		stateError        = "error"
	)
	last := stateInit

	beat := func() {
		hbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_, err := client.Heartbeat(hbCtx, agentclient.HeartbeatRequest{AgentVersion: agentVer})
		var (
			now    string
			line   string
			level  slog.Level
			detail string
		)
		switch {
		case err == nil:
			now = stateOK
			line = "heartbeat ok"
			level = slog.LevelInfo
		case errors.Is(err, agentclient.ErrDeviceNotApproved):
			now = stateNotApproved
			line = "heartbeat: device awaiting approval"
			level = slog.LevelInfo
		case errors.Is(err, agentclient.ErrUnauthorized):
			now = stateUnauthorized
			line = "heartbeat unauthorized"
			level = slog.LevelWarn
			detail = err.Error()
		default:
			now = stateError
			line = "heartbeat failed"
			level = slog.LevelWarn
			detail = err.Error()
		}

		// Demote repeat lines to DEBUG so a steady-state cadence stays
		// quiet. Surface every transition (and every error) at the
		// chosen level so transitions are visible.
		if now == last && level == slog.LevelInfo {
			level = slog.LevelDebug
		}
		attrs := []any{}
		if detail != "" {
			attrs = append(attrs, slog.String("err", detail))
		}
		if now != last && last != stateInit {
			attrs = append(attrs, slog.String("prev", last))
		}
		log.Log(ctx, level, line, attrs...)
		last = now
	}

	beat()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			beat()
		}
	}
}

// runInventory ticks at the configured interval and POSTs a full
// skill-scan report to /agent/inventory. Like the heartbeat loop it
// fires once immediately, never aborts on a transient error, and stays
// quiet in steady state: a successful run logs at DEBUG, a change in
// the skill count (or an error) logs at INFO/WARN so the operator sees
// material movement without per-tick noise.
//
// A scan + upload that the device isn't approved for is expected during
// the approval window and logged at INFO, mirroring heartbeat.
func runInventory(ctx context.Context, log *slog.Logger, client *agentclient.Client, interval time.Duration, agentVer string, configPath string) {
	lastCount := -1

	run := func() {
		report := agentscan.Scan(agentscan.Options{
			AgentVersion: agentVer,
			Logger:       log,
		})
		// Attach candidate-root discovery so the WebUI can offer one-click
		// registration. Reload allowed_roots each run so a register_root job is
		// reflected in the next inventory upload without restarting the agent.
		cfg, err := agentcfg.Load(configPath)
		if err != nil {
			log.Warn("inventory config reload failed", slog.String("err", err.Error()))
		} else {
			report.Roots = agentcandidates.Discover("", cfg.AllowedRoots)
		}

		invCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		resp, err := client.Inventory(invCtx, report)
		switch {
		case err == nil:
			if resp.SkillCount != lastCount {
				log.Info("inventory uploaded",
					slog.Int("skills", resp.SkillCount),
					slog.Int("roots", resp.RootCount),
				)
				lastCount = resp.SkillCount
			} else {
				log.Debug("inventory uploaded (unchanged)",
					slog.Int("skills", resp.SkillCount))
			}
		case errors.Is(err, agentclient.ErrDeviceNotApproved):
			log.Info("inventory: device awaiting approval")
		case errors.Is(err, agentclient.ErrUnauthorized):
			log.Warn("inventory unauthorized", slog.String("err", err.Error()))
		default:
			log.Warn("inventory upload failed", slog.String("err", err.Error()))
		}
	}

	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// Delegates to enrollclient.Run for the HTTP + filesystem dance so the
// CLI shell stays thin (and testable: see internal/enrollclient).
func runEnroll(args []string) error {
	fl := flag.NewFlagSet("skillfleet-agent enroll", flag.ContinueOnError)
	configPath := fl.String("config", agentcfg.DefaultPath, "path to write the agent JSON config")
	name := fl.String("name", "", "device name shown in the WebUI (defaults to hostname)")
	if err := fl.Parse(args); err != nil {
		return err
	}
	rest := fl.Args()
	if len(rest) != 2 {
		return fmt.Errorf("usage: skillfleet-agent enroll [-name NAME] [-config PATH] <server-url> <token>")
	}
	serverURL, token := rest[0], rest[1]
	if serverURL == "" || token == "" {
		return errors.New("server-url and token must be non-empty")
	}

	// If the operator didn't pass -name, fall back to the hostname so
	// the WebUI list isn't full of "agent"s. enrollclient also
	// auto-fills the hostname field for metadata; this only governs
	// the display name.
	resolvedName := *name
	if resolvedName == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			resolvedName = h
		} else {
			return errors.New("could not determine hostname; pass -name explicitly")
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	res, err := enrollclient.Run(ctx, enrollclient.Options{
		ServerURL:    serverURL,
		Token:        token,
		Name:         resolvedName,
		AgentVersion: version(),
		ConfigPath:   *configPath,
	})
	if err != nil {
		return mapEnrollError(err)
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  SkillFleet agent enrolled.")
	fmt.Fprintln(os.Stderr, "  device_id : "+res.DeviceID)
	fmt.Fprintln(os.Stderr, "  status    : "+res.Status+" (awaiting operator approval in WebUI)")
	fmt.Fprintln(os.Stderr, "  config    : "+res.ConfigPath)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  Next steps:")
	fmt.Fprintln(os.Stderr, "    1. Approve this device in the WebUI (Devices page).")
	fmt.Fprintln(os.Stderr, "    2. Register the skill directories this agent may manage, e.g.:")
	fmt.Fprintln(os.Stderr, "         skillfleet-agent roots add -tool claude-code -scope user -path ~/.claude/skills")
	fmt.Fprintln(os.Stderr, "       (without at least one root, install / enable-disable jobs cannot run.)")
	fmt.Fprintln(os.Stderr, "    3. Start the agent loop: skillfleet-agent")
	fmt.Fprintln(os.Stderr, "")
	return nil
}

// mapEnrollError rewrites sentinel errors into operator-friendly
// messages without leaking raw URLs / stack traces.
func mapEnrollError(err error) error {
	switch {
	case errors.Is(err, enrollclient.ErrTokenNotFound):
		return errors.New("enroll: token not recognised; mint a fresh one in the WebUI")
	case errors.Is(err, enrollclient.ErrTokenExpired):
		return errors.New("enroll: token expired; mint a fresh one in the WebUI")
	case errors.Is(err, enrollclient.ErrTokenNotUsable):
		return errors.New("enroll: token already used or revoked")
	case errors.Is(err, enrollclient.ErrAlreadyExists):
		return fmt.Errorf("%w (remove or move that file to re-enrol)", err)
	default:
		return err
	}
}

func printUsage(w *os.File) {
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  skillfleet-agent                                    # run agent loop")
	_, _ = fmt.Fprintln(w, "  skillfleet-agent enroll [-name N] <server-url> <token>  # one-shot enrolment")
	_, _ = fmt.Fprintln(w, "  skillfleet-agent roots list                         # list allowed skill roots")
	_, _ = fmt.Fprintln(w, "  skillfleet-agent roots add -tool T -scope S -path P # register a skill root")
	_, _ = fmt.Fprintln(w, "  skillfleet-agent roots scan                         # scan and select local roots")
	_, _ = fmt.Fprintln(w, "  skillfleet-agent roots rm <id>                      # remove a skill root")
	_, _ = fmt.Fprintln(w, "  skillfleet-agent -version                           # print version")
}

// versionOverride is injected by a release build via
// -ldflags "-X main.versionOverride=v0.10.0" (see Makefile / CI). When
// empty (plain `go build` / `go run`) version() falls back to the VCS
// revision from build info, then "dev".
var versionOverride string

// version returns the build version. Same logic as the server;
// duplicated rather than imported to keep the binary surface independent.
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
