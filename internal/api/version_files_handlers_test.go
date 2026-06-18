package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/skill"
)

// publishVersion is a test helper that publishes a version directly via
// the registry (bypassing the create-skill API) so file-tree tests can
// control the exact file set.
func publishVersion(t *testing.T, reg *registry.Store, name string, files []registry.InMemoryFile) registry.Version {
	t.Helper()
	v, err := reg.PublishFromFiles(context.Background(), files,
		registry.PublishParams{Name: name, Kind: registry.KindManual}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVersionFiles_ReturnsTree(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	big := strings.Repeat("x", int(skill.MaxEditableBytes)+10)
	v := publishVersion(t, reg, "demo", []registry.InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: demo\n---\n# Demo\n")},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\necho hi\n")},
		{Path: "blob.bin", Content: []byte{0x00, 0x01, 0x02, 0x03}},
		{Path: "big.txt", Content: []byte(big)},
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skill-versions/"+v.ID+"/files", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		Files []fileTreeEntry `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	byPath := map[string]fileTreeEntry{}
	for _, f := range got.Files {
		byPath[f.Path] = f
	}
	if len(got.Files) != 4 {
		t.Fatalf("files = %d, want 4", len(got.Files))
	}
	if !byPath["blob.bin"].Binary || byPath["blob.bin"].Editable {
		t.Errorf("blob.bin = %+v, want binary+non-editable", byPath["blob.bin"])
	}
	if byPath["big.txt"].Binary || byPath["big.txt"].Editable {
		t.Errorf("big.txt = %+v, want text but non-editable (oversize)", byPath["big.txt"])
	}
	if !byPath["SKILL.md"].Editable {
		t.Errorf("SKILL.md should be editable: %+v", byPath["SKILL.md"])
	}
	if !byPath["scripts/run.sh"].Editable {
		t.Errorf("run.sh should be editable: %+v", byPath["scripts/run.sh"])
	}
}

func TestVersionFile_ReturnsTextContent(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	v := publishVersion(t, reg, "demo", []registry.InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: demo\n---\n# 中文标题\n")},
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skill-versions/"+v.ID+"/files/SKILL.md", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got fileContentView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Editable || got.Encoding != "utf-8" {
		t.Errorf("view = %+v, want editable utf-8", got)
	}
	if !strings.Contains(got.Content, "中文标题") {
		t.Errorf("content missing Chinese text: %q", got.Content)
	}
}

func TestVersionFile_NestedPath(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	v := publishVersion(t, reg, "demo", []registry.InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: demo\n---\nx\n")},
		{Path: "references/deep/notes.md", Content: []byte("nested content\n")},
	})

	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/api/skill-versions/"+v.ID+"/files/references/deep/notes.md", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got fileContentView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "references/deep/notes.md" || !strings.Contains(got.Content, "nested content") {
		t.Errorf("nested file view = %+v", got)
	}
}

func TestVersionFile_BinaryMetadataOnly(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	v := publishVersion(t, reg, "demo", []registry.InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: demo\n---\nx\n")},
		{Path: "img.png", Content: []byte{0x89, 'P', 'N', 'G', 0x00, 0x01}},
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skill-versions/"+v.ID+"/files/img.png", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got fileContentView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Editable || got.Content != "" || !got.Binary {
		t.Errorf("binary view = %+v, want non-editable + no content", got)
	}
}

func TestVersionFile_MissingPath404(t *testing.T) {
	srv, d, reg := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	v := publishVersion(t, reg, "demo", []registry.InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: demo\n---\nx\n")},
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skill-versions/"+v.ID+"/files/nope.md", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestVersionFiles_UnknownVersion404(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skill-versions/sv_nope/files", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestVersionFiles_RequireAuth(t *testing.T) {
	srv, _, _ := newTestServerWithRegistry(t)
	resp, err := http.Get(srv.URL + "/api/skill-versions/sv_x/files")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
