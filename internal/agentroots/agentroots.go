package agentroots

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/yeluonight/skillfleet/internal/agentcandidates"
	"github.com/yeluonight/skillfleet/internal/agentcfg"
	"github.com/yeluonight/skillfleet/internal/inventory"
)

const (
	CodeRootPathInvalid   = "root_path_invalid"
	CodeRootOutsidePolicy = "root_outside_policy"
	CodeConfigWriteFailed = "config_write_failed"
	CodeRootNotFound      = "root_not_found"
)

// Error carries a stable machine-readable code for register/remove jobs.
type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func coded(code string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Err: err}
}

// ErrorCode extracts an agentroots error code, defaulting to fallback.
func ErrorCode(err error, fallback string) string {
	var codedErr *Error
	if errors.As(err, &codedErr) && codedErr.Code != "" {
		return codedErr.Code
	}
	return fallback
}

// Spec describes the root the operator wants to register. It deliberately
// carries only the root identity; local/remote policy and idempotency live in
// RegisterOption so CLI roots add and remote register_root do not share hidden
// policy knobs.
type Spec struct {
	Tool  string
	Scope string
	Path  string
	ID    string
}

// RemotePolicy is the extra validation applied to server-originated
// register_root jobs. Candidate matching is always allowed; AllowHomeChild is
// the explicit custom-path escape hatch for directories inside the agent user's
// home. CLI roots add does not use RemotePolicy.
type RemotePolicy struct {
	HomeDir        string
	AllowHomeChild bool
}

type registerOptions struct {
	idempotent bool
	remote     *RemotePolicy
}

// RegisterOption customises Register/RegisterConfig without mixing policy into
// Spec's root identity fields.
type RegisterOption func(*registerOptions)

// WithIdempotent makes registering an already-present path succeed without
// appending a duplicate root.
func WithIdempotent() RegisterOption {
	return func(o *registerOptions) { o.idempotent = true }
}

// WithRemotePolicy enables remote register_root validation.
func WithRemotePolicy(policy RemotePolicy) RegisterOption {
	return func(o *registerOptions) { o.remote = &policy }
}

func collectOptions(opts []RegisterOption) registerOptions {
	var out registerOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&out)
		}
	}
	return out
}

// RegisterResult reports whether Register appended a new allowed root or
// found an existing one (when WithIdempotent is used).
type RegisterResult struct {
	Root  agentcfg.AllowedRoot
	Added bool
}

var (
	validScopes       = map[string]bool{"user": true, "project": true, "system": true}
	validPolicyScopes = map[string]bool{"user": true, "system": true}
)

// Register adds spec to configPath's allowed_roots and persists the config
// atomically. The target directory must already exist; v1 does not create
// skill roots on behalf of the operator.
func Register(configPath string, spec Spec, opts ...RegisterOption) (RegisterResult, error) {
	cfg, err := agentcfg.Load(configPath)
	if err != nil {
		return RegisterResult{}, err
	}
	cfg, res, err := RegisterConfig(cfg, spec, opts...)
	if err != nil {
		return RegisterResult{}, err
	}
	if !res.Added {
		return res, nil
	}
	if err := agentcfg.SaveForce(configPath, cfg); err != nil {
		return RegisterResult{}, coded(CodeConfigWriteFailed, err)
	}
	return res, nil
}

// RegisterConfig is the pure mutation half of Register, split out for tests
// and callers that already loaded agent.json.
func RegisterConfig(cfg agentcfg.Config, spec Spec, opts ...RegisterOption) (agentcfg.Config, RegisterResult, error) {
	if spec.Tool == "" || spec.Scope == "" || spec.Path == "" {
		return cfg, RegisterResult{}, fmt.Errorf("roots register requires tool, scope and path")
	}
	if !validScopes[spec.Scope] {
		return cfg, RegisterResult{}, fmt.Errorf("invalid -scope %q (want user|project|system)", spec.Scope)
	}

	options := collectOptions(opts)
	var abs string
	var err error
	if options.remote != nil {
		cands := agentcandidates.Discover(options.remote.HomeDir, cfg.AllowedRoots)
		abs, err = Validate(cfg, cands, spec, *options.remote)
	} else {
		abs, err = resolveExistingDir(spec.Path)
	}
	if err != nil {
		return cfg, RegisterResult{}, err
	}

	if options.idempotent {
		for _, r := range cfg.AllowedRoots {
			if samePath(r.Path, abs) {
				return cfg, RegisterResult{Root: r, Added: false}, nil
			}
		}
	}

	rootID := spec.ID
	if rootID == "" {
		rootID = DedupeRootID(cfg.AllowedRoots, spec.Tool+"_"+spec.Scope)
	}
	for _, r := range cfg.AllowedRoots {
		if r.ID == rootID {
			return cfg, RegisterResult{}, fmt.Errorf("root id %q already exists (pass a different -id)", rootID)
		}
	}

	root := agentcfg.AllowedRoot{ID: rootID, Tool: spec.Tool, Scope: spec.Scope, Path: abs}
	cfg.AllowedRoots = append(cfg.AllowedRoots, root)
	return cfg, RegisterResult{Root: root, Added: true}, nil
}

// Validate enforces the remote root-registration policy. A path is accepted
// only when it is an existing directory and matches an agent-recomputed
// candidate root, or (when policy.AllowHomeChild is true) is a true child of
// the agent user's home directory. Already registered paths are accepted
// idempotently.
func Validate(cfg agentcfg.Config, candidates []inventory.RootCandidate, spec Spec, policy RemotePolicy) (string, error) {
	if spec.Tool == "" || spec.Scope == "" || spec.Path == "" {
		return "", coded(CodeRootPathInvalid, fmt.Errorf("roots register requires tool, scope and path"))
	}
	if !validPolicyScopes[spec.Scope] {
		return "", coded(CodeRootPathInvalid, fmt.Errorf("invalid root scope %q (want user|system)", spec.Scope))
	}

	abs, err := resolveExistingDir(spec.Path)
	if err != nil {
		return "", coded(CodeRootPathInvalid, err)
	}

	for _, r := range cfg.AllowedRoots {
		rootPath, err := resolveExistingDir(r.Path)
		if err == nil && samePath(rootPath, abs) {
			return abs, nil
		}
		if samePath(r.Path, abs) {
			return abs, nil
		}
	}

	if matchesCandidate(abs, spec, candidates) {
		return abs, nil
	}
	if policy.AllowHomeChild && isHomeChild(abs, policy.HomeDir) {
		return abs, nil
	}
	return "", coded(CodeRootOutsidePolicy, fmt.Errorf("root path %q is neither a known candidate nor inside home", abs))
}

func matchesCandidate(abs string, spec Spec, candidates []inventory.RootCandidate) bool {
	for _, c := range candidates {
		if c.Scope != spec.Scope {
			continue
		}
		if c.ToolKey != spec.Tool && !c.Shared {
			continue
		}
		candPath, err := resolveExistingDir(c.Path)
		if err != nil {
			continue
		}
		if samePath(candPath, abs) {
			return true
		}
	}
	return false
}

// IsHomeDescendant reports whether abs (an already-resolved, symlink-free
// absolute path) is a strict descendant of homeDir — not homeDir itself, not
// outside it. homeDir "" falls back to the OS home. This is the single
// home-subtree policy shared by remote root registration (Validate's
// AllowHomeChild) and skill capture (apps/agent reading a discovered skill),
// so the two cannot drift — notably both get the Windows case-fold and the
// reject-home-itself guard.
func IsHomeDescendant(abs, homeDir string) bool {
	return isHomeChild(abs, homeDir)
}

func isHomeChild(abs, homeDir string) bool {
	if homeDir == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return false
		}
	}
	home, err := resolveExistingDir(homeDir)
	if err != nil {
		return false
	}
	if samePath(home, abs) {
		return false
	}
	if runtime.GOOS == "windows" {
		h := strings.TrimRight(filepath.Clean(home), `\/`) + string(filepath.Separator)
		p := strings.TrimRight(filepath.Clean(abs), `\/`) + string(filepath.Separator)
		return strings.HasPrefix(strings.ToLower(p), strings.ToLower(h))
	}
	rel, err := filepath.Rel(home, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// Remove deletes a root by id from configPath's allowed_roots and persists the
// result. It returns the removed root for logging / job results.
func Remove(configPath, rootID string) (agentcfg.AllowedRoot, error) {
	cfg, err := agentcfg.Load(configPath)
	if err != nil {
		return agentcfg.AllowedRoot{}, err
	}
	cfg, removed, err := RemoveConfig(cfg, rootID)
	if err != nil {
		return agentcfg.AllowedRoot{}, err
	}
	if err := agentcfg.SaveForce(configPath, cfg); err != nil {
		return agentcfg.AllowedRoot{}, coded(CodeConfigWriteFailed, err)
	}
	return removed, nil
}

// RemoveConfig is the pure mutation half of Remove.
func RemoveConfig(cfg agentcfg.Config, rootID string) (agentcfg.Config, agentcfg.AllowedRoot, error) {
	if rootID == "" {
		return cfg, agentcfg.AllowedRoot{}, coded(CodeRootNotFound, fmt.Errorf("root id must not be empty"))
	}
	kept := cfg.AllowedRoots[:0:0]
	var removed agentcfg.AllowedRoot
	found := false
	for _, r := range cfg.AllowedRoots {
		if r.ID == rootID {
			removed = r
			found = true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return cfg, agentcfg.AllowedRoot{}, coded(CodeRootNotFound, fmt.Errorf("no root with id %q", rootID))
	}
	cfg.AllowedRoots = kept
	return cfg, removed, nil
}

// ResolveRootPath expands a leading ~ and makes the path absolute against the
// current directory, so the stored root is unambiguous regardless of where the
// agent later runs.
func ResolveRootPath(p string) (string, error) {
	expanded, err := agentcfg.ExpandHome(p)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", p, err)
	}
	return filepath.Clean(abs), nil
}

// ResolveExistingDir resolves p (expanding a leading ~, making it absolute,
// following symlinks) and confirms it is an existing directory, returning the
// cleaned real path. Shared by root registration and skill capture so both
// resolve paths identically before any policy check.
func ResolveExistingDir(p string) (string, error) {
	return resolveExistingDir(p)
}

func resolveExistingDir(p string) (string, error) {
	abs, err := ResolveRootPath(p)
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks for %q: %w", abs, err)
	}
	info, err := os.Stat(realPath)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", realPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not an existing directory", realPath)
	}
	return filepath.Clean(realPath), nil
}

func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// DedupeRootID returns base, or base_2 / base_3 / … if base (or a prior
// suffix) is already taken, so the auto-generated id is always unique.
func DedupeRootID(existing []agentcfg.AllowedRoot, base string) string {
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
