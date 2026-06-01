package deploy

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestRollbackPlanWireCompat locks the rollback plan_json shape: the
// fields the server writes must be exactly {target, skill_name,
// backup_dir} — the same key set the pre-refactor hand-built
// map[string]any produced (backup_was_empty omitted via omitempty when
// false). JSON object key order is irrelevant on the wire (the agent
// json.Unmarshals), so this compares decoded key sets, not byte order.
func TestRollbackPlanWireCompat(t *testing.T) {
	target := Target{ToolKey: "claude-code", Scope: "user"}
	plan := RollbackPlan{
		Target:    target,
		SkillName: "deploy-helper",
		BackupDir: "/backups/dj_1",
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}

	// The exact key set the prior map[string]any wrote.
	var got map[string]json.RawMessage
	if err := json.Unmarshal(planJSON, &got); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"target", "skill_name", "backup_dir"}
	if len(got) != len(wantKeys) {
		t.Errorf("key set = %v, want exactly %v (backup_was_empty must be omitted)", keysOf(got), wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q in %s", k, planJSON)
		}
	}

	// And it must round-trip back into the agent's decode target unchanged.
	var back RollbackPlan
	if err := json.Unmarshal(planJSON, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan, back) {
		t.Errorf("round-trip mismatch: %+v != %+v", plan, back)
	}

	// When BackupWasEmpty is true it DOES appear (omitempty only drops false).
	withEmpty, _ := json.Marshal(RollbackPlan{Target: target, SkillName: "s", BackupWasEmpty: true})
	var m map[string]json.RawMessage
	_ = json.Unmarshal(withEmpty, &m)
	if _, ok := m["backup_was_empty"]; !ok {
		t.Errorf("backup_was_empty=true should serialise, got %s", withEmpty)
	}
}

// TestDownlinkEnvelopeFieldNames pins the JSON field names of the shared
// downlink envelopes — these cross the server↔agent boundary and must not
// drift.
func TestDownlinkEnvelopeFieldNames(t *testing.T) {
	cj, _ := json.Marshal(ClaimedJob{ID: "dj_1", Operation: "install", RequestJSON: "{}", PlanJSON: "{}"})
	if got, want := string(cj), `{"id":"dj_1","operation":"install","request_json":"{}","plan_json":"{}"}`; got != want {
		t.Errorf("ClaimedJob wire = %s, want %s", got, want)
	}
	// plan_json omitempty: a job with no plan drops the field.
	cjNoPlan, _ := json.Marshal(ClaimedJob{ID: "dj_2", Operation: "rollback", RequestJSON: "{}"})
	if got, want := string(cjNoPlan), `{"id":"dj_2","operation":"rollback","request_json":"{}"}`; got != want {
		t.Errorf("ClaimedJob(no plan) wire = %s, want %s", got, want)
	}
	jr, _ := json.Marshal(JobResult{Status: "succeeded", ResultJSON: "{}"})
	if got, want := string(jr), `{"status":"succeeded","result_json":"{}"}`; got != want {
		t.Errorf("JobResult wire = %s, want %s", got, want)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
