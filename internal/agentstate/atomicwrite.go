// Package agentstate is the agent-side executor of state-change jobs
// (Phase 9): it flips a skill's native enable/disable state by a safe
// read-modify-write of one tool config file. It is the state-side peer of
// internal/agentinstall (which owns managed skill DIRECTORIES); this
// package owns the small set of out-of-band CONFIG files that govern
// whether a skill is active:
//
//	claude-code  ~/.claude/settings.json        skillOverrides[name]
//	codex        ~/.codex/config.toml           [[skills.config]].enabled
//	opencode     ~/.config/opencode/...json     permission.skill[name]
//
// Security posture (this code edits files that change how the user's
// tools behave, so it is treated as highest-risk like agentinstall):
//
//   - Preserve everything else. Each writer decodes the whole file into a
//     generic map (json) / map (toml), mutates ONLY the one nested key for
//     this skill, and re-encodes. Unrelated settings (permissions, env,
//     hooks, other skills' overrides, other config sections) survive
//     untouched. We never round-trip through a narrow struct that would
//     silently drop unknown fields.
//   - Atomic write. The re-encoded bytes go to a temp file in the SAME
//     directory, fsync'd, then renamed over the original — a crash leaves
//     either the old file or the new file, never a truncated one.
//   - Target is resolved against allowed_roots. Even though config files
//     live OUTSIDE the skill roots, claude-code and codex config paths are
//     derived only after deploy.Target resolves to a configured allowed
//     root (agentinstall.ResolveTarget) — the same refusal gate the
//     installer uses — so a job can't steer a write at an arbitrary path.
//
// atomicwrite.go: the shared crash-safe single-file write primitive.
package agentstate

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteFile writes data to path crash-safely: it creates a temp
// file in path's directory, writes + fsyncs it, then renames it over
// path. A same-directory rename is atomic on POSIX (no cross-device
// copy), so a reader either sees the complete old file or the complete
// new file — never a partial write. perm is applied to the final file.
//
// The temp file is removed on any error before the rename; after a
// successful rename there is nothing to clean up. The parent directory is
// created if missing (a tool whose config has never existed yet).
func atomicWriteFile(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return fmt.Errorf("agentstate: create config dir: %w", mkErr)
	}

	tmp, err := os.CreateTemp(dir, ".skillfleet-state-*.tmp")
	if err != nil {
		return fmt.Errorf("agentstate: create temp: %w", err)
	}
	tmpName := tmp.Name()
	// On any failure past this point, remove the temp file. After a
	// successful rename tmpName no longer exists, so the Remove is a
	// harmless no-op guarded by the success flag.
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("agentstate: write temp: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("agentstate: fsync temp: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("agentstate: close temp: %w", err)
	}
	if err = os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("agentstate: chmod temp: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("agentstate: rename temp over target: %w", err)
	}
	committed = true
	return nil
}
