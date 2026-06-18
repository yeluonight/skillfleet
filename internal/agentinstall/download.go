// agentinstall — download.go: fetch a package archive from the server
// and verify it before a single byte is unpacked (v1.0 §9.3 steps 4-5).
//
// Trust-but-verify: the plan arrives over the HMAC-signed downlink, so
// its ArchiveSHA256 is trustworthy, but the bytes still travel over the
// network. We hash the stream as we read it and compare against the
// plan's expected sha; a mismatch (corruption, truncation, a swapped
// blob) aborts before unpack. We also bound the read with the plan's
// declared size (plus a small slack) so a misbehaving or hostile server
// can't stream an unbounded body into a temp file.
package agentinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/yeluonight/skillfleet/internal/deploy"
)

// Errors returned by the download layer.
var (
	ErrArchiveSHAMismatch = errors.New("agentinstall: downloaded archive sha256 mismatch")
	ErrArchiveOversize    = errors.New("agentinstall: downloaded archive exceeds declared size")
	ErrNoArchiveSpec      = errors.New("agentinstall: plan has no archive sha/size")
)

// downloadSlack is the extra margin over plan.ArchiveBytes the reader
// tolerates before declaring an oversize. A correct archive is exactly
// ArchiveBytes; the slack only exists so an off-by-a-few server doesn't
// fail spuriously while still bounding the read.
const downloadSlack = 4096

// PackageFetcher fetches a package archive by its agent-relative
// download path (e.g. "/agent/packages/sv_xxx"), returning a stream the
// caller must Close. The agent client (t5) implements this; defining it
// here keeps download independently testable.
type PackageFetcher interface {
	FetchPackage(ctx context.Context, downloadPath string) (io.ReadCloser, error)
}

// DownloadVerified fetches the plan's package into a fresh temp file,
// hashing as it streams and bounding the read at the declared size. On a
// sha mismatch or oversize it removes the temp file and returns an
// error. On success it returns the temp file path; the caller owns it
// and must remove it when done (the executor unpacks then deletes it).
func DownloadVerified(ctx context.Context, fetcher PackageFetcher, plan deploy.Plan) (path string, err error) {
	if plan.ArchiveSHA256 == "" || plan.ArchiveBytes <= 0 {
		return "", ErrNoArchiveSpec
	}

	rc, err := fetcher.FetchPackage(ctx, plan.DownloadPath)
	if err != nil {
		return "", fmt.Errorf("agentinstall: fetch package: %w", err)
	}
	defer func() { _ = rc.Close() }()

	tmp, err := os.CreateTemp("", "skillfleet-pkg-*.tgz")
	if err != nil {
		return "", fmt.Errorf("agentinstall: temp archive: %w", err)
	}
	tmpPath := tmp.Name()
	// Clean up the temp file on any error path.
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	// Read at most ArchiveBytes+slack+1: the +1 lets us detect a stream
	// that claims to fit but actually overruns.
	limit := plan.ArchiveBytes + downloadSlack + 1
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(rc, limit))
	if err != nil {
		return "", fmt.Errorf("agentinstall: stream archive: %w", err)
	}
	if written > plan.ArchiveBytes+downloadSlack {
		err = fmt.Errorf("%w: read %d, declared %d", ErrArchiveOversize, written, plan.ArchiveBytes)
		return "", err
	}
	if cerr := tmp.Close(); cerr != nil {
		err = fmt.Errorf("agentinstall: close temp archive: %w", cerr)
		return "", err
	}

	got := hex.EncodeToString(hasher.Sum(nil))
	if got != plan.ArchiveSHA256 {
		err = fmt.Errorf("%w: got %s, want %s", ErrArchiveSHAMismatch, got, plan.ArchiveSHA256)
		return "", err
	}
	return tmpPath, nil
}
