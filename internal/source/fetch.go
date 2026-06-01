// fetch.go is the source package's network surface: it pulls a skill's
// files from a remote git repository and turns them into a deterministic
// manifest, without ever shelling out to the system git (phase 6
// decision: pure-Go go-git, so there is no command-injection surface and
// no git binary to deploy).
//
// Two operations matter to the update-check engine (t5):
//
//   - LsRemote: ask the remote for a ref's current commit WITHOUT
//     downloading any content. This is the cheap first step of an update
//     check — if the commit is unchanged since last_remote_commit, we
//     skip the clone entirely.
//   - FetchSubdir: shallow-clone the repo in memory, read the files under
//     a subdirectory, and compute the same content_sha256 the registry
//     would (via skill.Generate over a temp materialisation). t5 compares
//     that hash against the current version to decide "real update" vs
//     "commit moved but the skill subtree didn't".
//
// Security posture (phase 6 binds PUBLIC repos only; credentials are a
// later-phase task):
//   - URL scheme allow-list (https/http/git); everything else — notably
//     file:// and ssh:// — is refused so a bound source can't be aimed at
//     the server's own disk or an internal host.
//   - The subdir and every fetched file path are validated so a hostile
//     repo can't escape its subtree (safefs.CleanPackagePath does the
//     per-file check; the subdir gets its own ".."/absolute guard).
//   - Resource caps: shallow clone (Depth 1, single branch), a per-file
//     size cap, a total-bytes cap, and a file-count cap, all enforced
//     while reading the tree so a huge repo can't exhaust memory. The
//     caller's context bounds wall-clock time.
package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitstorage "github.com/go-git/go-git/v5/storage/memory"

	"github.com/yeluonight/skillfleet/internal/safefs"
	"github.com/yeluonight/skillfleet/internal/skill"
)

// Fetch limits. These bound a single fetch so a hostile or accidentally
// huge repository can't exhaust server memory. They are deliberately
// generous for real skill subtrees (a skill is docs + a few scripts) and
// far below anything that would threaten the process.
const (
	// DefaultMaxFileBytes caps one file pulled from a repo.
	DefaultMaxFileBytes = 8 * 1024 * 1024 // 8 MiB
	// DefaultMaxTotalBytes caps the sum of all files in the subdir.
	DefaultMaxTotalBytes = 64 * 1024 * 1024 // 64 MiB
	// DefaultMaxFileCount caps how many files a subdir may contain.
	DefaultMaxFileCount = 2000
	// DefaultTimeout bounds a single LsRemote/FetchSubdir when the caller
	// passes a context without its own deadline.
	DefaultTimeout = 60 * time.Second
)

// Errors returned by the fetcher. Callers branch on these via errors.Is
// to map to the right HTTP status and user-facing message.
var (
	ErrBadURL         = errors.New("source: invalid or disallowed repo URL")
	ErrBadSubdir      = errors.New("source: invalid subdir")
	ErrRefNotFound    = errors.New("source: ref not found on remote")
	ErrSubdirNotFound = errors.New("source: subdir not found in repo")
	ErrTooLarge       = errors.New("source: fetched content exceeds size limit")
	ErrTooManyFiles   = errors.New("source: subdir exceeds file-count limit")
)

// allowedSchemes is the URL scheme allow-list for a bound source. git://
// is unencrypted but is a legitimate public-repo transport; http is kept
// for self-hosted instances on a trusted network. file:// and ssh:// are
// intentionally absent — phase 6 binds remote public repos only.
var allowedSchemes = map[string]bool{
	"https": true,
	"http":  true,
	"git":   true,
}

// FetchedFile is one file read from a remote subdir. Path is
// subdir-relative and forward-slashed (e.g. "SKILL.md",
// "scripts/deploy.py"); it has passed safefs validation. Binary reports
// the in-memory binary sniff so callers can mark is_binary without
// re-reading the bytes.
type FetchedFile struct {
	Path    string
	Content []byte
	Binary  bool
}

// FetchResult is the outcome of FetchSubdir: the commit the content was
// read at, the skill manifest (carrying the content_sha256 that t5
// compares), and the files themselves.
type FetchResult struct {
	Commit   string
	Manifest skill.Manifest
	Files    []FetchedFile
}

// Fetcher pulls skill content from remote git repositories. The zero
// value is NOT ready; use NewFetcher so the limits are populated.
type Fetcher struct {
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxFileCount  int
	Timeout       time.Duration
}

// NewFetcher returns a Fetcher with the default limits.
func NewFetcher() *Fetcher {
	return &Fetcher{
		MaxFileBytes:  DefaultMaxFileBytes,
		MaxTotalBytes: DefaultMaxTotalBytes,
		MaxFileCount:  DefaultMaxFileCount,
		Timeout:       DefaultTimeout,
	}
}

// RemoteRef names what to resolve on the remote. Type is one of the
// RefType values (branch/tag/commit). An empty Type/Name means "the
// remote's default branch (HEAD)".
type RemoteRef struct {
	Type RefType
	Name string
}

// referenceName maps a RemoteRef to the plumbing reference go-git clones.
// A commit ref returns a zero ReferenceName (the caller resolves the
// commit after a default clone); branch/tag map to their ref namespaces.
func (r RemoteRef) referenceName() plumbing.ReferenceName {
	switch r.Type {
	case RefBranch:
		return plumbing.NewBranchReferenceName(r.Name)
	case RefTag:
		return plumbing.NewTagReferenceName(r.Name)
	default:
		return ""
	}
}

// LsRemote resolves ref to its current commit hash on the remote WITHOUT
// downloading any content. This is the cheap probe an update check runs
// first: a commit equal to the stored cursor means "nothing to do".
//
// For a commit ref the hash is returned verbatim (after a shape check) —
// there is nothing to resolve. For branch/tag the remote ref list is
// consulted. An empty ref resolves the remote HEAD.
func (f *Fetcher) LsRemote(ctx context.Context, repoURL string, ref RemoteRef) (string, error) {
	if err := validateRepoURL(repoURL); err != nil {
		return "", err
	}
	ctx, cancel := f.withTimeout(ctx)
	defer cancel()

	// A pinned commit needs no network round-trip to "resolve".
	if ref.Type == RefCommit {
		if !looksLikeHash(ref.Name) {
			return "", fmt.Errorf("%w: not a commit hash: %q", ErrRefNotFound, ref.Name)
		}
		return ref.Name, nil
	}

	rem := git.NewRemote(gitstorage.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{repoURL},
	})
	refs, err := rem.ListContext(ctx, &git.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("source: ls-remote: %w", err)
	}

	want := ref.referenceName()
	var head string
	for _, r := range refs {
		if r.Name() == plumbing.HEAD {
			// Capture the symbolic HEAD's target for the empty-ref case.
			if r.Type() == plumbing.HashReference {
				head = r.Hash().String()
			}
			continue
		}
		if ref.Name == "" {
			continue
		}
		if r.Name() == want {
			return r.Hash().String(), nil
		}
	}
	if ref.Name == "" && head != "" {
		return head, nil
	}
	return "", fmt.Errorf("%w: %s %q", ErrRefNotFound, ref.Type, ref.Name)
}

// FetchSubdir shallow-clones repoURL at ref into memory, reads the files
// under subdir, and returns them plus a manifest whose ContentSHA256 is
// identical to what the registry would compute for the same tree. An
// empty subdir means the repository root.
func (f *Fetcher) FetchSubdir(ctx context.Context, repoURL string, ref RemoteRef, subdir string) (FetchResult, error) {
	if err := validateRepoURL(repoURL); err != nil {
		return FetchResult{}, err
	}
	cleanSub, err := cleanSubdir(subdir)
	if err != nil {
		return FetchResult{}, err
	}
	ctx, cancel := f.withTimeout(ctx)
	defer cancel()

	commit, err := f.cloneAndResolve(ctx, repoURL, ref)
	if err != nil {
		return FetchResult{}, err
	}
	tree, err := commit.Tree()
	if err != nil {
		return FetchResult{}, fmt.Errorf("source: read tree: %w", err)
	}

	files, err := f.readSubtree(tree, cleanSub)
	if err != nil {
		return FetchResult{}, err
	}
	if len(files) == 0 {
		return FetchResult{}, fmt.Errorf("%w: %q", ErrSubdirNotFound, subdir)
	}

	manifest, err := manifestFromFiles(files)
	if err != nil {
		return FetchResult{}, err
	}
	return FetchResult{
		Commit:   commit.Hash.String(),
		Manifest: manifest,
		Files:    files,
	}, nil
}

// cloneAndResolve performs the shallow in-memory clone and returns the
// commit object the ref points at. For a commit ref it clones the
// default branch then looks the commit up by hash; for branch/tag it
// clones that single ref directly.
func (f *Fetcher) cloneAndResolve(ctx context.Context, repoURL string, ref RemoteRef) (*object.Commit, error) {
	opts := &git.CloneOptions{
		URL:          repoURL,
		Depth:        1,
		SingleBranch: true,
		Tags:         git.NoTags,
	}
	if rn := ref.referenceName(); rn != "" {
		opts.ReferenceName = rn
	}

	repo, err := git.CloneContext(ctx, gitstorage.NewStorage(), memfs.New(), opts)
	if err != nil {
		// go-git surfaces a missing branch/tag as a reference error; map
		// the common ones to ErrRefNotFound for a clean 4xx upstream.
		if errors.Is(err, plumbing.ErrReferenceNotFound) || errors.Is(err, git.NoMatchingRefSpecError{}) {
			return nil, fmt.Errorf("%w: %s %q", ErrRefNotFound, ref.Type, ref.Name)
		}
		return nil, fmt.Errorf("source: clone: %w", err)
	}

	if ref.Type == RefCommit {
		h := plumbing.NewHash(ref.Name)
		c, err := repo.CommitObject(h)
		if err != nil {
			return nil, fmt.Errorf("%w: commit %q not reachable in shallow clone: %v", ErrRefNotFound, ref.Name, err)
		}
		return c, nil
	}

	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("source: head: %w", err)
	}
	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, fmt.Errorf("source: head commit: %w", err)
	}
	return c, nil
}

// readSubtree walks tree and collects the files under sub (sub == "" is
// the whole tree). Each file path is re-validated through safefs, and the
// size/count caps are enforced as we go so an oversized repo is rejected
// before its bytes are fully buffered.
func (f *Fetcher) readSubtree(tree *object.Tree, sub string) ([]FetchedFile, error) {
	prefix := ""
	if sub != "" {
		prefix = sub + "/"
	}

	var (
		out   []FetchedFile
		total int64
		iter  = tree.Files()
		ferr  error
	)
	err := iter.ForEach(func(tf *object.File) error {
		full := tf.Name // forward-slash, repo-root-relative

		// Filter to the requested subtree.
		var rel string
		switch {
		case sub == "":
			rel = full
		case full == sub:
			// sub names a file, not a directory — treat it as a single-file subtree.
			rel = path.Base(full)
		case strings.HasPrefix(full, prefix):
			rel = strings.TrimPrefix(full, prefix)
		default:
			return nil // outside the subtree
		}

		if len(out)+1 > f.MaxFileCount {
			ferr = ErrTooManyFiles
			return errStop
		}
		// Validate the subdir-relative path can't escape (defence in
		// depth: the bytes are about to be materialised to a temp dir).
		if _, err := safefs.CleanPackagePath(rel); err != nil {
			return fmt.Errorf("source: repo file %q: %w", full, err)
		}
		if tf.Size > f.MaxFileBytes {
			ferr = fmt.Errorf("%w: %s is %d bytes", ErrTooLarge, full, tf.Size)
			return errStop
		}
		total += tf.Size
		if total > f.MaxTotalBytes {
			ferr = ErrTooLarge
			return errStop
		}

		content, err := readBlob(tf)
		if err != nil {
			return err
		}
		out = append(out, FetchedFile{
			Path:    rel,
			Content: content,
			Binary:  skill.IsBinaryContent(content),
		})
		return nil
	})
	if ferr != nil {
		return nil, ferr
	}
	if err != nil {
		return nil, fmt.Errorf("source: walk tree: %w", err)
	}
	return out, nil
}

// errStop is a sentinel returned from the tree iterator to halt
// iteration early once a cap is hit (ForEach stops on any non-nil error;
// we surface the real reason via the captured ferr).
var errStop = errors.New("source: stop iteration")

// readBlob reads a tree file's bytes with its reader closed promptly.
func readBlob(tf *object.File) ([]byte, error) {
	r, err := tf.Reader()
	if err != nil {
		return nil, fmt.Errorf("source: open %q: %w", tf.Name, err)
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("source: read %q: %w", tf.Name, err)
	}
	return b, nil
}

// manifestFromFiles materialises files into a temp directory and runs
// skill.Generate over it, yielding the same content_sha256 the registry
// computes (skill.Generate -> fingerprint.Compute cares only about
// paths + content + exec bits, not where the tree lives). This mirrors
// registry.PublishFromFiles' materialise-then-Generate pattern so the
// two hashes are directly comparable in t5.
func manifestFromFiles(files []FetchedFile) (skill.Manifest, error) {
	tmp, err := os.MkdirTemp("", "sf-fetch-*")
	if err != nil {
		return skill.Manifest{}, fmt.Errorf("source: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	for _, f := range files {
		clean, err := safefs.CleanPackagePath(f.Path)
		if err != nil {
			return skill.Manifest{}, fmt.Errorf("source: file path %q: %w", f.Path, err)
		}
		dest := filepath.Join(tmp, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return skill.Manifest{}, fmt.Errorf("source: mkdir for %s: %w", clean, err)
		}
		if err := os.WriteFile(dest, f.Content, 0o644); err != nil {
			return skill.Manifest{}, fmt.Errorf("source: write %s: %w", clean, err)
		}
	}
	return skill.Generate(tmp)
}

// withTimeout applies f.Timeout to ctx unless ctx already has a deadline.
func (f *Fetcher) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok || f.Timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, f.Timeout)
}

// validateRepoURL enforces the scheme allow-list. It parses strictly and
// requires a host so a relative or schemeless string can't slip through.
func validateRepoURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("%w: empty", ErrBadURL)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadURL, err)
	}
	if !allowedSchemes[strings.ToLower(u.Scheme)] {
		return fmt.Errorf("%w: scheme %q not allowed", ErrBadURL, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%w: missing host", ErrBadURL)
	}
	return nil
}

// cleanSubdir validates a caller-supplied subdir and returns its
// canonical forward-slash form ("" for the repo root). Unlike a package
// file path, a subdir may be empty (root) and names a directory, so it
// gets its own guard rather than CleanPackagePath (which requires a
// file). It still rejects absolute paths and any ".." escape.
func cleanSubdir(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.ContainsRune(raw, '\\') {
		return "", fmt.Errorf("%w: backslash not allowed", ErrBadSubdir)
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%w: control character", ErrBadSubdir)
		}
	}
	if strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("%w: absolute path", ErrBadSubdir)
	}
	for _, seg := range strings.Split(raw, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: parent-directory segment", ErrBadSubdir)
		}
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("%w: escapes repo root", ErrBadSubdir)
	}
	return cleaned, nil
}

// looksLikeHash reports whether s is a plausible git object hash (40 hex
// for SHA-1, 64 for SHA-256). It is a shape check, not an existence
// check — the commit's reachability is verified after the clone.
func looksLikeHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}
