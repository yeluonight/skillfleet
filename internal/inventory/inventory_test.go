package inventory

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/migrations"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "inv.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	// Anchor device for FKs.
	if _, err := d.Exec(`INSERT INTO devices(id, name, status, created_at) VALUES ('dev1', 'host', 'approved', 1)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func sampleReport() Report {
	return Report{
		AgentVersion: "0.3.0",
		Tools: []ToolInstance{
			{
				ToolKey: "claude-code", DisplayName: "Claude Code", Scope: "user",
				RootID: "claude_user", RootPath: "/home/me/.claude/skills",
				Skills: []Skill{
					{
						Name: "deploy", SkillPath: "/home/me/.claude/skills/deploy",
						HasSkillMD: true, Description: "deploys", EffectiveState: "on",
						NativeState: "available", ContentSHA256: "abc", FileCount: 3, TotalBytes: 100,
					},
					{
						Name: "lint", SkillPath: "/home/me/.claude/skills/lint",
						HasSkillMD: true, EffectiveState: "off", NativeState: "disabled",
						Warnings: []Warning{{Code: "missing_description", Message: "no desc"}},
					},
				},
			},
			{
				ToolKey: "codex", DisplayName: "Codex", Scope: "system",
				RootID: "codex_system", RootPath: "/etc/codex/skills",
				Skills: []Skill{
					{Name: "build", SkillPath: "/etc/codex/skills/build", HasSkillMD: true, EffectiveState: "on"},
				},
			},
		},
	}
}

func TestStore_HappyPath(t *testing.T) {
	d := newDB(t)
	now := time.Unix(1_700_000_000, 0)

	res, err := Store(context.Background(), d, "dev1", sampleReport(), now)
	if err != nil {
		t.Fatal(err)
	}
	if res.SkillCount != 3 || res.RootCount != 2 {
		t.Errorf("result = %+v, want skills=3 roots=2", res)
	}

	// inventory_runs row.
	var skillCount, rootCount int
	var agentVer string
	if err := d.QueryRow(`SELECT skill_count, root_count, agent_version FROM inventory_runs WHERE id=?`, res.RunID).
		Scan(&skillCount, &rootCount, &agentVer); err != nil {
		t.Fatal(err)
	}
	if skillCount != 3 || rootCount != 2 || agentVer != "0.3.0" {
		t.Errorf("run row = %d/%d/%q", skillCount, rootCount, agentVer)
	}

	// tool_instances + discovered_skills counts.
	var tiN, dsN int
	_ = d.QueryRow(`SELECT COUNT(*) FROM tool_instances WHERE device_id='dev1'`).Scan(&tiN)
	_ = d.QueryRow(`SELECT COUNT(*) FROM discovered_skills WHERE device_id='dev1'`).Scan(&dsN)
	if tiN != 2 || dsN != 3 {
		t.Errorf("counts ti=%d ds=%d, want 2/3", tiN, dsN)
	}

	// Skill fields round-trip, including warnings JSON.
	var hasMD int
	var eff, native, warnings sql.NullString
	if err := d.QueryRow(`
		SELECT has_skill_md, effective_state, native_state, warnings_json
		FROM discovered_skills WHERE device_id='dev1' AND name='lint'`).
		Scan(&hasMD, &eff, &native, &warnings); err != nil {
		t.Fatal(err)
	}
	if hasMD != 1 || eff.String != "off" || native.String != "disabled" {
		t.Errorf("lint row = md=%d eff=%q native=%q", hasMD, eff.String, native.String)
	}
	if !warnings.Valid || warnings.String == "" {
		t.Error("lint warnings_json should be populated")
	}
}

func TestStore_ReplacesPriorRun(t *testing.T) {
	d := newDB(t)
	now := time.Unix(1_700_000_000, 0)

	if _, err := Store(context.Background(), d, "dev1", sampleReport(), now); err != nil {
		t.Fatal(err)
	}
	// Second, smaller report should fully replace the first.
	smaller := Report{
		Tools: []ToolInstance{
			{ToolKey: "pi", DisplayName: "Pi", Scope: "user", RootID: "pi_user_agent", RootPath: "/p",
				Skills: []Skill{{Name: "only", SkillPath: "/p/only", HasSkillMD: true, EffectiveState: "on"}}},
		},
	}
	res, err := Store(context.Background(), d, "dev1", smaller, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	// Only one run survives.
	var runN int
	_ = d.QueryRow(`SELECT COUNT(*) FROM inventory_runs WHERE device_id='dev1'`).Scan(&runN)
	if runN != 1 {
		t.Errorf("run count = %d, want 1 (prior replaced)", runN)
	}
	// And it's the new one.
	var dsN int
	_ = d.QueryRow(`SELECT COUNT(*) FROM discovered_skills WHERE run_id=?`, res.RunID).Scan(&dsN)
	if dsN != 1 {
		t.Errorf("new run skill count = %d, want 1", dsN)
	}
	// No stale rows from run #1.
	var totalDS int
	_ = d.QueryRow(`SELECT COUNT(*) FROM discovered_skills WHERE device_id='dev1'`).Scan(&totalDS)
	if totalDS != 1 {
		t.Errorf("total discovered_skills = %d, want 1 (no stale)", totalDS)
	}
}

func TestStore_EmptyReportOK(t *testing.T) {
	d := newDB(t)
	res, err := Store(context.Background(), d, "dev1", Report{}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if res.SkillCount != 0 || res.RootCount != 0 {
		t.Errorf("empty report = %+v", res)
	}
	var runN int
	_ = d.QueryRow(`SELECT COUNT(*) FROM inventory_runs WHERE device_id='dev1'`).Scan(&runN)
	if runN != 1 {
		t.Errorf("empty report should still record a run, got %d", runN)
	}
}

func TestStore_RejectsInvalidScope(t *testing.T) {
	d := newDB(t)
	bad := Report{Tools: []ToolInstance{
		{ToolKey: "x", DisplayName: "X", Scope: "galaxy", RootID: "r", RootPath: "/p"},
	}}
	_, err := Store(context.Background(), d, "dev1", bad, time.Unix(1, 0))
	if !errors.Is(err, ErrInvalidReport) {
		t.Errorf("err = %v, want ErrInvalidReport", err)
	}
	// Nothing written.
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM inventory_runs WHERE device_id='dev1'`).Scan(&n)
	if n != 0 {
		t.Errorf("rejected report should write nothing, got %d runs", n)
	}
}

func TestStore_RejectsInvalidState(t *testing.T) {
	d := newDB(t)
	bad := Report{Tools: []ToolInstance{
		{ToolKey: "x", DisplayName: "X", Scope: "user", RootID: "r", RootPath: "/p",
			Skills: []Skill{{Name: "s", SkillPath: "/p/s", EffectiveState: "maybe"}}},
	}}
	_, err := Store(context.Background(), d, "dev1", bad, time.Unix(1, 0))
	if !errors.Is(err, ErrInvalidReport) {
		t.Errorf("err = %v, want ErrInvalidReport", err)
	}
}

func TestStore_RejectsEmptyName(t *testing.T) {
	d := newDB(t)
	bad := Report{Tools: []ToolInstance{
		{ToolKey: "x", DisplayName: "X", Scope: "user", RootID: "r", RootPath: "/p",
			Skills: []Skill{{Name: "", SkillPath: "/p/s", EffectiveState: "on"}}},
	}}
	_, err := Store(context.Background(), d, "dev1", bad, time.Unix(1, 0))
	if !errors.Is(err, ErrInvalidReport) {
		t.Errorf("err = %v, want ErrInvalidReport", err)
	}
}

func TestReport_Counts(t *testing.T) {
	r := sampleReport()
	if r.SkillCount() != 3 {
		t.Errorf("SkillCount = %d, want 3", r.SkillCount())
	}
	if r.RootCount() != 2 {
		t.Errorf("RootCount = %d, want 2", r.RootCount())
	}
}
