package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/agentapi"
	"github.com/yeluonight/skillfleet/internal/agentcfg"
	"github.com/yeluonight/skillfleet/internal/agentclient"
	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/enrollment"
	"github.com/yeluonight/skillfleet/internal/fingerprint"
	"github.com/yeluonight/skillfleet/internal/skill"
	"github.com/yeluonight/skillfleet/migrations"

	"net/http/httptest"
)

// testAgentConfig returns a Load/Save-valid config for tests that only care
// about allowed_roots but now exercise runOneJob's lazy config reload path.
func testAgentConfig(roots []agentcfg.AllowedRoot) agentcfg.Config {
	return agentcfg.Config{
		ServerURL:       "https://sf.example",
		DeviceID:        "dev_test",
		DeviceSecret:    "secret",
		HeartbeatIntSec: agentcfg.DefaultHeartbeatSec,
		InventoryIntSec: agentcfg.DefaultInventorySec,
		JobsIntSec:      agentcfg.DefaultJobsSec,
		AllowedRoots:    roots,
	}
}

type archivePackages struct{ path string }

func (a archivePackages) ArchiveForVersion(string) (*os.File, int64, error) {
	f, err := os.Open(a.path)
	if err != nil {
		return nil, 0, agentapi.ErrPackageNotFound
	}
	info, _ := f.Stat()
	return f, info.Size(), nil
}

// TestRunOneJob_EndToEndInstall is the §17 acceptance #1 end-to-end
// proof: a real downlink server hands the agent an install job, the
// agent downloads the real package, installs it into its allowed root,
// and the skill lands on disk with a marker — exercised through the
// actual agentclient + agentinstall, not mocks.
func TestRunOneJob_EndToEndInstall(t *testing.T) {
	ctx := context.Background()

	// --- build a real package + its plan ---
	src := t.TempDir()
	skillMD := "---\nname: deploy-helper\ndescription: x\n---\n\n# deploy-helper\n"
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	fp, err := fingerprint.Compute(src)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	info, err := skill.Pack(src, &buf)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "pkg.tgz")
	if err := os.WriteFile(archivePath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	specs := make([]deploy.FileSpec, 0, len(fp.Files))
	for _, fe := range fp.Files {
		specs = append(specs, deploy.FileSpec{Path: fe.Path, SHA256: fe.Hash, Size: fe.Size, Exec: fe.Exec})
	}
	plan := deploy.Plan{
		VersionID: "sv_1", SkillName: "deploy-helper",
		ContentSHA256: fp.Hash, ArchiveSHA256: info.SHA256, ArchiveBytes: info.Bytes,
		DownloadPath: "/agent/packages/sv_1",
		Marker:       deploy.InstallMarker{ManagedBy: "skillfleet", SkillName: "deploy-helper", InstalledVersionID: "sv_1", ContentSHA256: fp.Hash},
		Files:        specs,
	}
	planJSON, _ := json.Marshal(plan)

	// --- real downlink server + enrolled device ---
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	tx, _ := d.BeginTx(ctx, nil)
	tok, _ := enrollment.Create(ctx, d, time.Hour, now)
	enrollment.Consume(ctx, tx, tok.Plaintext, now)
	res, err := devices.Enroll(ctx, tx, devices.EnrollInput{Name: "n", OS: "linux", Arch: "amd64"}, now)
	if err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if err := devices.SetStatus(ctx, d, res.Device.ID, devices.StatusApproved); err != nil {
		t.Fatal(err)
	}

	store := deploy.New(d)
	job, err := store.Create(ctx, deploy.CreateParams{
		DeviceID:    res.Device.ID,
		Operation:   deploy.OpInstall,
		RequestJSON: `{"operation":"install","skill_name":"deploy-helper","version_id":"sv_1","target":{"tool_key":"claude-code","scope":"user","root_id":"r1"}}`,
		PlanJSON:    string(planJSON),
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(agentapi.NewRouter(agentapi.Deps{
		DB:       d,
		Now:      func() time.Time { return now },
		Audit:    audit.New(d, nil, func() time.Time { return now }),
		Packages: archivePackages{path: archivePath},
	}))
	t.Cleanup(srv.Close)

	// --- real client + agent config pointed at an allowed root ---
	client, err := agentclient.New(agentclient.Config{
		ServerURL: srv.URL, DeviceID: res.Device.ID, DeviceSecret: res.Secret,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	allowedRoot := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "agent.json")
	cfg := testAgentConfig([]agentcfg.AllowedRoot{{ID: "r1", Tool: "claude-code", Scope: "user", Path: allowedRoot}})
	if err := agentcfg.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	// --- claim the job (as the loop would) and run it ---
	claimed, ok, err := client.Jobs(ctx)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	runOneJob(ctx, log, client, cfgPath, t.TempDir(), claimed)

	// --- the skill must be installed on disk ---
	installed := filepath.Join(allowedRoot, "deploy-helper", "SKILL.md")
	got, rerr := os.ReadFile(installed)
	if rerr != nil {
		t.Fatalf("skill not installed: %v", rerr)
	}
	if string(got) != skillMD {
		t.Errorf("installed content = %q", string(got))
	}
	if _, err := os.Stat(filepath.Join(allowedRoot, "deploy-helper", ".skillfleet-install.json")); err != nil {
		t.Errorf("marker missing: %v", err)
	}

	// --- the job must be recorded succeeded on the server ---
	final, _ := store.Get(ctx, job.ID)
	if final.Status != deploy.StatusSucceeded {
		t.Errorf("job status = %q, want succeeded; result=%s", final.Status, final.ResultJSON)
	}
}

func TestRunRootJobsRegisterRemove(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent.json")
	cfg := agentcfg.Config{
		ServerURL: "https://sf.example", DeviceID: "dev_x", DeviceSecret: "sec",
		HeartbeatIntSec: 30, InventoryIntSec: 300, JobsIntSec: 15,
	}
	if err := agentcfg.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	rootDir := filepath.Join(home, "custom", "skills")
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := runRegisterRootJob(cfgPath, home, deploy.Request{
		Operation: deploy.OpRegisterRoot,
		Target:    deploy.Target{ToolKey: "claude-code", Scope: "user"},
		RootPath:  rootDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ResolvedRootPath != rootDir {
		t.Fatalf("register result = %+v", res)
	}
	cfg, err = agentcfg.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowedRoots) != 1 || cfg.AllowedRoots[0].Path != rootDir {
		t.Fatalf("roots after register = %+v", cfg.AllowedRoots)
	}

	res, err = runRemoveRootJob(cfgPath, deploy.Request{
		Operation: deploy.OpRemoveRoot,
		Target:    deploy.Target{RootID: cfg.AllowedRoots[0].ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ResolvedRootPath != rootDir {
		t.Fatalf("remove result = %+v", res)
	}
	cfg, err = agentcfg.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowedRoots) != 0 {
		t.Fatalf("roots after remove = %+v", cfg.AllowedRoots)
	}
}

// TestRunRegisterRootJob_AutoMkdir verifies that registering a root whose
// directory does not yet exist on disk creates it (mkdir -p) before writing
// allowed_roots — the fix for operators being unable to adopt a tool's own
// skills path before the tool has created the directory.
func TestRunRegisterRootJob_AutoMkdir(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent.json")
	if err := agentcfg.Save(cfgPath, agentcfg.Config{
		ServerURL: "https://sf.example", DeviceID: "dev_x", DeviceSecret: "sec",
		HeartbeatIntSec: 30, InventoryIntSec: 300, JobsIntSec: 15,
	}); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A path under home that does NOT exist yet — no pre-MkdirAll.
	rootDir := filepath.Join(home, "newtool", "skills")
	if _, err := os.Stat(rootDir); !os.IsNotExist(err) {
		t.Fatalf("precondition: %s should not exist, got stat err=%v", rootDir, err)
	}

	res, err := runRegisterRootJob(cfgPath, home, deploy.Request{
		Operation: deploy.OpRegisterRoot,
		Target:    deploy.Target{ToolKey: "newtool", Scope: "user"},
		RootPath:  rootDir,
	})
	if err != nil {
		t.Fatalf("register non-existent root: %v (result %+v)", err, res)
	}
	// Directory was created by the job.
	if info, err := os.Stat(rootDir); err != nil || !info.IsDir() {
		t.Fatalf("root dir not created by job: stat err=%v", err)
	}
	// And it landed in allowed_roots.
	cfg, err := agentcfg.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowedRoots) != 1 || cfg.AllowedRoots[0].Path != rootDir {
		t.Fatalf("roots after auto-mkdir register = %+v", cfg.AllowedRoots)
	}
}

// TestRunRegisterRootJob_MkdirFailed verifies that a root whose parent is not
// writable surfaces a mkdir_failed result instead of silently succeeding or
// crashing — e.g. a system root under a read-only location.
func TestRunRegisterRootJob_MkdirFailed(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent.json")
	if err := agentcfg.Save(cfgPath, agentcfg.Config{
		ServerURL: "https://sf.example", DeviceID: "dev_x", DeviceSecret: "sec",
		HeartbeatIntSec: 30, InventoryIntSec: 300, JobsIntSec: 15,
	}); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Make a read-only parent so MkdirAll of a child cannot create it.
	roParent := filepath.Join(home, "readonly")
	if err := os.MkdirAll(roParent, 0o755); err != nil {
		t.Fatal(err)
	}
	// As the test process, drop write on the parent. Skip the assertion on
	// platforms where chmod is advisory and the root test process can still
	// write (CI often runs as root, where 0o500 is ignored).
	if os.Geteuid() == 0 {
		t.Skip("running as root: read-only chmod is advisory, mkdir would succeed")
	}
	if err := os.Chmod(roParent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(roParent, 0o755) })
	rootDir := filepath.Join(roParent, "child", "skills")

	res, err := runRegisterRootJob(cfgPath, home, deploy.Request{
		Operation: deploy.OpRegisterRoot,
		Target:    deploy.Target{ToolKey: "newtool", Scope: "user"},
		RootPath:  rootDir,
	})
	if err == nil {
		t.Fatalf("expected mkdir to fail, got success: %+v", res)
	}
	if res.ErrorCode != "mkdir_failed" {
		t.Fatalf("expected error_code=mkdir_failed, got %q (err=%v)", res.ErrorCode, err)
	}
}

// TestRunRegisterRootJob_RollsBackOutOfPolicySymlink verifies the M1 fix:
// when registration targets a path that resolves outside home through a
// symlink, the job mkdir's the leaf, Register's Validate then rejects it as
// out-of-policy, and the job rolls the created directory back so nothing is
// left outside home.
func TestRunRegisterRootJob_RollsBackOutOfPolicySymlink(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent.json")
	if err := agentcfg.Save(cfgPath, agentcfg.Config{
		ServerURL: "https://sf.example", DeviceID: "dev_x", DeviceSecret: "sec",
		HeartbeatIntSec: 30, InventoryIntSec: 300, JobsIntSec: 15,
	}); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A writable directory OUTSIDE home (t.TempDir is under the system temp,
	// not under `home`).
	outside := t.TempDir()
	// A symlink inside home pointing at the outside dir, so MkdirAll of
	// <home>/evil/skills follows the link and creates <outside>/skills.
	linkDir := filepath.Join(home, "evil")
	if err := os.Symlink(outside, linkDir); err != nil {
		t.Fatal(err)
	}
	rootDir := filepath.Join(linkDir, "skills")

	res, err := runRegisterRootJob(cfgPath, home, deploy.Request{
		Operation: deploy.OpRegisterRoot,
		Target:    deploy.Target{ToolKey: "claude-code", Scope: "user"},
		RootPath:  rootDir,
	})
	if err == nil {
		t.Fatalf("expected out-of-policy rejection, got success: %+v", res)
	}
	if res.ErrorCode != "root_outside_policy" {
		t.Fatalf("expected error_code=root_outside_policy, got %q (err=%v)", res.ErrorCode, err)
	}
	// The leaf created outside home must have been rolled back.
	if _, statErr := os.Stat(filepath.Join(outside, "skills")); !os.IsNotExist(statErr) {
		t.Errorf("out-of-home dir should be rolled back, stat err=%v", statErr)
	}
	// And nothing was registered.
	cfg, err := agentcfg.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AllowedRoots) != 0 {
		t.Errorf("no root should be registered after rollback, got %+v", cfg.AllowedRoots)
	}
}

// TestRunRegisterRootJob_RejectsExistingNonDir verifies that registering a
// path that already exists as a FILE (not a directory) fails with
// root_path_invalid without attempting mkdir or registration.
func TestRunRegisterRootJob_RejectsExistingNonDir(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent.json")
	if err := agentcfg.Save(cfgPath, agentcfg.Config{
		ServerURL: "https://sf.example", DeviceID: "dev_x", DeviceSecret: "sec",
		HeartbeatIntSec: 30, InventoryIntSec: 300, JobsIntSec: 15,
	}); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	filePath := filepath.Join(home, "afile")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := runRegisterRootJob(cfgPath, home, deploy.Request{
		Operation: deploy.OpRegisterRoot,
		Target:    deploy.Target{ToolKey: "claude-code", Scope: "user"},
		RootPath:  filePath,
	})
	if err == nil {
		t.Fatalf("expected non-dir rejection, got success: %+v", res)
	}
	if res.ErrorCode != "root_path_invalid" {
		t.Fatalf("expected error_code=root_path_invalid, got %q (err=%v)", res.ErrorCode, err)
	}
	// The pre-existing file is untouched.
	if _, statErr := os.Stat(filePath); statErr != nil {
		t.Errorf("pre-existing file should be untouched: %v", statErr)
	}
}


// TestRunOneJob_EndToEndStateChange is the Phase 9 end-to-end proof: a
// real downlink server hands the agent a state_change job, and the agent
// flips the skill's claude-code state by writing skillOverrides in the
// root's settings.json — through the actual agentclient + agentstate, not
// mocks. The skill files are NOT touched (state lives out of band).
func TestRunOneJob_EndToEndStateChange(t *testing.T) {
	ctx := context.Background()

	// --- an allowed claude-code root with a skill already on disk ---
	allowedRoot := t.TempDir() // stands in for ~/.claude/skills
	skillDir := filepath.Join(allowedRoot, "deploy-helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMD := "---\nname: deploy-helper\ndescription: x\n---\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}

	// --- real downlink server + enrolled, approved device ---
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "e2e_sc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	tx, _ := d.BeginTx(ctx, nil)
	tok, _ := enrollment.Create(ctx, d, time.Hour, now)
	enrollment.Consume(ctx, tx, tok.Plaintext, now)
	res, err := devices.Enroll(ctx, tx, devices.EnrollInput{Name: "n", OS: "linux", Arch: "amd64"}, now)
	if err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if err := devices.SetStatus(ctx, d, res.Device.ID, devices.StatusApproved); err != nil {
		t.Fatal(err)
	}

	// The state-change plan + request the server would have stored.
	scPlan := deploy.StateChangePlan{
		Target:       deploy.Target{ToolKey: "claude-code", Scope: "user", RootID: "r1"},
		SkillName:    "deploy-helper",
		DesiredState: "off",
	}
	planJSON, _ := json.Marshal(scPlan)
	store := deploy.New(d)
	job, err := store.Create(ctx, deploy.CreateParams{
		DeviceID:    res.Device.ID,
		Operation:   deploy.OpStateChange,
		RequestJSON: `{"operation":"state_change","skill_name":"deploy-helper","desired_state":"off","target":{"tool_key":"claude-code","scope":"user","root_id":"r1"}}`,
		PlanJSON:    string(planJSON),
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(agentapi.NewRouter(agentapi.Deps{
		DB:    d,
		Now:   func() time.Time { return now },
		Audit: audit.New(d, nil, func() time.Time { return now }),
	}))
	t.Cleanup(srv.Close)

	client, err := agentclient.New(agentclient.Config{
		ServerURL: srv.URL, DeviceID: res.Device.ID, DeviceSecret: res.Secret,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	// The writer's home dir is irrelevant for claude-code (its config path
	// derives from the resolved root's parent); set it to a temp dir.
	cfgPath := filepath.Join(t.TempDir(), "agent.json")
	cfg := testAgentConfig([]agentcfg.AllowedRoot{{ID: "r1", Tool: "claude-code", Scope: "user", Path: allowedRoot}})
	if err := agentcfg.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	// --- claim + run the job ---
	claimed, ok, err := client.Jobs(ctx)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	runOneJob(ctx, log, client, cfgPath, t.TempDir(), claimed)

	// --- settings.json (sibling of the skills root) carries the override ---
	settingsPath := filepath.Join(filepath.Dir(allowedRoot), "settings.json")
	raw, rerr := os.ReadFile(settingsPath)
	if rerr != nil {
		t.Fatalf("settings.json not written: %v", rerr)
	}
	var settings struct {
		SkillOverrides map[string]string `json:"skillOverrides"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("settings.json invalid: %v", err)
	}
	if settings.SkillOverrides["deploy-helper"] != "off" {
		t.Errorf("override = %q, want off; settings=%s", settings.SkillOverrides["deploy-helper"], raw)
	}

	// --- the skill's own files are untouched (state is out of band) ---
	got, _ := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if string(got) != skillMD {
		t.Errorf("SKILL.md was modified by a state change: %q", got)
	}

	// --- the job is recorded succeeded ---
	final, _ := store.Get(ctx, job.ID)
	if final.Status != deploy.StatusSucceeded {
		t.Errorf("job status = %q, want succeeded; result=%s", final.Status, final.ResultJSON)
	}
}
