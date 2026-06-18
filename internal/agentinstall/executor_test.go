package agentinstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/fingerprint"
	"github.com/yeluonight/skillfleet/internal/skill"
)

// buildPackage writes the given files into a temp source dir, packs it
// into a deterministic archive, and returns (archiveBytes, plan) wired
// for an install of skillName into the test's allowed root. The plan's
// ContentSHA256 / ArchiveSHA256 / Files all reflect the real package, so
// a faithful install rescans to a match.
func buildPackage(t *testing.T, skillName string, files map[string]string, execBits map[string]bool) ([]byte, deploy.Plan) {
	t.Helper()
	src := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if execBits[rel] {
			mode = 0o755
		}
		if err := os.WriteFile(full, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}

	// Content fingerprint (what the post-install rescan must match).
	fp, err := fingerprint.Compute(src)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	// Deterministic archive + its sha (download integrity).
	var buf bytes.Buffer
	info, err := skill.Pack(src, &buf)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	specs := make([]deploy.FileSpec, 0, len(fp.Files))
	for _, fe := range fp.Files {
		specs = append(specs, deploy.FileSpec{
			Path: fe.Path, SHA256: fe.Hash, Size: fe.Size, Exec: fe.Exec,
		})
	}

	plan := deploy.Plan{
		VersionID:     "sv_test",
		SkillName:     skillName,
		ContentSHA256: fp.Hash,
		ArchiveSHA256: info.SHA256,
		ArchiveBytes:  info.Bytes,
		DownloadPath:  "/agent/packages/sv_test",
		Marker: deploy.InstallMarker{
			ManagedBy:          "skillfleet",
			SkillName:          skillName,
			InstalledVersionID: "sv_test",
			ContentSHA256:      fp.Hash,
		},
		Files: specs,
	}
	return buf.Bytes(), plan
}

// newExecutor builds an Executor whose single allowed root is rootDir,
// backups go under a temp dir, and the fetcher serves archiveBytes.
func newExecutor(t *testing.T, rootDir string, archiveBytes []byte) (*Executor, deploy.Target) {
	t.Helper()
	cfg := Config{
		BackupsDir: filepath.Join(t.TempDir(), "backups"),
		AllowedRoots: []AllowedRoot{
			{ID: "r1", Tool: "claude-code", Scope: "user", Path: rootDir},
		},
	}
	fetcher := &fakeFetcher{content: archiveBytes}
	now := func() time.Time { return time.UnixMilli(1_700_000_000_000) }
	return NewExecutor(cfg, fetcher, now), deploy.Target{RootID: "r1", ToolKey: "claude-code", Scope: "user"}
}

func liveRead(t *testing.T, rootDir, skillName, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(rootDir, skillName, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// TestExecutor_InstallSucceeds is acceptance #1 (can deploy to a target):
// a faithful install writes the files, the marker, and rescans to the
// planned content sha.
func TestExecutor_InstallSucceeds(t *testing.T) {
	rootDir := t.TempDir()
	files := map[string]string{
		"SKILL.md":       "---\nname: deploy-helper\ndescription: x\n---\n\n# deploy-helper\n",
		"scripts/run.sh": "#!/bin/sh\necho hi\n",
	}
	archive, plan := buildPackage(t, "deploy-helper", files, map[string]bool{"scripts/run.sh": true})
	x, target := newExecutor(t, rootDir, archive)

	res, err := x.Install(context.Background(), plan, target)
	if err != nil {
		t.Fatalf("Install: %v (code=%s)", err, res.ErrorCode)
	}
	if res.RescanContentSHA256 != plan.ContentSHA256 {
		t.Errorf("rescan sha = %q, want plan %q", res.RescanContentSHA256, plan.ContentSHA256)
	}
	if res.RolledBack {
		t.Error("a successful install reported RolledBack")
	}
	if got, ok := liveRead(t, rootDir, "deploy-helper", "SKILL.md"); !ok || got != files["SKILL.md"] {
		t.Errorf("SKILL.md not installed faithfully: %q", got)
	}
	// Marker present (hidden, so the rescan ignored it).
	if _, err := os.Stat(filepath.Join(rootDir, "deploy-helper", ".skillfleet-install.json")); err != nil {
		t.Errorf("install marker missing: %v", err)
	}
}

// TestExecutor_RescanMismatch_AutoRollback is acceptance #2 (install
// failure rolls back). We tamper the plan's ContentSHA256 so the
// post-install rescan can never match; the executor must roll the swap
// back AND restore the backup, leaving the prior install intact.
func TestExecutor_RescanMismatch_AutoRollback(t *testing.T) {
	rootDir := t.TempDir()
	// A prior install exists, with a local edit, so we can prove the
	// rollback restores the exact prior bytes.
	priorSkill := filepath.Join(rootDir, "deploy-helper")
	if err := os.MkdirAll(priorSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(priorSkill, "SKILL.md"), []byte("PRIOR with local edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"SKILL.md": "---\nname: deploy-helper\ndescription: x\n---\n\n# new\n",
	}
	archive, plan := buildPackage(t, "deploy-helper", files, nil)
	// Tamper: the rescan will compute the real sha, which won't equal this.
	plan.ContentSHA256 = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef0"
	plan.Marker.ContentSHA256 = plan.ContentSHA256

	x, target := newExecutor(t, rootDir, archive)
	res, err := x.Install(context.Background(), plan, target)
	if err == nil {
		t.Fatal("tampered install unexpectedly succeeded")
	}
	if res.ErrorCode != codeRescan {
		t.Errorf("error code = %q, want %q", res.ErrorCode, codeRescan)
	}
	if !res.RolledBack {
		t.Error("rescan mismatch did not report RolledBack")
	}
	// The prior install must be intact — exact bytes restored.
	got, ok := liveRead(t, rootDir, "deploy-helper", "SKILL.md")
	if !ok || got != "PRIOR with local edit" {
		t.Errorf("prior install not restored after rollback: got %q ok=%v", got, ok)
	}
}

// TestExecutor_PreservesExtraFiles is acceptance #3 (don't delete
// unmanaged files): a user file the prior install didn't own survives a
// reinstall and is reported as an extra.
func TestExecutor_PreservesExtraFiles(t *testing.T) {
	rootDir := t.TempDir()
	priorSkill := filepath.Join(rootDir, "deploy-helper")
	if err := os.MkdirAll(priorSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	// A prior managed install marker claiming only SKILL.md.
	marker := `{"managed_by":"skillfleet","skill_name":"deploy-helper","files":["SKILL.md"]}`
	if err := os.WriteFile(filepath.Join(priorSkill, ".skillfleet-install.json"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(priorSkill, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A user's hand-added file — NOT in the marker.
	if err := os.WriteFile(filepath.Join(priorSkill, "notes.local.md"), []byte("USER DATA"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"SKILL.md": "---\nname: deploy-helper\ndescription: x\n---\n\n# v2\n",
	}
	archive, plan := buildPackage(t, "deploy-helper", files, nil)
	x, target := newExecutor(t, rootDir, archive)

	res, err := x.Install(context.Background(), plan, target)
	if err != nil {
		t.Fatalf("Install: %v (code=%s)", err, res.ErrorCode)
	}
	// The extra survived the swap.
	if got, ok := liveRead(t, rootDir, "deploy-helper", "notes.local.md"); !ok || got != "USER DATA" {
		t.Errorf("unmanaged extra not preserved: got %q ok=%v", got, ok)
	}
	// And was reported.
	foundExtra := false
	for _, e := range res.ExtraFiles {
		if e == "notes.local.md" {
			foundExtra = true
		}
	}
	if !foundExtra {
		t.Errorf("extra not reported in result: %v", res.ExtraFiles)
	}
}

// TestExecutor_RootNotAllowed: a target that resolves to no allowed root
// is refused before any filesystem write.
func TestExecutor_RootNotAllowed(t *testing.T) {
	rootDir := t.TempDir()
	archive, plan := buildPackage(t, "deploy-helper",
		map[string]string{"SKILL.md": "---\nname: deploy-helper\n---\n# x\n"}, nil)
	x, _ := newExecutor(t, rootDir, archive)

	// A target whose root_id matches nothing.
	res, err := x.Install(context.Background(), plan, deploy.Target{RootID: "nonexistent"})
	if err == nil {
		t.Fatal("install into disallowed root succeeded")
	}
	if res.ErrorCode != codeRoot {
		t.Errorf("error code = %q, want %q", res.ErrorCode, codeRoot)
	}
}

// TestExecutor_BadArchiveSHA: a download whose bytes don't match the
// plan's archive sha is rejected (download_failed), nothing installed.
func TestExecutor_BadArchiveSHA(t *testing.T) {
	rootDir := t.TempDir()
	archive, plan := buildPackage(t, "deploy-helper",
		map[string]string{"SKILL.md": "---\nname: deploy-helper\n---\n# x\n"}, nil)
	plan.ArchiveSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	x, target := newExecutor(t, rootDir, archive)

	res, err := x.Install(context.Background(), plan, target)
	if err == nil {
		t.Fatal("install with bad archive sha succeeded")
	}
	if res.ErrorCode != codeDownload {
		t.Errorf("error code = %q, want %q", res.ErrorCode, codeDownload)
	}
	if _, err := os.Stat(filepath.Join(rootDir, "deploy-helper")); err == nil {
		t.Error("skill dir created despite download failure")
	}
}

// TestExecutor_FirstInstallRescanFailUninstalls: a brand-new install
// (no prior dir) whose rescan fails must leave NOTHING behind — the
// rollback removes the freshly-created skill dir.
func TestExecutor_FirstInstallRescanFailUninstalls(t *testing.T) {
	rootDir := t.TempDir()
	files := map[string]string{"SKILL.md": "---\nname: fresh\ndescription: x\n---\n# x\n"}
	archive, plan := buildPackage(t, "fresh", files, nil)
	plan.ContentSHA256 = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef0"
	plan.Marker.ContentSHA256 = plan.ContentSHA256

	x, target := newExecutor(t, rootDir, archive)
	res, err := x.Install(context.Background(), plan, target)
	if err == nil {
		t.Fatal("tampered first install succeeded")
	}
	if !res.RolledBack {
		t.Error("first-install failure did not report RolledBack")
	}
	if _, serr := os.Stat(filepath.Join(rootDir, "fresh")); serr == nil {
		t.Error("a failed first install left a skill dir behind")
	}
}

// TestExecutor_RollbackJob restores a prior backup via the manual
func TestExecutor_RollbackJob(t *testing.T) {
	rootDir := t.TempDir()
	// Install something first so a backup dir exists to roll back to.
	files := map[string]string{"SKILL.md": "---\nname: deploy-helper\ndescription: x\n---\n# v1\n"}
	archive, plan := buildPackage(t, "deploy-helper", files, nil)
	x, target := newExecutor(t, rootDir, archive)

	// Seed a prior install + backup by hand: write a backup dir holding
	// the "v0" bytes, then mutate live to "v1", then roll back.
	skillDir := filepath.Join(rootDir, "deploy-helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("LIVE v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(t.TempDir(), "bk-v0")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "SKILL.md"), []byte("BACKED UP v0"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = plan // plan/archive unused for the rollback path itself

	res, err := x.Rollback(deploy.RollbackPlan{
		Target:    target,
		SkillName: "deploy-helper",
		BackupDir: backupDir,
	})
	if err != nil {
		t.Fatalf("Rollback: %v (code=%s)", err, res.ErrorCode)
	}
	if !res.RolledBack {
		t.Error("rollback did not set RolledBack")
	}
	if got, ok := liveRead(t, rootDir, "deploy-helper", "SKILL.md"); !ok || got != "BACKED UP v0" {
		t.Errorf("rollback did not restore backup: got %q", got)
	}
}
