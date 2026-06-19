// planner.go turns an operator's install intent into a Plan — the
// authoritative new-content spec the agent executes. It runs entirely
// server-side: it reads the registry version (its manifest + archive)
// and produces the plan_json stored on the job. It does NOT know or
// resolve any device filesystem path; the Target in the request travels
// to the agent untouched, and the agent resolves it against its own
// allowed_roots.
//
// To keep this package from importing internal/registry (so the agent,
// which imports deploy for its wire types, doesn't transitively link the
// server-only registry + its archive code), the planner depends on a
// consumer-side interface — the same pattern used by drift.VersionLister
// and api.SourceFetcher elsewhere in this codebase. The server wires a
// tiny adapter over *registry.Store at call time.

package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeluonight/skillfleet/internal/skill"
)

// Errors returned by the planner.
var (
	ErrPlanNoVersion    = errors.New("deploy: plan version not found")
	ErrPlanNoArchive    = errors.New("deploy: plan version has no package archive")
	ErrPlanNameMismatch = errors.New("deploy: version does not belong to the named skill")
	ErrPlanNoSkill      = errors.New("deploy: state change requires a skill name")
	ErrPlanNoTool       = errors.New("deploy: state change requires a tool key")
)

// VersionRef is the minimal view of a registry version the planner
// needs. It mirrors the relevant fields of registry.Version so this
// package needn't import registry. The adapter in the server fills it
// from a real *registry.Store.
type VersionRef struct {
	ID            string
	Name          string
	BaseVersionID string
	ContentSHA256 string
	Manifest      skill.Manifest
	// PackagePath is the store-relative archive path
	// ("packages/<archiveSHA>.tgz"); the archive sha is its basename.
	PackagePath string
}

// RegistryReader is the consumer-side interface the planner needs from
// the registry. *registry.Store satisfies an adapter for this (Get
// returns registry.Version; the server maps it to VersionRef and
// supplies ArchivePath).
type RegistryReader interface {
	// GetVersion loads a version by id, returning ErrPlanNoVersion-class
	// absence as a non-nil error the planner maps.
	GetVersion(ctx context.Context, versionID string) (VersionRef, error)
	// ArchiveAbsPath returns the absolute path to the version's package
	// archive on the server disk, for stat-ing its size.
	ArchiveAbsPath(v VersionRef) string
}

// Planner builds install Plans from registry versions. It is stateless
// apart from the registry reader, so a fresh one can be constructed per
// request (mirroring how the update-check Checker is built).
type Planner struct {
	reg RegistryReader
}

// NewPlanner returns a Planner backed by reg.
func NewPlanner(reg RegistryReader) *Planner {
	return &Planner{reg: reg}
}

// PlanStateChange validates a state-change request and produces the
// StateChangePlan the agent executes. Unlike PlanInstall it touches no
// registry — a state change needs no version, archive, or file list, just
// the target + desired state — so it is a method on Planner only for API
// symmetry (a nil reg is fine here). It rejects, up front, any state the
// target tool cannot natively represent (statematrix.go), so a job that
// could never succeed is never minted; the agent's writer re-checks as a
// second line of defence.
func (p *Planner) PlanStateChange(req Request) (StateChangePlan, error) {
	if req.Operation != OpStateChange {
		return StateChangePlan{}, fmt.Errorf("deploy: PlanStateChange on non-state-change request %q", req.Operation)
	}
	if req.SkillName == "" {
		return StateChangePlan{}, ErrPlanNoSkill
	}
	if req.Target.ToolKey == "" {
		return StateChangePlan{}, ErrPlanNoTool
	}
	// Reject a state the tool cannot express (e.g. codex + "ask") or a
	// tool with no state-change support (antigravity / pi) before minting
	// a doomed job. ValidateStateChange returns the matrix errors the API
	// maps to 422.
	if err := ValidateStateChange(req.Target.ToolKey, req.DesiredState); err != nil {
		return StateChangePlan{}, err
	}
	return StateChangePlan{
		Target:       req.Target,
		SkillName:    req.SkillName,
		DesiredState: req.DesiredState,
	}, nil
}

// PlanInstall resolves an install request into a Plan. It loads the
// version, verifies it belongs to the requested skill (a version id from
// another skill is a client error, not a silent cross-install), stats
// the archive for its integrity sha + size, and copies the manifest's
// file list into the plan. marker provenance (source block) is taken
// from src when non-nil; the agent fills the marker's Files +
// InstalledAt at execution time.
//
// now stamps nothing in the plan itself (the install timestamp is the
// agent's, set when the swap commits); it is accepted for symmetry with
// the rest of the package and future use.
func (p *Planner) PlanInstall(ctx context.Context, req Request, src *MarkerSource, now time.Time) (Plan, error) {
	if req.Operation != OpInstall {
		return Plan{}, fmt.Errorf("deploy: PlanInstall on non-install request %q", req.Operation)
	}
	if req.VersionID == "" {
		return Plan{}, ErrPlanNoVersion
	}

	v, err := p.reg.GetVersion(ctx, req.VersionID)
	if err != nil {
		return Plan{}, err
	}
	// A version id must belong to the skill the operator named, so a
	// stale or hand-edited request can't install skill B's bytes under
	// skill A's name/marker.
	if req.SkillName != "" && v.Name != req.SkillName {
		return Plan{}, fmt.Errorf("%w: version %s is %q, requested %q",
			ErrPlanNameMismatch, v.ID, v.Name, req.SkillName)
	}
	if v.PackagePath == "" {
		return Plan{}, ErrPlanNoArchive
	}

	// Archive sha is the basename without the .tgz suffix (content
	// addressing, ADR-0008); size comes from stat-ing the file so the
	// agent can bound its download.
	archiveSHA := strings.TrimSuffix(filepath.Base(v.PackagePath), ".tgz")
	if archiveSHA == "" || archiveSHA == v.PackagePath {
		return Plan{}, ErrPlanNoArchive
	}
	info, err := os.Stat(p.reg.ArchiveAbsPath(v))
	if err != nil {
		return Plan{}, fmt.Errorf("deploy: stat archive: %w", err)
	}

	files := make([]FileSpec, 0, len(v.Manifest.Files))
	for _, f := range v.Manifest.Files {
		files = append(files, FileSpec{
			Path:   f.Path,
			SHA256: f.SHA256,
			Size:   f.Size,
			Exec:   f.Exec,
			Binary: f.Binary,
		})
	}

	plan := Plan{
		VersionID:     v.ID,
		SkillName:     v.Name,
		ContentSHA256: v.ContentSHA256,
		ArchiveSHA256: archiveSHA,
		ArchiveBytes:  info.Size(),
		DownloadPath:  "/agent/packages/" + v.ID,
		Marker: InstallMarker{
			ManagedBy:          "skillfleet",
			SkillName:          v.Name,
			InstalledVersionID: v.ID,
			BaseVersionID:      v.BaseVersionID,
			Source:             src,
			ContentSHA256:      v.ContentSHA256,
			// Files + InstalledAt are filled by the agent post-swap.
		},
		Files: files,
	}
	return plan, nil
}
