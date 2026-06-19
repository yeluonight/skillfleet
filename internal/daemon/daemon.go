// Package daemon installs and controls SkillFleet binaries as OS-level
// background services.
//
// Linux primary path: systemd user units written to
// ~/.config/systemd/user/<name>.service and controlled via
// `systemctl --user`. Operators should enable linger with
// `loginctl enable-linger <user>` when the service must survive logout.
//
// Windows uses a per-user Task Scheduler task that runs at logon. macOS
// launchd is intentionally documented as a manual setup path for now and
// returns ErrUnsupportedPlatform from the daemon control API.
package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Errors returned by the daemon package.
var (
	ErrUnsupportedPlatform = errors.New("daemon: unsupported platform (only Linux systemd user units and Windows scheduled tasks are supported)")
	ErrNotInstalled        = errors.New("daemon: service not installed (run `start` first)")
	ErrSystemctlMissing    = errors.New("daemon: systemctl not found on PATH")
	ErrSchtasksMissing     = errors.New("daemon: schtasks not found on PATH")
)

// Spec describes a service to install.
type Spec struct {
	Name        string   // service name without extension, e.g. "skillfleet-agent"
	Description string   // human label for the unit/task
	BinaryPath  string   // absolute path to executable
	Args        []string // arguments appended after BinaryPath
	Unit        string   // systemd unit template; empty means DefaultUnit
}

// Status is the aggregated state of an installed service.
type Status struct {
	Installed   bool
	ActiveState string
	SubState    string
	PID         string
	UnitPath    string
}

// AgentSpec builds the Spec for the skillfleet-agent service. The caller
// passes the absolute binary path (usually os.Executable) and an optional
// config path.
func AgentSpec(binaryPath, configPath string) Spec {
	args := []string{"-foreground"}
	if configPath != "" {
		args = append(args, "-config", configPath)
	}
	return Spec{
		Name:        "skillfleet-agent",
		Description: "SkillFleet agent (heartbeat, inventory, jobs)",
		BinaryPath:  binaryPath,
		Args:        args,
	}
}

// ServerSpec builds the Spec for the skillfleet-server service.
func ServerSpec(binaryPath, configPath string) Spec {
	args := []string{"-foreground"}
	if configPath != "" {
		args = append(args, "-config", configPath)
	}
	return Spec{
		Name:        "skillfleet-server",
		Description: "SkillFleet server (control plane and WebUI)",
		BinaryPath:  binaryPath,
		Args:        args,
	}
}

// OwnBinaryPath returns the absolute path of the running executable, for
// use as the ExecStart binary in a generated service unit. Both the agent
// and server CLIs call this before AgentSpec/ServerSpec.
func OwnBinaryPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return p, nil
}

// IsTerminal reports whether f refers to a terminal device. The agent and
// server use it to decide whether a bare invocation should detach into a
// background service (interactive terminal) or stay in the foreground
// (piped or redirected, e.g. CI). Uses only the standard library so the
// binaries stay dependency-free.
func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// LaunchdPlist is a named placeholder for the future macOS backend. launchd
// support is documented as a manual operator path for now.
func LaunchdPlist(Spec) string {
	return ""
}

func validateSpec(s Spec) error {
	if s.Name == "" {
		return fmt.Errorf("daemon: empty service name")
	}
	if s.BinaryPath == "" {
		return fmt.Errorf("daemon: empty binary path")
	}
	if !filepath.IsAbs(s.BinaryPath) {
		return fmt.Errorf("daemon: binary path must be absolute: %s", s.BinaryPath)
	}
	return nil
}

func renderUnit(body string, s Spec) (string, error) {
	tmpl, err := template.New("unit").Parse(body)
	if err != nil {
		return "", fmt.Errorf("daemon: parse unit template: %w", err)
	}
	execStart := systemdQuote(s.BinaryPath)
	for _, arg := range s.Args {
		execStart += " " + systemdQuote(arg)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{
		"Description": s.Description,
		"ExecStart":   execStart,
	}); err != nil {
		return "", fmt.Errorf("daemon: render unit: %w", err)
	}
	return buf.String(), nil
}

func systemdQuote(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\n\"'\\") {
		return s
	}
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		if r == '\'' {
			b.WriteString(`'\''`)
			continue
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

func serviceUnitName(name string) string {
	if strings.HasSuffix(name, ".service") {
		return name
	}
	return name + ".service"
}

// DefaultUnit is the systemd user unit template. {{.ExecStart}} and
// {{.Description}} are substituted by renderUnit.
const DefaultUnit = `[Unit]
Description={{.Description}}
After=network.target

[Service]
Type=simple
ExecStart={{.ExecStart}}
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=default.target
`
