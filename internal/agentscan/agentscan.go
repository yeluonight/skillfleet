// Package agentscan runs every registered tool adapter against the
// local machine and assembles the result into an inventory.Report
// ready to POST to /agent/inventory.
//
// It is the agent-side composition root for internal/adapters: the
// adapters themselves are pure (given a ScanContext they resolve roots
// and read skills), and this package wires the full set together, maps
// each DiscoveredSkill onto the inventory wire types, and collects
// per-adapter errors without letting one failing tool blind the rest.
package agentscan

import (
	"log/slog"
	"os"

	"github.com/yeluonight/skillfleet/internal/adapters"
	"github.com/yeluonight/skillfleet/internal/adapters/antigravity"
	"github.com/yeluonight/skillfleet/internal/adapters/antigravitycli"
	"github.com/yeluonight/skillfleet/internal/adapters/claudecode"
	"github.com/yeluonight/skillfleet/internal/adapters/codex"
	"github.com/yeluonight/skillfleet/internal/adapters/opencode"
	"github.com/yeluonight/skillfleet/internal/adapters/pi"
	"github.com/yeluonight/skillfleet/internal/inventory"
)

// All returns the full set of read-only adapters the agent scans. The
// order is stable so a report's tool list is deterministic.
func All() []adapters.ReadOnlyAdapter {
	return []adapters.ReadOnlyAdapter{
		claudecode.New(),
		codex.New(),
		opencode.New(),
		antigravity.New(),
		antigravitycli.New(),
		pi.New(),
	}
}

// Options controls a scan. AgentVersion is stamped onto the report;
// HomeDir / ProjectRoots feed the adapters' ScanContext (HomeDir
// defaults to os.UserHomeDir when empty).
type Options struct {
	AgentVersion string
	HomeDir      string
	ProjectRoots []string
	Logger       *slog.Logger
}

// Scan runs every adapter and returns the assembled report. Adapter /
// root errors are logged (when a Logger is set) and skipped; the scan
// always returns a usable report rather than failing wholesale, so a
// single misbehaving tool never blocks the inventory upload.
func Scan(opts Options) inventory.Report {
	home := opts.HomeDir
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}

	sc := adapters.ScanContext{
		HomeDir:      home,
		ProjectRoots: opts.ProjectRoots,
	}

	report := inventory.Report{AgentVersion: opts.AgentVersion}

	for _, ad := range All() {
		roots, err := ad.SkillRoots(sc)
		if err != nil {
			logWarn(opts.Logger, "skill roots failed", ad.Key(), err)
			continue
		}
		for _, root := range roots {
			skills, err := ad.ScanSkills(sc, root)
			if err != nil {
				logWarn(opts.Logger, "scan skills failed", ad.Key(), err)
				continue
			}
			report.Tools = append(report.Tools, toToolInstance(ad, root, skills))
		}
	}
	return report
}

// toToolInstance maps one adapter root + its skills onto the wire type.
func toToolInstance(ad adapters.ReadOnlyAdapter, root adapters.SkillRoot, skills []adapters.DiscoveredSkill) inventory.ToolInstance {
	ti := inventory.ToolInstance{
		ToolKey:     ad.Key(),
		DisplayName: ad.DisplayName(),
		Scope:       string(root.Scope),
		RootID:      root.ID,
		RootPath:    root.Path,
	}
	for _, ds := range skills {
		ti.Skills = append(ti.Skills, toSkill(ds))
	}
	return ti
}

// toSkill maps a DiscoveredSkill onto the wire Skill, flattening both
// the parser's and the adapter's warnings into one list.
func toSkill(ds adapters.DiscoveredSkill) inventory.Skill {
	sk := inventory.Skill{
		Name:           ds.Name,
		SkillPath:      ds.Path,
		HasSkillMD:     ds.HasSkillMD,
		Description:    ds.SkillMD.Description,
		EffectiveState: string(ds.EffectiveState),
		NativeState:    ds.NativeState,
		ContentSHA256:  ds.ContentSHA256,
		FileCount:      ds.FileCount,
		TotalBytes:     ds.TotalBytes,
	}
	for _, w := range ds.SkillMD.Warnings {
		sk.Warnings = append(sk.Warnings, inventory.Warning{Code: w.Code, Message: w.Message})
	}
	for _, w := range ds.Warnings {
		sk.Warnings = append(sk.Warnings, inventory.Warning{Code: w.Code, Message: w.Message})
	}
	return sk
}

func logWarn(log *slog.Logger, msg, tool string, err error) {
	if log == nil {
		return
	}
	log.Warn("agentscan: "+msg, slog.String("tool", tool), slog.String("err", err.Error()))
}
