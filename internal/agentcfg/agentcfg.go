// Package agentcfg loads / persists the SkillFleet agent configuration
// at `~/.skillfleet/agent/agent.json` (v1.0 §8.2).
//
// The file is the agent's only persistent state for phase 2:
//
//   - Before enrolment: missing entirely; the agent only accepts the
//     `enroll` subcommand in this state.
//   - After enrolment: contains server_url + device_id + device_secret
//   - interval defaults; persisted with 0o600 so the secret stays
//     readable only by the running user.
//
// JSON is preferred over YAML here because the file is touched
// programmatically by the agent itself (enroll writes it, future
// rotation will rewrite it) and JSON sidesteps yaml.v3's bespoke
// quoting rules for secrets.
package agentcfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DefaultPath is the canonical on-disk location.
const DefaultPath = "~/.skillfleet/agent/agent.json"

// Defaults for the interval fields when callers omit them — matches
// v1.0 §8.2 sample. Other fields have no useful default; the caller
// must populate them (typically from the enroll response).
const (
	DefaultHeartbeatSec = 30
	DefaultInventorySec = 300
	// DefaultJobsSec is the downlink poll cadence (phase 8). The agent
	// checks for pending deployment jobs this often; 15s keeps install
	// latency low without hammering the server.
	DefaultJobsSec = 15
)

// AllowedRoot is one filesystem location the agent may install into
// (v1.0 §9.1). It is the agent's local authority on where a deployment
// may write: the server addresses a target by {tool, scope, root id},
// and the agent resolves it against this list — a target matching none
// is refused, so the agent never writes outside an operator-registered
// root. Path must be absolute.
type AllowedRoot struct {
	ID    string `json:"id"`
	Tool  string `json:"tool"`
	Scope string `json:"scope"`
	Path  string `json:"path"`
}

// Config is the in-memory projection of agent.json.
type Config struct {
	ServerURL       string        `json:"server_url"`
	DeviceID        string        `json:"device_id"`
	DeviceSecret    string        `json:"device_secret"`
	HeartbeatIntSec int           `json:"heartbeat_interval_sec"`
	InventoryIntSec int           `json:"inventory_interval_sec"`
	JobsIntSec      int           `json:"jobs_interval_sec"`
	AllowedRoots    []AllowedRoot `json:"allowed_roots"`
}

// Load reads + validates the file at path. fs.ErrNotExist is returned
// verbatim so callers can branch on "not enrolled yet".
func Load(path string) (Config, error) {
	expanded, err := ExpandHome(path)
	if err != nil {
		return Config{}, fmt.Errorf("agentcfg: expand path: %w", err)
	}
	raw, err := os.ReadFile(expanded)
	if err != nil {
		return Config{}, err // wrap-less so errors.Is(err, fs.ErrNotExist) works
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("agentcfg: parse %s: %w", expanded, err)
	}
	if cfg.HeartbeatIntSec == 0 {
		cfg.HeartbeatIntSec = DefaultHeartbeatSec
	}
	if cfg.InventoryIntSec == 0 {
		cfg.InventoryIntSec = DefaultInventorySec
	}
	if cfg.JobsIntSec == 0 {
		cfg.JobsIntSec = DefaultJobsSec
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save writes the canonical JSON form of cfg to path with permissions
// 0o600. Parent directories are created with 0o700 so the secret never
// transits a world-readable directory. Save refuses to overwrite an
// existing file (use Rotate when explicit replacement is desired).
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	expanded, err := ExpandHome(path)
	if err != nil {
		return fmt.Errorf("agentcfg: expand path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(expanded), 0o700); err != nil {
		return fmt.Errorf("agentcfg: mkdir %s: %w", filepath.Dir(expanded), err)
	}
	f, err := os.OpenFile(expanded, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("agentcfg: %s already exists; refusing to overwrite", expanded)
		}
		return fmt.Errorf("agentcfg: create %s: %w", expanded, err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("agentcfg: write %s: %w", expanded, err)
	}
	return nil
}

// SaveForce writes cfg to path, OVERWRITING any existing file
// atomically (temp file in the same dir, fsync, rename). Unlike Save —
// which uses O_EXCL to protect the enrolment secret from an accidental
// re-enrol — SaveForce is for deliberate in-place edits of an
// already-enrolled config (the `roots` subcommands rewrite allowed_roots
// this way). The same-directory rename is atomic on POSIX, so a crash
// leaves either the old or the new config, never a truncated one. The
// final file keeps 0o600 (it still holds the device secret).
func SaveForce(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	expanded, err := ExpandHome(path)
	if err != nil {
		return fmt.Errorf("agentcfg: expand path: %w", err)
	}
	dir := filepath.Dir(expanded)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("agentcfg: mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".agent-*.json.tmp")
	if err != nil {
		return fmt.Errorf("agentcfg: create temp: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("agentcfg: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("agentcfg: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("agentcfg: close temp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("agentcfg: chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, expanded); err != nil {
		return fmt.Errorf("agentcfg: rename temp over config: %w", err)
	}
	committed = true
	return nil
}

// Validate enforces the few invariants the package can check in
// isolation. Server-side rejection of malformed device_id / secret
// remains authoritative.
func (c Config) Validate() error {
	if c.ServerURL == "" {
		return errors.New("agentcfg: server_url must not be empty")
	}
	if c.DeviceID == "" {
		return errors.New("agentcfg: device_id must not be empty")
	}
	if c.DeviceSecret == "" {
		return errors.New("agentcfg: device_secret must not be empty")
	}
	if c.HeartbeatIntSec <= 0 {
		return fmt.Errorf("agentcfg: heartbeat_interval_sec must be positive, got %d", c.HeartbeatIntSec)
	}
	if c.InventoryIntSec <= 0 {
		return fmt.Errorf("agentcfg: inventory_interval_sec must be positive, got %d", c.InventoryIntSec)
	}
	if c.JobsIntSec <= 0 {
		return fmt.Errorf("agentcfg: jobs_interval_sec must be positive, got %d", c.JobsIntSec)
	}
	// allowed_roots is optional (a device may report-only); but any root
	// present must be fully specified with an ABSOLUTE path, since the
	// agent will openat into it. A relative path here would resolve
	// against the agent's cwd — exactly the ambiguity we refuse.
	seen := make(map[string]bool, len(c.AllowedRoots))
	for i, r := range c.AllowedRoots {
		if r.ID == "" {
			return fmt.Errorf("agentcfg: allowed_roots[%d].id must not be empty", i)
		}
		if seen[r.ID] {
			return fmt.Errorf("agentcfg: allowed_roots[%d].id %q is duplicated", i, r.ID)
		}
		seen[r.ID] = true
		if r.Tool == "" || r.Scope == "" {
			return fmt.Errorf("agentcfg: allowed_roots[%d] (%s) must set tool and scope", i, r.ID)
		}
		if !filepath.IsAbs(r.Path) {
			return fmt.Errorf("agentcfg: allowed_roots[%d] (%s) path must be absolute, got %q", i, r.ID, r.Path)
		}
	}
	return nil
}

// ExpandHome turns a leading "~" into the user's home directory.
// Duplicated from internal/config rather than imported to keep the
// agent binary independent of server-only packages.
func ExpandHome(p string) (string, error) {
	if p == "" || p[0] != '~' {
		return p, nil
	}
	if len(p) > 1 && p[1] != '/' && p[1] != filepath.Separator {
		return "", fmt.Errorf("only leading ~/ is supported, got %q", p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if len(p) == 1 {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

// ExpandHomeForDisplay is the error-swallowing variant for log lines.
func ExpandHomeForDisplay(p string) string {
	if out, err := ExpandHome(p); err == nil {
		return out
	}
	return p
}
