// agentinstall — rollback.go: execute a manual rollback job by restoring
// a prior install's backup (v1.0 §14.1 POST /api/deployments/:id/rollback).
//
// The automatic, in-line rollback that fires when an install's rescan
// fails lives in executor.go (Executor.undo): it has the live Swap and
// BackupRef in hand. THIS file handles the separate, later operator
// action "undo deployment job J", where the only durable record of the
// prior state is the backup directory on disk. The server builds a
// rollback job referencing the original job's backup_path +
// resolved root + skill; the agent reconstructs a BackupRef from that
// directory and restores it through the same contained *os.Root path.
package agentinstall

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/safefs"
)

// ErrBackupMissing is returned when a rollback references a backup
// directory that no longer exists (GC'd, or the original install never
// recorded one). The rollback then fails honestly rather than guessing.
var ErrBackupMissing = errors.New("agentinstall: rollback backup directory missing")

// Rollback restores the skill directory to the contents of the spec's
// backup. It resolves + opens the target root (refusing anything outside
// the allowed set), rebuilds a BackupRef by listing the backup dir, and
// calls safefs.RestoreBackup. The Result records the restored root path
// and whether it rolled back (always true on success here). The input is
// deploy.RollbackPlan — the plan_json the server wrote for this job.
func (x *Executor) Rollback(spec deploy.RollbackPlan) (deploy.Result, error) {
	start := x.now()
	res := deploy.Result{RolledBack: true}

	resolved, rerr := ResolveTarget(x.cfg.AllowedRoots, spec.Target)
	if rerr != nil {
		return finish(res, start, x.now(), ierr(codeRoot, rerr))
	}
	res.ResolvedRootPath = filepath.Join(resolved.Path, spec.SkillName)

	root, oerr := safefs.OpenAllowedRoot(resolved.Path)
	if oerr != nil {
		return finish(res, start, x.now(), ierr(codeRoot, oerr))
	}
	defer func() { _ = root.Close() }()

	var ref safefs.BackupRef
	if spec.BackupWasEmpty {
		ref = safefs.BackupRef{Empty: true}
	} else {
		built, berr := rebuildBackupRef(spec.BackupDir)
		if berr != nil {
			return finish(res, start, x.now(), ierr(codeBackup, berr))
		}
		ref = built
	}

	if err := safefs.RestoreBackup(root, spec.SkillName, ref); err != nil {
		return finish(res, start, x.now(), ierr(codeSwap, err))
	}
	res.BackupPath = spec.BackupDir
	res.FilesWritten = ref.Files
	return finish(res, start, x.now(), nil)
}

// rebuildBackupRef reconstructs a BackupRef from a backup directory on
// disk: it lists the regular files (recording the marker separately) so
// RestoreBackup can copy them back. The directory must exist.
func rebuildBackupRef(backupDir string) (safefs.BackupRef, error) {
	if backupDir == "" {
		return safefs.BackupRef{}, ErrBackupMissing
	}
	info, err := os.Stat(backupDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return safefs.BackupRef{}, fmt.Errorf("%w: %s", ErrBackupMissing, backupDir)
		}
		return safefs.BackupRef{}, fmt.Errorf("stat backup: %w", err)
	}
	if !info.IsDir() {
		return safefs.BackupRef{}, fmt.Errorf("%w: not a directory: %s", ErrBackupMissing, backupDir)
	}

	ref := safefs.BackupRef{DestAbs: backupDir}
	err = filepath.WalkDir(backupDir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, rerr := filepath.Rel(backupDir, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if rel == safefs.MarkerName {
			data, derr := os.ReadFile(p)
			if derr != nil {
				return derr
			}
			ref.MarkerJSON = data
			return nil
		}
		ref.Files = append(ref.Files, rel)
		return nil
	})
	if err != nil {
		return safefs.BackupRef{}, fmt.Errorf("list backup: %w", err)
	}
	sort.Strings(ref.Files)
	if len(ref.Files) == 0 && len(ref.MarkerJSON) == 0 {
		// An empty backup dir means the original install backed up
		// nothing (first install) → restoring it uninstalls.
		ref.Empty = true
	}
	return ref, nil
}
