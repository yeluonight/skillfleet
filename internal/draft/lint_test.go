package draft

import (
	"context"
	"testing"
	"time"
)

// issueByCode returns the first issue with the given code, or nil.
func issueByCode(issues []Issue, code string) *Issue {
	for i := range issues {
		if issues[i].Code == code {
			return &issues[i]
		}
	}
	return nil
}

// seedSkillMD overwrites the draft's SKILL.md with a clean, warning-free
// frontmatter so lint findings on sibling files are the only issues.
func seedSkillMD(t *testing.T, ds *Store, ctx context.Context, id, name string) {
	t.Helper()
	body := "---\nname: " + name + "\ndescription: ok\n---\n# " + name + "\n"
	if _, err := ds.PutFile(ctx, id, "SKILL.md", []byte(body), time.UnixMilli(2)); err != nil {
		t.Fatal(err)
	}
}

func TestLint_ValidFilesNoIssues(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "clean"}, time.UnixMilli(1))
	seedSkillMD(t, ds, ctx, d.ID, "clean")
	ds.PutFile(ctx, d.ID, "a.json", []byte(`{"k": [1, 2, 3]}`), time.UnixMilli(3))
	ds.PutFile(ctx, d.ID, "b.yaml", []byte("key: value\nlist:\n  - one\n  - two\n"), time.UnixMilli(4))
	ds.PutFile(ctx, d.ID, "c.toml", []byte("title = \"x\"\n[owner]\nname = \"a\"\n"), time.UnixMilli(5))
	ds.PutFile(ctx, d.ID, "notes.txt", []byte("just text, no schema\n"), time.UnixMilli(6))

	issues, err := ds.Validate(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if HasErrors(issues) {
		t.Errorf("valid files produced errors: %+v", issues)
	}
}

func TestLint_JSONSyntaxErrorHasPosition(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "j"}, time.UnixMilli(1))
	seedSkillMD(t, ds, ctx, d.ID, "j")
	// Missing the closing brace; error lands on line 3.
	ds.PutFile(ctx, d.ID, "bad.json", []byte("{\n  \"a\": 1,\n  \"b\":\n"), time.UnixMilli(3))

	issues, _ := ds.Validate(ctx, d.ID)
	iss := issueByCode(issues, "json_syntax")
	if iss == nil {
		t.Fatalf("want json_syntax, got %+v", issues)
	}
	if iss.Severity != SeverityError {
		t.Errorf("json_syntax severity = %q, want error", iss.Severity)
	}
	if iss.Path != "bad.json" {
		t.Errorf("path = %q, want bad.json", iss.Path)
	}
	if iss.Line < 1 {
		t.Errorf("expected a positive line number, got %d", iss.Line)
	}
}

func TestLint_YAMLSyntaxErrorHasLine(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "y"}, time.UnixMilli(1))
	seedSkillMD(t, ds, ctx, d.ID, "y")
	// A tab in indentation and a bad mapping make yaml.v3 report a line.
	ds.PutFile(ctx, d.ID, "bad.yaml", []byte("a: 1\nb: [unterminated\n"), time.UnixMilli(3))

	issues, _ := ds.Validate(ctx, d.ID)
	iss := issueByCode(issues, "yaml_syntax")
	if iss == nil {
		t.Fatalf("want yaml_syntax, got %+v", issues)
	}
	if iss.Severity != SeverityError {
		t.Errorf("yaml_syntax severity = %q, want error", iss.Severity)
	}
	if iss.Line < 1 {
		t.Errorf("expected a positive line number, got %d", iss.Line)
	}
}

func TestLint_TOMLSyntaxErrorHasPosition(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "t"}, time.UnixMilli(1))
	seedSkillMD(t, ds, ctx, d.ID, "t")
	// A bare key with no value on line 2 is a TOML parse error.
	ds.PutFile(ctx, d.ID, "bad.toml", []byte("ok = 1\nbroken\n"), time.UnixMilli(3))

	issues, _ := ds.Validate(ctx, d.ID)
	iss := issueByCode(issues, "toml_syntax")
	if iss == nil {
		t.Fatalf("want toml_syntax, got %+v", issues)
	}
	if iss.Severity != SeverityError {
		t.Errorf("toml_syntax severity = %q, want error", iss.Severity)
	}
	if iss.Line < 1 {
		t.Errorf("expected a positive line number, got %d", iss.Line)
	}
}

func TestLint_NonUTF8TextIsError(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "u"}, time.UnixMilli(1))
	seedSkillMD(t, ds, ctx, d.ID, "u")

	// The binary sniff only inspects the first 512 bytes. To exercise
	// lint's whole-file UTF-8 guard (not the sniff), build a file whose
	// first 512+ bytes are clean ASCII — so it's classified text — then
	// plant an invalid 0xFF byte past the sniff window. The sniff lets
	// it through as text; lint must still catch the bad byte. This is
	// the §1.3.8 "never silently accept non-UTF-8" backstop.
	clean := make([]byte, 600)
	for i := range clean {
		clean[i] = 'a'
	}
	clean[550] = 0xFF // invalid UTF-8, well past the 512-byte sniff
	ds.PutFile(ctx, d.ID, "tail.txt", clean, time.UnixMilli(3))

	// Guard against a silent no-op: the test is only meaningful if the
	// file is stored as text. If the sniff ever changes to flag this as
	// binary, fail loudly so we revisit the lint reachability.
	d2, _ := ds.Load(ctx, d.ID)
	for _, f := range d2.Files {
		if f.Path == "tail.txt" && f.IsBinary {
			t.Fatal("tail.txt classified binary; lint UTF-8 guard would be unreachable — revisit construction")
		}
	}

	issues, _ := ds.Validate(ctx, d.ID)
	iss := issueByCode(issues, "invalid_utf8")
	if iss == nil {
		t.Fatalf("text file with invalid UTF-8 not flagged: %+v", issues)
	}
	if iss.Severity != SeverityError {
		t.Errorf("invalid_utf8 severity = %q, want error", iss.Severity)
	}
	if iss.Path != "tail.txt" {
		t.Errorf("path = %q, want tail.txt", iss.Path)
	}
	if iss.Line != 1 || iss.Col != 551 {
		t.Errorf("position = (%d,%d), want (1,551)", iss.Line, iss.Col)
	}
}

func TestLint_SkillMDNotDoubleLinted(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "s"}, time.UnixMilli(1))
	seedSkillMD(t, ds, ctx, d.ID, "s")

	issues, _ := ds.Validate(ctx, d.ID)
	// SKILL.md is markdown, not a structured-data extension, so even if
	// lint ran on it there'd be no syntax issue. The guard we assert is
	// that no lint issue carries SKILL.md as its path.
	for _, i := range issues {
		switch i.Code {
		case "json_syntax", "yaml_syntax", "toml_syntax", "invalid_utf8", "file_unreadable":
			if i.Path == "SKILL.md" {
				t.Errorf("SKILL.md was lint-checked: %+v", i)
			}
		}
	}
}

func TestOffsetToPos(t *testing.T) {
	content := []byte("ab\ncde\nf")
	cases := []struct {
		off       int
		line, col int
	}{
		{0, 1, 1},  // 'a'
		{1, 1, 2},  // 'b'
		{2, 1, 3},  // '\n'
		{3, 2, 1},  // 'c'
		{6, 2, 4},  // '\n' after "cde"
		{7, 3, 1},  // 'f'
		{99, 3, 2}, // clamp past EOF
		{-5, 1, 1}, // clamp negative
	}
	for _, c := range cases {
		line, col := offsetToPos(content, c.off)
		if line != c.line || col != c.col {
			t.Errorf("offsetToPos(%d) = (%d,%d), want (%d,%d)", c.off, line, col, c.line, c.col)
		}
	}
}
