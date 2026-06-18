package agentstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/yeluonight/skillfleet/internal/adapters"
)

// ---- claude (settings.json skillOverrides) ----

// TestClaude_RoundTrip writes a state and reads the override back.
func TestClaude_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := writeClaudeOverride(path, "deploy", adapters.StateOff); err != nil {
		t.Fatal(err)
	}
	got := readClaudeOverrides(t, path)
	if got["deploy"] != "off" {
		t.Errorf("deploy override = %q, want off", got["deploy"])
	}
}

// TestClaude_PreservesUnknownKeys is the headline safety guard: editing a
// skill override must not drop any unrelated settings. We seed a rich
// settings.json (permissions, env, hooks, another override) and assert
// every one survives the write.
func TestClaude_PreservesUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	seed := map[string]any{
		"permissions": map[string]any{"deny": []any{"Bash(rm*)"}},
		"env":         map[string]any{"FOO": "bar"},
		"hooks":       map[string]any{"PreToolUse": []any{"echo hi"}},
		"model":       "claude-opus",
		"skillOverrides": map[string]any{
			"other-skill": "name-only",
		},
	}
	writeJSON(t, path, seed)

	if err := writeClaudeOverride(path, "deploy", adapters.StateOff); err != nil {
		t.Fatal(err)
	}

	after := readJSON(t, path)
	// Unrelated top-level keys intact.
	if _, ok := after["permissions"]; !ok {
		t.Error("permissions dropped")
	}
	if env, ok := after["env"].(map[string]any); !ok || env["FOO"] != "bar" {
		t.Errorf("env dropped/changed: %v", after["env"])
	}
	if _, ok := after["hooks"]; !ok {
		t.Error("hooks dropped")
	}
	if after["model"] != "claude-opus" {
		t.Errorf("model = %v, want claude-opus", after["model"])
	}
	// Both the pre-existing override and the new one present.
	ov, _ := after["skillOverrides"].(map[string]any)
	if ov["other-skill"] != "name-only" {
		t.Errorf("pre-existing override dropped: %v", ov)
	}
	if ov["deploy"] != "off" {
		t.Errorf("new override missing: %v", ov)
	}
	// permissions.deny deep value intact.
	perm, _ := after["permissions"].(map[string]any)
	deny, _ := perm["deny"].([]any)
	if len(deny) != 1 || deny[0] != "Bash(rm*)" {
		t.Errorf("permissions.deny corrupted: %v", perm["deny"])
	}
}

// TestClaude_OnDeletesKey proves "on" removes the override (restoring the
// frontmatter default) rather than writing "on".
func TestClaude_OnDeletesKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeJSON(t, path, map[string]any{
		"skillOverrides": map[string]any{"deploy": "off", "keep": "ask"},
	})
	if err := writeClaudeOverride(path, "deploy", adapters.StateOn); err != nil {
		t.Fatal(err)
	}
	ov := readClaudeOverrides(t, path)
	if _, present := ov["deploy"]; present {
		t.Errorf("deploy override should be deleted for on, got %q", ov["deploy"])
	}
	if ov["keep"] != "ask" {
		t.Errorf("sibling override dropped: %v", ov)
	}
}

// TestClaude_OnEmptiesOverridesPrunesKey proves the skillOverrides object
// is removed entirely when the last entry is deleted (no litter).
func TestClaude_OnEmptiesOverridesPrunesKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeJSON(t, path, map[string]any{
		"model":          "x",
		"skillOverrides": map[string]any{"deploy": "off"},
	})
	if err := writeClaudeOverride(path, "deploy", adapters.StateOn); err != nil {
		t.Fatal(err)
	}
	after := readJSON(t, path)
	if _, present := after["skillOverrides"]; present {
		t.Errorf("empty skillOverrides should be pruned, got %v", after["skillOverrides"])
	}
	if after["model"] != "x" {
		t.Error("unrelated key lost while pruning")
	}
}

// TestClaude_CreatesMinimalConfig proves a missing settings.json is
// created containing only the new override.
func TestClaude_CreatesMinimalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	if err := writeClaudeOverride(path, "deploy", adapters.StateNameOnly); err != nil {
		t.Fatal(err)
	}
	after := readJSON(t, path)
	if len(after) != 1 {
		t.Errorf("minimal config should have 1 key, got %v", after)
	}
	ov, _ := after["skillOverrides"].(map[string]any)
	if ov["deploy"] != "name-only" {
		t.Errorf("override = %v", ov)
	}
}

// TestClaude_RejectsUnparseable proves an invalid settings.json is NOT
// clobbered (we error out rather than overwrite a config we can't read).
func TestClaude_RejectsUnparseable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeClaudeOverride(path, "deploy", adapters.StateOff); err == nil {
		t.Fatal("want error on unparseable settings, got nil")
	}
	// File unchanged.
	raw, _ := os.ReadFile(path)
	if string(raw) != "{not json" {
		t.Errorf("unparseable file was clobbered: %q", raw)
	}
}

// ---- codex (config.toml [[skills.config]]) ----

func TestCodex_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	key := "/home/me/.agents/skills/deploy/SKILL.md"
	if err := writeCodexEnabled(path, key, adapters.StateOff); err != nil {
		t.Fatal(err)
	}
	cfg := readCodexConfig(t, path)
	if e, ok := cfg[key]; !ok || e != false {
		t.Errorf("entry for %s = %v (ok=%v), want enabled=false", key, e, ok)
	}
}

// TestCodex_PreservesOtherSections is the codex safety guard: editing a
// skill's enabled flag must keep every other config section AND every
// other skill entry.
func TestCodex_PreservesOtherSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	seed := `
[model]
provider = "openai"
name = "gpt"

[[skills.config]]
path = "/skills/keep/SKILL.md"
enabled = true

[[skills.config]]
path = "/skills/deploy/SKILL.md"
enabled = true
`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeCodexEnabled(path, "/skills/deploy/SKILL.md", adapters.StateOff); err != nil {
		t.Fatal(err)
	}

	// Re-read generically and assert.
	var doc map[string]any
	raw, _ := os.ReadFile(path)
	if err := toml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	// [model] section intact.
	model, _ := doc["model"].(map[string]any)
	if model["provider"] != "openai" || model["name"] != "gpt" {
		t.Errorf("[model] section corrupted: %v", model)
	}
	// Both skill entries present; deploy now false, keep still true.
	cfg := readCodexConfig(t, path)
	if cfg["/skills/deploy/SKILL.md"] != false {
		t.Errorf("deploy not disabled: %v", cfg["/skills/deploy/SKILL.md"])
	}
	if cfg["/skills/keep/SKILL.md"] != true {
		t.Errorf("keep entry corrupted: %v", cfg["/skills/keep/SKILL.md"])
	}
}

// TestCodex_InsertsWhenAbsent proves a skill with no existing entry gets
// one appended (rather than silently doing nothing).
func TestCodex_InsertsWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	seed := "[[skills.config]]\npath = \"/skills/other/SKILL.md\"\nenabled = true\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	key := "/skills/new/SKILL.md"
	if err := writeCodexEnabled(path, key, adapters.StateOff); err != nil {
		t.Fatal(err)
	}
	cfg := readCodexConfig(t, path)
	if _, ok := cfg[key]; !ok {
		t.Errorf("new entry not inserted; got %v", cfg)
	}
	if cfg["/skills/other/SKILL.md"] != true {
		t.Error("existing entry lost on insert")
	}
}

func TestCodex_RejectsUnsupportedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	for _, s := range []adapters.EffectiveState{adapters.StateAsk, adapters.StateNameOnly, adapters.StateUserInvocableOnly} {
		if err := writeCodexEnabled(path, "/x/SKILL.md", s); err == nil {
			t.Errorf("codex+%s: want error, got nil", s)
		}
	}
	// No file should have been created by a rejected write.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("rejected codex write created a file")
	}
}

// ---- opencode (opencode.json permission.skill) ----

func TestOpencode_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := writeOpencodePermission(path, "deploy", adapters.StateAsk); err != nil {
		t.Fatal(err)
	}
	if got := readOpencodePerms(t, path)["deploy"]; got != "ask" {
		t.Errorf("deploy perm = %q, want ask", got)
	}
	if err := writeOpencodePermission(path, "deploy", adapters.StateOff); err != nil {
		t.Fatal(err)
	}
	if got := readOpencodePerms(t, path)["deploy"]; got != "deny" {
		t.Errorf("deploy perm = %q, want deny", got)
	}
}

func TestOpencode_PreservesUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	seed := map[string]any{
		"theme": "dark",
		"permission": map[string]any{
			"bash": "allow",
			"skill": map[string]any{
				"keep-skill": "ask",
			},
		},
	}
	writeJSON(t, path, seed)

	if err := writeOpencodePermission(path, "deploy", adapters.StateOff); err != nil {
		t.Fatal(err)
	}
	after := readJSON(t, path)
	if after["theme"] != "dark" {
		t.Error("theme dropped")
	}
	perm, _ := after["permission"].(map[string]any)
	if perm["bash"] != "allow" {
		t.Errorf("permission.bash dropped: %v", perm)
	}
	skill, _ := perm["skill"].(map[string]any)
	if skill["keep-skill"] != "ask" {
		t.Errorf("sibling skill perm dropped: %v", skill)
	}
	if skill["deploy"] != "deny" {
		t.Errorf("new perm missing: %v", skill)
	}
}

func TestOpencode_OnDeletesKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	writeJSON(t, path, map[string]any{
		"permission": map[string]any{"skill": map[string]any{"deploy": "deny", "keep": "ask"}},
	})
	if err := writeOpencodePermission(path, "deploy", adapters.StateOn); err != nil {
		t.Fatal(err)
	}
	perms := readOpencodePerms(t, path)
	if _, present := perms["deploy"]; present {
		t.Errorf("deploy perm should be deleted for on, got %q", perms["deploy"])
	}
	if perms["keep"] != "ask" {
		t.Error("sibling perm dropped")
	}
}

func TestOpencode_RejectsUnsupportedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	for _, s := range []adapters.EffectiveState{adapters.StateNameOnly, adapters.StateUserInvocableOnly} {
		if err := writeOpencodePermission(path, "x", s); err == nil {
			t.Errorf("opencode+%s: want error, got nil", s)
		}
	}
}

// ---- atomic write ----

// TestAtomicWrite_NoTempLeftBehind proves a successful write leaves no
// .tmp files in the directory.
func TestAtomicWrite_NoTempLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := writeClaudeOverride(path, "deploy", adapters.StateOff); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || len(e.Name()) > 0 && e.Name()[0] == '.' && e.Name() != ".gitkeep" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 || entries[0].Name() != "settings.json" {
		t.Errorf("dir should contain only settings.json, got %v", names(entries))
	}
}

// TestAtomicWrite_OverwritePreservesNothingStale proves a second write
// fully replaces the file (no leftover bytes from a longer previous file).
func TestAtomicWrite_OverwritePreservesNothingStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	if err := atomicWriteFile(path, []byte("AAAAAAAAAAAAAAAAAAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(path, []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "B" {
		t.Errorf("overwrite left stale bytes: %q", raw)
	}
}

// ---- test helpers ----

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("re-read %s: %v", path, err)
	}
	return m
}

func readClaudeOverrides(t *testing.T, path string) map[string]string {
	t.Helper()
	m := readJSON(t, path)
	out := map[string]string{}
	if ov, ok := m["skillOverrides"].(map[string]any); ok {
		for k, v := range ov {
			out[k], _ = v.(string)
		}
	}
	return out
}

func readOpencodePerms(t *testing.T, path string) map[string]string {
	t.Helper()
	m := readJSON(t, path)
	out := map[string]string{}
	if perm, ok := m["permission"].(map[string]any); ok {
		if sk, ok := perm["skill"].(map[string]any); ok {
			for k, v := range sk {
				out[k], _ = v.(string)
			}
		}
	}
	return out
}

// readCodexConfig returns path→enabled for every [[skills.config]] entry.
func readCodexConfig(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Skills struct {
			Config []struct {
				Path    string `toml:"path"`
				Enabled bool   `toml:"enabled"`
			} `toml:"config"`
		} `toml:"skills"`
	}
	if err := toml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("re-read %s: %v", path, err)
	}
	out := map[string]bool{}
	for _, c := range doc.Skills.Config {
		out[c.Path] = c.Enabled
	}
	return out
}

func names(entries []os.DirEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
