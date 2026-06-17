package agentcfg

import "testing"

func baseValidConfig() Config {
	return Config{
		ServerURL:       "https://sf.example.com",
		DeviceID:        "dev_abc",
		DeviceSecret:    "plaintext-secret",
		HeartbeatIntSec: 30,
		InventoryIntSec: 300,
		JobsIntSec:      15,
	}
}

func TestValidate_AllowedRoots(t *testing.T) {
	cases := []struct {
		name string
		mods func(*Config)
		want bool
	}{
		{
			name: "valid single root",
			mods: func(cfg *Config) {
				cfg.AllowedRoots = []AllowedRoot{{
					ID:    "claude_user",
					Tool:  "claude-code",
					Scope: "user",
					Path:  "/home/me/.claude/skills",
				}}
			},
			want: false,
		},
		{
			name: "empty id",
			mods: func(cfg *Config) {
				cfg.AllowedRoots = []AllowedRoot{{
					Tool:  "claude-code",
					Scope: "user",
					Path:  "/home/me/.claude/skills",
				}}
			},
			want: true,
		},
		{
			name: "duplicate id",
			mods: func(cfg *Config) {
				cfg.AllowedRoots = []AllowedRoot{
					{ID: "dup", Tool: "claude-code", Scope: "user", Path: "/home/me/.claude/skills"},
					{ID: "dup", Tool: "claude-code", Scope: "user", Path: "/home/me/.claude/other"},
				}
			},
			want: true,
		},
		{
			name: "empty tool",
			mods: func(cfg *Config) {
				cfg.AllowedRoots = []AllowedRoot{{
					ID:    "root1",
					Scope: "user",
					Path:  "/home/me/.claude/skills",
				}}
			},
			want: true,
		},
		{
			name: "empty scope",
			mods: func(cfg *Config) {
				cfg.AllowedRoots = []AllowedRoot{{
					ID:   "root1",
					Tool: "claude-code",
					Path: "/home/me/.claude/skills",
				}}
			},
			want: true,
		},
		{
			name: "relative path",
			mods: func(cfg *Config) {
				cfg.AllowedRoots = []AllowedRoot{{
					ID:    "root1",
					Tool:  "claude-code",
					Scope: "user",
					Path:  "relative/path",
				}}
			},
			want: true,
		},
		{
			name: "empty allowed roots",
			mods: func(cfg *Config) {
				cfg.AllowedRoots = nil
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseValidConfig()
			tc.mods(&cfg)
			err := cfg.Validate()
			if tc.want && err == nil {
				t.Fatal("expected error")
			}
			if !tc.want && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
