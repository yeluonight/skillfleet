// Package inventory defines the wire types for the /agent/inventory
// submission (v1.0 §14.2) plus the server-side persistence that lands
// a report into the migration-0005 tables.
//
// The Report type is shared by the agent (which builds it from adapter
// scans) and the server (which validates + stores it), so the JSON
// contract lives in exactly one place. Persistence is server-only and
// guarded by a build-tag-free split: the agent imports only the wire
// types, the server imports Store too. There is no import cycle
// because the types carry no behaviour.
package inventory

// Report is the full inventory payload an agent POSTs after scanning
// every tool's roots. It is a snapshot: the server replaces the
// device's prior tool_instances + discovered_skills with this set.
type Report struct {
	// AgentVersion is the reporting agent's build version, recorded on
	// the inventory_runs row for drift diagnostics.
	AgentVersion string `json:"agent_version,omitempty"`

	// Tools is one entry per (tool, scope-root) the agent scanned.
	Tools []ToolInstance `json:"tools"`
}

// ToolInstance is one scanned root: a tool at a particular scope and
// filesystem location, plus the skills found beneath it.
type ToolInstance struct {
	ToolKey     string  `json:"tool_key"`
	DisplayName string  `json:"display_name"`
	Scope       string  `json:"scope"`     // user | project | system
	RootID      string  `json:"root_id"`   // adapter-local id, e.g. "claude_user"
	RootPath    string  `json:"root_path"` // absolute
	Skills      []Skill `json:"skills"`
}

// Skill is one discovered skill within a ToolInstance.
type Skill struct {
	Name           string    `json:"name"`
	SkillPath      string    `json:"skill_path"`
	HasSkillMD     bool      `json:"has_skill_md"`
	Description    string    `json:"description,omitempty"`
	EffectiveState string    `json:"effective_state"`
	NativeState    string    `json:"native_state,omitempty"`
	ContentSHA256  string    `json:"content_sha256,omitempty"`
	FileCount      int       `json:"file_count"`
	TotalBytes     int64     `json:"total_bytes"`
	Warnings       []Warning `json:"warnings,omitempty"`
}

// Warning mirrors an adapter / parser finding so the WebUI can surface
// it inline in the matrix.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SkillCount returns the total number of skills across all tools — the
// denormalised summary stored on the inventory_runs row.
func (r Report) SkillCount() int {
	n := 0
	for _, t := range r.Tools {
		n += len(t.Skills)
	}
	return n
}

// RootCount returns the number of scanned roots (tool instances).
func (r Report) RootCount() int { return len(r.Tools) }
