package drift

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/internal/inventory"
	"github.com/yeluonight/skillfleet/migrations"
)

// fakeLister is an in-memory VersionLister: skill name → its registry
// content_sha256 set. A name present with an empty/zero set still counts
// as "tracked" via the count it returns.
type fakeLister struct {
	byName map[string]map[string]string // name → (sha → versionID)
	err    error
}

func (f fakeLister) ListVersionSHAs(_ context.Context, name string) (map[string]string, int, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	shas, ok := f.byName[name]
	if !ok {
		return map[string]string{}, 0, nil
	}
	return shas, len(shas), nil
}

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "drift.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO devices(id, name, status, created_at) VALUES ('dev1', 'host', 'approved', 1)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// storeReport lands an inventory run for dev1 with the given skills so
// ComputeDeviceDrift has discovered_skills rows to classify.
func storeReport(t *testing.T, d *sql.DB, skills []inventory.Skill) {
	t.Helper()
	report := inventory.Report{
		AgentVersion: "0.7.0",
		Tools: []inventory.ToolInstance{{
			ToolKey: "claude-code", DisplayName: "Claude Code", Scope: "user",
			RootID: "claude_user", RootPath: "/home/me/.claude/skills",
			Skills: skills,
		}},
	}
	if _, err := inventory.Store(context.Background(), d, "dev1", report, time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatal(err)
	}
}

// TestComputeDeviceDrift_ThreeStates exercises the full aggregate: a
// device whose three reported skills land one in each state — clean
// (sha matches registry), local_modified (name tracked, sha differs),
// untracked (registry has no such name).
func TestComputeDeviceDrift_ThreeStates(t *testing.T) {
	d := newDB(t)
	storeReport(t, d, []inventory.Skill{
		{Name: "deploy", SkillPath: "/s/deploy", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "sha-deploy-v1"},
		{Name: "lint", SkillPath: "/s/lint", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "sha-lint-edited"},
		{Name: "local-only", SkillPath: "/s/local-only", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "sha-x"},
	})

	reg := fakeLister{byName: map[string]map[string]string{
		"deploy": {"sha-deploy-v1": "sv_deploy_1", "sha-deploy-v0": "sv_deploy_0"},
		"lint":   {"sha-lint-v1": "sv_lint_1"}, // tracked, but device's sha not among them
		// "local-only" intentionally absent → untracked
	}}

	got, err := ComputeDeviceDrift(context.Background(), d, reg, "dev1")
	if err != nil {
		t.Fatal(err)
	}

	byName := make(map[string]SkillDrift, len(got))
	for _, dft := range got {
		byName[dft.Name] = dft
	}

	if d := byName["deploy"]; d.LocalState != StateClean || d.MatchedVersionID != "sv_deploy_1" {
		t.Errorf("deploy: want clean/sv_deploy_1, got %q/%q", d.LocalState, d.MatchedVersionID)
	}
	if d := byName["deploy"]; d.RegistryVersionCount != 2 {
		t.Errorf("deploy: want 2 registry versions, got %d", d.RegistryVersionCount)
	}
	if d := byName["lint"]; d.LocalState != StateLocalModified || d.MatchedVersionID != "" {
		t.Errorf("lint: want local_modified/empty, got %q/%q", d.LocalState, d.MatchedVersionID)
	}
	if d := byName["local-only"]; d.LocalState != StateUntracked {
		t.Errorf("local-only: want untracked, got %q", d.LocalState)
	}
}

// TestComputeDeviceDrift_NoInventory: a device that never reported
// inventory yields an empty slice, not an error.
func TestComputeDeviceDrift_NoInventory(t *testing.T) {
	d := newDB(t)
	got, err := ComputeDeviceDrift(context.Background(), d, fakeLister{}, "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("device with no inventory: want empty, got %d rows", len(got))
	}
}

// TestComputeDeviceDrift_ShaMatchEqualsClean is the aggregate-layer view
// of the core guard: a device whose reported content_sha256 equals a
// registry version's sha must classify clean, not local_modified — even
// though the registry also holds other versions for that name.
func TestComputeDeviceDrift_ShaMatchEqualsClean(t *testing.T) {
	d := newDB(t)
	storeReport(t, d, []inventory.Skill{
		{Name: "deploy", SkillPath: "/s/deploy", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "sha-match"},
	})
	reg := fakeLister{byName: map[string]map[string]string{
		"deploy": {"sha-other": "sv_old", "sha-match": "sv_running"},
	}}

	got, err := ComputeDeviceDrift(context.Background(), d, reg, "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 drift row, got %d", len(got))
	}
	if got[0].LocalState != StateClean || got[0].MatchedVersionID != "sv_running" {
		t.Errorf("sha match must be clean: got %q/%q", got[0].LocalState, got[0].MatchedVersionID)
	}
}

// TestComputeDeviceDrift_MemoisesPerName: the same skill name under
// multiple tools triggers exactly one registry lookup. We assert this by
// counting calls through a wrapping lister.
func TestComputeDeviceDrift_MemoisesPerName(t *testing.T) {
	d := newDB(t)
	report := inventory.Report{
		AgentVersion: "0.7.0",
		Tools: []inventory.ToolInstance{
			{
				ToolKey: "claude-code", DisplayName: "Claude Code", Scope: "user",
				RootID: "claude_user", RootPath: "/a",
				Skills: []inventory.Skill{{Name: "deploy", SkillPath: "/a/deploy", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "s1"}},
			},
			{
				ToolKey: "codex", DisplayName: "Codex", Scope: "user",
				RootID: "codex_user", RootPath: "/b",
				Skills: []inventory.Skill{{Name: "deploy", SkillPath: "/b/deploy", HasSkillMD: true, EffectiveState: "on", ContentSHA256: "s1"}},
			},
		},
	}
	if _, err := inventory.Store(context.Background(), d, "dev1", report, time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatal(err)
	}

	calls := map[string]int{}
	reg := countingLister{calls: calls, inner: fakeLister{byName: map[string]map[string]string{
		"deploy": {"s1": "sv1"},
	}}}

	got, err := ComputeDeviceDrift(context.Background(), d, reg, "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows (deploy under 2 tools), got %d", len(got))
	}
	if calls["deploy"] != 1 {
		t.Errorf("registry should be queried once per distinct name, got %d for deploy", calls["deploy"])
	}
}

type countingLister struct {
	calls map[string]int
	inner fakeLister
}

func (c countingLister) ListVersionSHAs(ctx context.Context, name string) (map[string]string, int, error) {
	c.calls[name]++
	return c.inner.ListVersionSHAs(ctx, name)
}
