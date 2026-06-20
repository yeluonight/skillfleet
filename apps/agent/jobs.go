// apps/agent — jobs.go: the downlink poll loop (phase 8). It is the
// third agent goroutine alongside heartbeat + inventory. Each tick it
// asks the server for a pending deployment job; when it gets one it
// resolves the target against the agent's allowed_roots, runs the
// install (or rollback) through internal/agentinstall — which does the
// download/verify/backup/atomic-swap/rescan/auto-rollback dance — and
// reports the Result back. Like the other loops it never aborts on a
// transient error; the next tick retries.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/yeluonight/skillfleet/internal/agentcfg"
	"github.com/yeluonight/skillfleet/internal/agentclient"
	"github.com/yeluonight/skillfleet/internal/agentinstall"
	"github.com/yeluonight/skillfleet/internal/agentroots"
	"github.com/yeluonight/skillfleet/internal/agentstate"
	"github.com/yeluonight/skillfleet/internal/deploy"
)

// fetcherAdapter adapts *agentclient.Client to agentinstall.PackageFetcher
// (the method names differ; the signatures match).
type fetcherAdapter struct{ c *agentclient.Client }

func (f fetcherAdapter) FetchPackage(ctx context.Context, downloadPath string) (io.ReadCloser, error) {
	return f.c.DownloadPackage(ctx, downloadPath)
}

// agentRoots maps the config's allowed roots onto the installer's type.
func agentRoots(cfg agentcfg.Config) []agentinstall.AllowedRoot {
	out := make([]agentinstall.AllowedRoot, 0, len(cfg.AllowedRoots))
	for _, r := range cfg.AllowedRoots {
		out = append(out, agentinstall.AllowedRoot{ID: r.ID, Tool: r.Tool, Scope: r.Scope, Path: r.Path})
	}
	return out
}

// backupsDir is the agent-owned directory under which per-job backups
// are written (v1.0 §16.2 ~/.skillfleet/agent/backups). Derived from the
// config path's directory so a custom -config location keeps its
// backups alongside it.
func backupsDir() string {
	base, err := agentcfg.ExpandHome("~/.skillfleet/agent")
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "backups")
}

// runJobs polls /agent/jobs at the configured cadence and executes any
// claimed job. A first poll fires immediately so a queued install starts
// without waiting a full interval.
func runJobs(ctx context.Context, log *slog.Logger, client *agentclient.Client, interval time.Duration, configPath string) {
	// stateWriter executes state-change jobs (Phase 9): it edits the
	// tool's out-of-band config to enable/disable a skill. It needs the home
	// dir for per-user codex/opencode config paths.
	home, err := os.UserHomeDir()
	if err != nil {
		// A missing home is non-fatal here: state-change jobs will fail to
		// resolve per-user config paths and report it; install/rollback are
		// unaffected. Log once.
		log.Warn("could not resolve home dir; state-change jobs may fail",
			slog.String("err", err.Error()))
	}

	poll := func() {
		// Claim one job; if none, quietly return. Config/dependency setup is
		// deferred until after a claim so idle polls don't read agent.json or
		// allocate executors on every tick.
		pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		job, ok, err := client.Jobs(pollCtx)
		cancel()
		if err != nil {
			log.Warn("jobs poll failed", slog.String("err", err.Error()))
			return
		}
		if !ok {
			return
		}
		log.Info("claimed deployment job",
			slog.String("job", job.ID), slog.String("op", job.Operation))
		runOneJob(ctx, log, client, configPath, home, job)
	}

	poll()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}

// runOneJob executes a single claimed job and reports its result. It
// decodes the request to learn the operation + target, runs the matching
// executor path, and POSTs the Result (status succeeded/failed). A
// decode or execution error still produces a failed result so the job
// doesn't hang in "claimed" forever.
func runOneJob(ctx context.Context, log *slog.Logger, client *agentclient.Client, configPath, home string, job deploy.ClaimedJob) {
	var req deploy.Request
	if err := json.Unmarshal([]byte(job.RequestJSON), &req); err != nil {
		reportResult(ctx, log, client, job.ID, deploy.StatusFailed,
			deploy.Result{ErrorCode: "bad_request", ErrorMessage: "undecodable request: " + err.Error()})
		return
	}

	var (
		res     deploy.Result
		execErr error
	)
	switch deploy.Operation(job.Operation) {
	case deploy.OpInstall, deploy.OpRollback, deploy.OpStateChange:
		roots, err := loadJobRoots(configPath)
		if err != nil {
			reportResult(ctx, log, client, job.ID, deploy.StatusFailed,
				deploy.Result{ErrorCode: "config_load_failed", ErrorMessage: err.Error()})
			return
		}
		exec := agentinstall.NewExecutor(agentinstall.Config{
			BackupsDir:   backupsDir(),
			AllowedRoots: roots,
		}, fetcherAdapter{client}, time.Now)
		stateWriter := agentstate.NewWriter(roots, home)

		switch deploy.Operation(job.Operation) {
		case deploy.OpInstall:
			var plan deploy.Plan
			if err := json.Unmarshal([]byte(job.PlanJSON), &plan); err != nil {
				reportResult(ctx, log, client, job.ID, deploy.StatusFailed,
					deploy.Result{ErrorCode: "bad_plan", ErrorMessage: "undecodable plan: " + err.Error()})
				return
			}
			res, execErr = exec.Install(ctx, plan, req.Target)
		case deploy.OpRollback:
			// A rollback job carries the target + backup reference in its plan;
			// decode what the server recorded.
			var spec deploy.RollbackPlan
			if job.PlanJSON != "" {
				_ = json.Unmarshal([]byte(job.PlanJSON), &spec)
			}
			res, execErr = exec.Rollback(spec)
		case deploy.OpStateChange:
			// A state-change job's plan_json is a StateChangePlan, but the
			// writer drives everything from the Request (target + skill +
			// desired state), which the plan mirrors; we pass the decoded
			// request straight through. The plan is decoded only to honour the
			// recorded desired state if it ever diverged from the request (it
			// does not today; the request is authoritative).
			res, execErr = stateWriter.StateChange(ctx, req)
		}
	case deploy.OpRegisterRoot:
		res, execErr = runRegisterRootJob(configPath, home, req)
	case deploy.OpRemoveRoot:
		res, execErr = runRemoveRootJob(configPath, req)
	case deploy.OpCaptureSkill:
		res, execErr = runCaptureSkillJob(ctx, client, home, req)
	default:
		reportResult(ctx, log, client, job.ID, deploy.StatusFailed,
			deploy.Result{ErrorCode: "bad_operation", ErrorMessage: "unknown operation " + job.Operation})
		return
	}

	status := deploy.StatusSucceeded
	if execErr != nil {
		status = deploy.StatusFailed
		log.Warn("deployment job failed",
			slog.String("job", job.ID),
			slog.String("code", res.ErrorCode),
			slog.String("err", execErr.Error()),
			slog.Bool("rolled_back", res.RolledBack))
	} else {
		log.Info("deployment job succeeded",
			slog.String("job", job.ID),
			slog.String("root", res.ResolvedRootPath))
	}
	reportResult(ctx, log, client, job.ID, status, res)
}

func loadJobRoots(configPath string) ([]agentinstall.AllowedRoot, error) {
	cfg, err := agentcfg.Load(configPath)
	if err != nil {
		return nil, err
	}
	return agentRoots(cfg), nil
}

func runRegisterRootJob(configPath, home string, req deploy.Request) (deploy.Result, error) {
	if req.Target.ToolKey == "" || req.Target.Scope == "" || req.RootPath == "" {
		err := errors.New("register_root requires target.tool_key, target.scope and root_path")
		return deploy.Result{ErrorCode: "bad_request", ErrorMessage: err.Error()}, err
	}
	// Auto-create the root directory so an operator can register an
	// adapter-surfaced candidate before creating it on disk (e.g. an opencode
	// ~/.config/opencode/skills the tool hasn't populated yet). Only the empty
	// root directory is created — never skill content — so this stays out of
	// safefs's container semantics: safefs exposes no mkdir helper and its
	// domain is skill-package writes, not root directories. Expand ~ first so
	// MkdirAll doesn't build a literal "~" directory.
	//
	// created tracks whether THIS call mkdir'd the root, so a registration
	// that Validate later rejects can roll the directory back. Register's
	// Validate (EvalSymlinks + candidate match / isHomeChild) is the authority
	// on whether the path is acceptable; if it rejects a path we just created
	// — a `..` that escapes home, or a symlink that lands outside home — we
	// remove the empty leaf we made so no out-of-policy directory is left
	// behind. (Only the leaf is removed; MkdirAll may also create intermediate
	// dirs inside home, which are harmless. The out-of-home multi-segment
	// `..` case needs a root-running agent plus a malicious authenticated
	// operator and can leave empty intermediate dirs — residual, low risk.)
	abs, err := agentroots.ResolveRootPath(req.RootPath)
	if err != nil {
		return deploy.Result{ErrorCode: "root_path_invalid", ErrorMessage: err.Error()}, err
	}
	created := false
	if info, statErr := os.Stat(abs); statErr == nil {
		if !info.IsDir() {
			err := errors.New("root path exists and is not a directory: " + abs)
			return deploy.Result{ErrorCode: "root_path_invalid", ErrorMessage: err.Error()}, err
		}
	} else if !os.IsNotExist(statErr) {
		return deploy.Result{ErrorCode: "mkdir_failed", ErrorMessage: "stat root: " + statErr.Error()}, statErr
	} else if mkErr := os.MkdirAll(abs, 0o755); mkErr != nil {
		return deploy.Result{ErrorCode: "mkdir_failed", ErrorMessage: "mkdir root: " + mkErr.Error()}, mkErr
	} else {
		created = true
	}
	res, err := agentroots.Register(configPath, agentroots.Spec{
		Tool:  req.Target.ToolKey,
		Scope: req.Target.Scope,
		Path:  req.RootPath,
	},
		agentroots.WithIdempotent(),
		agentroots.WithRemotePolicy(agentroots.RemotePolicy{HomeDir: home, AllowHomeChild: true}),
	)
	if err != nil {
		// Roll back an empty root we created but failed to register, so a
		// rejected out-of-policy path (../escape or symlink outside home)
		// leaves no directory behind on disk.
		if created {
			_ = os.Remove(abs)
		}
		code := agentroots.ErrorCode(err, "root_register_failed")
		return deploy.Result{ErrorCode: code, ErrorMessage: err.Error()}, err
	}
	return deploy.Result{ResolvedRootPath: res.Root.Path}, nil
}

func runRemoveRootJob(configPath string, req deploy.Request) (deploy.Result, error) {
	if req.Target.RootID == "" {
		err := errors.New("remove_root requires target.root_id")
		return deploy.Result{ErrorCode: "bad_request", ErrorMessage: err.Error()}, err
	}
	removed, err := agentroots.Remove(configPath, req.Target.RootID)
	if err != nil {
		code := agentroots.ErrorCode(err, "root_remove_failed")
		return deploy.Result{ErrorCode: code, ErrorMessage: err.Error()}, err
	}
	return deploy.Result{ResolvedRootPath: removed.Path}, nil
}

// reportResult marshals the Result and POSTs it. A report failure is
// logged but not retried here (the next poll won't re-claim a job that's
// already past pending; a stuck job is visible server-side).
func reportResult(ctx context.Context, log *slog.Logger, client *agentclient.Client, jobID string, status deploy.Status, res deploy.Result) {
	raw, err := json.Marshal(res)
	if err != nil {
		log.Error("marshal result", slog.String("job", jobID), slog.String("err", err.Error()))
		return
	}
	rptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := client.JobResult(rptCtx, jobID, deploy.JobResult{
		Status:     string(status),
		ResultJSON: string(raw),
	}); err != nil {
		log.Warn("report result failed", slog.String("job", jobID), slog.String("err", err.Error()))
	}
}
