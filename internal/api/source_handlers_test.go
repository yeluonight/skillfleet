package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/ratelimit"
	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/skill"
	"github.com/yeluonight/skillfleet/internal/source"
	"github.com/yeluonight/skillfleet/migrations"
)

// fakeFetcher is an in-memory SourceFetcher for handler tests: it returns
// canned results without touching the network. Each method consults a
// configurable result/error so a test can drive the success path or any
// fetch-error branch.
type fakeFetcher struct {
	lsCommit string
	lsErr    error
	result   source.FetchResult
	fetchErr error
	// lastSubdir records what FetchSubdir was called with, for assertions.
	lastURL    string
	lastSubdir string
	lastRef    source.RemoteRef
}

func (f *fakeFetcher) LsRemote(_ context.Context, _ string, _ source.RemoteRef) (string, error) {
	return f.lsCommit, f.lsErr
}

func (f *fakeFetcher) FetchSubdir(_ context.Context, url string, ref source.RemoteRef, subdir string) (source.FetchResult, error) {
	f.lastURL = url
	f.lastRef = ref
	f.lastSubdir = subdir
	if f.fetchErr != nil {
		return source.FetchResult{}, f.fetchErr
	}
	return f.result, nil
}

// cannedSkillFiles is a minimal valid skill tree the fake returns so the
// baseline publish (skill.Generate over the files) succeeds.
func cannedSkillFiles(name string) []source.FetchedFile {
	md := "---\nname: " + name + "\ndescription: upstream skill\n---\n\n# " + name + "\n"
	return []source.FetchedFile{
		{Path: "SKILL.md", Content: []byte(md)},
		{Path: "run.sh", Content: []byte("#!/bin/sh\necho hi\n")},
	}
}

// newTestServerWithSource is newTestServerWithRegistry plus a source.Store
// and an injectable fake fetcher, for the bind/detach/check-updates
// routes. Returns the fetcher so a test can set its canned result/error.
func newTestServerWithSource(t *testing.T) (*httptest.Server, *sql.DB, *registry.Store, *source.Store, *fakeFetcher) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "sources.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(t.TempDir(), "store")
	reg, err := registry.New(d, storeDir)
	if err != nil {
		t.Fatal(err)
	}
	srcStore := source.New(d)
	fetcher := &fakeFetcher{}
	rate := ratelimit.Rate{Limit: 100, Window: time.Minute}
	srv := httptest.NewServer(NewRouter(Deps{
		DB:         d,
		Now:        time.Now,
		Audit:      audit.New(d, nil, time.Now),
		SessionTTL: time.Hour,
		LoginIP:    rate,
		LoginUser:  rate,
		Registry:   reg,
		Sources:    srcStore,
		Fetcher:    fetcher,
		Deploy:     deploy.New(d),
	}))
	t.Cleanup(func() {
		srv.Close()
		_ = d.Close()
	})
	return srv, d, reg, srcStore, fetcher
}

// seedSkill publishes an initial manual version so a skill "exists" for
// binding. Returns nothing; the skill is addressable by name.
func seedSkill(t *testing.T, reg *registry.Store, name string) {
	t.Helper()
	_, err := reg.PublishFromFiles(context.Background(),
		[]registry.InMemoryFile{{Path: "SKILL.md", Content: []byte("---\nname: " + name + "\ndescription: x\n---\n# " + name + "\n")}},
		registry.PublishParams{Name: name, Kind: registry.KindManual, VersionLabel: "initial"},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("seedSkill: %v", err)
	}
}

func bindBody() map[string]string {
	return map[string]string{
		"source_type": "github_repo",
		"url":         "https://github.com/acme/skills",
		"provider":    "github",
		"owner":       "acme",
		"repo":        "skills",
		"ref_type":    "branch",
		"ref_name":    "main",
		"subdir":      "deploy-helper",
	}
}

func TestBindSource_Success(t *testing.T) {
	srv, d, reg, srcStore, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedSkill(t, reg, "deploy-helper")
	fetcher.result = source.FetchResult{
		Commit: "abc123commit",
		Files:  cannedSkillFiles("deploy-helper"),
	}

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/bind-source", bindBody())
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var got bindSourceResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Source.ID == "" || got.Source.URL != "https://github.com/acme/skills" {
		t.Errorf("source view = %+v", got.Source)
	}
	if got.Source.LastRemoteCommit != "abc123commit" {
		t.Errorf("LastRemoteCommit = %q, want abc123commit", got.Source.LastRemoteCommit)
	}
	if got.Version.Kind != "upstream" {
		t.Errorf("baseline version kind = %q, want upstream", got.Version.Kind)
	}

	// The fetcher saw the requested coordinates.
	if fetcher.lastSubdir != "deploy-helper" || fetcher.lastRef.Name != "main" {
		t.Errorf("fetch called with subdir=%q ref=%+v", fetcher.lastSubdir, fetcher.lastRef)
	}

	// The binding is persisted and the baseline version carries source_id.
	srcs, err := srcStore.ListAll(context.Background())
	if err != nil || len(srcs) != 1 {
		t.Fatalf("ListAll = %v (len %d), want 1 source", err, len(srcs))
	}
	versions, err := reg.ListByName(context.Background(), "deploy-helper")
	if err != nil {
		t.Fatal(err)
	}
	var hasUpstream bool
	for _, v := range versions {
		if v.Kind == registry.KindUpstream && v.SourceID == srcs[0].ID {
			hasUpstream = true
		}
	}
	if !hasUpstream {
		t.Errorf("no upstream version linked to source %q", srcs[0].ID)
	}
}

func TestBindSource_SkillNotFound(t *testing.T) {
	srv, d, _, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	fetcher.result = source.FetchResult{Commit: "x", Files: cannedSkillFiles("ghost")}

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/ghost/bind-source", bindBody())
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestBindSource_AlreadyBound(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedSkill(t, reg, "deploy-helper")
	fetcher.result = source.FetchResult{Commit: "abc", Files: cannedSkillFiles("deploy-helper")}

	// First bind succeeds.
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/bind-source", bindBody())
	resp := authedDo(t, sc, cc, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first bind status = %d, want 201", resp.StatusCode)
	}

	// Second bind is rejected.
	req2 := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/bind-source", bindBody())
	resp2 := authedDo(t, sc, cc, req2)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second bind status = %d, want 409", resp2.StatusCode)
	}
}

func TestBindSource_BadSourceType(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedSkill(t, reg, "deploy-helper")

	body := bindBody()
	body["source_type"] = "zip_upload" // valid enum, but not fetchable
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/bind-source", body)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestBindSource_FetchErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		fetchErr error
		want     int
	}{
		{"bad_url", source.ErrBadURL, http.StatusBadRequest},
		{"bad_subdir", source.ErrBadSubdir, http.StatusBadRequest},
		{"ref_not_found", source.ErrRefNotFound, http.StatusUnprocessableEntity},
		{"subdir_not_found", source.ErrSubdirNotFound, http.StatusUnprocessableEntity},
		{"too_large", source.ErrTooLarge, http.StatusUnprocessableEntity},
		{"too_many_files", source.ErrTooManyFiles, http.StatusUnprocessableEntity},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, d, reg, _, fetcher := newTestServerWithSource(t)
			sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
			seedSkill(t, reg, "deploy-helper")
			fetcher.fetchErr = c.fetchErr

			req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/bind-source", bindBody())
			resp := authedDo(t, sc, cc, req)
			defer resp.Body.Close()
			if resp.StatusCode != c.want {
				t.Fatalf("%s: status = %d, want %d", c.name, resp.StatusCode, c.want)
			}
		})
	}
}

func TestBindSource_RequiresAuth(t *testing.T) {
	srv, _, reg, _, _ := newTestServerWithSource(t)
	seedSkill(t, reg, "deploy-helper")
	// No session cookie at all.
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/bind-source", bindBody())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// --- t8: bind-source/preview (dry-run wizard probe) ---

func TestBindSourcePreview_Success(t *testing.T) {
	srv, d, reg, srcStore, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	// Preview deliberately does NOT require the skill to exist; it's a pure
	// "what's at this URL?" probe. Give the fetcher a real manifest so the
	// response carries an honest content_sha256 / file tree.
	files := cannedSkillFiles("deploy-helper")
	fetcher.result = source.FetchResult{
		Commit:   "previewcommit",
		Files:    files,
		Manifest: manifestForFiles(t, files),
	}

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/bind-source/preview", bindBody())
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got bindPreviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Commit != "previewcommit" {
		t.Errorf("commit = %q, want previewcommit", got.Commit)
	}
	if got.Name != "deploy-helper" || got.Description != "upstream skill" {
		t.Errorf("name/description = %q / %q", got.Name, got.Description)
	}
	if !got.HasSkillMD || got.ContentSHA256 == "" {
		t.Errorf("has_skill_md=%v content_sha256=%q", got.HasSkillMD, got.ContentSHA256)
	}
	if len(got.Files) != 2 {
		t.Errorf("files = %d, want 2 (SKILL.md + run.sh)", len(got.Files))
	}

	// Crucially, a preview writes NOTHING: no binding row, no upstream version.
	srcs, err := srcStore.ListAll(context.Background())
	if err != nil || len(srcs) != 0 {
		t.Errorf("preview created %d source rows, want 0 (dry-run must not persist)", len(srcs))
	}
	if versions, _ := reg.ListByName(context.Background(), "deploy-helper"); len(versions) != 0 {
		t.Errorf("preview published %d versions, want 0", len(versions))
	}
	// The fetcher saw the requested coordinates.
	if fetcher.lastSubdir != "deploy-helper" || fetcher.lastRef.Name != "main" {
		t.Errorf("fetch called with subdir=%q ref=%+v", fetcher.lastSubdir, fetcher.lastRef)
	}
}

func TestBindSourcePreview_FetchErrorMapping(t *testing.T) {
	// Preview reuses writeFetchError, so a missing subdir is a 422 the wizard
	// can show before the user commits — same mapping as the real bind.
	srv, d, _, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	fetcher.fetchErr = source.ErrSubdirNotFound

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/whatever/bind-source/preview", bindBody())
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

func TestBindSourcePreview_BadSourceType(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	body := bindBody()
	body["source_type"] = "zip_upload" // valid enum, not fetchable
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/x/bind-source/preview", body)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestBindSourcePreview_RequiresAuth(t *testing.T) {
	srv, _, _, _, _ := newTestServerWithSource(t)
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/x/bind-source/preview", bindBody())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBindSourcePreview_RequiresCSRF(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/x/bind-source/preview", bindBody())
	req.AddCookie(sc)
	req.AddCookie(cc) // session + csrf cookie present, but no X-CSRF-Token header
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (CSRF)", resp.StatusCode)
	}
}

// --- t6: check-updates / detach-source / detail+summary source state ---

// bindForTest seeds a skill, binds it via the API, and returns the
// baseline upstream version's content_sha256 (from the bind response). A
// check that wants to reproduce the no-skill-change guard sets the
// fetcher's result manifest hash to THIS value so the engine's
// content_sha256 comparison sees an identical tree.
func bindForTest(t *testing.T, srv *httptest.Server, sc, cc *http.Cookie, reg *registry.Store, fetcher *fakeFetcher, name string) string {
	t.Helper()
	seedSkill(t, reg, name)
	fetcher.result = source.FetchResult{Commit: "commit1", Files: cannedSkillFiles(name)}
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/"+name+"/bind-source", bindBody())
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("bindForTest %s: status %d, body=%s", name, resp.StatusCode, body)
	}
	var got bindSourceResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Version.ContentSHA256 == "" {
		t.Fatal("bindForTest: baseline content_sha256 empty")
	}
	return got.Version.ContentSHA256
}

func TestCheckUpdates_UpToDate(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	// Remote commit unchanged since bind (commit1) → up_to_date, no fetch.
	fetcher.lsCommit = "commit1"
	fetcher.result = source.FetchResult{} // would corrupt if fetched; asserts skip

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/check-updates", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got checkUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.UpstreamState != string(source.StateUpToDate) {
		t.Errorf("state = %q, want up_to_date", got.UpstreamState)
	}
	if got.PendingVersionID != "" {
		t.Errorf("pending_version_id = %q, want empty", got.PendingVersionID)
	}
}

func TestCheckUpdates_UpdateAvailable(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	// Remote moved AND content differs: fetch returns different files with a
	// fresh (real) manifest hash so the engine publishes a pending version.
	changed := []source.FetchedFile{
		{Path: "SKILL.md", Content: []byte("---\nname: deploy-helper\ndescription: upstream skill\n---\n\n# deploy-helper\n\nNEW upstream body\n")},
		{Path: "run.sh", Content: []byte("#!/bin/sh\necho hi\n")},
	}
	fetcher.lsCommit = "commit2"
	fetcher.result = source.FetchResult{
		Commit:   "commit2",
		Files:    changed,
		Manifest: manifestForFiles(t, changed),
	}

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/check-updates", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got checkUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.UpstreamState != string(source.StateUpdateAvailable) {
		t.Fatalf("state = %q, want update_available", got.UpstreamState)
	}
	if got.PendingVersionID == "" {
		t.Error("update_available but no pending_version_id")
	}
	// A new upstream version was published (baseline + pending = 2).
	assertUpstreamCount(t, reg, "deploy-helper", 2)
}

// TestCheckUpdates_NoSkillChange is the CORE GUARD at the HTTP boundary:
// the repo commit moved (commit1→commit2) but the fetched subtree hashes
// identically to the baseline, so the response MUST be
// remote_changed_no_skill_change with NO pending version — never a false
// update_available.
func TestCheckUpdates_NoSkillChange(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	baselineSHA := bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	// Commit advanced, but the fetched content is byte-identical to the
	// baseline → its manifest hash equals baselineSHA.
	same := cannedSkillFiles("deploy-helper")
	fetcher.lsCommit = "commit2"
	fetcher.result = source.FetchResult{
		Commit:   "commit2",
		Files:    same,
		Manifest: skill.Manifest{ContentSHA256: baselineSHA},
	}

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/check-updates", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got checkUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.UpstreamState != string(source.StateRemoteChangedNoSkillChange) {
		t.Fatalf("state = %q, want remote_changed_no_skill_change (false-update guard breached at HTTP layer!)", got.UpstreamState)
	}
	if got.PendingVersionID != "" {
		t.Errorf("pending_version_id = %q, want empty (no update)", got.PendingVersionID)
	}
	// No pending version published — still just the baseline.
	assertUpstreamCount(t, reg, "deploy-helper", 1)
}

func TestCheckUpdates_NotBound(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedSkill(t, reg, "lonely") // exists but never bound

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/lonely/check-updates", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestCheckUpdates_RequiresAuth(t *testing.T) {
	srv, _, _, _, _ := newTestServerWithSource(t)
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/x/check-updates", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestCheckUpdates_RequiresCSRF(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	// Send the session cookie but omit the X-CSRF-Token header.
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/check-updates", nil)
	req.AddCookie(sc)
	req.AddCookie(cc)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (CSRF)", resp.StatusCode)
	}
}

func TestDetachSource_Success(t *testing.T) {
	srv, d, reg, srcStore, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/detach-source", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Binding row is gone.
	if _, err := srcStore.GetBySkillName(context.Background(), "deploy-helper"); !errors.Is(err, source.ErrNotFound) {
		t.Errorf("GetBySkillName after detach = %v, want ErrNotFound", err)
	}
	// The upstream baseline version is RETAINED as a historical snapshot.
	assertUpstreamCount(t, reg, "deploy-helper", 1)

	// Re-detach is a 404 (nothing left to detach).
	req2 := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/detach-source", nil)
	resp2 := authedDo(t, sc, cc, req2)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("second detach status = %d, want 404", resp2.StatusCode)
	}
}

func TestDetachSource_NotBound(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedSkill(t, reg, "lonely")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/lonely/detach-source", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetSkill_WithSource(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/deploy-helper", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got skillDetailView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.SourceState != "bound" {
		t.Errorf("source_state = %q, want bound", got.SourceState)
	}
	if got.Source == nil || got.Source.URL != "https://github.com/acme/skills" {
		t.Errorf("source view = %+v", got.Source)
	}
	if got.LastCheckedAt == 0 {
		t.Error("last_checked_at not set for a bound skill")
	}
}

func TestGetSkill_Unbound(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedSkill(t, reg, "lonely")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/lonely", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got skillDetailView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.SourceState != "unbound" {
		t.Errorf("source_state = %q, want unbound", got.SourceState)
	}
	if got.Source != nil {
		t.Errorf("unbound skill has source = %+v, want nil", got.Source)
	}
}

func TestListSkills_SourceColumn(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")
	seedSkill(t, reg, "lonely") // unbound

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got struct {
		Skills []skillSummaryView `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, s := range got.Skills {
		states[s.Name] = s.SourceState
	}
	if states["deploy-helper"] != "bound" {
		t.Errorf("deploy-helper source_state = %q, want bound", states["deploy-helper"])
	}
	if states["lonely"] != "unbound" {
		t.Errorf("lonely source_state = %q, want unbound", states["lonely"])
	}
}

// manifestForFiles computes the real manifest (and content_sha256) for a
// file set the way the production fetcher does, so update_available tests
// supply a genuine hash rather than a hand-picked string.
func manifestForFiles(t *testing.T, files []source.FetchedFile) skill.Manifest {
	t.Helper()
	tmp := t.TempDir()
	for _, f := range files {
		dest := filepath.Join(tmp, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, f.Content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := skill.Generate(tmp)
	if err != nil {
		t.Fatalf("skill.Generate: %v", err)
	}
	return m
}

// assertUpstreamCount asserts how many upstream-kind versions a skill has,
// so detach/check tests can prove a pending version was / wasn't created
// and that detach retains the baseline.
func assertUpstreamCount(t *testing.T, reg *registry.Store, name string, want int) {
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
