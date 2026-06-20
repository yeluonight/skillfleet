// agentinstall — executor_helpers.go: the pieces the install state
// machine in executor.go calls out to (unpack, validate, verify, carry
// extras, write marker), kept here so executor.go reads as the flow.
package agentinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"

	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/safefs"
	"github.com/yeluonight/skillfleet/internal/skill"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

// Errors surfaced by the helpers.
var (
	errNoSkillMD   = errors.New("agentinstall: package has no SKILL.md")
	errFileCount   = errors.New("agentinstall: unpacked file count != plan")
	errFileMissing = errors.New("agentinstall: planned file missing from package")
	errFileSHA     = errors.New("agentinstall: unpacked file sha != plan")
	errExtraInPkg  = errors.New("agentinstall: package contains a file not in the plan")
)

// unpackArchive opens the verified archive file and unpacks it into
// destRoot via skill.Unpack (which path-guards every entry, rejects
// symlinks, and bounds count/size).
func unpackArchive(archivePath, destRoot string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := skill.Unpack(f, destRoot); err != nil {
		return err
	}
	return nil
}

// validateSkillMD confirms the unpacked package has a SKILL.md that
// parses. A package whose SKILL.md is missing or unparseable is refused
// before it can replace a working install (§9.3 step 7). We treat a
// parse that returns a Result as success (warnings are non-fatal); only
// a hard parse error or a missing file fails.
func validateSkillMD(scratch string) error {
	path := filepath.Join(scratch, "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errNoSkillMD
		}
		return fmt.Errorf("stat SKILL.md: %w", err)
	}
	if _, err := skillmd.ParseFile(path); err != nil {
		return fmt.Errorf("parse SKILL.md: %w", err)
	}
	return nil
}

// loadAndVerify reads every planned file from the unpacked scratch dir,
// checks its sha matches the plan, and returns the StagedFiles ready to
// write into the root. It also rejects a package that carries a file NOT
// in the plan (the archive and the plan must agree exactly), so a
// tampered archive can't smuggle extra content past the manifest check.
func loadAndVerify(scratch string, planFiles []deploy.FileSpec) ([]safefs.StagedFile, error) {
	want := make(map[string]deploy.FileSpec, len(planFiles))
	for _, f := range planFiles {
		want[f.Path] = f
	}

	// Enumerate what the archive actually produced (skip hidden, mirror
	// fingerprint) so we can detect extras-in-package.
	gotPaths, err := listScratchFiles(scratch)
	if err != nil {
		return nil, err
	}
	if len(gotPaths) != len(planFiles) {
		return nil, fmt.Errorf("%w: got %d, want %d", errFileCount, len(gotPaths), len(planFiles))
	}

	staged := make([]safefs.StagedFile, 0, len(planFiles))
	for _, rel := range gotPaths {
		spec, ok := want[rel]
		if !ok {
			return nil, fmt.Errorf("%w: %s", errExtraInPkg, rel)
		}
		full := filepath.Join(scratch, filepath.FromSlash(rel))
		data, rerr := os.ReadFile(full)
		if rerr != nil {
			return nil, fmt.Errorf("read unpacked %q: %w", rel, rerr)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != spec.SHA256 {
			return nil, fmt.Errorf("%w: %s", errFileSHA, rel)
		}
		staged = append(staged, safefs.StagedFile{Path: rel, Content: data, Exec: spec.Exec})
	}
	// Every planned file must have been present.
	if len(staged) != len(planFiles) {
		return nil, fmt.Errorf("%w", errFileMissing)
	}
	return staged, nil
}

// listScratchFiles returns the slash-relative regular-file paths under
// scratch, skipping hidden segments (matching fingerprint's view, which
// is what the plan's file list reflects).
func listScratchFiles(scratch string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(scratch, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(scratch, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if safefs.HasHiddenSegment(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk unpacked: %w", err)
	}
	sort.Strings(out)
	return out, nil
}

// reapplyExtras copies each unmanaged extra file from the backup (the
// authoritative copy of the prior on-disk tree) into the now-live skill
// directory, so a user's hand-added files survive the replace (§9.4).
// It runs AFTER the post-install rescan, so extras never perturb the
// content-sha check, and writes through the same *os.Root (contained).
// An extra absent from the backup (it was a symlink, skipped) is
// silently ignored — we never fabricate content we didn't capture.
func reapplyExtras(root *os.Root, skillRel, backupDir string, extras []string) error {
	for _, rel := range extras {
		srcAbs := filepath.Join(backupDir, filepath.FromSlash(rel))
		data, err := os.ReadFile(srcAbs)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // not captured (e.g. a symlink extra) — skip
			}
			return fmt.Errorf("read backed-up extra %q: %w", rel, err)
		}
		exec := false
		if fi, serr := os.Stat(srcAbs); serr == nil && fi.Mode()&0o100 != 0 {
			exec = true
		}
		dstRel := joinUnderSkill(skillRel, rel)
		if dir := pathDir(dstRel); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("reapply extra mkdir %q: %w", dir, err)
			}
		}
		mode := os.FileMode(0o644)
		if exec {
			mode = 0o755
		}
		fh, err := root.OpenFile(dstRel, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return fmt.Errorf("reapply extra create %q: %w", rel, err)
		}
		_, werr := fh.Write(data)
		cerr := fh.Close()
		if werr != nil {
			return fmt.Errorf("reapply extra write %q: %w", rel, werr)
		}
		if cerr != nil {
			return fmt.Errorf("reapply extra close %q: %w", rel, cerr)
		}
	}
	return nil
}

// writeMarker writes the install marker into the staging dir so it
// becomes the live marker after the swap. The marker records the managed
// file set (the plan's files), the version provenance, and the install
// time. Its name is hidden so the rescan ignores it.
func writeMarker(root *os.Root, stagingRel string, plan deploy.Plan, managedFiles []string, now time.Time) error {
	marker := plan.Marker
	marker.Files = append([]string(nil), managedFiles...)
	sort.Strings(marker.Files)
	marker.InstalledAt = now.UTC().Format(time.RFC3339)
	if marker.ManagedBy == "" {
		marker.ManagedBy = "skillfleet"
	}

	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal marker: %w", err)
	}
	// Write the marker directly at the staging root (not via StageFiles,
	// which forbids the leading-dot name through CleanPackagePath).
	markerRel := stagingRel + "/" + safefs.MarkerName
	fh, err := root.OpenFile(markerRel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create marker: %w", err)
	}
	_, werr := fh.Write(data)
	cerr := fh.Close()
	if werr != nil {
		return fmt.Errorf("write marker: %w", werr)
	}
	return cerr
}

// jobBackupName derives a per-install backup directory name from the
// plan. Using the version id keeps it stable + readable; the caller
// joins it under the agent's backups root.
func jobBackupName(plan deploy.Plan) string {
	return plan.SkillName + "-" + plan.VersionID
}

// backupMarkerFiles extracts the prior install's managed file list from
// the backup's captured marker JSON, so Reconcile knows what the old
// install owned. An absent/!unparseable marker yields an empty set (the
// conservative choice: nothing is treated as old-managed, so nothing is
// dropped as stale and every pre-existing file is treated as an extra to
// preserve).
func backupMarkerFiles(ref safefs.BackupRef) []string {
	if len(ref.MarkerJSON) == 0 {
		return nil
	}
	var m deploy.InstallMarker
	if err := json.Unmarshal(ref.MarkerJSON, &m); err != nil {
		return nil
	}
	return m.Files
}

// planFilePaths returns just the paths of a plan's file specs, sorted.
func planFilePaths(files []deploy.FileSpec) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

// joinUnderSkill joins rel under skillRel (or returns rel at the root).
func joinUnderSkill(skillRel, rel string) string {
	if skillRel == "" || skillRel == "." {
		return rel
	}
	return skillRel + "/" + rel
}

// pathDir is path.Dir over a forward-slash path.
func pathDir(p string) string {
	return path.Dir(p)
}
