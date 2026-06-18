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

	// Roots is the agent's candidate-root discovery: every user/system
	// location a tool COULD keep skills, whether or not it exists, joined
	// with which are already registered in the agent's allowed_roots
	// (Phase 11). The server stores this so the WebUI can offer one-click
	// registration. Distinct from Tools, which only lists existing
	// scanned roots; Roots also lists not-yet-created suggestions.
	Roots []RootCandidate `json:"roots,omitempty"`
}

// RootCandidate is one discoverable skill location reported to the
// server for the WebUI's registration UI. It mirrors
// adapters.CandidateRoot plus the Registered/RootID join the agent
// computes against its own allowed_roots — the server never resolves
// paths itself, it only displays what the agent reports.
type RootCandidate struct {
	ToolKey      string `json:"tool_key"`
	Scope        string `json:"scope"` // user | system (project omitted this phase)
	Path         string `json:"path"`  // absolute, ~-expanded
	DisplayTmpl  string `json:"display_tmpl,omitempty"`
	Exists       bool   `json:"exists"`
	Registered   bool   `json:"registered"`
	RootID       string `json:"root_id,omitempty"` // set when Registered
	ToolDetected bool   `json:"tool_detected,omitempty"`
	Shared       bool   `json:"shared,omitempty"`
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
	// ModifiedAt is the newest file mtime in the skill dir, unix millis
	// (0 when unknown). Lets the WebUI show "last edited on device".
	ModifiedAt int64     `json:"modified_at,omitempty"`
	Warnings   []Warning `json:"warnings,omitempty"`
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
