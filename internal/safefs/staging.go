// safefs — staging.go: open an allowed root as a contained *os.Root and
// build a staging subtree inside it (v1.0 §9.3 steps 6/9, §1.3.1).
//
// Why os.Root (Go 1.24+): a *os.Root resolves every path through openat
// relative to the root directory, so a symlink inside the tree that
// points at /etc, or a "../" smuggled past a higher layer, cannot make a
// write land outside the root. This is kernel-enforced containment, a
// stronger guarantee than string path checks alone — we still run
// CleanPackagePath on every relative path (defence in depth, and to
// reject control chars / absolute / drive forms early), then let os.Root
// enforce the boundary at the syscall.
//
// Why staging lives INSIDE the allowed root (not in ~/.skillfleet/agent/
// staging/): the final install is a rename of the staged tree onto the
// live skill directory, and rename is only atomic within one filesystem.
// A staging dir under the root is guaranteed same-filesystem; a sibling
// of the root might be on a different mount. The staging dir name begins
// with "." so fingerprint.Compute skips it — a half-built staging tree
// never pollutes a rescan, and a crash leaves an obviously-temporary
// ".skillfleet-staging-*" behind.
package safefs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
)

// StagingPrefix is the leading-dot prefix of every staging dir, so
// fingerprint.Compute (which skips any path segment starting with ".")
// ignores it. Exported so tests and the executor can assert/clean it.
const StagingPrefix = ".skillfleet-staging-"

// MaxStageFileBytes caps a single staged file. It mirrors the package
// size limits enforced elsewhere; a plan whose file exceeds it is
// rejected rather than written.
const MaxStageFileBytes = 64 << 20 // 64 MiB

// Errors returned by the staging layer.
var (
	ErrRootNotDir    = errors.New("safefs: allowed root is not a directory")
	ErrStageTooBig   = errors.New("safefs: staged file exceeds size limit")
	ErrStageNotEmpty = errors.New("safefs: staging path already exists")
)

// StagedFile is one file to write into a staging tree. Path is package-
// relative (validated via CleanPackagePath); Exec selects 0755 vs 0644.
// Content is the exact bytes (never transformed — non-UTF-8 stays as-is,
// v1.0 §7.7).
type StagedFile struct {
	Path    string
	Content []byte
	Exec    bool
}

// OpenAllowedRoot opens absPath as a contained *os.Root after confirming
// it exists and is a directory. The caller (agentinstall/roots.go) is
// responsible for having decided absPath is in the agent's allowed set;
// this function only establishes the kernel-level containment handle.
// The returned *os.Root must be Closed by the caller.
func OpenAllowedRoot(absPath string) (*os.Root, error) {
	info, err := os.Lstat(absPath)
	if err != nil {
		return nil, fmt.Errorf("safefs: stat allowed root: %w", err)
	}
	if !info.IsDir() {
		// A symlink or file masquerading as the root is refused: we want a
		// real directory we can openat into, not a link we'd follow out.
		return nil, fmt.Errorf("%w: %s", ErrRootNotDir, absPath)
	}
	root, err := os.OpenRoot(absPath)
	if err != nil {
		return nil, fmt.Errorf("safefs: open root: %w", err)
	}
	return root, nil
}

// CreateStaging makes a fresh, empty staging directory directly under
// root and returns its root-relative name plus a cleanup func that
// removes it (safe to call even after a successful swap, since the swap
// renames the staging dir away — cleanup then no-ops on the missing
// path). The name is unguessable (random suffix) so concurrent installs
// in the same root don't collide.
func CreateStaging(root *os.Root) (stagingRel string, cleanup func() error, err error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, fmt.Errorf("safefs: staging rand: %w", err)
	}
	stagingRel = StagingPrefix + hex.EncodeToString(raw[:])

	// O_EXCL semantics via Mkdir: if the (random) name somehow exists,
	// that's an error, not a silent reuse.
	if err := root.Mkdir(stagingRel, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", nil, fmt.Errorf("%w: %s", ErrStageNotEmpty, stagingRel)
		}
		return "", nil, fmt.Errorf("safefs: mkdir staging: %w", err)
	}

	cleanup = func() error {
		if err := root.RemoveAll(stagingRel); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("safefs: cleanup staging: %w", err)
		}
		return nil
	}
	return stagingRel, cleanup, nil
}

// StageFiles writes files into the staging subtree at stagingRel. Each
// file's Path is validated by CleanPackagePath and joined under the
// staging dir; parent directories are created with root.MkdirAll. Every
// write goes through *os.Root, so even a path that survived CleanPackage
// Path but pointed through a symlink can't escape. Files are written
// with O_CREATE|O_EXCL so a duplicate path in the plan is an error, not
// a silent overwrite. Exec bit selects 0755 vs 0644 (matching the
// archive layer's permission model). Content is written verbatim.
func StageFiles(root *os.Root, stagingRel string, files []StagedFile) error {
	for _, f := range files {
		clean, err := CleanPackagePath(f.Path)
		if err != nil {
			return fmt.Errorf("safefs: stage %q: %w", f.Path, err)
		}
		if int64(len(f.Content)) > MaxStageFileBytes {
			return fmt.Errorf("%w: %s (%d bytes)", ErrStageTooBig, clean, len(f.Content))
		}

		full := stagingRel + "/" + clean
		if dir := path.Dir(full); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("safefs: stage mkdir %q: %w", dir, err)
			}
		}
		mode := os.FileMode(0o644)
		if f.Exec {
			mode = 0o755
		}
		fh, err := root.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return fmt.Errorf("safefs: stage create %q: %w", clean, err)
		}
		_, werr := fh.Write(f.Content)
		cerr := fh.Close()
		if werr != nil {
			return fmt.Errorf("safefs: stage write %q: %w", clean, werr)
		}
		if cerr != nil {
			return fmt.Errorf("safefs: stage close %q: %w", clean, cerr)
		}
	}
	return nil
}
