// safefs — manifest_delete.go: the §9.4 guarantee that SkillFleet only
// ever deletes files it installed.
//
// The install marker (.skillfleet-install.json) records the exact set of
// files a managed install owns. Two operations build on that record:
//
//   - Reconcile classifies what's currently on disk in the skill
//     directory against the OLD marker's file set and the NEW manifest's
//     file set, yielding:
//
//   - carryExtras — files on disk that neither the old install nor
//     the new version claims (a user's hand-added note.txt). These
//     are NOT ours to delete; the executor copies them into staging
//     so they survive the rename-swap, and reports them as
//     extra_files (§9.4: surface, don't auto-remove).
//
//   - dropStale — files the old install owned that the new version
//     drops. With the rename-swap model these vanish by omission
//     (they simply aren't staged), so Reconcile only NAMES them for
//     the result; it does not delete them itself.
//
//   - DeleteManaged performs an explicit, defensive deletion of a given
//     managed file set (used by in-place paths). Its load-bearing
//     invariant: it refuses to delete any path not in the supplied
//     managed set, and before each unlink it Lstats to confirm a regular
//     file and never follows a symlink out of the tree.
//
// All filesystem access is through *os.Root, so traversal and unlink are
// confined to the root no matter what symlinks the directory contains.
package safefs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
)

// ErrDeleteUnmanaged is returned by DeleteManaged if asked (via a bug) to
// remove a path outside the managed set. It should never surface in
// normal operation; it exists so a logic error fails loudly instead of
// deleting user data.
var ErrDeleteUnmanaged = errors.New("safefs: refusing to delete unmanaged path")

// listSkillFiles walks the skill directory at skillRel under root and
// returns the skill-relative paths of every regular file, EXCLUDING the
// install marker and any symlink (symlinks are reported separately so a
// caller can decide; here they are simply not treated as managed regular
// files). A missing directory yields an empty list (a first-time
// install). Returned paths are forward-slash and relative to skillRel,
// so they compare directly against marker / manifest file lists.
func listSkillFiles(root *os.Root, skillRel string) ([]string, error) {
	fsys := root.FS()
	// os.Root.FS() rejects a leading "/" and uses forward slashes; an
	// empty skillRel means the root itself.
	walkRoot := skillRel
	if walkRoot == "" {
		walkRoot = "."
	}

	info, err := fs.Stat(fsys, walkRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // first install: nothing on disk yet
		}
		return nil, fmt.Errorf("safefs: stat skill dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("safefs: skill path is not a directory: %s", skillRel)
	}

	var out []string
	err = fs.WalkDir(fsys, walkRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip hidden directories entirely (mirrors fingerprint), which
			// also skips any ".skillfleet-*" staging/marker dirs.
			if p != walkRoot && HasHiddenSegment(p) {
				return fs.SkipDir
			}
			return nil
		}
		// Regular files only: a symlink has d.Type()&ModeSymlink set and is
		// NOT a managed regular file — never delete or carry it as content.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		// Strip the skillRel prefix to get a skill-relative path.
		rel := p
		if walkRoot != "." {
			rel = p[len(walkRoot)+1:] // +1 for the "/"
		}
		if HasHiddenSegment(rel) { // marker + any dotfile
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("safefs: walk skill dir: %w", err)
	}
	sort.Strings(out)
	return out, nil
}

// HasHiddenSegment reports whether any segment of rel starts with ".".
// Matches fingerprint.shouldSkip, so the set of files Reconcile sees is
// exactly the set a rescan hashes (the marker and staging dirs
// excluded). Exported so the agent installer can apply the identical
// skip rule when enumerating an unpacked package.
func HasHiddenSegment(rel string) bool {
	for rel != "." && rel != "" {
		base := path.Base(rel)
		if len(base) > 0 && base[0] == '.' {
			return true
		}
		parent := path.Dir(rel)
		if parent == rel {
			break
		}
		rel = parent
	}
	return false
}

// Reconcile classifies the current on-disk skill directory against the
// old install's managed file set and the new version's file set.
// carryExtras are unmanaged files to preserve (copy into staging);
// dropStale are old-managed files the new version omits (they vanish via
// the swap, named here only for reporting). Both results are sorted.
//
// oldMarkerFiles is the prior install marker's Files (empty for a
// first-time install); newManifestFiles is the incoming version's file
// list. Paths are skill-relative forward-slash on all three sides.
func Reconcile(root *os.Root, skillRel string, oldMarkerFiles, newManifestFiles []string) (carryExtras, dropStale []string, err error) {
	onDisk, err := listSkillFiles(root, skillRel)
	if err != nil {
		return nil, nil, err
	}
	oldSet := toSet(oldMarkerFiles)
	newSet := toSet(newManifestFiles)
	diskSet := toSet(onDisk)

	// Extras: on disk but claimed by neither old nor new. Not ours.
	for _, f := range onDisk {
		if !oldSet[f] && !newSet[f] {
			carryExtras = append(carryExtras, f)
		}
	}
	// Stale: old install owned it, new version drops it, and it is
	// actually present (so we don't report a phantom).
	for _, f := range oldMarkerFiles {
		if !newSet[f] && diskSet[f] {
			dropStale = append(dropStale, f)
		}
	}
	sort.Strings(carryExtras)
	sort.Strings(dropStale)
	return carryExtras, dropStale, nil
}

// DeleteManaged removes exactly the files in managed from the skill
// directory at skillRel, returning the paths it deleted and the paths it
// kept (present in managed but absent/ineligible on disk). It NEVER
// deletes a path not in managed, and before each unlink it Lstats the
// target: a symlink or non-regular file in a managed slot is kept (not
// followed/unlinked) and recorded in kept, so a tampered tree cannot
// trick us into removing something through a link.
//
// This is the explicit-deletion path; the normal install relies on the
// rename-swap dropping stale files by omission, so DeleteManaged is used
// by in-place flows. managed paths are skill-relative.
func DeleteManaged(root *os.Root, skillRel string, managed []string) (deleted, kept []string, err error) {
	managedSet := toSet(managed)
	for _, m := range managed {
		clean, cerr := CleanPackagePath(m)
		if cerr != nil {
			// A marker file list should always be clean; a bad entry is
			// kept (never deleted) and surfaced via error.
			return deleted, kept, fmt.Errorf("safefs: managed path %q: %w", m, cerr)
		}
		// Belt and suspenders: the cleaned path must still be in the
		// managed set (CleanPackagePath is idempotent on clean input, so
		// this only trips on a bug).
		if !managedSet[clean] {
			return deleted, kept, fmt.Errorf("%w: %s", ErrDeleteUnmanaged, clean)
		}

		full := joinSkill(skillRel, clean)
		info, lerr := root.Lstat(full)
		if lerr != nil {
			if errors.Is(lerr, fs.ErrNotExist) {
				kept = append(kept, clean) // already gone; nothing to do
				continue
			}
			return deleted, kept, fmt.Errorf("safefs: lstat %q: %w", clean, lerr)
		}
		// Only unlink plain regular files. A symlink (ModeSymlink) or any
		// special file is left in place — we will not follow a link out of
		// the tree, and we will not remove something we didn't write.
		if !info.Mode().IsRegular() {
			kept = append(kept, clean)
			continue
		}
		if rerr := root.Remove(full); rerr != nil {
			return deleted, kept, fmt.Errorf("safefs: remove %q: %w", clean, rerr)
		}
		deleted = append(deleted, clean)
	}
	sort.Strings(deleted)
	sort.Strings(kept)
	return deleted, kept, nil
}

// toSet builds a lookup set from a path slice.
func toSet(paths []string) map[string]bool {
	m := make(map[string]bool, len(paths))
	for _, p := range paths {
		m[p] = true
	}
	return m
}

// joinSkill joins a skill-relative path under skillRel (or returns it
// as-is when skillRel is empty/root).
func joinSkill(skillRel, rel string) string {
	if skillRel == "" || skillRel == "." {
		return rel
	}
	return skillRel + "/" + rel
}
