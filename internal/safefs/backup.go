// safefs — backup.go: a faithful, persistent copy of the current
// install, taken before a replace so any failure can be undone (v1.0
// §9.3 step 10, §13 backup/rollback).
//
// Why a content copy and not just the rename-aside from atomic_replace:
// the aside-dir lives inside the allowed root and is removed on a
// successful commit, so it cannot serve a LATER manual rollback. More
// importantly, only a content copy can restore files the registry never
// had — a user's local edits, or untracked extras — because those exist
// only on the device. The backup is therefore the single source of truth
// for "put it back exactly as it was". It is written OUTSIDE the root
// (under ~/.skillfleet/agent/backups/<jobID>/) so it survives the swap.
//
// Reads go through *os.Root (contained, symlink-safe); the destination
// is an ordinary directory the agent owns. We copy only regular files
// and we never follow a symlink: a symlink in the install dir is
// recorded as skipped, not chased out of the tree.
package safefs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
)

// BackupRef describes a taken backup. Empty is true when the skill
// directory did not exist (a first-time install has nothing to back up,
// and a rollback to "nothing" means "remove the install").
type BackupRef struct {
	DestAbs    string   // absolute path of the backup directory
	Files      []string // skill-relative paths copied (sorted)
	MarkerJSON []byte   // the old .skillfleet-install.json bytes, if present
	Skipped    []string // skill-relative paths skipped (symlinks/special)
	Empty      bool
}

// MaxBackupFileBytes caps any single file copied into a backup, a
// backstop against a pathological install dir.
const MaxBackupFileBytes = 64 << 20 // 64 MiB

// BackupInstall copies the current contents of the skill directory at
// skillRel (under root) into destAbs, an agent-owned directory created
// fresh for this job. It copies every regular file (including the
// install marker, so a restore is faithful), preserves the executable
// bit, and records symlinks/special files as Skipped rather than
// following them. If the skill directory does not exist, it returns a
// BackupRef with Empty=true and creates nothing.
//
// destAbs must not already exist (a per-job backup dir); BackupInstall
// creates it. The caller is responsible for choosing destAbs under the
// agent's backups/ root.
func BackupInstall(root *os.Root, skillRel, destAbs string) (BackupRef, error) {
	fsys := root.FS()
	walkRoot := skillRel
	if walkRoot == "" {
		walkRoot = "."
	}

	info, err := fs.Stat(fsys, walkRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return BackupRef{DestAbs: destAbs, Empty: true}, nil
		}
		return BackupRef{}, fmt.Errorf("safefs: stat for backup: %w", err)
	}
	if !info.IsDir() {
		return BackupRef{}, fmt.Errorf("safefs: backup source not a directory: %s", skillRel)
	}

	if err := os.MkdirAll(destAbs, 0o755); err != nil {
		return BackupRef{}, fmt.Errorf("safefs: mkdir backup: %w", err)
	}

	ref := BackupRef{DestAbs: destAbs}
	err = fs.WalkDir(fsys, walkRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel := p
		if walkRoot != "." {
			rel = p[len(walkRoot)+1:]
		}
		// Symlinks and special files are recorded, never followed/copied.
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			ref.Skipped = append(ref.Skipped, rel)
			return nil
		}

		fi, ierr := d.Info()
		if ierr != nil {
			return fmt.Errorf("safefs: backup info %q: %w", rel, ierr)
		}
		if fi.Size() > MaxBackupFileBytes {
			return fmt.Errorf("safefs: backup file too big: %s (%d)", rel, fi.Size())
		}

		data, rerr := fs.ReadFile(fsys, p)
		if rerr != nil {
			return fmt.Errorf("safefs: backup read %q: %w", rel, rerr)
		}

		destFile := filepath.Join(destAbs, filepath.FromSlash(rel))
		if mkErr := os.MkdirAll(filepath.Dir(destFile), 0o755); mkErr != nil {
			return fmt.Errorf("safefs: backup mkdir %q: %w", rel, mkErr)
		}
		mode := os.FileMode(0o644)
		if fi.Mode()&0o100 != 0 {
			mode = 0o755
		}
		if werr := os.WriteFile(destFile, data, mode); werr != nil {
			return fmt.Errorf("safefs: backup write %q: %w", rel, werr)
		}

		// Capture the marker bytes for a faithful restore record.
		if rel == MarkerName {
			ref.MarkerJSON = data
		} else {
			ref.Files = append(ref.Files, rel)
		}
		return nil
	})
	if err != nil {
		return BackupRef{}, err
	}
	sort.Strings(ref.Files)
	sort.Strings(ref.Skipped)
	return ref, nil
}

// RestoreBackup copies a backup directory's contents back into the skill
// directory at skillRel under root, used by rollback. It first removes
// the current skill directory (so the restore is exact, not a merge),
// then writes every backed-up file through *os.Root. A backup taken with
// Empty=true restores "nothing" — the skill directory is removed and not
// recreated (rolling back a first-time install uninstalls it).
//
// Files are written verbatim with the executable bit inferred from the
// backup file's mode. All writes are contained by os.Root.
func RestoreBackup(root *os.Root, skillRel string, ref BackupRef) error {
	// Clear the current install dir entirely so the restore is faithful.
	target := skillRel
	if target == "" {
		target = "."
	}
	if target != "." {
		if err := root.RemoveAll(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("safefs: restore clear: %w", err)
		}
	}
	if ref.Empty {
		return nil // rolled back to "no install"
	}

	// Recreate and copy each backed-up file (and the marker).
	if target != "." {
		if err := root.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("safefs: restore mkdir: %w", err)
		}
	}

	copyOne := func(rel string) error {
		src := filepath.Join(ref.DestAbs, filepath.FromSlash(rel))
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("safefs: restore read %q: %w", rel, err)
		}
		mode := os.FileMode(0o644)
		if fi, serr := os.Stat(src); serr == nil && fi.Mode()&0o100 != 0 {
			mode = 0o755
		}
		dst := joinSkill(skillRel, rel)
		if dir := path.Dir(dst); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("safefs: restore mkdir %q: %w", dir, err)
			}
		}
		fh, err := root.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return fmt.Errorf("safefs: restore create %q: %w", rel, err)
		}
		_, werr := fh.Write(data)
		cerr := fh.Close()
		if werr != nil {
			return fmt.Errorf("safefs: restore write %q: %w", rel, werr)
		}
		return cerr
	}

	for _, f := range ref.Files {
		if err := copyOne(f); err != nil {
			return err
		}
	}
	if len(ref.MarkerJSON) > 0 {
		if err := copyOne(MarkerName); err != nil {
			return err
		}
	}
	return nil
}
