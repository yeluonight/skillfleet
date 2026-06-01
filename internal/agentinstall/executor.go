// agentinstall — executor.go: the §9.3 install state machine, run on the
// agent against a deploy.Plan. It is the only writer of a managed skill
// directory and the place every safefs primitive comes together.
//
// The flow (v1.0 §9.3), with the failure handling that makes it safe:
//
//  1. (job already claimed + device-checked by the caller)
//     2-3. caller verified device + expiry before constructing us
//     4-5. DownloadVerified: fetch archive, verify sha256, bound size
//  6. skill.Unpack archive → scratch dir (each entry path-guarded)
//  7. validate SKILL.md parses (a package without a usable SKILL.md
//     is refused before it can replace a good install)
//  8. verify the unpacked tree matches plan.Files (count + per-file
//     sha), so the bytes we install are exactly what was planned
//  9. OpenAllowedRoot(target abs) — contained handle (caller resolved
//     the abs path from the allowed set)
//  10. BackupInstall: persistent copy of the current install (rollback
//     source); Reconcile: carry unmanaged extras into staging so the
//     swap doesn't drop them
//  11. StageFiles into a root-internal staging dir; BeginSwap renames it
//     into place (old dir set aside)
//     write the install marker
//  12. rescan the live dir; if its content sha != plan.ContentSHA256,
//     Swap.Rollback AND RestoreBackup, then fail — an install that
//     didn't land exactly as planned is undone, never left half-applied
//  13. Swap.Commit; return the Result the agent reports
//
// Any error before the swap leaves the live install untouched. Any error
// after the swap triggers the rollback path. The Result always reflects
// what actually happened on disk.
package agentinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/fingerprint"
	"github.com/yeluonight/skillfleet/internal/safefs"
)

// Error codes reported in Result.ErrorCode so the WebUI can classify a
// failure without parsing the message.
const (
	codeDownload = "download_failed"
	codeUnpack   = "unpack_failed"
	codeSkillMD  = "skill_md_invalid"
	codeManifest = "manifest_mismatch"
	codeRoot     = "root_not_allowed"
	codeBackup   = "backup_failed"
	codeStage    = "stage_failed"
	codeSwap     = "swap_failed"
	codeRescan   = "rescan_mismatch"
	codeInternal = "internal_error"
)

// Config carries the host-side inputs an Executor needs that aren't in
// the plan: where to put backups, and the agent's allowed roots.
type Config struct {
	// BackupsDir is the agent-owned directory under which per-job backups
	// are written (e.g. ~/.skillfleet/agent/backups). Created on demand.
	BackupsDir string
	// AllowedRoots is the agent's configured install destinations.
	AllowedRoots []AllowedRoot
}

// Executor runs install jobs. It is constructed per run with the host
// config and a package fetcher (the agent client).
type Executor struct {
	cfg     Config
	fetcher PackageFetcher
	now     func() time.Time
}

// NewExecutor returns an Executor. now is injected for deterministic
// timestamps in tests; nil falls back to time.Now.
func NewExecutor(cfg Config, fetcher PackageFetcher, now func() time.Time) *Executor {
	if now == nil {
		now = time.Now
	}
	return &Executor{cfg: cfg, fetcher: fetcher, now: now}
}

// installError carries an error code alongside the error so Install can
// build a Result with the right classification.
type installError struct {
	code string
	err  error
}

func (e *installError) Error() string { return e.err.Error() }
func (e *installError) Unwrap() error { return e.err }

func ierr(code string, err error) *installError { return &installError{code: code, err: err} }

// Install executes an install plan against the resolved target and
// returns a Result describing what happened. The target comes from the
// job's request (the server addresses a destination by {tool_key, scope,
// root_id}; the agent resolves it here). A non-nil error is returned
// only for an actual failure; the Result is always populated (so the
// caller reports it whether the install succeeded or failed). On a
// post-swap failure the live install is rolled back to its backup and
// Result.RolledBack is true.
func (x *Executor) Install(ctx context.Context, plan deploy.Plan, target deploy.Target) (deploy.Result, error) {
	start := x.now()
	res := deploy.Result{}

	// 9. Resolve + open the target root (caller passes the allowed set).
	resolved, rerr := ResolveTarget(x.cfg.AllowedRoots, target)
	if rerr != nil {
		return finish(res, start, x.now(), ierr(codeRoot, rerr))
	}
	res.ResolvedRootPath = filepath.Join(resolved.Path, plan.SkillName)

	root, oerr := safefs.OpenAllowedRoot(resolved.Path)
	if oerr != nil {
		return finish(res, start, x.now(), ierr(codeRoot, oerr))
	}
	defer func() { _ = root.Close() }()

	// 4-5. Download + verify the archive.
	archivePath, derr := DownloadVerified(ctx, x.fetcher, plan)
	if derr != nil {
		return finish(res, start, x.now(), ierr(codeDownload, derr))
	}
	defer func() { _ = os.Remove(archivePath) }()

	// 6. Unpack into a scratch dir (outside the root; we re-stage from it).
	scratch, serr := os.MkdirTemp("", "skillfleet-unpack-*")
	if serr != nil {
		return finish(res, start, x.now(), ierr(codeInternal, serr))
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	if uerr := unpackArchive(archivePath, scratch); uerr != nil {
		return finish(res, start, x.now(), ierr(codeUnpack, uerr))
	}

	// 7. Validate SKILL.md parses.
	if verr := validateSkillMD(scratch); verr != nil {
		return finish(res, start, x.now(), ierr(codeSkillMD, verr))
	}

	// 8. Verify the unpacked tree matches the plan's file list.
	staged, verr := loadAndVerify(scratch, plan.Files)
	if verr != nil {
		return finish(res, start, x.now(), ierr(codeManifest, verr))
	}

	skillRel := plan.SkillName

	// 10. Backup the current install (rollback source) + reconcile extras.
	backupDir := filepath.Join(x.cfg.BackupsDir, jobBackupName(plan))
	backupRef, berr := safefs.BackupInstall(root, skillRel, backupDir)
	if berr != nil {
		return finish(res, start, x.now(), ierr(codeBackup, berr))
	}
	res.BackupPath = backupDir

	oldMarkerFiles := backupMarkerFiles(backupRef)
	newFiles := planFilePaths(plan.Files)
	extras, stale, recErr := safefs.Reconcile(root, skillRel, oldMarkerFiles, newFiles)
	if recErr != nil {
		return finish(res, start, x.now(), ierr(codeInternal, recErr))
	}
	res.ExtraFiles = extras
	res.FilesDeleted = stale

	// 11. Stage the new files + carried extras, then swap into place.
	stagingRel, cleanup, cerr := safefs.CreateStaging(root)
	if cerr != nil {
		return finish(res, start, x.now(), ierr(codeStage, cerr))
	}
	stageOK := false
	defer func() {
		// If we never committed a swap, remove the staging dir.
		if !stageOK {
			_ = cleanup()
		}
	}()

	if serr := safefs.StageFiles(root, stagingRel, staged); serr != nil {
		return finish(res, start, x.now(), ierr(codeStage, serr))
	}
	// Write the install marker into staging (it becomes the live marker
	// after the swap). The marker records the managed file set. Extras are
	// NOT staged here: they are reapplied after the rescan (see below), so
	// the rescan hashes exactly the planned managed tree.
	if merr := writeMarker(root, stagingRel, plan, newFiles, x.now()); merr != nil {
		return finish(res, start, x.now(), ierr(codeStage, merr))
	}

	swap, swerr := safefs.BeginSwap(root, skillRel, stagingRel)
	if swerr != nil {
		return finish(res, start, x.now(), ierr(codeSwap, swerr))
	}

	// 12. Rescan the live dir; the content sha must match the plan. The
	// marker + staging dirs are hidden, so the rescan hashes only the
	// managed skill content — and since extras aren't staged yet, the
	// live tree at this instant is exactly the planned managed tree. A
	// mismatch means the install didn't land as planned — roll the swap
	// back AND restore the backup, then fail.
	liveAbs := filepath.Join(resolved.Path, skillRel)
	fp, ferr := fingerprint.Compute(liveAbs)
	if ferr != nil {
		x.undo(swap, root, skillRel, backupRef, &res)
		return finish(res, start, x.now(), ierr(codeRescan, ferr))
	}
	res.RescanContentSHA256 = fp.Hash
	if fp.Hash != plan.ContentSHA256 {
		x.undo(swap, root, skillRel, backupRef, &res)
		return finish(res, start, x.now(),
			ierr(codeRescan, fmt.Errorf("%w: got %s want %s", errRescanMismatch, fp.Hash, plan.ContentSHA256)))
	}

	// 12b. Reapply unmanaged extras into the now-live directory from the
	// backup, so a user's hand-added files survive the replace (§9.4). We
	// do this AFTER the rescan so they don't perturb the content-sha
	// check, and from the backup (the authoritative copy of the prior
	// tree). A failure here is non-fatal to the install itself — the
	// managed content is correct and committed — but is surfaced.
	if rerr := reapplyExtras(root, skillRel, backupRef.DestAbs, extras); rerr != nil {
		x.undo(swap, root, skillRel, backupRef, &res)
		return finish(res, start, x.now(), ierr(codeStage, rerr))
	}

	// 13. Commit: the swap sticks, old bytes discarded (backup remains).
	if cerr := swap.Commit(); cerr != nil {
		// Commit only cleans up the aside dir; a failure here means the
		// install is live but the old dir lingers. Surface it, don't roll
		// back (the new install is correct).
		res.FilesWritten = newFiles
		return finish(res, start, x.now(), ierr(codeSwap, cerr))
	}
	stageOK = true
	res.FilesWritten = newFiles
	return finish(res, start, x.now(), nil)
}

var errRescanMismatch = errors.New("agentinstall: post-install content sha mismatch")

// undo reverses a completed swap, recording RolledBack on the result.
// The swap's rename-dance is the primary restore: Rollback moves the new
// tree out of the live name and the set-aside old dir back into it,
// reinstating the exact prior directory. The persistent backup is the
// FALLBACK: only if the rename-rollback fails (a torn state) do we
// rebuild the prior tree from the backup copy. Restoring from backup
// unconditionally would be wrong — it would RemoveAll the just-restored
// prior dir and rewrite it, losing anything the backup didn't capture
// (e.g. a symlink) for no benefit. Best-effort: errors are swallowed
// since the caller already has the primary (rescan) failure to report.
func (x *Executor) undo(swap *safefs.Swap, root *os.Root, skillRel string, backupRef safefs.BackupRef, res *deploy.Result) {
	res.RolledBack = true
	if err := swap.Rollback(); err != nil {
		// Rename-rollback failed → fall back to the persistent backup,
		// which also covers the first-install case (restore to "empty").
		_ = safefs.RestoreBackup(root, skillRel, backupRef)
	}
}

// finish stamps the duration and maps an installError onto the result's
// error fields, returning (result, error). A nil ierr is success.
func finish(res deploy.Result, start, end time.Time, ie *installError) (deploy.Result, error) {
	res.DurationMS = end.Sub(start).Milliseconds()
	if ie == nil {
		return res, nil
	}
	res.ErrorCode = ie.code
	res.ErrorMessage = ie.err.Error()
	sort.Strings(res.FilesWritten)
	return res, ie
}
