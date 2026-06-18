package draft

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/skill"
	"github.com/yeluonight/skillfleet/internal/skillmd"
)

// Severity classifies a validation finding.
type Severity string

const (
	// SeverityError blocks publish (e.g. no SKILL.md, broken
	// frontmatter, name mismatch).
	SeverityError Severity = "error"
	// SeverityWarning is advisory; publish proceeds (e.g. missing
	// description).
	SeverityWarning Severity = "warning"
)

// Issue is one validation finding for a draft (v1.0 §7.3 "Validate").
type Issue struct {
	Severity Severity
	Code     string
	Path     string // package-relative file the issue concerns, or ""
	Message  string
	// Line/Col locate the issue within Path, 1-based. Zero means the
	// finding has no specific position (file-level or whole-package).
	Line int
	Col  int
}

// ErrValidationFailed is returned by Publish when the draft has
// SeverityError issues.
var ErrValidationFailed = errors.New("draft: validation failed")

// Validate checks a draft's content for publish-readiness and returns
// the findings. A draft with no SeverityError issues is publishable.
// Validation never mutates the draft.
func (s *Store) Validate(ctx context.Context, draftID string) ([]Issue, error) {
	d, err := s.Load(ctx, draftID)
	if err != nil {
		return nil, err
	}
	return s.validateDraft(ctx, d), nil
}

// validateDraft runs the content checks. Kept separate so Publish can
// reuse it without a second Load.
func (s *Store) validateDraft(ctx context.Context, d Draft) []Issue {
	var issues []Issue

	// 1. A SKILL.md must exist at the package root.
	var skillMD *File
	for i := range d.Files {
		if d.Files[i].Path == skill.SkillMDName {
			skillMD = &d.Files[i]
			break
		}
	}
	if skillMD == nil {
		issues = append(issues, Issue{
			Severity: SeverityError, Code: "missing_skill_md",
			Message: "package must contain a SKILL.md at its root",
		})
		// Without a SKILL.md there's nothing more to check on it.
		return issues
	}

	// 2. SKILL.md must be text (a binary SKILL.md is meaningless).
	if skillMD.IsBinary {
		issues = append(issues, Issue{
			Severity: SeverityError, Code: "skill_md_binary", Path: skill.SkillMDName,
			Message: "SKILL.md must be a UTF-8 text file",
		})
		return issues
	}

	// 3. Parse SKILL.md frontmatter. Read fresh so we validate the
	// stored bytes (Load inlines text, but be defensive for blobs).
	content, _, err := s.ReadFile(ctx, d.ID, skill.SkillMDName)
	if err != nil {
		issues = append(issues, Issue{
			Severity: SeverityError, Code: "skill_md_unreadable", Path: skill.SkillMDName,
			Message: err.Error(),
		})
		return issues
	}
	res, perr := skillmd.Parse(content, d.Name)
	if perr != nil {
		issues = append(issues, Issue{
			Severity: SeverityError, Code: "frontmatter_invalid", Path: skill.SkillMDName,
			Message: perr.Error(),
		})
		return issues
	}

	// 4. frontmatter.name should match the draft/skill name.
	if res.Name == "" {
		issues = append(issues, Issue{
			Severity: SeverityError, Code: "missing_name", Path: skill.SkillMDName,
			Message: "SKILL.md frontmatter must declare a name",
		})
	} else if res.Name != d.Name {
		issues = append(issues, Issue{
			Severity: SeverityError, Code: "name_mismatch", Path: skill.SkillMDName,
			Message: fmt.Sprintf("frontmatter name %q does not match skill name %q", res.Name, d.Name),
		})
	}

	// 5. description is recommended but not required.
	if res.Description == "" {
		issues = append(issues, Issue{
			Severity: SeverityWarning, Code: "missing_description", Path: skill.SkillMDName,
			Message: "SKILL.md has no description",
		})
	}

	// 6. Carry through any skillmd parser warnings (encoding, etc.).
	for _, w := range res.Warnings {
		issues = append(issues, Issue{
			Severity: SeverityWarning, Code: w.Code, Path: skill.SkillMDName, Message: w.Message,
		})
	}

	// 7. Lint every text file by extension (JSON/YAML/TOML syntax,
	// UTF-8 validity) with line/column positions. Structured-data
	// files that won't parse block publish; encoding problems too.
	issues = append(issues, s.lintFiles(ctx, d)...)

	return issues
}

// HasErrors reports whether any issue is SeverityError.
func HasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == SeverityError {
			return true
		}
	}
	return false
}

// PublishResult is returned by Publish.
type PublishResult struct {
	Version registry.Version
	Issues  []Issue // validation findings (warnings only on success)
}

// Publish validates an open draft and, if it has no error-level issues,
// materialises its files into a new immutable registry version (kind
// draft_publish, base = the draft's base version), then marks the draft
// published. Returns ErrValidationFailed (with the issues) when the
// draft has errors, ErrNotOpen if it isn't open, ErrNotFound if absent.
func (s *Store) Publish(ctx context.Context, draftID string, now time.Time) (PublishResult, error) {
	d, err := s.Load(ctx, draftID)
	if err != nil {
		return PublishResult{}, err
	}
	if d.Status != StatusOpen {
		return PublishResult{}, ErrNotOpen
	}

	issues := s.validateDraft(ctx, d)
	if HasErrors(issues) {
		return PublishResult{Issues: issues}, ErrValidationFailed
	}

	// Materialise every file (text inline + binary from blob) into the
	// in-memory set the registry publishes from.
	files := make([]registry.InMemoryFile, 0, len(d.Files))
	for _, f := range d.Files {
		content, _, err := s.ReadFile(ctx, d.ID, f.Path)
		if err != nil {
			return PublishResult{}, fmt.Errorf("draft: read %q for publish: %w", f.Path, err)
		}
		files = append(files, registry.InMemoryFile{Path: f.Path, Content: content})
	}

	v, err := s.registry.PublishFromFiles(ctx, files, registry.PublishParams{
		Name:          d.Name,
		Kind:          registry.KindDraftPublish,
		BaseVersionID: d.BaseVersionID,
	}, now)
	if err != nil {
		return PublishResult{}, fmt.Errorf("draft: publish to registry: %w", err)
	}

	// Mark the draft published. This is the one allowed status
	// transition out of open (besides discard); after it, file
	// mutations are rejected (status != open).
	if _, err := s.db.ExecContext(ctx,
		`UPDATE skill_drafts SET status = ?, updated_at = ? WHERE id = ?`,
		StatusPublished, now.UnixMilli(), d.ID); err != nil {
		return PublishResult{}, fmt.Errorf("draft: mark published: %w", err)
	}

	return PublishResult{Version: v, Issues: issues}, nil
}
