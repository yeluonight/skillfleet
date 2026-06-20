// update_check.go is the source package's update-check engine (v1.0 §8.4).
// Given a bound source it answers one question: has the upstream skill
// subtree actually changed since the last snapshot we recorded?
//
// The six-step flow (§8.4), and where each step lives here:
//
//	1. ls-remote the ref's current commit            — Fetcher.LsRemote
//	2. commit == last_remote_commit ⇒ up_to_date     — short-circuit, no clone
//	3. otherwise fetch the subdir                     — Fetcher.FetchSubdir
//	4. compute the remote manifest + content_sha256   — FetchSubdir result
//	5. compare against the current upstream version    — content_sha256 diff
//	6. content changed ⇒ update_available (+pending     — Registry publish
//	   upstream version); content same ⇒
//	   remote_changed_no_skill_change
//
// THE CORE ACCEPTANCE GUARD (§8.4 last line, IMPLEMENTATION_PLAN §2.9):
// "a repo whose commit moved but whose skill subtree didn't must NOT be
// reported as an update." We enforce this by comparing content_sha256
// (the manifest hash over paths+content+exec-bits), never the commit.
// Step 5 is where the guard lives — when the freshly fetched hash equals
// the last upstream version's hash we return RemoteChangedNoSkillChange,
// not UpdateAvailable, even though the commit advanced. The tests
// construct exactly this case (commit moves, content identical) and
// assert the outcome, so the guard is provably reachable rather than
// dead defensive code.
//
// Cursor discipline: last_checked_at advances on every completed check
// (success or failure) so the UI can show "checked N minutes ago".
// last_remote_commit advances ONLY on a check that actually observed the
// remote content (up_to_date keeps the same commit; a real fetch writes
// the new one). A failed check does NOT advance the commit, so the next
// run re-attempts from the same baseline rather than silently swallowing
// the missed update.
package source

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yeluonight/skillfleet/internal/registry"
)

// UpstreamState mirrors v1.0 §11.3 upstream_state. CheckUpdate returns one
// of these as the outcome of a check.
type UpstreamState string

const (
	// StateUpToDate: the remote ref's commit is unchanged since the last
	// recorded snapshot — nothing was fetched.
	StateUpToDate UpstreamState = "up_to_date"
	// StateUpdateAvailable: the remote subtree's content_sha256 differs
	// from the current upstream version — a pending upstream version was
	// published.
	StateUpdateAvailable UpstreamState = "update_available"
	// StateRemoteChangedNoSkillChange: the commit moved but the subtree's
	// content_sha256 is identical — the CORE GUARD outcome. No update.
	StateRemoteChangedNoSkillChange UpstreamState = "remote_changed_no_skill_change"
	// StateCheckFailed: the check could not complete (network, bad ref,
	// oversized tree, …). The commit cursor is left untouched.
	StateCheckFailed UpstreamState = "check_failed"
)

// CheckResult is the outcome of a single update check. It is intended to
// drive both the API response (t6) and the scheduler's logging (t7).
type CheckResult struct {
	SourceID string
	State    UpstreamState

	// RemoteCommit is the commit the check resolved on the remote. Set for
	// every non-failed state (up_to_date echoes the unchanged commit).
	RemoteCommit string

	// RemoteContentSHA256 is the freshly fetched subtree hash. Empty when
	// no fetch happened (up_to_date) or the check failed before fetching.
	RemoteContentSHA256 string

	// CurrentContentSHA256 is the hash of the upstream version the remote
	// was compared against. Empty when no baseline upstream version exists
	// (the binding has no upstream version yet — treated as a real update).
	CurrentContentSHA256 string

	// PendingVersionID is set only for StateUpdateAvailable: the id of the
	// newly published pending upstream version carrying the new content.
	// Empty for every other state.
	PendingVersionID string

	// Err carries the underlying error for StateCheckFailed (nil
	// otherwise). The state is the authoritative signal; Err is for logs.
	Err error
}

// Errors specific to the update-check engine. Fetch/store errors are
// wrapped and surfaced via CheckResult.Err with State=check_failed.
var (
	// ErrCheckNotBound is returned (not a CheckResult) when a source can't
	// be used for a content check at all — it isn't a fetchable git/github
	// repo, so there is nothing to ls-remote. This is a caller/programming
	// error, distinct from a check that ran and failed.
	ErrCheckNotBound = errors.New("source: not a fetchable git source")
)

// refFetcher is the fetch surface the engine needs. It is the same shape
// as the API layer's SourceFetcher, defined here (consumer side) so tests
// inject a fake without a real clone. *Fetcher satisfies it.
type refFetcher interface {
	LsRemote(ctx context.Context, repoURL string, ref RemoteRef) (string, error)
	FetchSubdir(ctx context.Context, repoURL string, ref RemoteRef, subdir string) (FetchResult, error)
}

// versionPublisher is the registry surface the engine needs: look up the
// current upstream baseline for a source, and publish a new pending
// upstream version when the content changed. *registry.Store satisfies it.
type versionPublisher interface {
	LatestVersionBySource(ctx context.Context, sourceID string, kind registry.VersionKind) (registry.Version, bool, error)
	PublishFromFiles(ctx context.Context, files []registry.InMemoryFile, p registry.PublishParams, now time.Time) (registry.Version, error)
}

// Checker runs §8.4 update checks for bound sources. It reads the source
// cursor and the registry baseline, fetches the remote, compares content
// hashes, and (on a real change) publishes a pending upstream version. It
// owns no state beyond its collaborators.
type Checker struct {
	Sources  *Store
	Fetcher  refFetcher
	Registry versionPublisher
}

// NewChecker wires a Checker. All three collaborators are required; a nil
// one makes Check return an error rather than panic at the call site.
func NewChecker(sources *Store, fetcher refFetcher, reg versionPublisher) *Checker {
	return &Checker{Sources: sources, Fetcher: fetcher, Registry: reg}
}

// Check runs the §8.4 flow for the source identified by sourceID. now is
// injected so callers (and tests) control the timestamp written to the
// check cursor. The returned CheckResult.State is always set; on a failed
// check the error is also returned so the caller can log it, but the
// cursor has still been advanced (last_checked_at) so "last checked" is
// truthful even for failures.
//
// A non-existent source returns (zero, ErrNotFound) without touching any
// cursor — there is nothing to check.
func (c *Checker) Check(ctx context.Context, sourceID string, now time.Time) (CheckResult, error) {
	if c.Sources == nil || c.Fetcher == nil || c.Registry == nil {
		return CheckResult{}, errors.New("source: checker not fully configured")
	}

	src, err := c.Sources.Get(ctx, sourceID)
	if err != nil {
		// ErrNotFound or a real DB error — either way we can't check, and
		// we have no row whose cursor to advance.
		return CheckResult{}, err
	}

	// A check only makes sense for a fetchable git/github source. Other
	// source types (local_import, zip_upload, …) have no remote to probe.
	if !isFetchableType(src.Type) {
		return CheckResult{SourceID: sourceID, State: StateCheckFailed, Err: ErrCheckNotBound}, ErrCheckNotBound
	}

	ref := RemoteRef{Type: src.RefType, Name: src.RefName}

	// Step 1: ls-remote the ref's current commit (no content download).
	remoteCommit, err := c.Fetcher.LsRemote(ctx, src.URL, ref)
	if err != nil {
		return c.fail(ctx, src, now, fmt.Errorf("ls-remote: %w", err))
	}

	// Step 2: unchanged commit ⇒ up_to_date. We still advance
	// last_checked_at (and re-write the same commit) so the UI reflects a
	// fresh check, but we skip the clone entirely — the cheap path §8.4
	// exists to protect.
	if src.LastRemoteCommit != "" && remoteCommit == src.LastRemoteCommit {
		if err := c.Sources.UpdateCheckCursor(ctx, sourceID, remoteCommit, now); err != nil {
			return CheckResult{SourceID: sourceID, State: StateCheckFailed, Err: err}, err
		}
		return CheckResult{
			SourceID:     sourceID,
			State:        StateUpToDate,
			RemoteCommit: remoteCommit,
		}, nil
	}

	// Step 3 + 4: fetch the subdir and compute its content_sha256.
	fetched, err := c.Fetcher.FetchSubdir(ctx, src.URL, ref, src.Subdir)
	if err != nil {
		return c.fail(ctx, src, now, fmt.Errorf("fetch subdir: %w", err))
	}
	remoteSHA := fetched.Manifest.ContentSHA256
	// Prefer the commit the content was actually read at (FetchSubdir
	// resolves the ref to a concrete commit); fall back to the ls-remote
	// value if the fetcher didn't populate it.
	effectiveCommit := fetched.Commit
	if effectiveCommit == "" {
		effectiveCommit = remoteCommit
	}

	// Step 5: compare against the current upstream baseline. The baseline
	// is the latest version produced from THIS source with kind=upstream —
	// the last snapshot we recorded for it.
	baseline, hasBaseline, err := c.Registry.LatestVersionBySource(ctx, sourceID, registry.KindUpstream)
	if err != nil {
		return c.fail(ctx, src, now, fmt.Errorf("load baseline: %w", err))
	}

	// ── CORE ACCEPTANCE GUARD ──────────────────────────────────────────
	// A baseline exists and the freshly fetched content hashes identically
	// to it: the commit moved (we only got here because step 2 didn't
	// short-circuit) but the skill subtree is byte-for-byte the same.
	// Report remote_changed_no_skill_change, NOT update_available, and
	// advance the commit cursor so subsequent checks short-circuit at step
	// 2 instead of re-fetching the same unchanged tree.
	if hasBaseline && baseline.ContentSHA256 == remoteSHA {
		if err := c.Sources.UpdateCheckCursor(ctx, sourceID, effectiveCommit, now); err != nil {
			return CheckResult{SourceID: sourceID, State: StateCheckFailed, Err: err}, err
		}
		return CheckResult{
			SourceID:             sourceID,
			State:                StateRemoteChangedNoSkillChange,
			RemoteCommit:         effectiveCommit,
			RemoteContentSHA256:  remoteSHA,
			CurrentContentSHA256: baseline.ContentSHA256,
		}, nil
	}

	// Step 6: content differs (or no baseline yet) ⇒ a real update.
	// Publish the fetched tree as a pending upstream version. PublishFromFiles
	// is idempotent on (name, content_sha256): if this exact content was
	// published before it reuses that row rather than duplicating, which is
	// the correct behaviour for a re-check that re-observes the same change.
	files := make([]registry.InMemoryFile, 0, len(fetched.Files))
	for _, f := range fetched.Files {
		files = append(files, registry.InMemoryFile{Path: f.Path, Content: f.Content})
	}
	pending, err := c.Registry.PublishFromFiles(ctx, files, registry.PublishParams{
		Name:         src.Name,
		Kind:         registry.KindUpstream,
		VersionLabel: "upstream update",
		SourceID:     sourceID,
		SourceCommit: effectiveCommit,
	}, now)
	if err != nil {
		// The publish failed: do NOT advance the commit cursor, so the next
		// check re-attempts and re-publishes rather than marking this commit
		// "seen" with no pending version to show for it.
		return c.fail(ctx, src, now, fmt.Errorf("publish pending: %w", err))
	}

	// Publish succeeded: advance the commit cursor to the fetched commit so
	// the next check short-circuits unless the remote moves again.
	if err := c.Sources.UpdateCheckCursor(ctx, sourceID, effectiveCommit, now); err != nil {
		return CheckResult{SourceID: sourceID, State: StateCheckFailed, Err: err}, err
	}

	var currentSHA string
	if hasBaseline {
		currentSHA = baseline.ContentSHA256
	}
	return CheckResult{
		SourceID:             sourceID,
		State:                StateUpdateAvailable,
		RemoteCommit:         effectiveCommit,
		RemoteContentSHA256:  remoteSHA,
		CurrentContentSHA256: currentSHA,
		PendingVersionID:     pending.ID,
	}, nil
}

// fail records a failed check: it advances last_checked_at (so "checked
// N ago" is truthful) WITHOUT advancing last_remote_commit (so the next
// run re-attempts from the same baseline), then returns a check_failed
// result carrying the cause. A failure to even write the cursor is folded
// into the returned error but the original cause is preserved as the
// result's Err.
func (c *Checker) fail(ctx context.Context, src Source, now time.Time, cause error) (CheckResult, error) {
	res := CheckResult{SourceID: src.ID, State: StateCheckFailed, Err: cause}
	// Re-write the EXISTING commit (unchanged) and bump last_checked_at.
	// nullable("") is fine if the source never had a commit yet.
	if curErr := c.Sources.UpdateCheckCursor(ctx, src.ID, src.LastRemoteCommit, now); curErr != nil {
		// Surface the cursor failure to the caller but keep the original
		// cause in the result — the check still "failed" for its real
		// reason, and the cursor problem is secondary.
		return res, fmt.Errorf("%w (and cursor update failed: %v)", cause, curErr)
	}
	return res, cause
}

// isFetchableType reports whether a source type has a remote git ref the
// engine can probe. Mirrors the API layer's isFetchableSourceType so the
// engine and the bind handler agree on what "bound to a repo" means.
func isFetchableType(t SourceType) bool {
	switch t {
	case TypeGitRepo, TypeGitHubRepo, TypeGitHubRelease:
		return true
	}
	return false
}
