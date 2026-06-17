package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/skill"
)

// fakeRegistry implements RegistryReader over an in-memory version map.
// ArchiveAbsPath returns a real temp file path so the planner's os.Stat
// (for archive size) succeeds.
type fakeRegistry struct {
	versions map[string]VersionRef
	archive  string // path to a temp file standing in for the .tgz
}

func (f fakeRegistry) GetVersion(_ context.Context, id string) (VersionRef, error) {
	v, ok := f.versions[id]
	if !ok {
		return VersionRef{}, ErrPlanNoVersion
	}
	return v, nil
}

func (f fakeRegistry) ArchiveAbsPath(VersionRef) string { return f.archive }

func newFakeRegistry(t *testing.T, v VersionRef) fakeRegistry {
	t.Helper()
	arc := filepath.Join(t.TempDir(), "pkg.tgz")
	// 42 bytes so plan.ArchiveBytes is a recognisable non-zero value.
	if err := os.WriteFile(arc, make([]byte, 42), 0o644); err != nil {
		t.Fatal(err)
	}
	return fakeRegistry{versions: map[string]VersionRef{v.ID: v}, archive: arc}
}

func sampleVersion() VersionRef {
	return VersionRef{
		ID:            "sv_1",
		Name:          "deploy-helper",
		BaseVersionID: "sv_0",
		ContentSHA256: "treehash123",
		PackagePath:   "packages/aaaabbbbcccc.tgz",
		Manifest: skill.Manifest{
			Name:          "deploy-helper",
			ContentSHA256: "treehash123",
			Files: []skill.File{
				{Path: "SKILL.md", SHA256: "sha-skillmd", Size: 120, Exec: false, Binary: false},
				{Path: "scripts/deploy.py", SHA256: "sha-deploy", Size: 300, Exec: true, Binary: false},
			},
		},
	}
}

// TestPlanInstall_BuildsPlan: a valid install request yields a plan
// whose content sha, archive sha (from the package path basename),
// download path, file list, and marker all reflect the version.
func TestPlanInstall_BuildsPlan(t *testing.T) {
	v := sampleVersion()
	reg := newFakeRegistry(t, v)
	p := NewPlanner(reg)

	src := &MarkerSource{Type: "github_repo", URL: "https://example/x", Commit: "c1"}
	plan, err := p.PlanInstall(context.Background(), Request{
		Operation: OpInstall,
		SkillName: "deploy-helper",
		VersionID: "sv_1",
		Target:    Target{ToolKey: "claude-code", Scope: "user", RootID: "r1"},
	}, src, time.UnixMilli(1))
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}

	if plan.VersionID != "sv_1" || plan.SkillName != "deploy-helper" {
		t.Errorf("plan identity wrong: %+v", plan)
	}
	if plan.ContentSHA256 != "treehash123" {
		t.Errorf("content sha = %q", plan.ContentSHA256)
	}
	if plan.ArchiveSHA256 != "aaaabbbbcccc" {
		t.Errorf("archive sha = %q, want basename without .tgz", plan.ArchiveSHA256)
	}
	if plan.ArchiveBytes != 42 {
		t.Errorf("archive bytes = %d, want 42 (stat of temp file)", plan.ArchiveBytes)
	}
	if plan.DownloadPath != "/agent/packages/sv_1" {
		t.Errorf("download path = %q", plan.DownloadPath)
	}
	if len(plan.Files) != 2 || plan.Files[1].Path != "scripts/deploy.py" || !plan.Files[1].Exec {
		t.Errorf("files not copied from manifest: %+v", plan.Files)
	}
	if plan.Marker.ManagedBy != "skillfleet" || plan.Marker.InstalledVersionID != "sv_1" ||
		plan.Marker.BaseVersionID != "sv_0" || plan.Marker.Source != src {
		t.Errorf("marker wrong: %+v", plan.Marker)
	}
	// The agent fills these; the planner must leave them empty.
	if len(plan.Marker.Files) != 0 || plan.Marker.InstalledAt != "" {
		t.Errorf("planner pre-filled agent-owned marker fields: %+v", plan.Marker)
	}
}

// TestPlanInstall_NameMismatch: a version id that belongs to a different
// skill than the request names is rejected, so bytes can't be installed
// under the wrong name/marker.
func TestPlanInstall_NameMismatch(t *testing.T) {
	v := sampleVersion() // name deploy-helper
	reg := newFakeRegistry(t, v)
	p := NewPlanner(reg)

	_, err := p.PlanInstall(context.Background(), Request{
		Operation: OpInstall,
		SkillName: "some-other-skill",
		VersionID: "sv_1",
	}, nil, time.UnixMilli(1))
	if !errors.Is(err, ErrPlanNameMismatch) {
		t.Errorf("err = %v, want ErrPlanNameMismatch", err)
	}
}

func TestPlanInstall_VersionNotFound(t *testing.T) {
	reg := newFakeRegistry(t, sampleVersion())
	p := NewPlanner(reg)
	_, err := p.PlanInstall(context.Background(), Request{
		Operation: OpInstall, VersionID: "sv_ghost",
	}, nil, time.UnixMilli(1))
	if err != ErrPlanNoVersion {
		t.Errorf("err = %v, want ErrPlanNoVersion", err)
	}
}

// TestPlanInstall_NoArchive: a version with no package path can't be
// planned (nothing to download).
func TestPlanInstall_NoArchive(t *testing.T) {
	v := sampleVersion()
	v.PackagePath = ""
	reg := newFakeRegistry(t, v)
	p := NewPlanner(reg)
	_, err := p.PlanInstall(context.Background(), Request{
		Operation: OpInstall, SkillName: "deploy-helper", VersionID: "sv_1",
	}, nil, time.UnixMilli(1))
	if err != ErrPlanNoArchive {
		t.Errorf("err = %v, want ErrPlanNoArchive", err)
	}
}

func TestPlanInstall_WrongOperation(t *testing.T) {
	reg := newFakeRegistry(t, sampleVersion())
	p := NewPlanner(reg)
	_, err := p.PlanInstall(context.Background(), Request{Operation: OpRollback, VersionID: "sv_1"}, nil, time.UnixMilli(1))
	if err == nil {
		t.Error("PlanInstall accepted a rollback request")
	}
}
