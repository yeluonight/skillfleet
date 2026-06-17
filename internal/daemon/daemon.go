// Package daemon installs and controls SkillFleet binaries as OS-level
// background services.
//
// Linux primary path: systemd user units written to
// ~/.config/systemd/user/<name>.service and controlled via
// `systemctl --user`. Operators should enable linger with
// `loginctl enable-linger <user>` when the service must survive logout.
//
// macOS launchd and Windows services are intentionally out of scope for
// this cycle and return ErrUnsupportedPlatform.
package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/yeluonight/skillfleet/internal/safefs"
)

// Errors returned by the daemon package.
var (
	ErrUnsupportedPlatform = errors.New("daemon: unsupported platform (only Linux systemd user units are supported)")
	ErrNotInstalled        = errors.New("daemon: service not installed (run `start` first)")
	ErrSystemctlMissing    = errors.New("daemon: systemctl not found on PATH")
)

// Spec describes a service to install.
type Spec struct {
	Name        string   // service name without extension, e.g. "skillfleet-agent"
	Description string   // human label for the unit
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
	args := []string{}
	if configPath != "" {
		args = []string{"-config", configPath}
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
	args := []string{}
	if configPath != "" {
		args = []string{"-config", configPath}
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

// LaunchdPlist is a named placeholder for the future macOS backend. launchd
// support is P2 and not implemented in this cycle.
func LaunchdPlist(Spec) string {
	return ""
}

// Install writes the user unit and reloads systemd. It is idempotent:
// re-running overwrites the unit so the ExecStart path/args stay current.
func Install(s Spec) error {
	if runtime.GOOS != "linux" {
		return ErrUnsupportedPlatform
	}
	if err := validateSpec(s); err != nil {
		return err
	}
	if err := requireSystemctl(); err != nil {
		return err
	}
	dir, err := unitDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("daemon: mkdir %s: %w", dir, err)
	}
	body := s.Unit
	if body == "" {
		body = DefaultUnit
	}
	rendered, err := renderUnit(body, s)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, s.Name+".service")
	if err := safefs.WriteFile(path, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("daemon: write unit %s: %w", path, err)
	}
	if err := systemctlUser("daemon-reload"); err != nil {
		return fmt.Errorf("daemon: systemctl daemon-reload: %w", err)
	}
	return nil
}

// Start installs when needed, then enables and starts the service.
func Start(s Spec) error {
	if runtime.GOOS != "linux" {
		return ErrUnsupportedPlatform
	}
	if err := validateSpec(s); err != nil {
		return err
	}
	if err := requireSystemctl(); err != nil {
		return err
	}
	st, err := StatusOf(s.Name)
	if err != nil {
		return err
	}
	if !st.Installed {
		if err := Install(s); err != nil {
			return err
		}
	}
	if err := systemctlUser("enable", "--now", serviceUnitName(s.Name)); err != nil {
		return fmt.Errorf("daemon: enable --now %s: %w", s.Name, err)
	}
	return nil
}

// Stop stops and disables the service. The unit file remains on disk so a
// later start can reuse it.
func Stop(name string) error {
	if runtime.GOOS != "linux" {
		return ErrUnsupportedPlatform
	}
	if name == "" {
		return fmt.Errorf("daemon: empty service name")
	}
	if err := requireSystemctl(); err != nil {
		return err
	}
	st, err := StatusOf(name)
	if err != nil {
		return err
	}
	if !st.Installed {
		return ErrNotInstalled
	}
	if err := systemctlUser("stop", serviceUnitName(name)); err != nil {
		return fmt.Errorf("daemon: stop %s: %w", name, err)
	}
	if err := systemctlUser("disable", serviceUnitName(name)); err != nil {
		return fmt.Errorf("daemon: disable %s: %w", name, err)
	}
	return nil
}

// Restart stops then starts the service.
func Restart(s Spec) error {
	if err := Stop(s.Name); err != nil && !errors.Is(err, ErrNotInstalled) {
		return err
	}
	return Start(s)
}

// Uninstall stops, disables, and deletes the unit file.
func Uninstall(name string) error {
	if runtime.GOOS != "linux" {
		return ErrUnsupportedPlatform
	}
	if name == "" {
		return fmt.Errorf("daemon: empty service name")
	}
	_ = Stop(name)
	dir, err := unitDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, name+".service")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("daemon: remove unit %s: %w", path, err)
	}
	if err := requireSystemctl(); err != nil {
		return err
	}
	return systemctlUser("daemon-reload")
}

// StatusOf reads current systemd state. A unit file that exists but is not
// loaded by systemd is reported as Installed with empty ActiveState.
func StatusOf(name string) (Status, error) {
	if runtime.GOOS != "linux" {
		return Status{}, ErrUnsupportedPlatform
	}
	if name == "" {
		return Status{}, fmt.Errorf("daemon: empty service name")
	}
	dir, err := unitDir()
	if err != nil {
		return Status{}, err
	}
	path := filepath.Join(dir, name+".service")
	st := Status{UnitPath: path}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, fmt.Errorf("daemon: stat unit: %w", err)
	}
	st.Installed = true

	out, err := exec.Command("systemctl", "--user", "show", serviceUnitName(name),
		"--property=ActiveState,SubState,ExecMainPID").CombinedOutput()
	if err != nil {
		return st, nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		if eq := strings.Index(line, "="); eq > 0 {
			k, v := line[:eq], line[eq+1:]
			switch k {
			case "ActiveState":
				st.ActiveState = v
			case "SubState":
				st.SubState = v
			case "ExecMainPID":
				st.PID = v
			}
		}
	}
	return st, nil
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

func requireSystemctl() error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return ErrSystemctlMissing
	}
	return nil
}

func unitDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("daemon: resolve home: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func systemctlUser(args ...string) error {
	full := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, bytes.TrimSpace(out))
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
