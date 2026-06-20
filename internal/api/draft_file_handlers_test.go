package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// createDraftForTest opens a blank draft via the API and returns its id.
func createDraftForTest(t *testing.T, srv string, sc, cc *http.Cookie, name string) string {
	t.Helper()
	req := newJSONReq(t, http.MethodPost, srv+"/api/skill-drafts", map[string]string{"name": name})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	var v draftView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func TestDraftFile_CreateThenConflict(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "files")

	url := srv.URL + "/api/skill-drafts/" + id + "/files/scripts/run.sh"
	req := newJSONReq(t, http.MethodPost, url, map[string]string{"content": "#!/bin/sh\necho hi\n"})
	resp := authedDo(t, sc, cc, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d", resp.StatusCode)
	}

	// POST same path again → 409.
	req2 := newJSONReq(t, http.MethodPost, url, map[string]string{"content": "dup"})
	resp2 := authedDo(t, sc, cc, req2)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Errorf("duplicate create = %d, want 409", resp2.StatusCode)
	}
}

func TestDraftFile_PutReplaces(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "files")

	url := srv.URL + "/api/skill-drafts/" + id + "/files/SKILL.md"
	req := newJSONReq(t, http.MethodPut, url,
		map[string]string{"content": "---\nname: files\ndescription: edited\n---\n# Edited\n"})
	resp := authedDo(t, sc, cc, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put = %d", resp.StatusCode)
	}

	greq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skill-drafts/"+id, nil)
	gresp := authedDo(t, sc, cc, greq)
	defer gresp.Body.Close()
	var got draftView
	if err := json.NewDecoder(gresp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range got.Files {
		if f.Path == "SKILL.md" {
			found = true
			if !strings.Contains(f.Content, "description: edited") {
				t.Errorf("SKILL.md not replaced: %q", f.Content)
			}
		}
	}
	if !found {
		t.Error("SKILL.md missing after PUT")
	}
}

func TestDraftFile_Delete(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "files")

	url := srv.URL + "/api/skill-drafts/" + id + "/files/extra.md"
	authedDo(t, sc, cc, newJSONReq(t, http.MethodPost, url, map[string]string{"content": "x\n"})).Body.Close()

	dreq, _ := http.NewRequest(http.MethodDelete, url, nil)
	dresp := authedDo(t, sc, cc, dreq)
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", dresp.StatusCode)
	}

	// Second delete → 404.
	dreq2, _ := http.NewRequest(http.MethodDelete, url, nil)
	dresp2 := authedDo(t, sc, cc, dreq2)
	defer dresp2.Body.Close()
	if dresp2.StatusCode != http.StatusNotFound {
		t.Errorf("re-delete = %d, want 404", dresp2.StatusCode)
	}
}

func TestDeleteDraft(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "doomed")

	dreq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/skill-drafts/"+id, nil)
	dresp := authedDo(t, sc, cc, dreq)
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete draft = %d, want 204", dresp.StatusCode)
	}

	greq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skill-drafts/"+id, nil)
	gresp := authedDo(t, sc, cc, greq)
	defer gresp.Body.Close()
	if gresp.StatusCode != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", gresp.StatusCode)
	}
}

func TestDraftFile_RequiresCSRF(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "files")

	// POST with session but no CSRF header → 403.
	url := srv.URL + "/api/skill-drafts/" + id + "/files/x.md"
	req := newJSONReq(t, http.MethodPost, url, map[string]string{"content": "x"})
	req.AddCookie(sc) // session only; deliberately omit the CSRF header
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("no-csrf = %d, want 403", resp.StatusCode)
	}
}
