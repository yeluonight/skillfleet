package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/registry"
)

func TestCreateDraft_Blank(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts",
		map[string]string{"name": "blank-skill", "title": "Work in progress"})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got draftView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.Status != "open" || got.Name != "blank-skill" {
		t.Errorf("draft = %+v", got)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "SKILL.md" {
		t.Fatalf("files = %+v, want one SKILL.md", got.Files)
	}
	if !strings.Contains(got.Files[0].Content, "name: blank-skill") {
		t.Errorf("SKILL.md content = %q", got.Files[0].Content)
	}
}

func TestCreateDraft_ForkFromVersion(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	base, err := reg.PublishFromFiles(context.Background(), []registry.InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: forkme\n---\n# Forkme\n")},
		{Path: "notes.md", Content: []byte("notes\n")},
	}, registry.PublishParams{Name: "forkme", Kind: registry.KindManual}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts",
		map[string]string{"base_version_id": base.ID})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got draftView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "forkme" || got.BaseVersionID != base.ID {
		t.Errorf("draft = %+v", got)
	}
	if len(got.Files) != 2 {
		t.Errorf("files = %d, want 2", len(got.Files))
	}
}

func TestCreateDraft_NeedsNameOrBase(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts", map[string]string{})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateDraft_BadName(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts", map[string]string{"name": "bad name"})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateDraft_UnknownBase(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts",
		map[string]string{"base_version_id": "sv_ghost"})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestGetDraft_RoundTrip(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	creq := newJSONReq(t, http.MethodPost, srv.URL+"/api/skill-drafts", map[string]string{"name": "rt"})
	cresp := authedDo(t, sc, cc, creq)
	var created draftView
	json.NewDecoder(cresp.Body).Decode(&created)
	cresp.Body.Close()

	greq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skill-drafts/"+created.ID, nil)
	resp := authedDo(t, sc, cc, greq)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got draftView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.Name != "rt" || len(got.Files) != 1 {
		t.Errorf("draft = %+v", got)
	}
}

func TestGetDraft_NotFound(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skill-drafts/dft_ghost", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestCreateDraft_RequiresAuth(t *testing.T) {
	srv, _, _ := newTestServerWithRegistry(t)
	resp, err := http.Post(srv.URL+"/api/skill-drafts", "application/json", strings.NewReader(`{"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
