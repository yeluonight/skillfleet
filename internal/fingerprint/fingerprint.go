// Package fingerprint computes a stable, content-addressed digest of a
// skill directory (v1.0 §8.2 "content_sha256"). The Agent uses it to
// detect local modifications by comparing the live tree hash against
// the value recorded at install time.
//
// Properties:
//
//   - Stable across operating systems: file order is sorted by the
//     forward-slash relative path, mode bits are limited to the
//     execute permission (the only thing meaningful across FS types).
//   - Cheap: one sha256 pass per file plus one rollup hash. No
//     content is loaded into memory beyond a 64 KiB streaming buffer.
//   - Defensive: caps individual file size (10 MiB) and total bytes
//     (100 MiB) so a malicious symlink loop or a stray DB dump can't
//     blow up memory.
//   - Skips: symlinks (followed only at the root entry), hidden files
//     (any path component starting with "."), the runtime marker
//     ".skillfleet/" subtree (v1.0 §9.2 managed-by metadata is
//     ours, not the skill's content).
//
// The output is deterministic: identical inputs produce byte-identical
// Fingerprint.Hash regardless of when or where it was computed.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Limits the scanner enforces. Adjustable for tests; the defaults
// are public so adapters can detect when they're about to bump
// against them and emit a friendlier warning.
const (
	MaxFileBytes  = 10 << 20  // 10 MiB per file
	MaxTotalBytes = 100 << 20 // 100 MiB per tree
	ReadChunk     = 64 << 10  // 64 KiB streaming buffer
)

// Errors surfaced by Compute.
var (
	ErrRootMissing  = errors.New("fingerprint: root does not exist")
	ErrRootNotDir   = errors.New("fingerprint: root is not a directory")
	ErrFileTooLarge = errors.New("fingerprint: file exceeds size limit")
	ErrTreeTooLarge = errors.New("fingerprint: tree exceeds total size limit")
)

// FileEntry is one file's contribution to the tree hash. Path is the
// forward-slash-normalised path relative to the scan root.
type FileEntry struct {
	Path string // e.g. "scripts/deploy.py"
	Hash string // lowercase hex sha256(file bytes)
	Size int64  // bytes
	Exec bool   // owner-executable bit set
}

// Fingerprint is the rollup of a tree scan.
type Fingerprint struct {
	Hash       string      // lowercase hex sha256 over all entries
	FileCount  int         // total files included
	TotalBytes int64       // sum of all file sizes
	Files      []FileEntry // sorted by Path
}

// Compute walks root and returns its content fingerprint. The root
// itself must be a directory; symlinks above it are followed when
// resolving the path, but symlinks discovered inside are skipped (to
// avoid escaping the scope and to keep the result reproducible).
func Compute(root string) (Fingerprint, error) {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Fingerprint{}, fmt.Errorf("%w: %s", ErrRootMissing, root)
		}
		return Fingerprint{}, fmt.Errorf("fingerprint: stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return Fingerprint{}, fmt.Errorf("%w: %s", ErrRootNotDir, root)
	}

	var (
		entries []FileEntry
		total   int64
	)

	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Compute the slash-relative path once for skip checks + entry.
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if shouldSkip(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// Reject anything that is not a regular file (symlinks,
		// devices, sockets) so the hash represents real content only.
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 || !mode.IsRegular() {
			return nil
		}
		if info.Size() > MaxFileBytes {
			return fmt.Errorf("%w: %s (%d bytes)", ErrFileTooLarge, rel, info.Size())
		}
		if total+info.Size() > MaxTotalBytes {
			return fmt.Errorf("%w: would exceed %d after %s", ErrTreeTooLarge, MaxTotalBytes, rel)
		}

		h, err := hashFile(p)
		if err != nil {
			return err
		}
		entries = append(entries, FileEntry{
			Path: rel,
			Hash: h,
			Size: info.Size(),
			Exec: mode&0o100 != 0,
		})
		total += info.Size()
		return nil
	})
	if walkErr != nil {
		return Fingerprint{}, walkErr
	}

	// Stable order so the rollup hash is reproducible.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	roll := sha256.New()
	for _, e := range entries {
		// "exec<TAB>path<TAB>hash<NL>" — a fixed line shape that no
		// filename can spoof (TAB is illegal in some FSes; we
		// pre-reject any path containing one below).
		execChar := "0"
		if e.Exec {
			execChar = "1"
		}
		line := execChar + "\t" + e.Path + "\t" + e.Hash + "\n"
		if _, err := roll.Write([]byte(line)); err != nil {
			return Fingerprint{}, err
		}
	}

	return Fingerprint{
		Hash:       hex.EncodeToString(roll.Sum(nil)),
		FileCount:  len(entries),
		TotalBytes: total,
		Files:      entries,
	}, nil
}

// shouldSkip is true for paths the scanner must ignore entirely. The
// rules deliberately mirror v1.0 §9.2 (managed-by metadata is ours,
// not skill content) and conventional hidden-file behaviour.
//
// Note: we walk top-down, so returning fs.SkipDir on a hidden
// directory pruning is cheaper than visiting every child first.
func shouldSkip(rel string) bool {
	// Reject suspicious filenames before they reach the hash. A tab
	// would let two distinct paths hash identically.
	if strings.ContainsAny(rel, "\t\n") {
		return true
	}
	for _, seg := range strings.Split(rel, "/") {
		// Conventional hidden files / directories.
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// hashFile streams the file at path and returns its lowercase hex
// sha256. Streaming avoids loading multi-megabyte files into memory.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, f, make([]byte, ReadChunk)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
