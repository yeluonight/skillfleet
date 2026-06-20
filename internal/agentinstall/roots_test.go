package agentinstall

import (
	"errors"
	"testing"

	"github.com/yeluonight/skillfleet/internal/deploy"
)

func TestResolveTarget(t *testing.T) {
	roots := []AllowedRoot{
		{ID: "root-claude-user", Tool: "claude", Scope: "user", Path: "/tmp/claude/user"},
		{ID: "root-codex-project", Tool: "codex", Scope: "project", Path: "/tmp/codex/project"},
	}

	tests := []struct {
		name     string
		roots    []AllowedRoot
		target   deploy.Target
		wantPath string
		wantErr  error
	}{
		{
			name:     "root id exact match succeeds",
			roots:    roots,
			target:   deploy.Target{RootID: "root-claude-user"},
			wantPath: "/tmp/claude/user",
		},
		{
			name:     "root id with matching tool and scope succeeds",
			roots:    roots,
			target:   deploy.Target{RootID: "root-claude-user", ToolKey: "claude", Scope: "user"},
			wantPath: "/tmp/claude/user",
		},
		{
			name:    "root id with mismatched tool is refused",
			roots:   roots,
			target:  deploy.Target{RootID: "root-claude-user", ToolKey: "codex", Scope: "user"},
			wantErr: ErrRootNotAllowed,
		},
		{
			name:    "root id with mismatched scope is refused",
			roots:   roots,
			target:  deploy.Target{RootID: "root-claude-user", ToolKey: "claude", Scope: "project"},
			wantErr: ErrRootNotAllowed,
		},
		{
			name:    "missing root id does not fall back to matching tool and scope",
			roots:   roots,
			target:  deploy.Target{RootID: "missing-root", ToolKey: "claude", Scope: "user"},
			wantErr: ErrRootNotAllowed,
		},
		{
			name:     "tool and scope match succeeds without root id",
			roots:    roots,
			target:   deploy.Target{ToolKey: "codex", Scope: "project"},
			wantPath: "/tmp/codex/project",
		},
		{
			name:    "tool without scope is refused without root id",
			roots:   roots,
			target:  deploy.Target{ToolKey: "claude"},
			wantErr: ErrRootNotAllowed,
		},
		{
			name:    "unmatched tool and scope is refused without root id",
			roots:   roots,
			target:  deploy.Target{ToolKey: "cursor", Scope: "user"},
			wantErr: ErrRootNotAllowed,
		},
		{
			name:    "empty roots is refused",
			roots:   nil,
			target:  deploy.Target{ToolKey: "claude", Scope: "user"},
			wantErr: ErrRootNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTarget(tt.roots, tt.target)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ResolveTarget() error = %v, want errors.Is %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveTarget() unexpected error = %v", err)
			}
			if got.Path != tt.wantPath {
				t.Fatalf("ResolveTarget() path = %q, want %q", got.Path, tt.wantPath)
			}
		})
	}
}
