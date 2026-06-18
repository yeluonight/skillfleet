package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// putDraftFile is a helper to replace a draft file's content via PUT.
func putDraftFile(t *testing.T, srv string, sc, cc *http.Cookie, id, path, content string) {
	t.Helper()
	req := newJSONReq(t, http.MethodPut, srv+"/api/skill-drafts/"+id+"/files/"+path,
		map[string]string{"content": content})
	resp := authedDo(t, sc, cc, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put %s = %d", path, resp.StatusCode)
	}
}

func TestValidateDraft_CleanOK(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "valid-skill")
	putDraftFile(t, srv.URL, sc, cc, id, "SKILL.md", "---\nname: valid-skill\ndescription: d\n---\n# x\n")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts/"+id+"/validate", map[string]string{})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got validateResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.OK {
		t.Errorf("ok = false, issues = %+v", got.Issues)
	}
}

func TestValidateDraft_ReportsErrors(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "real")
	putDraftFile(t, srv.URL, sc, cc, id, "SKILL.md", "---\nname: mismatch\n---\nx\n")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts/"+id+"/validate", map[string]string{})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var got validateResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if got.OK {
		t.Error("ok = true, want false for name mismatch")
	}
	if len(got.Issues) == 0 {
		t.Error("expected issues")
	}
}

func TestPublishDraft_HappyPath(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "shippable")
	putDraftFile(t, srv.URL, sc, cc, id, "SKILL.md", "---\nname: shippable\ndescription: ready\n---\n# Ship\n")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts/"+id+"/publish", map[string]string{})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got publishResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.VersionID == "" || got.Name != "shippable" {
		t.Errorf("publish response = %+v", got)
	}
	// The version is now in the registry.
	if _, err := reg.Get(t.Context(), got.VersionID); err != nil {
		t.Errorf("published version not in registry: %v", err)
	}
}

func TestPublishDraft_BlockedByErrors(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "right")
	putDraftFile(t, srv.URL, sc, cc, id, "SKILL.md", "---\nname: wrong\n---\nx\n")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts/"+id+"/publish", map[string]string{})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	var got validateResponse
	json.NewDecoder(resp.Body).Decode(&got)
	if got.OK {
		t.Error("ok = true, want false")
	}
}

func TestPublishDraft_SecondPublishConflict(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "once-only")
	putDraftFile(t, srv.URL, sc, cc, id, "SKILL.md", "---\nname: once-only\ndescription: d\n---\nx\n")

	first := authedDo(t, sc, cc, newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts/"+id+"/publish", map[string]string{}))
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first publish = %d", first.StatusCode)
	}
	second := authedDo(t, sc, cc, newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts/"+id+"/publish", map[string]string{}))
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Errorf("second publish = %d, want 409", second.StatusCode)
	}
}

func TestPublishDraft_Unknown404(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts/dft_ghost/publish", map[string]string{})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
