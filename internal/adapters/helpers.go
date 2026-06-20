// Shared helpers the concrete adapters build on. These encapsulate the
// mechanics common to every tool — path expansion, existence checks,
// and the "each child directory with a SKILL.md is one skill" scan that
// most adapters follow — so the per-tool packages only encode what is
// genuinely tool-specific (root locations + native-state decoding).

package adapters

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yeluonight/skillfleet/internal/fingerprint"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

// SkillFileName is the canonical manifest filename every tool uses.
const SkillFileName = "SKILL.md"

// ProjectRootID builds a stable per-project root id of the form
// "<base>_<index>", so multiple registered projects don't collide.
// Centralised here because every adapter that scans project scope
// needs the same shape.
func ProjectRootID(base string, index int) string {
	return base + "_" + strconv.Itoa(index)
}

// ExpandHome turns a leading "~" into homeDir. Only the leading ~/ form
// is supported (no ~user). An empty homeDir with a ~-prefixed path is
// an error so adapters fail loudly rather than scanning "/".
func ExpandHome(p, homeDir string) (string, error) {
	if p == "" || p[0] != '~' {
		return p, nil
	}
	if len(p) > 1 && p[1] != '/' && p[1] != filepath.Separator {
		return "", fmt.Errorf("adapters: only leading ~/ supported, got %q", p)
	}
	if homeDir == "" {
		return "", fmt.Errorf("adapters: cannot expand %q without a home directory", p)
	}
	if len(p) == 1 {
		return homeDir, nil
	}
	return filepath.Join(homeDir, p[2:]), nil
}

// DirExists reports whether path exists and is a directory. Any stat
// error other than "is a regular file" is treated as "does not exist"
// so a permission-denied root is silently skipped rather than aborting
// a whole device scan.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// ConfigDirExists reports whether a ~-relative config directory exists,
// e.g. ConfigDirExists(home, "~/.claude") for the Claude Code install
// hint. It is the preferred installed-heuristic signal because a tool's
// config dir survives across shells and service contexts, unlike PATH.
// A path that cannot be expanded (no home dir) reports false.
func ConfigDirExists(homeDir, rel string) bool {
	p, err := ExpandHome(rel, homeDir)
	if err != nil {
		return false
	}
	return DirExists(p)
}

// BinaryOnPath reports whether name resolves on the current PATH. It is
// a SECONDARY install hint only: a service-context PATH often differs
// from an interactive shell's, so a false here does not mean "not
// installed". Adapters prefer ConfigDirExists and fall back to this.
func BinaryOnPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ScanStandardRoot implements the common layout: every immediate child
// directory of root that contains a SKILL.md is one skill, named after
// the folder (v1.0 §7.6: folder name is authoritative). The nativeState
// callback lets each adapter decide the per-skill EffectiveState +
// native string; pass nil to default every skill to StateUnknown.
//
// Errors reading a single skill (bad SKILL.md, fingerprint failure)
// become Warnings on that skill, never abort the loop.
func ScanStandardRoot(
	root SkillRoot,
	nativeState func(skillName, skillPath string, md skillmd.Result) (EffectiveState, string),
) ([]DiscoveredSkill, error) {
	entries, err := os.ReadDir(root.Path)
	if err != nil {
		return nil, fmt.Errorf("adapters: read root %s: %w", root.Path, err)
	}

	var out []DiscoveredSkill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Skip hidden directories (.git, .cache, …) — never skills.
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		skillPath := filepath.Join(root.Path, entry.Name())
		ds := buildDiscoveredSkill(root, entry.Name(), skillPath, nativeState)
		out = append(out, ds)
	}
	return out, nil
}

// buildDiscoveredSkill assembles one DiscoveredSkill from a directory,
// degrading gracefully: a missing/unreadable SKILL.md or a fingerprint
// error is recorded as a Warning rather than dropping the skill.
func buildDiscoveredSkill(
	root SkillRoot,
	name, skillPath string,
	nativeState func(string, string, skillmd.Result) (EffectiveState, string),
) DiscoveredSkill {
	ds := DiscoveredSkill{
		Name:           name,
		RootID:         root.ID,
		Path:           skillPath,
		EffectiveState: StateUnknown,
	}

	mdPath := filepath.Join(skillPath, SkillFileName)
	if DirExists(skillPath) {
		if md, err := skillmd.ParseFile(mdPath); err == nil {
			ds.SkillMD = md
			ds.HasSkillMD = true
		} else if errors.Is(err, fs.ErrNotExist) {
			ds.Warnings = append(ds.Warnings, Warning{
				Code:    "no_skill_md",
				Message: "directory has no SKILL.md",
			})
		} else {
			ds.Warnings = append(ds.Warnings, Warning{
				Code:    "skill_md_unreadable",
				Message: err.Error(),
			})
		}
	}

	if fp, err := fingerprint.Compute(skillPath); err == nil {
		ds.ContentSHA256 = fp.Hash
		ds.FileCount = fp.FileCount
		ds.TotalBytes = fp.TotalBytes
		ds.ModifiedAt = fp.ModTime
	} else {
		ds.Warnings = append(ds.Warnings, Warning{
			Code:    "fingerprint_failed",
			Message: err.Error(),
		})
	}

	// Resolve effective + native state once the SKILL.md is parsed so
	// the adapter callback can read frontmatter hints if it needs to.
	if nativeState != nil {
		eff, native := nativeState(name, skillPath, ds.SkillMD)
		ds.EffectiveState = eff
		ds.NativeState = native
	}
	return ds
}
