package safefs

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanPackagePath_Valid(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"SKILL.md", "SKILL.md"},
		{"scripts/deploy.py", "scripts/deploy.py"},
		{"references/checklist.md", "references/checklist.md"},
		{"a/b/c/d.txt", "a/b/c/d.txt"},
		{"config/targets.toml", "config/targets.toml"},
		// Names with dots that are not "." or ".." are fine.
		{".gitignore", ".gitignore"},
		{"a/.hidden/file", "a/.hidden/file"},
		{"weird..name.md", "weird..name.md"},
		{"a..b/c", "a..b/c"},
		// Unicode filenames (Chinese) must pass — UTF-8 content is core.
		{"文档/说明.md", "文档/说明.md"},
		{"assets/图标.png", "assets/图标.png"},
	}
	for _, c := range cases {
		got, err := CleanPackagePath(c.in)
		if err != nil {
			t.Errorf("CleanPackagePath(%q) unexpected err: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("CleanPackagePath(%q) = %q, want %q", c.in, got, c.want)
		}
		if !IsCleanPackagePath(c.want) {
			t.Errorf("IsCleanPackagePath(%q) = false, want true", c.want)
		}
	}
}

func TestCleanPackagePath_Rejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrEmptyPath},
		{"absolute", "/etc/passwd", ErrAbsolutePath},
		{"absolute root only", "/", ErrAbsolutePath},
		{"dotdot leading", "../secret", ErrDotDot},
		{"dotdot interior", "a/../../b", ErrDotDot},
		{"dotdot only", "..", ErrDotDot},
		{"dotdot trailing", "a/..", ErrDotDot},
		{"backslash", "scripts\\deploy.py", ErrBackslash},
		{"backslash escape", "..\\..\\x", ErrBackslash},
		{"drive upper", "C:/Windows/system32", ErrDriveLetter},
		{"drive lower", "c:stuff", ErrDriveLetter},
		{"control nul", "a\x00b", ErrControlChar},
		{"control newline", "a\nb", ErrControlChar},
		{"control tab", "a\tb", ErrControlChar},
		{"control del", "a\x7fb", ErrControlChar},
		{"double slash", "a//b", ErrEmptyPath},
		{"trailing slash", "scripts/", ErrTrailingSlash},
		{"dot segment", "a/./b", ErrReservedName},
		{"dot only", ".", ErrReservedName},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := CleanPackagePath(c.in)
			if !errors.Is(err, c.want) {
				t.Errorf("CleanPackagePath(%q) err = %v, want %v", c.in, err, c.want)
			}
		})
	}
}

func TestCleanPackagePath_LengthAndDepth(t *testing.T) {
	long := strings.Repeat("a", MaxPathBytes+1)
	if _, err := CleanPackagePath(long); !errors.Is(err, ErrPathTooLong) {
		t.Errorf("over-length path err = %v, want ErrPathTooLong", err)
	}

	// Exactly at the byte limit (no separators) is allowed.
	atLimit := strings.Repeat("a", MaxPathBytes)
	if _, err := CleanPackagePath(atLimit); err != nil {
		t.Errorf("at-limit path err = %v, want nil", err)
	}

	// Build a path with one more component than MaxPathDepth allows.
	segs := make([]string, MaxPathDepth+1)
	for i := range segs {
		segs[i] = "x"
	}
	tooDeep := strings.Join(segs, "/")
	if _, err := CleanPackagePath(tooDeep); !errors.Is(err, ErrPathTooDeep) {
		t.Errorf("too-deep path err = %v, want ErrPathTooDeep", err)
	}

	// Exactly MaxPathDepth components is allowed.
	okDepth := strings.Join(segs[:MaxPathDepth], "/")
	if _, err := CleanPackagePath(okDepth); err != nil {
		t.Errorf("at-depth path err = %v, want nil", err)
	}
}

func TestIsCleanPackagePath(t *testing.T) {
	if IsCleanPackagePath("../x") {
		t.Error("IsCleanPackagePath(../x) = true, want false")
	}
	if !IsCleanPackagePath("a/b.md") {
		t.Error("IsCleanPackagePath(a/b.md) = false, want true")
	}
	// A path that is valid but not canonical is not "clean": there is
	// no such case here because we reject rather than rewrite, but the
	// guard must still agree with CleanPackagePath's output.
	if IsCleanPackagePath("") {
		t.Error("IsCleanPackagePath(empty) = true, want false")
	}
}
