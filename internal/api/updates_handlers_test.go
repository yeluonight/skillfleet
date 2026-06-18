package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yeluonight/skillfleet/internal/inventory"
	"github.com/yeluonight/skillfleet/internal/skill"
	"github.com/yeluonight/skillfleet/internal/source"
)

// findDimension returns the dimension with the given key, failing the test if
// absent — every response must carry all six §13.7 dimensions.
func findDimension(t *testing.T, resp updatesResponse, key string) updateDimension {
	t.Helper()
	for _, d := range resp.Dimensions {
		if d.Key == key {
			return d
		}
	}
	t.Fatalf("dimension %q missing from response", key)
	return updateDimension{}
}

// skillManifestWithSHA builds a manifest carrying just a content hash, for
// constructing the "commit moved, content identical" case (the engine
// compares only ContentSHA256 against the baseline).
func skillManifestWithSHA(sha string) skill.Manifest {
	return skill.Manifest{ContentSHA256: sha}
}

// TestUpdates_NoUpdates: a freshly bound skill has only its baseline upstream
// version, so the upstream dimension is empty and the summary count is 0.
func TestUpdates_NoUpdates(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/updates", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got updatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	up := findDimension(t, got, dimUpstreamUpdate)
	if len(up.Items) != 0 {
		t.Errorf("upstream dimension has %d items, want 0 (baseline only)", len(up.Items))
	}
	if got.Summary.UpstreamUpdates != 0 {
		t.Errorf("summary.upstream_updates = %d, want 0", got.Summary.UpstreamUpdates)
	}
}

// TestUpdates_WithUpstreamUpdate: bind, then a check that finds changed
// content publishes a pending upstream version. The skill must now appear in
// the upstream dimension with baseline != pending, and the summary counts 1.
func TestUpdates_WithUpstreamUpdate(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	// A real content change: different SKILL.md body → different manifest hash.
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
	checkReq := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/check-updates", nil)
	checkResp := authedDo(t, sc, cc, checkReq)
	checkResp.Body.Close()
	if checkResp.StatusCode != http.StatusOK {
		t.Fatalf("check-updates status = %d, want 200", checkResp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/updates", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got updatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	up := findDimension(t, got, dimUpstreamUpdate)
	if len(up.Items) != 1 {
		t.Fatalf("upstream dimension has %d items, want 1", len(up.Items))
	}
	item := up.Items[0]
	if item.Name != "deploy-helper" {
		t.Errorf("item name = %q, want deploy-helper", item.Name)
	}
	if item.PendingVersionID == "" || item.BaselineVersionID == "" {
		t.Errorf("missing version ids: baseline=%q pending=%q", item.BaselineVersionID, item.PendingVersionID)
	}
	if item.PendingVersionID == item.BaselineVersionID {
		t.Error("pending == baseline; an update must have a distinct newest version")
	}
	if item.PendingContentSHA256 == "" || item.PendingCreatedAt == 0 {
		t.Errorf("missing pending content/timestamp: sha=%q at=%d", item.PendingContentSHA256, item.PendingCreatedAt)
	}
	if got.Summary.UpstreamUpdates != 1 {
		t.Errorf("summary.upstream_updates = %d, want 1", got.Summary.UpstreamUpdates)
	}
}

// TestUpdates_NoSkillChangeIsNotAnUpdate is the core guard at the Updates
// Page boundary: a check where the commit moved but the content is identical
// publishes NO pending version, so the skill must NOT appear as an update.
func TestUpdates_NoSkillChangeIsNotAnUpdate(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	baselineSHA := bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")

	// Commit moved, content identical (manifest hash == baseline).
	same := cannedSkillFiles("deploy-helper")
	fetcher.lsCommit = "commit2"
	fetcher.result = source.FetchResult{
		Commit:   "commit2",
		Files:    same,
		Manifest: skillManifestWithSHA(baselineSHA),
	}
	checkReq := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/check-updates", nil)
	checkResp := authedDo(t, sc, cc, checkReq)
	checkResp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/updates", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got updatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	up := findDimension(t, got, dimUpstreamUpdate)
	if len(up.Items) != 0 {
		t.Errorf("upstream dimension has %d items, want 0 (commit moved but content same is NOT an update)", len(up.Items))
	}
	if got.Summary.UpstreamUpdates != 0 {
		t.Errorf("summary.upstream_updates = %d, want 0", got.Summary.UpstreamUpdates)
	}
}

// TestUpdates_PlaceholderDimensions: all six dimensions are present. The
// three real ones (upstream_update + the two local dimensions, phase 7 t8)
// are pending=false; the three Phase 8 ones stay pending=true placeholders.
func TestUpdates_PlaceholderDimensions(t *testing.T) {
	srv, d, _, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/updates", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got updatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Dimensions) != 6 {
		t.Fatalf("got %d dimensions, want all 6", len(got.Dimensions))
	}
	// upstream + the two local dimensions are real (pending=false); the three
	// Phase 8 dimensions are placeholders (pending=true).
	realDims := map[string]bool{
		dimUpstreamUpdate:   true,
		dimLocalEdit:        true,
		dimLocalAndUpstream: true,
	}
	for _, dim := range got.Dimensions {
		wantPending := !realDims[dim.Key]
		if dim.Pending != wantPending {
			t.Errorf("dimension %q pending=%v, want %v", dim.Key, dim.Pending, wantPending)
		}
		if dim.Items == nil {
			t.Errorf("dimension %q items is nil, want non-nil slice", dim.Key)
		}
	}
}

func TestUpdates_RequiresAuth(t *testing.T) {
	srv, _, _, _, _ := newTestServerWithSource(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/updates", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestUpdates_LocalEditDimension (phase 7 t8): a device running an edited
// copy of a tracked skill (its content_sha256 matches no registry version)
// surfaces in the "local edits" dimension. A second skill whose sha matches a
// registry version stays clean and must NOT appear — the content_sha256 guard
// at the Updates layer.
func TestUpdates_LocalEditDimension(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	cleanSHA := publishVersion(t, reg, "clean-skill", oneSkillFile("clean-skill", "v1")).ContentSHA256
	publishVersion(t, reg, "edited-skill", oneSkillFile("edited-skill", "v1")) // device sha will differ
	seedDevice(t, d, "dev1")
	landRun(t, d, "dev1", []inventory.Skill{
		{Name: "clean-skill", SkillPath: "/s/clean", HasSkillMD: true, EffectiveState: "on", ContentSHA256: cleanSHA},
		{Name: "edited-skill", SkillPath: "/s/edited", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "sha-edited-locally"},
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/updates", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got updatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	local := findDimension(t, got, dimLocalEdit)
	if local.Pending {
		t.Error("local_edit dimension should be real (pending=false) in phase 7")
	}
	if len(local.Items) != 1 {
		t.Fatalf("local_edit items = %d, want 1 (only the edited skill)", len(local.Items))
	}
	item := local.Items[0]
	if item.Name != "edited-skill" {
		t.Errorf("local edit item = %q, want edited-skill", item.Name)
	}
	if item.DeviceID != "dev1" || item.ToolKey != "claude-code" || item.Scope != "user" {
		t.Errorf("local edit provenance wrong: device=%q tool=%q scope=%q", item.DeviceID, item.ToolKey, item.Scope)
	}
	if item.LocalState != "local_modified" {
		t.Errorf("local_state = %q, want local_modified", item.LocalState)
	}
	if got.Summary.LocalEdits != 1 {
		t.Errorf("summary.local_edits = %d, want 1", got.Summary.LocalEdits)
	}
}

// TestUpdates_CleanDeviceNoLocalEdit is the Updates-layer core guard: a
// device whose content_sha256 matches a registry version is clean, so the
// local_edit dimension is empty even though the device reports the skill.
func TestUpdates_CleanDeviceNoLocalEdit(t *testing.T) {
	srv, d, reg, _, _ := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	// Registry holds two versions; the device runs the newer one's exact sha.
	publishVersion(t, reg, "deploy", oneSkillFile("deploy", "v0"))
	runningSHA := publishVersion(t, reg, "deploy", oneSkillFile("deploy", "v1")).ContentSHA256
	seedDevice(t, d, "dev1")
	landRun(t, d, "dev1", []inventory.Skill{
		{Name: "deploy", SkillPath: "/s/deploy", HasSkillMD: true, EffectiveState: "on", ContentSHA256: runningSHA},
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/updates", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got updatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	local := findDimension(t, got, dimLocalEdit)
	if len(local.Items) != 0 {
		t.Errorf("clean device must not appear as a local edit, got %d items", len(local.Items))
	}
	if got.Summary.LocalEdits != 0 {
		t.Errorf("summary.local_edits = %d, want 0 (sha matches a version)", got.Summary.LocalEdits)
	}
}

// TestUpdates_LocalAndUpstreamDimension: a skill that is BOTH locally
// modified on a device AND has a pending upstream update lands in the
// combined dimension, not the local-only one.
func TestUpdates_LocalAndUpstreamDimension(t *testing.T) {
	srv, d, reg, _, fetcher := newTestServerWithSource(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	// Bind deploy-helper and publish a pending upstream update for it.
	bindForTest(t, srv, sc, cc, reg, fetcher, "deploy-helper")
	changed := []source.FetchedFile{
		{Path: "SKILL.md", Content: []byte("---\nname: deploy-helper\ndescription: upstream skill\n---\n\n# deploy-helper\n\nNEW body\n")},
		{Path: "run.sh", Content: []byte("#!/bin/sh\necho hi\n")},
	}
	fetcher.lsCommit = "commit2"
	fetcher.result = source.FetchResult{Commit: "commit2", Files: changed, Manifest: manifestForFiles(t, changed)}
	authedDo(t, sc, cc, newJSONReq(t, http.MethodPost, srv.URL+"/api/skills/deploy-helper/check-updates", nil)).Body.Close()

	// A device runs a locally-edited deploy-helper (sha matches no version).
	seedDevice(t, d, "dev1")
	landRun(t, d, "dev1", []inventory.Skill{
		{Name: "deploy-helper", SkillPath: "/s/deploy-helper", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "sha-locally-edited"},
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/updates", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got updatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	combined := findDimension(t, got, dimLocalAndUpstream)
	if len(combined.Items) != 1 {
		t.Fatalf("local_and_upstream items = %d, want 1", len(combined.Items))
	}
	if combined.Items[0].Name != "deploy-helper" {
		t.Errorf("combined item = %q, want deploy-helper", combined.Items[0].Name)
	}
	// It must NOT also appear in the local-only dimension.
	localOnly := findDimension(t, got, dimLocalEdit)
	for _, it := range localOnly.Items {
		if it.Name == "deploy-helper" {
			t.Error("deploy-helper is local+upstream; must not also be in local-only")
		}
	}
}
