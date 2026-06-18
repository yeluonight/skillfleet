// Package skill — package.go implements deterministic tar.gz packing
// and guarded unpacking of a Skill Package (ADR-0008).
//
// "Deterministic" means: the same package contents always produce the
// byte-identical archive, so the archive's sha256 is a stable storage
// key and content-dedup works. This is achieved by:
//
//   - ordering entries by fingerprint's sorted (forward-slash) path,
//   - emitting only regular files (no directory entries; Unpack
//     recreates parents), so there is no ambiguity about dir order,
//   - zeroing every non-content header field: mtime = epoch, uid/gid =
//     0, uname/gname = "", and collapsing the permission bits to
//     exactly 0644 (regular) or 0755 (executable).
//
// Unpack is the import side. Every archive entry's name is run through
// safefs.CleanPackagePath before it touches disk, so a hostile archive
// cannot write outside destRoot ("../../etc/...") or smuggle absolute
// paths, drive letters, or control characters. Non-regular entries
// (symlinks, devices, hardlinks) are refused outright.
package skill

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/yeluonight/skillfleet/internal/fingerprint"
	"github.com/yeluonight/skillfleet/internal/safefs"
)

// MaxArchiveFiles caps the number of entries Unpack will extract from
// one archive — a tar-bomb backstop independent of the byte limit.
const MaxArchiveFiles = 10000

// epoch is the fixed modification time stamped on every archive entry.
// Using a constant (not the wall clock) is what makes the archive
// reproducible; the real timestamps live in the database row.
var epoch = time.Unix(0, 0).UTC()

// Errors returned by Unpack.
var (
	ErrTooManyFiles  = errors.New("skill: archive exceeds file count limit")
	ErrArchiveTooBig = errors.New("skill: archive exceeds total size limit")
	ErrFileTooBig    = errors.New("skill: archive entry exceeds file size limit")
	ErrBadEntry      = errors.New("skill: archive entry is not a regular file")
)

// ArchiveInfo describes a written archive.
type ArchiveInfo struct {
	// SHA256 is the lowercase hex sha256 over the archive bytes. This
	// is the storage key (store/packages/<sha>.tgz), distinct from the
	// manifest's content_sha256 (which hashes the tree, not the
	// archive) — see ADR-0008.
	SHA256 string
	// Bytes is the total archive size written to the sink.
	Bytes int64
}

// Pack writes a deterministic tar.gz of the package at root to w and
// returns the archive's sha256 + size. The file list and exec bits are
// taken from fingerprint.Compute, so Pack honours the same skip rules
// (hidden files, symlinks, the .skillfleet marker) and size caps as
// the manifest — a packed archive and its Manifest always agree.
func Pack(root string, w io.Writer) (ArchiveInfo, error) {
	fp, err := fingerprint.Compute(root)
	if err != nil {
		return ArchiveInfo{}, fmt.Errorf("skill: pack fingerprint: %w", err)
	}

	hasher := sha256.New()
	counter := &countingWriter{w: io.MultiWriter(w, hasher)}

	gz, err := gzip.NewWriterLevel(counter, gzip.BestCompression)
	if err != nil {
		return ArchiveInfo{}, err
	}
	// Leave gz.Header at its zero value (empty Name/Comment, zero
	// ModTime) so the gzip wrapper is reproducible too.
	tw := tar.NewWriter(gz)

	for _, fe := range fp.Files {
		mode := int64(0o644)
		if fe.Exec {
			mode = 0o755
		}
		hdr := &tar.Header{
			Name:     fe.Path, // already forward-slash, root-relative
			Mode:     mode,
			Size:     fe.Size,
			ModTime:  epoch,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatPAX, // stable across long/unicode names
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return ArchiveInfo{}, fmt.Errorf("skill: tar header %s: %w", fe.Path, err)
		}
		if err := copyFileInto(tw, filepath.Join(root, filepath.FromSlash(fe.Path)), fe.Size); err != nil {
			return ArchiveInfo{}, err
		}
	}

	if err := tw.Close(); err != nil {
		return ArchiveInfo{}, fmt.Errorf("skill: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return ArchiveInfo{}, fmt.Errorf("skill: close gzip: %w", err)
	}

	return ArchiveInfo{SHA256: hex.EncodeToString(hasher.Sum(nil)), Bytes: counter.n}, nil
}

// copyFileInto streams exactly size bytes from the file at path into w.
// It guards against the file changing size between fingerprint and pack
// (a TOCTOU) by refusing a short/long copy.
func copyFileInto(w io.Writer, path string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("skill: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	n, err := io.Copy(w, io.LimitReader(f, size))
	if err != nil {
		return fmt.Errorf("skill: copy %s: %w", path, err)
	}
	if n != size {
		return fmt.Errorf("skill: %s changed size during pack (%d != %d)", path, n, size)
	}
	return nil
}

// Unpack extracts a tar.gz archive read from r into destRoot, creating
// destRoot if needed. Every entry name is validated by
// safefs.CleanPackagePath; the cleaned path is joined to destRoot, so
// extraction can never escape it. Returns the cleaned, sorted-as-read
// list of extracted file paths.
//
// destRoot must be an empty or non-existent directory in practice; we
// do not clear it. Callers that need a clean staging dir create a fresh
// temp dir first.
func Unpack(r io.Reader, destRoot string) ([]string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("skill: gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return nil, fmt.Errorf("skill: mkdir dest: %w", err)
	}

	var (
		extracted []string
		total     int64
		count     int
	)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("skill: tar next: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("%w: %s (type %d)", ErrBadEntry, hdr.Name, hdr.Typeflag)
		}

		clean, err := safefs.CleanPackagePath(hdr.Name)
		if err != nil {
			return nil, fmt.Errorf("skill: unsafe archive path %q: %w", hdr.Name, err)
		}

		count++
		if count > MaxArchiveFiles {
			return nil, ErrTooManyFiles
		}
		if hdr.Size > fingerprint.MaxFileBytes {
			return nil, fmt.Errorf("%w: %s (%d bytes)", ErrFileTooBig, clean, hdr.Size)
		}
		total += hdr.Size
		if total > fingerprint.MaxTotalBytes {
			return nil, fmt.Errorf("%w: after %s", ErrArchiveTooBig, clean)
		}

		dest := filepath.Join(destRoot, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, fmt.Errorf("skill: mkdir %s: %w", filepath.Dir(dest), err)
		}
		mode := os.FileMode(0o644)
		if hdr.Mode&0o100 != 0 {
			mode = 0o755
		}
		if err := writeFile(dest, tr, hdr.Size, mode); err != nil {
			return nil, err
		}
		extracted = append(extracted, clean)
	}
	return extracted, nil
}

// writeFile creates dest and copies exactly size bytes from r into it
// with the given mode. The LimitReader guard means a header that lies
// about Size (claims small, streams large) cannot overrun the byte
// budget the caller already accounted for.
func writeFile(dest string, r io.Reader, size int64, mode os.FileMode) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("skill: create %s: %w", dest, err)
	}
	n, err := io.Copy(f, io.LimitReader(r, size))
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("skill: write %s: %w", dest, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("skill: close %s: %w", dest, err)
	}
	if n != size {
		return fmt.Errorf("skill: %s truncated (%d != %d)", dest, n, size)
	}
	return nil
}

// countingWriter tallies bytes written so Pack can report archive size
// without a second stat.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
