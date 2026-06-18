package drift

import "testing"

// TestClassify_ShaMatchesIsClean is the core Phase 7 guard at the engine
// layer: when the device's content_sha256 equals a registry version's
// content_sha256, the skill is clean — never local_modified — and the
// matched version id is returned.
//
// Mutation proof: change Classify's hit branch to return
// StateLocalModified unconditionally and this test fails. That confirms
// the test exercises the guard rather than passing by coincidence.
func TestClassify_ShaMatchesIsClean(t *testing.T) {
	registry := map[string]string{
		"sha-aaa": "sv_1",
		"sha-bbb": "sv_2", // the version the device is actually running
		"sha-ccc": "sv_3",
	}

	state, matched := Classify("sha-bbb", registry, true)

	if state != StateClean {
		t.Fatalf("content sha matches a registry version: want %q, got %q", StateClean, state)
	}
	if matched != "sv_2" {
		t.Fatalf("clean must report the matched version id: want sv_2, got %q", matched)
	}
}

// TestClassify_ShaDiffersIsModified: the name is tracked but no version
// has the device's sha ⇒ the copy was edited locally.
func TestClassify_ShaDiffersIsModified(t *testing.T) {
	registry := map[string]string{"sha-aaa": "sv_1", "sha-bbb": "sv_2"}

	state, matched := Classify("sha-edited", registry, true)

	if state != StateLocalModified {
		t.Fatalf("sha absent from a tracked name: want %q, got %q", StateLocalModified, state)
	}
	if matched != "" {
		t.Fatalf("local_modified has no matched version, got %q", matched)
	}
}

// TestClassify_NoRegistryNameIsUntracked: the registry has never heard of
// this skill name ⇒ untracked, not local_modified (nothing to diff
// against). hasName=false even though an empty map is also passed.
func TestClassify_NoRegistryNameIsUntracked(t *testing.T) {
	state, matched := Classify("sha-whatever", map[string]string{}, false)

	if state != StateUntracked {
		t.Fatalf("unknown skill name: want %q, got %q", StateUntracked, state)
	}
	if matched != "" {
		t.Fatalf("untracked has no matched version, got %q", matched)
	}
}

// TestClassify_EmptyShaIsUntracked: no fingerprint reported ⇒ we cannot
// assert a match, so we must NOT fabricate local_modified. The honest
// classification is untracked. This guards the false-positive direction:
// a missing fingerprint should never read as "edited".
func TestClassify_EmptyShaIsUntracked(t *testing.T) {
	// Even when the registry tracks the name, an empty local sha is
	// untracked, not local_modified.
	registry := map[string]string{"sha-aaa": "sv_1"}

	state, matched := Classify("", registry, true)

	if state != StateUntracked {
		t.Fatalf("empty fingerprint: want %q, got %q", StateUntracked, state)
	}
	if matched != "" {
		t.Fatalf("empty fingerprint has no matched version, got %q", matched)
	}
}

// TestClassify_MatchWinsOverHasName: a content match is clean even when
// hasName is true and the map has many entries — the hit branch takes
// precedence over the local_modified branch. This pins the ordering the
// mutation proof relies on.
func TestClassify_MatchWinsOverHasName(t *testing.T) {
	registry := map[string]string{
		"sha-1": "sv_a",
		"sha-2": "sv_b",
		"sha-3": "sv_c",
	}

	state, matched := Classify("sha-3", registry, true)

	if state != StateClean || matched != "sv_c" {
		t.Fatalf("match must win: want clean/sv_c, got %q/%q", state, matched)
	}
}
