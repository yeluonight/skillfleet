// Package skillmd parses SKILL.md files (v1.0 §7.6).
//
// A SKILL.md is a Markdown document whose first block is a YAML
// frontmatter delimited by lines containing only "---". The
// frontmatter carries the skill's name, description, and tool-specific
// hints; the Markdown body is the prompt the agent eventually sees.
//
// Spec checks this package enforces:
//
//   - File MUST be valid UTF-8 (BOM tolerated; other encodings flagged
//     via Result.Encoding instead of erroring so adapters can still
//     scan a directory that includes one bad file).
//   - First non-empty line MUST be a YAML frontmatter opener "---".
//     Files without frontmatter parse, but emit a warning so adapters
//     can decide what to do (most treat it as a hard error).
//   - YAML frontmatter MUST be parseable; failure is a hard error and
//     leaves Result.Frontmatter nil.
//   - `description` MUST be a non-empty string OR a warning is emitted
//     (some tools tolerate skills without a description).
//   - `name` MUST match the parent directory name when both are
//     present; mismatch is a warning, not an error (folder name
//     remains authoritative).
//
// What this package deliberately does NOT do:
//
//   - It does not interpret tool-specific fields (allowed-tools,
//     user-invocable, permission maps, …). Each adapter owns its own
//     decoding off of Result.Frontmatter (a typed map).
//   - It does not walk directories or resolve sibling files. That
//     belongs to the per-tool adapter (internal/adapters).
//   - It does not sanitise the Markdown body. Preview-time
//     sanitisation lives in the preview layer (Phase 5).
package skillmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// MaxFileBytes caps SKILL.md size to keep a malformed scan from
// eating memory. 1 MiB is generous (most SKILL.md sit under 10 KB).
const MaxFileBytes = 1 << 20 // 1 MiB

// Encoding describes the encoding state of the file's raw bytes.
type Encoding string

const (
	EncodingUTF8    Encoding = "utf-8"
	EncodingUTF8BOM Encoding = "utf-8-bom"
	EncodingNonUTF8 Encoding = "non-utf-8"
)

// Errors returned by Parse. Sentinel-shaped so adapters can wrap them
// with file context without losing the category.
var (
	ErrTooLarge       = errors.New("skillmd: file exceeds size limit")
	ErrFrontmatterBad = errors.New("skillmd: frontmatter YAML invalid")
)

// Warning describes a non-fatal spec violation. Adapters surface
// these to the operator via the inventory matrix.
type Warning struct {
	Code    string // stable identifier (e.g. "missing_description")
	Message string // human-friendly explanation
}

// Result is the in-memory projection of a parsed SKILL.md.
type Result struct {
	// Name is the skill's declared name (frontmatter.name). Empty if
	// the frontmatter omits it.
	Name string

	// Description is frontmatter.description. Empty if missing.
	Description string

	// Frontmatter is the full decoded YAML map. Adapter-specific
	// fields (allowed-tools, user-invocable, etc.) live here.
	Frontmatter map[string]any

	// Body is the Markdown content following the frontmatter (with
	// the leading/trailing newline trimmed). When the file has no
	// frontmatter, Body holds the entire UTF-8 text.
	Body string

	// Encoding records how the source file was encoded. Adapters
	// can decide whether to treat non-UTF-8 as a hard error.
	Encoding Encoding

	// Warnings is the (possibly empty) list of non-fatal findings.
	// Ordered as discovered; not deduplicated.
	Warnings []Warning
}

// ParseFile reads path and runs Parse on its contents. The skill's
// directory name (derived from filepath.Base(filepath.Dir(path))) is
// used to validate frontmatter.name agreement. A non-SKILL.md basename
// is permitted so adapters that link or rename for staging still work.
func ParseFile(path string) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, fmt.Errorf("skillmd: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// Size check up-front so we don't waste an io.ReadAll on a huge
	// file. We add 1 to MaxFileBytes so the limit is "<= MaxFileBytes"
	// rather than "< MaxFileBytes".
	if info, statErr := f.Stat(); statErr == nil && info.Size() > MaxFileBytes {
		return Result{}, fmt.Errorf("%w: %d bytes > %d", ErrTooLarge, info.Size(), MaxFileBytes)
	}

	raw, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return Result{}, fmt.Errorf("skillmd: read %s: %w", path, err)
	}
	if int64(len(raw)) > MaxFileBytes {
		return Result{}, fmt.Errorf("%w: > %d", ErrTooLarge, MaxFileBytes)
	}

	dirName := filepath.Base(filepath.Dir(path))
	res, err := Parse(raw, dirName)
	if err != nil {
		return res, fmt.Errorf("skillmd: parse %s: %w", path, err)
	}
	return res, nil
}

// Parse parses the raw bytes of a SKILL.md. dirName is the parent
// directory's base name; pass "" to skip the name-vs-folder check.
//
// Returns ErrFrontmatterBad when the YAML block is present but
// unparseable; everything else (missing description, name mismatch,
// non-UTF-8) is a warning so adapters can still ingest partial data.
func Parse(raw []byte, dirName string) (Result, error) {
	res := Result{Encoding: detectEncoding(raw)}

	// Strip BOM before any further parsing so frontmatter detection
	// doesn't have to special-case it.
	if res.Encoding == EncodingUTF8BOM {
		raw = raw[3:]
	}

	if res.Encoding == EncodingNonUTF8 {
		// Don't try to parse non-UTF-8 — the YAML decoder would
		// either fail noisily or quietly mangle multibyte chars.
		// Surface the warning and stop here; Body stays empty.
		res.Warnings = append(res.Warnings, Warning{
			Code:    "non_utf8",
			Message: "file is not valid UTF-8; skipped",
		})
		return res, nil
	}

	fm, body, hadFrontmatter := splitFrontmatter(raw)
	if !hadFrontmatter {
		res.Body = strings.TrimSpace(string(raw))
		res.Warnings = append(res.Warnings, Warning{
			Code:    "missing_frontmatter",
			Message: "SKILL.md must begin with a YAML frontmatter delimited by '---'",
		})
		return res, nil
	}

	var decoded map[string]any
	if err := yaml.Unmarshal(fm, &decoded); err != nil {
		return res, fmt.Errorf("%w: %s", ErrFrontmatterBad, err.Error())
	}
	if decoded == nil {
		// Empty (but well-formed) frontmatter — treat as empty map
		// so the caller's field lookups don't NPE.
		decoded = map[string]any{}
	}
	res.Frontmatter = decoded
	res.Body = strings.TrimSpace(string(body))

	if n, ok := decoded["name"].(string); ok {
		res.Name = strings.TrimSpace(n)
	}
	if d, ok := decoded["description"].(string); ok {
		res.Description = strings.TrimSpace(d)
	}

	if res.Description == "" {
		res.Warnings = append(res.Warnings, Warning{
			Code:    "missing_description",
			Message: "frontmatter is missing a non-empty description",
		})
	}
	if dirName != "" && res.Name != "" && res.Name != dirName {
		res.Warnings = append(res.Warnings, Warning{
			Code: "name_folder_mismatch",
			Message: fmt.Sprintf(
				"frontmatter.name (%q) differs from folder name (%q); folder wins",
				res.Name, dirName,
			),
		})
	}
	return res, nil
}

// detectEncoding returns EncodingUTF8 / EncodingUTF8BOM / EncodingNonUTF8.
// The BOM signature is the first 3 bytes 0xEF 0xBB 0xBF.
func detectEncoding(raw []byte) Encoding {
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		// Validate the body (after BOM) for completeness.
		if utf8.Valid(raw[3:]) {
			return EncodingUTF8BOM
		}
		return EncodingNonUTF8
	}
	if utf8.Valid(raw) {
		return EncodingUTF8
	}
	return EncodingNonUTF8
}

// splitFrontmatter finds the YAML block delimited by lines containing
// only "---". The opening delimiter must be the first non-empty line.
// Returns (frontmatterBytes, bodyBytes, true) on success; (_,_,false)
// when no frontmatter block was found.
//
// Edge cases:
//   - Trailing whitespace on the "---" line is tolerated.
//   - CRLF is normalised by working on a byte-line basis (each line
//     is compared after stripping a trailing \r).
//   - An unterminated frontmatter (opener with no matching closer)
//     is treated as "no frontmatter" so the file's body remains
//     intact and the missing_frontmatter warning fires; adapters
//     decide whether to escalate.
func splitFrontmatter(raw []byte) (fm, body []byte, ok bool) {
	lines := bytes.SplitAfter(raw, []byte("\n"))
	// Find the opener.
	openIdx := -1
	for i, ln := range lines {
		trimmed := trimRightLine(ln)
		if len(trimmed) == 0 {
			continue
		}
		if string(trimmed) == "---" {
			openIdx = i
			break
		}
		// Anything other than blank-or-"---" means no frontmatter.
		return nil, raw, false
	}
	if openIdx == -1 {
		return nil, raw, false
	}
	// Find the closer (first "---" after the opener).
	closeIdx := -1
	for j := openIdx + 1; j < len(lines); j++ {
		if string(trimRightLine(lines[j])) == "---" {
			closeIdx = j
			break
		}
	}
	if closeIdx == -1 {
		return nil, raw, false
	}
	fm = bytes.Join(lines[openIdx+1:closeIdx], nil)
	body = bytes.Join(lines[closeIdx+1:], nil)
	return fm, body, true
}

// trimRightLine returns ln without its trailing \n / \r\n / spaces.
func trimRightLine(ln []byte) []byte {
	return bytes.TrimRight(ln, " \t\r\n")
}
