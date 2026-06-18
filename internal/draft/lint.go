package draft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/yeluonight/skillfleet/internal/skill"
	"gopkg.in/yaml.v3"
)

// lintFiles validates every text file in the draft by extension and
// returns the findings (v1.0 §7.3, §1.3.8). Structured-data files that
// won't parse (JSON/YAML/TOML) produce error-severity issues that block
// publish; a text file that isn't valid UTF-8 is likewise an error,
// since SkillFleet never silently rewrites non-UTF-8 content.
//
// SKILL.md is skipped here: its frontmatter and encoding are fully
// validated by validateDraft's steps 1–6, so re-linting would duplicate
// findings. Binary files are skipped entirely.
//
// Positions are 1-based. A finding may carry Line>0 with Col==0 when the
// underlying parser reports a line but no column (the frontend treats
// that as "start of line").
func (s *Store) lintFiles(ctx context.Context, d Draft) []Issue {
	var issues []Issue

	for i := range d.Files {
		f := d.Files[i]
		if f.IsBinary || f.Path == skill.SkillMDName {
			continue
		}

		content, ok := s.lintContent(ctx, d.ID, f, &issues)
		if !ok {
			continue
		}

		// UTF-8 first: parsing non-UTF-8 as structured data is
		// meaningless and would mangle multibyte runes. If the file
		// isn't valid UTF-8, flag it and skip the syntax check.
		if off, bad := firstInvalidUTF8(content); bad {
			line, col := offsetToPos(content, off)
			issues = append(issues, Issue{
				Severity: SeverityError, Code: "invalid_utf8", Path: f.Path,
				Message: "file is not valid UTF-8", Line: line, Col: col,
			})
			continue
		}

		switch strings.ToLower(filepath.Ext(f.Path)) {
		case ".json":
			issues = appendIssue(issues, lintJSON(f.Path, content))
		case ".yaml", ".yml":
			issues = appendIssue(issues, lintYAML(f.Path, content))
		case ".toml":
			issues = appendIssue(issues, lintTOML(f.Path, content))
		}
	}

	return issues
}

// lintContent resolves a text file's bytes, preferring the content Load
// already inlined and falling back to a blob read for oversized text.
// On an unreadable file it appends a warning and reports ok=false so the
// caller skips it.
func (s *Store) lintContent(ctx context.Context, draftID string, f File, issues *[]Issue) ([]byte, bool) {
	content := f.Content
	if content == nil && f.Size > 0 {
		var err error
		content, _, err = s.ReadFile(ctx, draftID, f.Path)
		if err != nil {
			*issues = append(*issues, Issue{
				Severity: SeverityWarning, Code: "file_unreadable", Path: f.Path,
				Message: fmt.Sprintf("could not read file for validation: %v", err),
			})
			return nil, false
		}
	}
	return content, true
}

// appendIssue appends iss only when it's non-nil, keeping the call sites
// in lintFiles compact.
func appendIssue(issues []Issue, iss *Issue) []Issue {
	if iss == nil {
		return issues
	}
	return append(issues, *iss)
}

// lintJSON reports the first JSON syntax error, if any. Decoding into an
// empty interface validates structure and trailing-garbage without
// caring about a schema.
func lintJSON(path string, content []byte) *Issue {
	var v any
	err := json.Unmarshal(content, &v)
	if err == nil {
		return nil
	}
	iss := Issue{
		Severity: SeverityError, Code: "json_syntax", Path: path,
		Message: err.Error(),
	}
	var se *json.SyntaxError
	if errors.As(err, &se) {
		iss.Line, iss.Col = offsetToPos(content, int(se.Offset))
		iss.Message = fmt.Sprintf("JSON syntax error: %s", se.Error())
	}
	return &iss
}

// yamlLineRe extracts the line number yaml.v3 embeds in its error
// strings, e.g. "yaml: line 3: mapping values are not allowed here".
var yamlLineRe = regexp.MustCompile(`line (\d+):`)

// lintYAML reports a YAML parse error, if any. Decoding into an empty
// interface surfaces syntax errors without imposing a schema; yaml.v3
// reports a line (but no column) in its message, which we extract.
func lintYAML(path string, content []byte) *Issue {
	var v any
	err := yaml.Unmarshal(content, &v)
	if err == nil {
		return nil
	}
	iss := Issue{
		Severity: SeverityError, Code: "yaml_syntax", Path: path,
		Message: fmt.Sprintf("YAML syntax error: %s", err.Error()),
	}
	if m := yamlLineRe.FindStringSubmatch(err.Error()); m != nil {
		// Errors helper: ParseInt is overkill for a small line number.
		line := 0
		for _, c := range m[1] {
			line = line*10 + int(c-'0')
		}
		iss.Line = line // Col stays 0: yaml.v3 doesn't report a column.
	}
	return &iss
}

// lintTOML reports a TOML parse error, if any. BurntSushi surfaces a
// ParseError carrying a byte offset, which we convert to line/column.
func lintTOML(path string, content []byte) *Issue {
	var v map[string]any
	err := toml.Unmarshal(content, &v)
	if err == nil {
		return nil
	}
	iss := Issue{
		Severity: SeverityError, Code: "toml_syntax", Path: path,
		Message: fmt.Sprintf("TOML syntax error: %s", err.Error()),
	}
	var pe toml.ParseError
	if errors.As(err, &pe) {
		if pe.Position.Start > 0 {
			iss.Line, iss.Col = offsetToPos(content, pe.Position.Start)
		} else if pe.Position.Line > 0 {
			iss.Line = pe.Position.Line
		}
	}
	return &iss
}

// offsetToPos converts a byte offset into a 1-based (line, column). The
// column is counted in bytes, which matches ASCII-dominant structured
// files closely enough for jump-to-position; a clamp keeps an
// out-of-range offset (some parsers report one past EOF) in bounds.
func offsetToPos(content []byte, offset int) (line, col int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(content) {
		offset = len(content)
	}
	line, col = 1, 1
	for i := 0; i < offset; i++ {
		if content[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// firstInvalidUTF8 returns the byte offset of the first invalid UTF-8
// sequence, or ok=false when the whole slice is valid.
func firstInvalidUTF8(content []byte) (offset int, ok bool) {
	for i := 0; i < len(content); {
		r, size := utf8.DecodeRune(content[i:])
		if r == utf8.RuneError && size == 1 {
			return i, true
		}
		i += size
	}
	return 0, false
}
