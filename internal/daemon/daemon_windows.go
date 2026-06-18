//go:build windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yeluonight/skillfleet/internal/safefs"
)

// Install validates the spec and returns a scheduled-task-backed setup.
func Install(s Spec) error {
	if err := validateSpec(s); err != nil {
		return err
	}
	if err := requireSchtasks(); err != nil {
		return err
	}
	return createTask(s)
}

// Start installs when needed, then creates or updates the scheduled task and
// starts it immediately. Install already validates the spec and checks for
// schtasks, so Start does not repeat those guards.
func Start(s Spec) error {
	if err := Install(s); err != nil {
		return err
	}
	return runTask(s.Name)
}

// Stop ends the scheduled task if present.
func Stop(name string) error {
	if name == "" {
		return fmt.Errorf("daemon: empty service name")
	}
	if err := requireSchtasks(); err != nil {
		return err
	}
	if !taskExists(name) {
		return ErrNotInstalled
	}
	return endTask(name)
}

// Restart stops then starts the task.
func Restart(s Spec) error {
	if err := Stop(s.Name); err != nil && !errors.Is(err, ErrNotInstalled) {
		return err
	}
	return Start(s)
}

// Uninstall deletes the scheduled task.
func Uninstall(name string) error {
	if name == "" {
		return fmt.Errorf("daemon: empty service name")
	}
	if err := requireSchtasks(); err != nil {
		return err
	}
	if !taskExists(name) {
		return nil
	}
	return deleteTask(name)
}

// StatusOf reads scheduled task state. A single schtasks /query call serves
// both as the existence check (non-zero exit means the task is absent) and as
// the source of the parsed status, instead of spawning schtasks twice.
func StatusOf(name string) (Status, error) {
	if name == "" {
		return Status{}, fmt.Errorf("daemon: empty service name")
	}
	out, err := exec.Command("schtasks", queryArgs(name)...).CombinedOutput()
	if err != nil {
		return Status{}, nil
	}
	st := Status{Installed: true}
	parseTaskQuery(&st, string(out))
	return st, nil
}

func requireSchtasks() error {
	if _, err := exec.LookPath("schtasks"); err != nil {
		return ErrSchtasksMissing
	}
	return nil
}

func taskExists(name string) bool {
	cmd := exec.Command("schtasks", queryArgs(name)...)
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func createTask(s Spec) error {
	xmlPath := filepath.Join(os.TempDir(), s.Name+".task.xml")
	if err := safefs.WriteFile(xmlPath, []byte(taskXML(s, windowsUserID())), 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(xmlPath) }()
	return runSchtasks(createXMLArgs(s.Name, xmlPath)...)
}
func runTask(name string) error    { return runSchtasks(runArgs(name)...) }
func endTask(name string) error    { return runSchtasks(endArgs(name)...) }
func deleteTask(name string) error { return runSchtasks(deleteArgs(name)...) }

func windowsUserID() string {
	if domain, user := os.Getenv("USERDOMAIN"), os.Getenv("USERNAME"); domain != "" && user != "" {
		return domain + `\` + user
	}
	if user := os.Getenv("USERNAME"); user != "" {
		return user
	}
	return os.Getenv("USER")
}
func runSchtasks(args ...string) error {
	cmd := exec.Command("schtasks", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
