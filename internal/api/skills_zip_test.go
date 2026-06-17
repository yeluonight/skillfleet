package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// zipReq builds a POST /api/skills request carrying a zip body.
func zipReq(t *testing.T, url string, files map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/zip")
	return req
}

func TestCreateSkill_FromZip_NameFromSkillMD(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req := zipReq(t, srv.URL+"/api/skills", map[string]string{
		"SKILL.md":       "---\nname: zipped-skill\ndescription: from zip\n---\n# Z\n",
		"scripts/run.sh": "#!/bin/sh\necho hi\n",
		"refs/notes.md":  "notes\n",
	})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got skillDetailView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "zipped-skill" {
		t.Errorf("name = %q, want zipped-skill (from SKILL.md)", got.Name)
	}
	if len(got.Versions) != 1 || got.Versions[0].Kind != "import" {
		t.Fatalf("version = %+v", got.Versions)
	}
	if got.Versions[0].FileCount != 3 {
		t.Errorf("file count = %d, want 3", got.Versions[0].FileCount)
	}
}

func TestCreateSkill_FromZip_NameFromQuery(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	// SKILL.md name differs; the ?name= query param wins.
	req := zipReq(t, srv.URL+"/api/skills?name=override", map[string]string{
		"SKILL.md": "---\nname: ignored\n---\nx\n",
	})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got skillDetailView
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Name != "override" {
		t.Errorf("name = %q, want override (from query)", got.Name)
	}
}

func TestCreateSkill_FromZip_StripsTopDir(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req := zipReq(t, srv.URL+"/api/skills", map[string]string{
		"wrapper/SKILL.md":      "---\nname: unwrapped\n---\nx\n",
		"wrapper/scripts/go.sh": "#!/bin/sh\n",
	})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got skillDetailView
	json.NewDecoder(resp.Body).Decode(&got)

	// The SKILL.md must be at the package root post-strip.
	v, err := reg.Get(t.Context(), got.Versions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	foundRoot := false
	for _, f := range v.Manifest.Files {
		if f.Path == "SKILL.md" {
			foundRoot = true
		}
		if f.Path == "wrapper/SKILL.md" {
			t.Error("top dir not stripped on import")
		}
	}
	if !foundRoot {
		t.Error("SKILL.md not at root after import strip")
	}
}

func TestCreateSkill_FromZip_DuplicateConflict(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	mk := func() *http.Response {
		return authedDo(t, sc, cc, zipReq(t, srv.URL+"/api/skills", map[string]string{
			"SKILL.md": "---\nname: dup-zip\n---\nx\n",
		}))
	}
	r1 := mk()
	r1.Body.Close()
	if r1.StatusCode != http.StatusCreated {
		t.Fatalf("first import = %d", r1.StatusCode)
	}
	r2 := mk()
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusConflict {
		t.Errorf("duplicate import = %d, want 409", r2.StatusCode)
	}
}

func TestCreateSkill_FromZip_NoNameRejected(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	// No SKILL.md and no ?name= → can't determine a name.
	req := zipReq(t, srv.URL+"/api/skills", map[string]string{
		"README.md": "# hello\n",
	})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateSkill_FromZip_PathEscapeRejected(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req := zipReq(t, srv.URL+"/api/skills?name=evil", map[string]string{
		"../escape.txt": "pwn",
	})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for path escape", resp.StatusCode)
	}
}
