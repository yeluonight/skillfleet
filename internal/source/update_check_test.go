package source

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/migrations"
)

// fakeRefFetcher is an in-memory refFetcher for engine tests: it returns
// canned ls-remote / fetch results without touching the network, and
// records whether FetchSubdir was called so a test can prove the
// up_to_date path skips the clone.
type fakeRefFetcher struct {
	lsCommit string
	lsErr    error
	result   FetchResult
	fetchErr error

	lsCalled    bool
	fetchCalled bool
}

func (f *fakeRefFetcher) LsRemote(_ context.Context, _ string, _ RemoteRef) (string, error) {
	f.lsCalled = true
	return f.lsCommit, f.lsErr
}

func (f *fakeRefFetcher) FetchSubdir(_ context.Context, _ string, _ RemoteRef, _ string) (FetchResult, error) {
	f.fetchCalled = true
	if f.fetchErr != nil {
		return FetchResult{}, f.fetchErr
	}
	return f.result, nil
}

// openCheckerTest stands up a real source.Store + real registry.Store over
// a fresh migrated SQLite db, plus an injectable fake fetcher. Using real
// stores (only the network is faked) means the content_sha256 the engine
// compares is genuinely computed by skill.Generate on both sides — the
// guard is exercised end-to-end, not stubbed.
func openCheckerTest(t *testing.T) (*Checker, *Store, *registry.Store, *fakeRefFetcher, *sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "check_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatalf("migrations.Apply: %v", err)
	}
	reg, err := registry.New(d, filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	srcStore := New(d)
	fetcher := &fakeRefFetcher{}
	return NewChecker(srcStore, fetcher, reg), srcStore, reg, fetcher, d, ctx
}

// skillFiles returns a minimal valid skill tree as FetchedFiles. body lets
// a test vary the content so two trees hash differently (or identically).
func skillFiles(name, body string) []FetchedFile {
	md := "---\nname: " + name + "\ndescription: upstream skill\n---\n\n# " + name + "\n\n" + body
	return []FetchedFile{
		{Path: "SKILL.md", Content: []byte(md)},
		{Path: "run.sh", Content: []byte("#!/bin/sh\necho hi\n")},
	}
}

// fetchResult builds a FetchResult whose Manifest.ContentSHA256 is the
// REAL hash skill.Generate produces for files (via the same
// manifestFromFiles the production fetcher uses), tagged with commit. This
// is what makes the guard test honest: the engine compares this hash
// against the registry baseline's hash, and both are computed the same way.
func fetchResult(t *testing.T, files []FetchedFile, commit string) FetchResult {
	t.Helper()
	m, err := manifestFromFiles(files)
	if err != nil {
		t.Fatalf("manifestFromFiles: %v", err)
	}
	return FetchResult{Commit: commit, Manifest: m, Files: files}
}

// bindSource creates a source row and publishes a baseline upstream
// version from baselineFiles tagged with that source id, mirroring what
// the bind-source handler (t4) does. Returns the source and the baseline
// content_sha256 so a test can assert against it.
func bindSource(t *testing.T, srcStore *Store, reg *registry.Store, name string, baselineFiles []FetchedFile, commit string) (Source, string) {
	t.Helper()
	src, err := srcStore.Create(context.Background(), Source{
		Name:             name,
		Type:             TypeGitHubRepo,
		URL:              "https://github.com/acme/skills",
		RefType:          RefBranch,
		RefName:          "main",
		Subdir:           name,
		LastRemoteCommit: commit,
		LastCheckedAt:    fixedNow,
	}, fixedNow)
	if err != nil {
		t.Fatalf("Create source: %v", err)
	}
	inmem := make([]registry.InMemoryFile, 0, len(baselineFiles))
	for _, f := range baselineFiles {
		inmem = append(inmem, registry.InMemoryFile{Path: f.Path, Content: f.Content})
	}
	v, err := reg.PublishFromFiles(context.Background(), inmem, registry.PublishParams{
		Name:         name,
		Kind:         registry.KindUpstream,
		VersionLabel: "upstream baseline",
		SourceID:     src.ID,
		SourceCommit: commit,
	}, fixedNow)
	if err != nil {
		t.Fatalf("publish baseline: %v", err)
	}
	return src, v.ContentSHA256
}

// later is a timestamp strictly after fixedNow, for the check call so the
// cursor advance is observable.
var later = fixedNow.Add(time.Hour)

// ── THE CORE ACCEPTANCE GUARD ─────────────────────────────────────────
//
// Repo commit moves but the skill subtree is byte-identical: the check
// MUST return remote_changed_no_skill_change, NEVER update_available. This
// test constructs exactly that case (baseline at commit1, remote advanced
// to commit2 with identical content) and asserts the guard outcome, so the
// branch is provably reachable — the whole point of t5.
func TestCheck_CommitMovedContentSame_NoFalseUpdate(t *testing.T) {
	checker, srcStore, reg, fetcher, _, ctx := openCheckerTest(t)

	files := skillFiles("deploy-helper", "stable body")
	src, baselineSHA := bindSource(t, srcStore, reg, "deploy-helper", files, "commit1")

	// Remote advanced to commit2, but returns the SAME files → same hash.
	fetcher.lsCommit = "commit2"
	fetcher.result = fetchResult(t, files, "commit2")

	res, err := checker.Check(ctx, src.ID, later)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.State != StateRemoteChangedNoSkillChange {
		t.Fatalf("state = %q, want remote_changed_no_skill_change (false-update guard breached!)", res.State)
	}
	if res.RemoteContentSHA256 != baselineSHA || res.CurrentContentSHA256 != baselineSHA {
		t.Errorf("hashes differ: remote=%q current=%q baseline=%q", res.RemoteContentSHA256, res.CurrentContentSHA256, baselineSHA)
	}
	if res.PendingVersionID != "" {
		t.Errorf("PendingVersionID = %q, want empty (no version on no-change)", res.PendingVersionID)
	}

	// No pending upstream version should have been published — still 1.
	assertUpstreamVersionCount(t, reg, "deploy-helper", 1)

	// The commit cursor advanced to commit2 so the next check short-circuits.
	got, _ := srcStore.Get(ctx, src.ID)
	if got.LastRemoteCommit != "commit2" {
		t.Errorf("cursor commit = %q, want commit2", got.LastRemoteCommit)
	}
	if !got.LastCheckedAt.Equal(later) {
		t.Errorf("last_checked_at = %v, want %v", got.LastCheckedAt, later)
	}
	if !fetcher.fetchCalled {
		t.Error("expected a fetch (commit changed), but FetchSubdir was not called")
	}
}

func TestCheck_CommitUnchanged_UpToDate_SkipsFetch(t *testing.T) {
	checker, srcStore, reg, fetcher, _, ctx := openCheckerTest(t)
	files := skillFiles("deploy-helper", "body")
	src, _ := bindSource(t, srcStore, reg, "deploy-helper", files, "commit1")

	fetcher.lsCommit = "commit1" // unchanged
	// result deliberately left zero — a fetch would produce an empty hash
	// and corrupt the result; this asserts we never fetch.

	res, err := checker.Check(ctx, src.ID, later)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.State != StateUpToDate {
		t.Fatalf("state = %q, want up_to_date", res.State)
	}
	if fetcher.fetchCalled {
		t.Error("FetchSubdir called on up_to_date — the cheap path must skip the clone")
	}
	if res.RemoteCommit != "commit1" {
		t.Errorf("RemoteCommit = %q, want commit1", res.RemoteCommit)
	}
	got, _ := srcStore.Get(ctx, src.ID)
	if !got.LastCheckedAt.Equal(later) {
		t.Errorf("last_checked_at not advanced: %v", got.LastCheckedAt)
	}
}

func TestCheck_ContentChanged_UpdateAvailable(t *testing.T) {
	checker, srcStore, reg, fetcher, _, ctx := openCheckerTest(t)
	baseline := skillFiles("deploy-helper", "old body")
	src, baselineSHA := bindSource(t, srcStore, reg, "deploy-helper", baseline, "commit1")

	changed := skillFiles("deploy-helper", "NEW body, genuinely different")
	fetcher.lsCommit = "commit2"
	fetcher.result = fetchResult(t, changed, "commit2")

	res, err := checker.Check(ctx, src.ID, later)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.State != StateUpdateAvailable {
		t.Fatalf("state = %q, want update_available", res.State)
	}
	if res.PendingVersionID == "" {
		t.Error("update_available but no PendingVersionID")
	}
	if res.RemoteContentSHA256 == baselineSHA {
		t.Error("remote hash equals baseline — content should differ")
	}
	if res.CurrentContentSHA256 != baselineSHA {
		t.Errorf("CurrentContentSHA256 = %q, want baseline %q", res.CurrentContentSHA256, baselineSHA)
	}

	// A new pending upstream version was published (now 2 upstream versions).
	assertUpstreamVersionCount(t, reg, "deploy-helper", 2)

	got, _ := srcStore.Get(ctx, src.ID)
	if got.LastRemoteCommit != "commit2" {
		t.Errorf("cursor commit = %q, want commit2", got.LastRemoteCommit)
	}
}

func TestCheck_NoBaseline_TreatedAsUpdate(t *testing.T) {
	checker, srcStore, reg, fetcher, _, ctx := openCheckerTest(t)
	// Create a bound source with NO upstream baseline version at all.
	src, err := srcStore.Create(ctx, Source{
		Name:    "fresh-skill",
		Type:    TypeGitRepo,
		URL:     "https://example.com/acme/skills",
		RefType: RefBranch,
		RefName: "main",
		Subdir:  "fresh-skill",
		// no LastRemoteCommit → never checked
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}

	files := skillFiles("fresh-skill", "body")
	fetcher.lsCommit = "commitA"
	fetcher.result = fetchResult(t, files, "commitA")

	res, err := checker.Check(ctx, src.ID, later)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.State != StateUpdateAvailable {
		t.Fatalf("state = %q, want update_available (no baseline ⇒ first snapshot is an update)", res.State)
	}
	if res.PendingVersionID == "" {
		t.Error("expected a published pending version")
	}
	if res.CurrentContentSHA256 != "" {
		t.Errorf("CurrentContentSHA256 = %q, want empty (no baseline)", res.CurrentContentSHA256)
	}
	assertUpstreamVersionCount(t, reg, "fresh-skill", 1)
}

func TestCheck_LsRemoteFails_CheckFailed_CursorCommitUnchanged(t *testing.T) {
	checker, srcStore, reg, fetcher, _, ctx := openCheckerTest(t)
	files := skillFiles("deploy-helper", "body")
	src, _ := bindSource(t, srcStore, reg, "deploy-helper", files, "commit1")

	fetcher.lsErr = errors.New("network down")

	res, err := checker.Check(ctx, src.ID, later)
	if !errors.Is(err, fetcher.lsErr) {
		t.Fatalf("err = %v, want wrapped network error", err)
	}
	if res.State != StateCheckFailed {
		t.Fatalf("state = %q, want check_failed", res.State)
	}
	// last_checked_at advances (truthful "checked N ago") but the commit
	// cursor must NOT move, so the next run re-attempts from commit1.
	got, _ := srcStore.Get(ctx, src.ID)
	if got.LastRemoteCommit != "commit1" {
		t.Errorf("cursor commit = %q, want commit1 (must not advance on failure)", got.LastRemoteCommit)
	}
	if !got.LastCheckedAt.Equal(later) {
		t.Errorf("last_checked_at = %v, want %v (failures still update the check time)", got.LastCheckedAt, later)
	}
}

func TestCheck_FetchFails_CheckFailed_CursorCommitUnchanged(t *testing.T) {
	checker, srcStore, reg, fetcher, _, ctx := openCheckerTest(t)
	files := skillFiles("deploy-helper", "body")
	src, _ := bindSource(t, srcStore, reg, "deploy-helper", files, "commit1")

	fetcher.lsCommit = "commit2" // commit moved, so we proceed to fetch
	fetcher.fetchErr = ErrSubdirNotFound

	res, err := checker.Check(ctx, src.ID, later)
	if !errors.Is(err, ErrSubdirNotFound) {
		t.Fatalf("err = %v, want ErrSubdirNotFound", err)
	}
	if res.State != StateCheckFailed {
		t.Fatalf("state = %q, want check_failed", res.State)
	}
	got, _ := srcStore.Get(ctx, src.ID)
	if got.LastRemoteCommit != "commit1" {
		t.Errorf("cursor commit = %q, want commit1 (fetch failure must not advance commit)", got.LastRemoteCommit)
	}
	assertUpstreamVersionCount(t, reg, "deploy-helper", 1) // no pending published
}

func TestCheck_NotFetchableType(t *testing.T) {
	checker, srcStore, _, _, _, ctx := openCheckerTest(t)
	src, err := srcStore.Create(ctx, Source{
		Name: "local-skill",
		Type: TypeLocalImport, // not a fetchable git source
	}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	res, err := checker.Check(ctx, src.ID, later)
	if !errors.Is(err, ErrCheckNotBound) {
		t.Fatalf("err = %v, want ErrCheckNotBound", err)
	}
	if res.State != StateCheckFailed {
		t.Errorf("state = %q, want check_failed", res.State)
	}
}

func TestCheck_SourceNotFound(t *testing.T) {
	checker, _, _, _, _, ctx := openCheckerTest(t)
	_, err := checker.Check(ctx, "src_does_not_exist", later)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCheck_Idempotent_SameChangeReObserved(t *testing.T) {
	// Re-checking after an update_available, with the SAME changed content
	// still on the remote, must not keep minting duplicate versions:
	// PublishFromFiles dedups on (name, content_sha256). The second check
	// short-circuits at up_to_date because the cursor advanced.
	checker, srcStore, reg, fetcher, _, ctx := openCheckerTest(t)
	baseline := skillFiles("deploy-helper", "v1")
	src, _ := bindSource(t, srcStore, reg, "deploy-helper", baseline, "commit1")

	changed := skillFiles("deploy-helper", "v2")
	fetcher.lsCommit = "commit2"
	fetcher.result = fetchResult(t, changed, "commit2")

	if _, err := checker.Check(ctx, src.ID, later); err != nil {
		t.Fatalf("first check: %v", err)
	}
	assertUpstreamVersionCount(t, reg, "deploy-helper", 2)

	// Second check: remote still at commit2 → up_to_date, no new version.
	res, err := checker.Check(ctx, src.ID, later.Add(time.Hour))
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if res.State != StateUpToDate {
		t.Errorf("second check state = %q, want up_to_date", res.State)
	}
	assertUpstreamVersionCount(t, reg, "deploy-helper", 2) // still 2, no dupe
}

// assertUpstreamVersionCount asserts the number of upstream-kind versions
// for a skill name, so tests can prove a pending version was / wasn't
// published.
func assertUpstreamVersionCount(t *testing.T, reg *registry.Store, name string, want int) {
	t.Helper()
	versions, err := reg.ListByName(context.Background(), name)
	if err != nil {
		t.Fatalf("ListByName: %v", err)
	}
	got := 0
	for _, v := range versions {
		if v.Kind == registry.KindUpstream {
			got++
		}
	}
	if got != want {
		t.Errorf("upstream version count = %d, want %d", got, want)
	}
}
