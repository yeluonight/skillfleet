package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultsMatchSpec(t *testing.T) {
	d := Defaults()
	if d.Server.Bind != "0.0.0.0:7890" {
		t.Errorf("server.bind = %q, want 0.0.0.0:7890", d.Server.Bind)
	}
	if d.Server.DataDir != "~/.skillfleet/server" {
		t.Errorf("server.data_dir = %q, want ~/.skillfleet/server", d.Server.DataDir)
	}
	if d.Auth.SessionTTL != 720*time.Hour {
		t.Errorf("auth.session_ttl = %s, want 720h", d.Auth.SessionTTL)
	}
	if d.Auth.RateLimit.LoginPerIP != "10/min" {
		t.Errorf("auth.rate_limit.login_per_ip = %q, want 10/min", d.Auth.RateLimit.LoginPerIP)
	}
	if d.Agent.HMACTimestampWindow != 300*time.Second {
		t.Errorf("agent.hmac_timestamp_window = %s, want 300s", d.Agent.HMACTimestampWindow)
	}
	if d.Agent.NonceBurstPerMinute != 1000 {
		t.Errorf("agent.nonce_burst_per_minute = %d, want 1000", d.Agent.NonceBurstPerMinute)
	}
	if d.GC.PackageRetentionDays != 30 {
		t.Errorf("gc.package_retention_days = %d, want 30", d.GC.PackageRetentionDays)
	}
	if d.Scheduler.UpdateCheckInterval != 6*time.Hour {
		t.Errorf("scheduler.update_check_interval = %s, want 6h", d.Scheduler.UpdateCheckInterval)
	}
	if d.Scheduler.Concurrency != 2 {
		t.Errorf("scheduler.concurrency = %d, want 2", d.Scheduler.Concurrency)
	}
	if d.Logging.Level != "info" {
		t.Errorf("logging.level = %q, want info", d.Logging.Level)
	}
	if d.Logging.Format != "text" {
		t.Errorf("logging.format = %q, want text", d.Logging.Format)
	}
}

func TestLoadFullConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `server:
  bind: "127.0.0.1:9000"
  external_url: "https://sf.example.com"
  data_dir: "` + dir + `"
auth:
  session_ttl: "1h"
  rate_limit:
    login_per_ip: "20/min"
    login_per_user: "8/min"
agent:
  hmac_timestamp_window: "60s"
  nonce_burst_per_minute: 50
gc:
  package_retention_days: 7
  schedule: "04:30"
scheduler:
  update_check_interval: "30m"
  concurrency: 4
logging:
  level: "debug"
  format: "json"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Bind != "127.0.0.1:9000" {
		t.Errorf("bind = %q", cfg.Server.Bind)
	}
	if cfg.Server.ExternalURL != "https://sf.example.com" {
		t.Errorf("external_url = %q", cfg.Server.ExternalURL)
	}
	if cfg.Auth.SessionTTL != time.Hour {
		t.Errorf("session_ttl = %s", cfg.Auth.SessionTTL)
	}
	if cfg.Auth.RateLimit.LoginPerIP != "20/min" {
		t.Errorf("login_per_ip = %q", cfg.Auth.RateLimit.LoginPerIP)
	}
	if cfg.Agent.HMACTimestampWindow != 60*time.Second {
		t.Errorf("hmac window = %s", cfg.Agent.HMACTimestampWindow)
	}
	if cfg.Scheduler.UpdateCheckInterval != 30*time.Minute {
		t.Errorf("scheduler.update_check_interval = %s, want 30m", cfg.Scheduler.UpdateCheckInterval)
	}
	if cfg.Scheduler.Concurrency != 4 {
		t.Errorf("scheduler.concurrency = %d, want 4", cfg.Scheduler.Concurrency)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("level = %q", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "json" {
		t.Errorf("format = %q", cfg.Logging.Format)
	}
}

func TestLoadAppliesDefaultsForMissingFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `server:
  bind: "127.0.0.1:7891"
  data_dir: "` + dir + `"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.SessionTTL != 720*time.Hour {
		t.Errorf("session_ttl fallback = %s, want 720h", cfg.Auth.SessionTTL)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("level fallback = %q", cfg.Logging.Level)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `server:
  bind: "127.0.0.1:7890"
  data_dir: "` + dir + `"
  typo_field: 1
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected unknown-field error, got nil")
	}
	if !strings.Contains(err.Error(), "typo_field") {
		t.Errorf("error %q should mention typo_field", err)
	}
}

func TestLoadRejectsInvalidLogging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `server:
  bind: "127.0.0.1:7890"
  data_dir: "` + dir + `"
logging:
  level: "verbose"
  format: "text"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "logging.level") {
		t.Errorf("error %q should mention logging.level", err)
	}
}

func TestLoadAcceptsDisabledScheduler(t *testing.T) {
	// update_check_interval "0" is the explicit disabled sentinel and must
	// load cleanly even with concurrency 0 (the poller never runs).
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `server:
  bind: "127.0.0.1:7890"
  data_dir: "` + dir + `"
scheduler:
  update_check_interval: "0"
  concurrency: 0
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load disabled scheduler: %v", err)
	}
	if cfg.Scheduler.UpdateCheckInterval != 0 {
		t.Errorf("interval = %s, want 0 (disabled)", cfg.Scheduler.UpdateCheckInterval)
	}
}

func TestLoadRejectsEnabledSchedulerZeroConcurrency(t *testing.T) {
	// A positive interval with concurrency < 1 would spin a poller that
	// checks nothing — reject it at load.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `server:
  bind: "127.0.0.1:7890"
  data_dir: "` + dir + `"
scheduler:
  update_check_interval: "6h"
  concurrency: 0
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for concurrency 0 with enabled poller")
	}
	if !strings.Contains(err.Error(), "scheduler.concurrency") {
		t.Errorf("error %q should mention scheduler.concurrency", err)
	}
}

func TestWriteDefaultRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.yaml")
	if err := WriteDefault(path); err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	// data_dir in the default file is "~/.skillfleet/server"; Load will
	// expand it via Resolve, which requires a real home. We bypass that
	// by reading the file directly and checking key markers.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`bind: "0.0.0.0:7890"`,
		`session_ttl: "720h"`,
		`hmac_timestamp_window: "300s"`,
		`nonce_burst_per_minute: 1000`,
		`update_check_interval: "6h"`,
		`level: "info"`,
		`format: "text"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("default YAML missing %q", want)
		}
	}
}

func TestWriteDefaultRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteDefault(path)
	if err == nil {
		t.Fatal("expected refuse-to-overwrite error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q should mention 'already exists'", err)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this host")
	}
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"", "", false},
		{"~", home, false},
		{"~/foo", filepath.Join(home, "foo"), false},
		{"/abs/path", "/abs/path", false},
		{"relative", "relative", false},
		{"~bob", "", true},
	}
	for _, c := range cases {
		got, err := ExpandHome(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ExpandHome(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestExpandHomeNoHomeDir exercises the error path where os.UserHomeDir
// itself fails. Linux honours HOME first; unsetting it (and the fallback
// LOGNAME/USER pair) forces the failure deterministically.
func TestExpandHomeNoHomeDir(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("LOGNAME", "")
	t.Setenv("USER", "")

	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("this host resolves a home dir even without HOME/LOGNAME/USER (likely non-linux)")
	}

	if _, err := ExpandHome("~/foo"); err == nil {
		t.Error("expected ExpandHome to surface the os.UserHomeDir error")
	}
	if _, err := ExpandHome("~"); err == nil {
		t.Error(`expected ExpandHome("~") to surface the os.UserHomeDir error`)
	}
	// Paths that don't trigger expansion must still succeed.
	if got, err := ExpandHome("/abs"); err != nil || got != "/abs" {
		t.Errorf(`ExpandHome("/abs") = %q, %v; want "/abs", nil`, got, err)
	}
}

func TestExpandHomeForDisplaySwallowsError(t *testing.T) {
	// "~bob" would error in ExpandHome; the display variant returns the
	// input unchanged so log messages remain useful.
	if got := ExpandHomeForDisplay("~bob/foo"); got != "~bob/foo" {
		t.Errorf("ExpandHomeForDisplay(%q) = %q, want unchanged", "~bob/foo", got)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if got := ExpandHomeForDisplay("~/x"); got != filepath.Join(home, "x") {
			t.Errorf("ExpandHomeForDisplay(%q) = %q, want %q", "~/x", got, filepath.Join(home, "x"))
		}
	}
}
