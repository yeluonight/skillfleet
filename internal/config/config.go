// Package config loads SkillFleet server configuration from a YAML file
// described in IMPLEMENTATION_PLAN.md §8.1.
//
// The package is intentionally small: it returns a fully populated
// [Config] with defaults filled in, expands `~` in paths, and exposes a
// helper to write the canonical default file on first launch.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the in-memory representation of `~/.skillfleet/server/config.yaml`.
//
// All durations are parsed via [time.ParseDuration]; missing fields fall
// back to the values produced by [Defaults]. Unknown fields cause Load to
// fail so typos surface immediately.
type Config struct {
	Server    ServerSection    `yaml:"server"`
	Auth      AuthSection      `yaml:"auth"`
	Agent     AgentSection     `yaml:"agent"`
	GC        GCSection        `yaml:"gc"`
	Scheduler SchedulerSection `yaml:"scheduler"`
	Logging   LoggingSection   `yaml:"logging"`
}

type ServerSection struct {
	Bind        string `yaml:"bind"`
	ExternalURL string `yaml:"external_url"`
	DataDir     string `yaml:"data_dir"`
}

type AuthSection struct {
	SessionTTL time.Duration   `yaml:"session_ttl"`
	RateLimit  RateLimitConfig `yaml:"rate_limit"`
}

// RateLimitConfig keeps the raw "N/unit" strings from YAML so the auth
// layer can parse them with its own bucket implementation later.
type RateLimitConfig struct {
	LoginPerIP   string `yaml:"login_per_ip"`
	LoginPerUser string `yaml:"login_per_user"`
}

type AgentSection struct {
	HMACTimestampWindow time.Duration `yaml:"hmac_timestamp_window"`
	NonceBurstPerMinute int           `yaml:"nonce_burst_per_minute"`
}

type GCSection struct {
	PackageRetentionDays int    `yaml:"package_retention_days"`
	Schedule             string `yaml:"schedule"`
}

// SchedulerSection configures the background update-check poller (§2.9 t7).
// The poller walks every bound source on a fixed cadence and runs the §8.4
// check, publishing a pending upstream version when (and only when) the
// skill subtree content actually changed.
type SchedulerSection struct {
	// UpdateCheckInterval is the cadence between full sweeps of all bound
	// sources. Zero disables the poller entirely (manual check-updates only).
	UpdateCheckInterval time.Duration `yaml:"update_check_interval"`
	// Concurrency caps how many sources are checked in parallel within a
	// sweep. Kept low by default to avoid hammering upstream forges /
	// tripping rate limits. Must be >= 1 when the poller is enabled.
	Concurrency int `yaml:"concurrency"`
}

type LoggingSection struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Defaults returns the canonical default Config (§8.1 verbatim).
// The DataDir is the unexpanded "~/.skillfleet/server" so the value is
// portable across hosts; call [Config.Resolve] to expand it.
func Defaults() Config {
	return Config{
		Server: ServerSection{
			Bind:        "0.0.0.0:7890",
			ExternalURL: "",
			DataDir:     "~/.skillfleet/server",
		},
		Auth: AuthSection{
			SessionTTL: 720 * time.Hour,
			RateLimit: RateLimitConfig{
				LoginPerIP:   "10/min",
				LoginPerUser: "5/min",
			},
		},
		Agent: AgentSection{
			HMACTimestampWindow: 300 * time.Second,
			NonceBurstPerMinute: 1000,
		},
		GC: GCSection{
			PackageRetentionDays: 30,
			Schedule:             "03:00",
		},
		Scheduler: SchedulerSection{
			UpdateCheckInterval: 6 * time.Hour,
			Concurrency:         2,
		},
		Logging: LoggingSection{
			Level:  "info",
			Format: "text",
		},
	}
}

// DefaultPath is the expected on-disk location of the config file.
const DefaultPath = "~/.skillfleet/server/config.yaml"

// defaultYAML is the canonical default file written on first launch.
// It is kept in sync with [Defaults]; the package init enforces this
// by parsing the YAML back and panicking if any field drifts. That
// way a missed update to either constant fails the process at startup
// instead of writing a config that no longer matches Defaults().
const defaultYAML = `# SkillFleet server configuration
# Generated on first launch. Edit and restart the server to apply changes.
# Reference: IMPLEMENTATION_PLAN.md §8.1.
server:
  bind: "0.0.0.0:7890"
  external_url: ""              # reverse-proxy scenarios fill this; empty = derive from Host header
  data_dir: "~/.skillfleet/server"

auth:
  session_ttl: "720h"           # 30 days
  rate_limit:
    login_per_ip: "10/min"
    login_per_user: "5/min"

agent:
  hmac_timestamp_window: "300s"
  nonce_burst_per_minute: 1000

gc:
  package_retention_days: 30
  schedule: "03:00"             # 24-hour local time

scheduler:
  update_check_interval: "6h"   # cadence for the bound-source update poller; "0" disables it
  concurrency: 2                # max sources checked in parallel per sweep (keep low to avoid rate limits)

logging:
  level: "info"                 # debug | info | warn | error
  format: "text"                # text | json
`

func init() {
	var parsed Config
	dec := yaml.NewDecoder(bytes.NewReader([]byte(defaultYAML)))
	dec.KnownFields(true)
	if err := dec.Decode(&parsed); err != nil {
		panic(fmt.Sprintf("config: defaultYAML is not parseable: %v", err))
	}
	if parsed != Defaults() {
		panic(fmt.Sprintf("config: defaultYAML drifted from Defaults():\n  yaml:    %+v\n  default: %+v", parsed, Defaults()))
	}
}

// Load reads the config at path, merges it on top of [Defaults], expands
// `~` in path fields, and validates the result. A missing file is a
// fatal error — callers (i.e. the server main) should call
// [WriteDefault] first when bootstrapping.
func Load(path string) (Config, error) {
	expanded, err := ExpandHome(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: expand path: %w", err)
	}

	raw, err := os.ReadFile(expanded)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", expanded, err)
	}

	cfg := Defaults()
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true) // reject unknown keys to surface typos
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", expanded, err)
	}

	if err := cfg.Resolve(); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// WriteDefault writes [defaultYAML] to path, creating parent directories
// as needed. It refuses to overwrite an existing file so re-running the
// server cannot clobber a hand-edited config; the refusal is atomic via
// O_CREATE|O_EXCL so two concurrent first-launch races cannot both
// silently "win" by passing a stat-then-write check separately.
func WriteDefault(path string) error {
	expanded, err := ExpandHome(path)
	if err != nil {
		return fmt.Errorf("config: expand path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(expanded), 0o755); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", filepath.Dir(expanded), err)
	}
	f, err := os.OpenFile(expanded, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("config: %s already exists; refusing to overwrite", expanded)
		}
		return fmt.Errorf("config: create %s: %w", expanded, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write([]byte(defaultYAML)); err != nil {
		return fmt.Errorf("config: write %s: %w", expanded, err)
	}
	return nil
}

// Resolve expands `~` in path fields. Idempotent.
func (c *Config) Resolve() error {
	expanded, err := ExpandHome(c.Server.DataDir)
	if err != nil {
		return fmt.Errorf("config: server.data_dir: %w", err)
	}
	c.Server.DataDir = expanded
	return nil
}

// Validate enforces the few invariants we can check without touching the
// network or filesystem. The auth/agent layers will revalidate their
// pieces when they consume the values.
func (c Config) Validate() error {
	if c.Server.Bind == "" {
		return errors.New("config: server.bind must not be empty")
	}
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: logging.level must be debug|info|warn|error, got %q", c.Logging.Level)
	}
	switch c.Logging.Format {
	case "text", "json":
	default:
		return fmt.Errorf("config: logging.format must be text|json, got %q", c.Logging.Format)
	}
	if c.Auth.SessionTTL <= 0 {
		return fmt.Errorf("config: auth.session_ttl must be positive, got %s", c.Auth.SessionTTL)
	}
	if c.Agent.HMACTimestampWindow <= 0 {
		return fmt.Errorf("config: agent.hmac_timestamp_window must be positive, got %s", c.Agent.HMACTimestampWindow)
	}
	if c.Agent.NonceBurstPerMinute <= 0 {
		return fmt.Errorf("config: agent.nonce_burst_per_minute must be positive, got %d", c.Agent.NonceBurstPerMinute)
	}
	if c.GC.PackageRetentionDays <= 0 {
		return fmt.Errorf("config: gc.package_retention_days must be positive, got %d", c.GC.PackageRetentionDays)
	}
	// A negative interval is always a mistake; zero is the explicit "disabled"
	// sentinel. When the poller is enabled, concurrency must be at least 1 or
	// no source would ever be checked.
	if c.Scheduler.UpdateCheckInterval < 0 {
		return fmt.Errorf("config: scheduler.update_check_interval must not be negative, got %s", c.Scheduler.UpdateCheckInterval)
	}
	if c.Scheduler.UpdateCheckInterval > 0 && c.Scheduler.Concurrency < 1 {
		return fmt.Errorf("config: scheduler.concurrency must be >= 1 when the poller is enabled, got %d", c.Scheduler.Concurrency)
	}
	return nil
}

// ExpandHome turns a leading "~" into the user's home directory.
// "~user" is intentionally not supported — Go has no portable helper.
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

// ExpandHomeForDisplay is the error-swallowing variant of [ExpandHome]
// for log lines and stderr messages: if expansion fails for any reason
// (malformed prefix, no home directory), the original path is returned
// unchanged so the message remains useful.
func ExpandHomeForDisplay(p string) string {
	if out, err := ExpandHome(p); err == nil {
		return out
	}
	return p
}
