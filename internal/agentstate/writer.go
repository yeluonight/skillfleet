package agentstate

import (
	"context"
	"fmt"

	"github.com/yeluonight/skillfleet/internal/agentinstall"
	"github.com/yeluonight/skillfleet/internal/deploy"
)

// writer.go: the StateChange entry point, the state-side peer of
// agentinstall.Executor.Install. It resolves the job's target against the
// agent's allowed roots (the refusal gate), derives the tool's config
// path, and dispatches to the per-tool writer.

// Writer executes state-change jobs. It holds the agent's allowed roots
// (to resolve + gate targets) and home directory (for the per-user codex
// / opencode config paths). The zero value is not usable; use NewWriter.
type Writer struct {
	roots   []agentinstall.AllowedRoot
	homeDir string
}

// NewWriter builds a Writer over the agent's allowed roots and home dir.
func NewWriter(roots []agentinstall.AllowedRoot, homeDir string) *Writer {
	return &Writer{roots: roots, homeDir: homeDir}
}

// StateChange applies the request's desired state to the addressed skill
// by editing the tool's config file. It returns a deploy.Result with
// ResolvedRootPath set (so the operator sees which root governed the
// change) and a non-nil error on any refusal/failure; the caller marks
// the job failed and reports the populated Result either way.
//
// The flow:
//  1. Resolve req.Target against allowed roots — refuse if it matches none
//     (same gate as installs; a state-change job can't steer a write at an
//     arbitrary path).
//  2. Validate the tool supports the desired state at all (claude/codex/
//     opencode); unknown tools are refused here so antigravity/pi jobs —
//     which the planner already rejects — also fail safe if one slips
//     through.
//  3. Derive the config path and dispatch to the tool's writer.
func (w *Writer) StateChange(ctx context.Context, req deploy.Request) (deploy.Result, error) {
	state := req.DesiredState
	skill := req.SkillName
	target := req.Target

	root, err := agentinstall.ResolveTarget(w.roots, target)
	if err != nil {
		return deploy.Result{
			ErrorCode:    "root_not_allowed",
			ErrorMessage: err.Error(),
		}, fmt.Errorf("agentstate: %w", err)
	}

	res := deploy.Result{ResolvedRootPath: root.Path}

	var writeErr error
	switch target.ToolKey {
	case "claude-code":
		writeErr = writeClaudeOverride(claudeSettingsPath(root), skill, state)
	case "codex":
		writeErr = writeCodexEnabled(codexConfigPath(w.homeDir), codexSkillKey(root, skill), state)
	case "opencode":
		writeErr = writeOpencodePermission(opencodeConfigPath(w.homeDir), skill, state)
	default:
		err := fmt.Errorf("%w: %q", ErrUnknownTool, target.ToolKey)
		res.ErrorCode = "unsupported_tool"
		res.ErrorMessage = err.Error()
		return res, err
	}

	if writeErr != nil {
		res.ErrorCode = "state_write_failed"
		res.ErrorMessage = writeErr.Error()
		return res, writeErr
	}
	return res, nil
}
