package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/yeluonight/skillfleet/internal/agentcfg"
)

// roots.go implements the `skillfleet-agent roots` subcommands, which
// manage the agent's allowed_roots — the filesystem locations a
// deployment / state-change job may touch (v1.0 §9.1). enroll does NOT
// populate these (it only writes the device identity), so without `roots
// add` a freshly-enrolled agent can scan + report but every install /
// enable-disable job fails to resolve its target. These commands let an
// operator register roots on the device without hand-editing agent.json.
//
// All three load the existing config (refusing if the agent isn't
// enrolled yet), mutate allowed_roots, run the same Config.Validate the
// loader enforces, and persist with agentcfg.SaveForce (atomic overwrite).

// validScopes is the set ResolveTarget matches against; mirrors
// adapters.Scope without importing the server-side package.
var validScopes = map[string]bool{"user": true, "project": true, "system": true}

// runRoots dispatches `roots <list|add|rm>`.
func runRoots(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: skillfleet-agent roots <list|add|rm> [flags]")
	}
	switch args[0] {
	case "list":
		return runRootsList(args[1:])
	case "add":
		return runRootsAdd(args[1:])
	case "rm", "remove":
		return runRootsRm(args[1:])
	default:
		return fmt.Errorf("unknown roots command %q (want list|add|rm)", args[0])
	}
}

// loadForRoots loads the config at the given path, mapping a missing file
// to a clear "enroll first" message (the roots commands are meaningless
// before enrolment).
func loadForRoots(configPath string) (agentcfg.Config, error) {
	cfg, err := agentcfg.Load(configPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return agentcfg.Config{}, fmt.Errorf(
				"no agent config at %s; run `skillfleet-agent enroll <url> <token>` first",
				agentcfg.ExpandHomeForDisplay(configPath),
			)
		}
		return agentcfg.Config{}, err
	}
	return cfg, nil
}

func runRootsList(args []string) error {
	fl := flag.NewFlagSet("skillfleet-agent roots list", flag.ContinueOnError)
	configPath := fl.String("config", agentcfg.DefaultPath, "path to agent JSON config")
	if err := fl.Parse(args); err != nil {
		return err
	}
	cfg, err := loadForRoots(*configPath)
	if err != nil {
		return err
	}
	if len(cfg.AllowedRoots) == 0 {
		fmt.Fprintln(os.Stderr, "no allowed roots configured.")
		fmt.Fprintln(os.Stderr, "add one with: skillfleet-agent roots add -tool <tool> -scope <scope> -path <dir>")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tTOOL\tSCOPE\tPATH")
	for _, r := range cfg.AllowedRoots {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.ID, r.Tool, r.Scope, r.Path)
	}
	return tw.Flush()
}

func runRootsAdd(args []string) error {
	fl := flag.NewFlagSet("skillfleet-agent roots add", flag.ContinueOnError)
	configPath := fl.String("config", agentcfg.DefaultPath, "path to agent JSON config")
	tool := fl.String("tool", "", "tool key, e.g. claude-code / codex / opencode")
	scope := fl.String("scope", "", "scope: user | project | system")
	path := fl.String("path", "", "absolute path to the skills root directory")
	id := fl.String("id", "", "stable root id (defaults to <tool>_<scope>, deduped)")
	if err := fl.Parse(args); err != nil {
		return err
	}

	if *tool == "" || *scope == "" || *path == "" {
		return errors.New("roots add requires -tool, -scope and -path")
	}
	if !validScopes[*scope] {
		return fmt.Errorf("invalid -scope %q (want user|project|system)", *scope)
	}
	// Resolve ~ and make absolute so Validate (which requires an absolute
	// path) accepts it and the stored value is unambiguous.
	abs, err := resolveRootPath(*path)
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(abs); statErr != nil || !info.IsDir() {
		return fmt.Errorf("path %q is not an existing directory", abs)
	}

	cfg, err := loadForRoots(*configPath)
	if err != nil {
		return err
	}

	rootID := *id
	if rootID == "" {
		rootID = dedupeRootID(cfg.AllowedRoots, *tool+"_"+*scope)
	}
	for _, r := range cfg.AllowedRoots {
		if r.ID == rootID {
			return fmt.Errorf("root id %q already exists (pass a different -id)", rootID)
		}
	}

	cfg.AllowedRoots = append(cfg.AllowedRoots, agentcfg.AllowedRoot{
		ID: rootID, Tool: *tool, Scope: *scope, Path: abs,
	})
	if err := agentcfg.SaveForce(*configPath, cfg); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "added root %q → %s (%s/%s)\n", rootID, abs, *tool, *scope)
	return nil
}

func runRootsRm(args []string) error {
	fl := flag.NewFlagSet("skillfleet-agent roots rm", flag.ContinueOnError)
	configPath := fl.String("config", agentcfg.DefaultPath, "path to agent JSON config")
	if err := fl.Parse(args); err != nil {
		return err
	}
	rest := fl.Args()
	if len(rest) != 1 {
		return errors.New("usage: skillfleet-agent roots rm [-config PATH] <id>")
	}
	target := rest[0]

	cfg, err := loadForRoots(*configPath)
	if err != nil {
		return err
	}
	kept := cfg.AllowedRoots[:0:0]
	found := false
	for _, r := range cfg.AllowedRoots {
		if r.ID == target {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return fmt.Errorf("no root with id %q", target)
	}
	cfg.AllowedRoots = kept
	if err := agentcfg.SaveForce(*configPath, cfg); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "removed root %q\n", target)
	return nil
}

// resolveRootPath expands a leading ~ and makes the path absolute against
// the current directory, so the stored root is unambiguous regardless of
// where the agent later runs.
func resolveRootPath(p string) (string, error) {
	expanded, err := agentcfg.ExpandHome(p)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", p, err)
	}
	return abs, nil
}

// dedupeRootID returns base, or base_2 / base_3 / … if base (or a prior
// suffix) is already taken, so the auto-generated id is always unique.
func dedupeRootID(existing []agentcfg.AllowedRoot, base string) string {
	taken := make(map[string]bool, len(existing))
	for _, r := range existing {
		taken[r.ID] = true
	}
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s_%d", base, n)
		if !taken[candidate] {
			return candidate
		}
	}
}
