//go:build linux

package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yeluonight/skillfleet/internal/safefs"
)

// Install writes the user unit and reloads systemd. It is idempotent:
// re-running overwrites the unit so the ExecStart path/args stay current.
func Install(s Spec) error {
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

// Start writes the latest unit, then enables and starts the service.
func Start(s Spec) error {
	if err := validateSpec(s); err != nil {
		return err
	}
	if err := requireSystemctl(); err != nil {
		return err
	}
	if err := Install(s); err != nil {
		return err
	}
	if err := systemctlUser("enable", "--now", serviceUnitName(s.Name)); err != nil {
		return fmt.Errorf("daemon: enable --now %s: %w", s.Name, err)
	}
	return nil
}

// Stop stops and disables the service. The unit file remains on disk so a
// later start can reuse it.
func Stop(name string) error {
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
