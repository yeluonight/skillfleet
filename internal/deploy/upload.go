package deploy

// Upload wire types for skill adoption (mgmt-refactor track A). These cross
// the agent↔server boundary on POST /agent/upload: the agent (after running
// a capture_skill job) reads a discovered skill's files and uploads the
// bytes; the server adopts them into the registry as a new version. They
// live in deploy alongside the other wire types so both agentclient (caller)
// and agentapi (handler) can share them without either importing the other.

// UploadFile is one file of an adopted skill, content base64-encoded so it
// survives JSON transport regardless of binary vs text.
type UploadFile struct {
	Path          string `json:"path"`           // package-relative, forward-slash
	ContentBase64 string `json:"content_base64"` // base64 of the raw bytes
}

// AdoptSource records where the captured skill came from, for provenance /
// audit. All fields advisory; the server overrides DeviceID from the signed
// request context.
type AdoptSource struct {
	DeviceID string `json:"device_id,omitempty"`
	ToolKey  string `json:"tool_key,omitempty"`
	Scope    string `json:"scope,omitempty"`
	RootID   string `json:"root_id,omitempty"`
}

// UploadRequest is the POST /agent/upload body.
type UploadRequest struct {
	SkillName string       `json:"skill_name"`
	Files     []UploadFile `json:"files"`
	Source    AdoptSource  `json:"source,omitempty"`
}

// UploadResponse is the POST /agent/upload reply.
type UploadResponse struct {
	VersionID string `json:"version_id"`
}

// AdoptFile is one decoded skill file passed to a SkillAdopter. The upload
// handler decodes UploadFile.ContentBase64 once into Content; the adopter
// consumes raw bytes, so the base64 is never decoded twice.
type AdoptFile struct {
	Path    string
	Content []byte
}
