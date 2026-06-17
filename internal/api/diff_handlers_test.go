package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yeluonight/skillfleet/internal/source"
)

// findDiffFile returns the diff entry for a path, or fails.
func findDiffFile(t *testing.T, files []diffFile, path string) diffFile {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("diff file %q not found in %+v", path, files)
	return diffFile{}
}

// stageUpstreamUpdate primes the fake fetcher to return the given files at a
// new commit, so a subsequent check-updates publishes a pending upstream
// version a diff can compare against.
func stageUpstreamUpdate(t *testing.T, files []source.FetchedFile, fetcher *fakeFetcher, commit string) {
	t.Helper()
	fetcher.lsCommit = commit
	fetcher.result = source.FetchResult{
		Commit:   commit,
		Files:    files,
		Manifest: manifestForFiles(t, files),
	}
}

func TestUpstreamDiff_NotBound(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	seedSkill(t, reg, "lonely")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/lonely/upstream-diff", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (not bound)", resp.StatusCode)
	}
}

func TestUpstreamDiff_NoPendingUpdate(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	// Only the baseline upstream exists → no update to diff.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/deploy-helper/upstream-diff", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got upstreamDiffResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.HasUpdate {
		t.Errorf("has_update = true, want false (baseline only)")
	}
	if len(got.Files) != 0 {
		t.Errorf("files = %d, want 0", len(got.Files))
	}
}

// TestUpstreamDiff_ModifiedFile: bind, then a check publishes a pending
// version with a changed SKILL.md. The diff must report SKILL.md modified
// with both sides' content, and run.sh unchanged (omitted, counted).
func TestUpstreamDiff_ModifiedFile(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	// Pending: SKILL.md body changes, run.sh stays identical to the baseline.
	changed := []source.FetchedFile{
		{Path: "SKILL.md", Content: []byte("---\nname: deploy-helper\ndescription: upstream skill\n---\n\n# deploy-helper\n\nNEW upstream body\n")},
		{Path: "run.sh", Content: []byte("#!/bin/sh\necho hi\n")},
	}
	stageUpstreamUpdate(t, changed, fetcher, "commit2")
	checkReq := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/check-updates", nil)
	checkResp := authedDo(t, sc, cc, checkReq)
	checkResp.Body.Close()
	if checkResp.StatusCode != http.StatusOK {
		t.Fatalf("check status = %d, want 200", checkResp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/deploy-helper/upstream-diff", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got upstreamDiffResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.HasUpdate {
		t.Fatal("has_update = false, want true")
	}
	if got.BaseVersionID == "" || got.TargetVersionID == "" || got.BaseVersionID == got.TargetVersionID {
		t.Errorf("base/target ids wrong: base=%q target=%q", got.BaseVersionID, got.TargetVersionID)
	}
	// Only SKILL.md changed; run.sh is unchanged (omitted + counted).
	if len(got.Files) != 1 {
		t.Fatalf("changed files = %d, want 1 (SKILL.md): %+v", len(got.Files), got.Files)
	}
	skillMd := findDiffFile(t, got.Files, "SKILL.md")
	if skillMd.Status != diffModified {
		t.Errorf("SKILL.md status = %q, want modified", skillMd.Status)
	}
	if !skillMd.Editable {
		t.Error("SKILL.md should be editable text (line diff meaningful)")
	}
	if skillMd.BaseContent == "" || skillMd.TargetContent == "" {
		t.Error("modified text file must carry both sides' content")
	}
	if skillMd.BaseContent == skillMd.TargetContent {
		t.Error("base and target content identical for a modified file")
	}
	if got.Unchanged != 1 {
		t.Errorf("unchanged = %d, want 1 (run.sh)", got.Unchanged)
	}
}

// TestUpstreamDiff_AddedAndRemoved: pending adds a file and drops one.
func TestUpstreamDiff_AddedAndRemoved(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	// Baseline had SKILL.md + run.sh. Pending drops run.sh, adds docs.md,
	// keeps SKILL.md byte-identical.
	changed := []source.FetchedFile{
		{Path: "SKILL.md", Content: []byte("---\nname: deploy-helper\ndescription: upstream skill\n---\n\n# deploy-helper\n")},
		{Path: "docs.md", Content: []byte("# docs\n\nnew file\n")},
	}
	stageUpstreamUpdate(t, changed, fetcher, "commit2")
	checkReq := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/check-updates", nil)
	authedDo(t, sc, cc, checkReq).Body.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/deploy-helper/upstream-diff", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got upstreamDiffResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.HasUpdate {
		t.Fatal("has_update = false, want true")
	}
	docs := findDiffFile(t, got.Files, "docs.md")
	if docs.Status != diffAdded {
		t.Errorf("docs.md status = %q, want added", docs.Status)
	}
	if docs.BasePresent || !docs.TargetPresent {
		t.Errorf("docs.md presence wrong: base=%v target=%v", docs.BasePresent, docs.TargetPresent)
	}
	if docs.TargetContent == "" {
		t.Error("added file should carry target content")
	}
	run := findDiffFile(t, got.Files, "run.sh")
	if run.Status != diffRemoved {
		t.Errorf("run.sh status = %q, want removed", run.Status)
	}
	if !run.BasePresent || run.TargetPresent {
		t.Errorf("run.sh presence wrong: base=%v target=%v", run.BasePresent, run.TargetPresent)
	}
	// SKILL.md unchanged.
	if got.Unchanged != 1 {
		t.Errorf("unchanged = %d, want 1 (SKILL.md)", got.Unchanged)
	}
}

func TestUpstreamDiff_RequiresAuth(t *testing.T) {
	srv, _, _, _, _ := newTestServerWithSource(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/x/upstream-diff", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
