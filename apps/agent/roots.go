package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/yeluonight/skillfleet/internal/agentcandidates"
	"github.com/yeluonight/skillfleet/internal/agentcfg"
	"github.com/yeluonight/skillfleet/internal/agentroots"
)

// roots.go implements the `skillfleet-agent roots` subcommands, which
// manage the agent's allowed_roots — the filesystem locations a
// deployment / state-change job may touch (v1.0 §9.1). enroll does NOT
// populate these (it only writes the device identity), so without `roots
// add` a freshly-enrolled agent can scan + report but every install /
// enable-disable job fails to resolve its target. These commands let an
// operator register roots on the device without hand-editing agent.json.
//
// All commands load the existing config (refusing if the agent isn't
// enrolled yet), mutate allowed_roots through internal/agentroots, and
// persist with agentcfg.SaveForce (atomic overwrite).

// runRoots dispatches `roots <list|add|rm|scan>`.
func runRoots(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: skillfleet-agent roots <list|add|rm|scan> [flags]")
	}
	switch args[0] {
	case "list":
		return runRootsList(args[1:])
	case "add":
		return runRootsAdd(args[1:])
	case "rm", "remove":
		return runRootsRm(args[1:])
	case "scan":
		return runRootsScan(args[1:])
	default:
		return fmt.Errorf("unknown roots command %q (want list|add|rm|scan)", args[0])
	}
}

// loadForRoots loads the config at the given path, mapping a missing file
// to a clear "enroll first" message (the roots commands are meaningless
// before enrolment).
func loadForRoots(configPath string) (agentcfg.Config, error) {
	cfg, err := agentcfg.Load(configPath)
	if err != nil {
		return agentcfg.Config{}, mapRootsConfigError(configPath, err)
	}
	return cfg, nil
}

func mapRootsConfigError(configPath string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf(
			"no agent config at %s; run `skillfleet-agent enroll <url> <token>` first",
			agentcfg.ExpandHomeForDisplay(configPath),
		)
	}
	return err
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
	res, err := agentroots.Register(*configPath, agentroots.Spec{
		Tool:  *tool,
		Scope: *scope,
		Path:  *path,
		ID:    *id,
	})
	if err != nil {
		return mapRootsConfigError(*configPath, err)
	}
	fmt.Fprintf(os.Stderr, "added root %q → %s (%s/%s)\n", res.Root.ID, res.Root.Path, res.Root.Tool, res.Root.Scope)
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

	if _, err := agentroots.Remove(*configPath, target); err != nil {
		return mapRootsConfigError(*configPath, err)
	}
	fmt.Fprintf(os.Stderr, "removed root %q\n", target)
	return nil
}

func runRootsScan(args []string) error {
	return runRootsScanInteractive(args, os.Stdin, os.Stdout, os.Stderr)
}

func runRootsScanInteractive(args []string, in io.Reader, out, errOut io.Writer) error {
	fl := flag.NewFlagSet("skillfleet-agent roots scan", flag.ContinueOnError)
	configPath := fl.String("config", agentcfg.DefaultPath, "path to agent JSON config")
	if err := fl.Parse(args); err != nil {
		return err
	}
	cfg, err := loadForRoots(*configPath)
	if err != nil {
		return err
	}
	roots := agentcandidates.Discover("", cfg.AllowedRoots)
	if len(roots) == 0 {
		_, _ = fmt.Fprintln(errOut, "no candidate roots discovered.")
		return nil
	}

	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "#\tSTATE\tTOOL\tSCOPE\tDIR\tTOOL?\tSHARED\tPATH")
	for i, r := range roots {
		state := "available"
		if r.Registered {
			state = "registered"
		}
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			i+1, state, r.ToolKey, r.Scope, yesNo(r.Exists), yesNo(r.ToolDetected), yesNo(r.Shared), r.Path)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(errOut, "")
	_, _ = fmt.Fprintln(errOut, "Select roots to register by number (e.g. 1,3-5), or 'all'. Empty cancels.")
	_, _ = fmt.Fprint(errOut, "> ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return err
	}
	selected, err := parseRootSelection(line, len(roots))
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		_, _ = fmt.Fprintln(errOut, "cancelled.")
		return nil
	}

	added := 0
	for _, idx := range selected {
		r := roots[idx]
		if r.Registered {
			_, _ = fmt.Fprintf(errOut, "already registered %q (%s)\n", r.RootID, r.Path)
			continue
		}
		if !r.Exists {
			_, _ = fmt.Fprintf(errOut, "skipped %s: directory does not exist\n", r.Path)
			continue
		}
		res, err := agentroots.Register(*configPath, agentroots.Spec{
			Tool:  r.ToolKey,
			Scope: r.Scope,
			Path:  r.Path,
		}, agentroots.WithIdempotent())
		if err != nil {
			return err
		}
		if res.Added {
			added++
			_, _ = fmt.Fprintf(errOut, "added root %q → %s (%s/%s)\n", res.Root.ID, res.Root.Path, res.Root.Tool, res.Root.Scope)
		} else {
			_, _ = fmt.Fprintf(errOut, "already registered %q (%s)\n", res.Root.ID, res.Root.Path)
		}
	}
	_, _ = fmt.Fprintf(errOut, "registered %d new root(s).\n", added)
	return nil
}

func parseRootSelection(input string, max int) ([]int, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, nil
	}
	if strings.EqualFold(trimmed, "all") || trimmed == "*" {
		out := make([]int, max)
		for i := range out {
			out[i] = i
		}
		return out, nil
	}

	seen := map[int]bool{}
	var out []int
	fields := strings.FieldsFunc(trimmed, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	for _, f := range fields {
		if f == "" {
			continue
		}
		start, end, err := parseSelectionToken(f)
		if err != nil {
			return nil, err
		}
		if start < 1 || end > max {
			return nil, fmt.Errorf("selection %q out of range 1-%d", f, max)
		}
		for n := start; n <= end; n++ {
			idx := n - 1
			if !seen[idx] {
				seen[idx] = true
				out = append(out, idx)
			}
		}
	}
	return out, nil
}

func parseSelectionToken(token string) (int, int, error) {
	parts := strings.Split(token, "-")
	if len(parts) == 1 {
		n, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid selection %q", token)
		}
		return n, n, nil
	}
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, 0, fmt.Errorf("invalid selection %q", token)
	}
	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid selection %q", token)
	}
	end, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid selection %q", token)
	}
	if end < start {
		return 0, 0, fmt.Errorf("invalid descending range %q", token)
	}
	return start, end, nil
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
