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
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/yeluonight/skillfleet/internal/agentcfg"
	"github.com/yeluonight/skillfleet/internal/agentclient"
	"github.com/yeluonight/skillfleet/internal/agentinstall"
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
func runJobs(ctx context.Context, log *slog.Logger, client *agentclient.Client, interval time.Duration, cfg agentcfg.Config) {
	roots := agentRoots(cfg)
	exec := agentinstall.NewExecutor(agentinstall.Config{
		BackupsDir:   backupsDir(),
		AllowedRoots: roots,
	}, fetcherAdapter{client}, time.Now)

	// stateWriter executes state-change jobs (Phase 9): it edits the
	// tool's out-of-band config to enable/disable a skill. It shares the
	// same allowed roots as the installer (its target-resolution gate) and
	// needs the home dir for the per-user codex/opencode config paths.
	home, err := os.UserHomeDir()
	if err != nil {
		// A missing home is non-fatal here: state-change jobs will fail to
		// resolve per-user config paths and report it; install/rollback are
		// unaffected. Log once.
		log.Warn("could not resolve home dir; state-change jobs may fail",
			slog.String("err", err.Error()))
	}
	stateWriter := agentstate.NewWriter(roots, home)

	poll := func() {
		// Claim one job; if none, quietly return.
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
		runOneJob(ctx, log, client, exec, stateWriter, job)
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
func runOneJob(ctx context.Context, log *slog.Logger, client *agentclient.Client, exec *agentinstall.Executor, stateWriter *agentstate.Writer, job deploy.ClaimedJob) {
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
