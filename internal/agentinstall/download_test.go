package agentinstall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/yeluonight/skillfleet/internal/deploy"
)

type fakeFetcher struct {
	content      []byte
	err          error
	gotPath      string
	gotPathCount int
}

func (f *fakeFetcher) FetchPackage(ctx context.Context, downloadPath string) (io.ReadCloser, error) {
	f.gotPath = downloadPath
	f.gotPathCount++
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(bytes.NewReader(f.content)), nil
}

func TestDownloadVerifiedSuccess(t *testing.T) {
	content := []byte("hello archive")
	plan := deploy.Plan{
		ArchiveSHA256: sha256Hex(content),
		ArchiveBytes:  int64(len(content)),
		DownloadPath:  "/agent/packages/sv_1",
	}
	fetcher := &fakeFetcher{content: content}

	path, err := DownloadVerified(context.Background(), fetcher, plan)
	if err != nil {
		t.Fatalf("DownloadVerified() unexpected error = %v", err)
	}
	defer os.Remove(path)

	if path == "" {
		t.Fatal("DownloadVerified() returned empty path")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("temp file content = %q, want %q", got, content)
	}
	if fetcher.gotPath != "/agent/packages/sv_1" {
		t.Fatalf("FetchPackage() path = %q, want %q", fetcher.gotPath, "/agent/packages/sv_1")
	}
}

func TestDownloadVerifiedSHAMismatch(t *testing.T) {
	content := []byte("hello archive")
	fetcher := &fakeFetcher{content: content}
	plan := deploy.Plan{
		ArchiveSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		ArchiveBytes:  int64(len(content)),
		DownloadPath:  "/agent/packages/sv_1",
	}

	path, err := DownloadVerified(context.Background(), fetcher, plan)
	if !errors.Is(err, ErrArchiveSHAMismatch) {
		t.Fatalf("DownloadVerified() error = %v, want errors.Is %v", err, ErrArchiveSHAMismatch)
	}
	if path != "" {
		t.Fatalf("DownloadVerified() path = %q, want empty", path)
	}
}

func TestDownloadVerifiedOversize(t *testing.T) {
	content := make([]byte, 5000)
	fetcher := &fakeFetcher{content: content}
	plan := deploy.Plan{
		ArchiveSHA256: sha256Hex(content),
		ArchiveBytes:  1,
		DownloadPath:  "/agent/packages/sv_oversize",
	}

	path, err := DownloadVerified(context.Background(), fetcher, plan)
	if !errors.Is(err, ErrArchiveOversize) {
		t.Fatalf("DownloadVerified() error = %v, want errors.Is %v", err, ErrArchiveOversize)
	}
	if path != "" {
		t.Fatalf("DownloadVerified() path = %q, want empty", path)
	}
}

func TestDownloadVerifiedNoArchiveSpec(t *testing.T) {
	content := []byte("hello archive")
	tests := []struct {
		name string
		plan deploy.Plan
	}{
		{
			name: "empty archive sha",
			plan: deploy.Plan{ArchiveBytes: int64(len(content)), DownloadPath: "/agent/packages/sv_1"},
		},
		{
			name: "zero archive bytes",
			plan: deploy.Plan{ArchiveSHA256: sha256Hex(content), DownloadPath: "/agent/packages/sv_1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := &fakeFetcher{content: content}
			path, err := DownloadVerified(context.Background(), fetcher, tt.plan)
			if !errors.Is(err, ErrNoArchiveSpec) {
				t.Fatalf("DownloadVerified() error = %v, want errors.Is %v", err, ErrNoArchiveSpec)
			}
			if path != "" {
				t.Fatalf("DownloadVerified() path = %q, want empty", path)
			}
			if fetcher.gotPathCount != 0 {
				t.Fatalf("FetchPackage() called %d times, want 0", fetcher.gotPathCount)
			}
		})
	}
}

func TestDownloadVerifiedFetcherError(t *testing.T) {
	fetchErr := errors.New("fetch failed")
	fetcher := &fakeFetcher{err: fetchErr}
	content := []byte("hello archive")
	plan := deploy.Plan{
		ArchiveSHA256: sha256Hex(content),
		ArchiveBytes:  int64(len(content)),
		DownloadPath:  "/agent/packages/sv_1",
	}

	path, err := DownloadVerified(context.Background(), fetcher, plan)
	if !errors.Is(err, fetchErr) {
		t.Fatalf("DownloadVerified() error = %v, want errors.Is %v", err, fetchErr)
	}
	if path != "" {
		t.Fatalf("DownloadVerified() path = %q, want empty", path)
	}
	if fetcher.gotPath != "/agent/packages/sv_1" {
		t.Fatalf("FetchPackage() path = %q, want %q", fetcher.gotPath, "/agent/packages/sv_1")
	}
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
