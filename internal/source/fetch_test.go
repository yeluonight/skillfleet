package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/skill"
)

// --- URL allow-list ---------------------------------------------------

func TestValidateRepoURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://github.com/acme/skills", true},
		{"http://gitea.internal/acme/skills", true},
		{"git://example.com/acme/skills", true},
		{"https://github.com/acme/skills.git", true},
		{"", false},
		{"file:///etc/passwd", false},               // local disk — must be refused
		{"ssh://git@github.com/acme/skills", false}, // ssh — refused (no creds in phase 6)
		{"git@github.com:acme/skills.git", false},   // scp-style — no scheme
		{"/etc/passwd", false},                      // bare path
		{"ftp://example.com/x", false},              // disallowed scheme
		{"https://", false},                         // missing host
	}
	for _, c := range cases {
		err := validateRepoURL(c.url)
		if c.ok && err != nil {
			t.Errorf("validateRepoURL(%q) = %v, want nil", c.url, err)
		}
		if !c.ok && err == nil {
			t.Errorf("validateRepoURL(%q) = nil, want error", c.url)
		}
		if !c.ok && err != nil && !errors.Is(err, ErrBadURL) {
			t.Errorf("validateRepoURL(%q) err = %v, want ErrBadURL", c.url, err)
		}
	}
}

// --- subdir cleaning / traversal guard --------------------------------

func TestCleanSubdir(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", true},
		{"deploy-helper", "deploy-helper", true},
		{"a/b/c", "a/b/c", true},
		{"a//b", "a/b", true}, // path.Clean collapses
		{"./a", "a", true},
		{"..", "", false},
		{"../escape", "", false},
		{"a/../../b", "", false},
		{"/abs", "", false},
		{"a\\b", "", false},   // backslash
		{"a\x00b", "", false}, // control char
	}
	for _, c := range cases {
		got, err := cleanSubdir(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("cleanSubdir(%q) = %v, want nil", c.in, err)
				continue
			}
			if got != c.want {
				t.Errorf("cleanSubdir(%q) = %q, want %q", c.in, got, c.want)
			}
		} else {
			if err == nil {
				t.Errorf("cleanSubdir(%q) = %q, want error", c.in, got)
			} else if !errors.Is(err, ErrBadSubdir) {
				t.Errorf("cleanSubdir(%q) err = %v, want ErrBadSubdir", c.in, err)
			}
		}
	}
}

// --- commit-hash shape check ------------------------------------------

func TestLooksLikeHash(t *testing.T) {
	good := []string{
		"0123456789abcdef0123456789abcdef01234567", // 40 hex
		strings.Repeat("a", 64),                    // 64 hex
		"ABCDEF0123456789ABCDEF0123456789ABCDEF01", // upper ok
	}
	bad := []string{"", "abc", "main", strings.Repeat("g", 40), strings.Repeat("a", 41)}
	for _, s := range good {
		if !looksLikeHash(s) {
			t.Errorf("looksLikeHash(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if looksLikeHash(s) {
			t.Errorf("looksLikeHash(%q) = true, want false", s)
		}
	}
}

func TestLsRemote_CommitRefNoNetwork(t *testing.T) {
	// A pinned commit ref resolves without any network round-trip.
	f := NewFetcher()
	hash := "0123456789abcdef0123456789abcdef01234567"
	got, err := f.LsRemote(context.Background(), "https://github.com/acme/skills", RemoteRef{Type: RefCommit, Name: hash})
	if err != nil {
		t.Fatalf("LsRemote commit: %v", err)
	}
	if got != hash {
		t.Errorf("LsRemote commit = %q, want %q", got, hash)
	}

	// A non-hash commit ref is rejected before any network call.
	if _, err := f.LsRemote(context.Background(), "https://github.com/acme/skills", RemoteRef{Type: RefCommit, Name: "main"}); !errors.Is(err, ErrRefNotFound) {
		t.Errorf("LsRemote bad commit err = %v, want ErrRefNotFound", err)
	}

	// A bad URL is rejected before any network call.
	if _, err := f.LsRemote(context.Background(), "file:///etc/passwd", RemoteRef{Type: RefBranch, Name: "main"}); !errors.Is(err, ErrBadURL) {
		t.Errorf("LsRemote bad url err = %v, want ErrBadURL", err)
	}
}

// --- readSubtree + manifestFromFiles over a real git tree -------------

// buildRepo creates a real on-disk git repository, writes the given
// files (path->content, forward-slashed), commits them, and returns the
// commit's tree. This exercises the same go-git tree-reading path
// FetchSubdir uses after a clone, without needing the network.
func buildRepo(t *testing.T, files map[string]string) *object.Tree {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	for p, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		if _, err := wt.Add(p); err != nil {
			t.Fatalf("add %s: %v", p, err)
		}
	}
	h, err := wt.Commit("test", &git.CommitOptions{
		Author: testSignature(),
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	c, err := repo.CommitObject(h)
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	tree, err := c.Tree()
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	return tree
}

func testSignature() *object.Signature {
	// Fixed signature for determinism (go-git requires a non-zero When,
	// but the commit hash doesn't affect content_sha256, which is what we
	// assert on).
	return &object.Signature{Name: "t", Email: "t@example.com"}
}

func TestReadSubtree_FiltersToSubdir(t *testing.T) {
	tree := buildRepo(t, map[string]string{
		"README.md":                  "root readme",
		"deploy-helper/SKILL.md":     "# deploy",
		"deploy-helper/scripts/x.sh": "echo hi",
		"other-skill/SKILL.md":       "# other",
	})
	f := NewFetcher()
	got, err := f.readSubtree(tree, "deploy-helper")
	if err != nil {
		t.Fatalf("readSubtree: %v", err)
	}
	paths := map[string]bool{}
	for _, ff := range got {
		paths[ff.Path] = true
	}
	if !paths["SKILL.md"] || !paths["scripts/x.sh"] {
		t.Errorf("subdir files = %v, want SKILL.md + scripts/x.sh", paths)
	}
	if paths["README.md"] || len(got) != 2 {
		t.Errorf("subdir leaked files outside deploy-helper: %v", paths)
	}
}

func TestReadSubtree_RootWholeTree(t *testing.T) {
	tree := buildRepo(t, map[string]string{
		"SKILL.md": "# root skill",
		"a/b.txt":  "b",
	})
	f := NewFetcher()
	got, err := f.readSubtree(tree, "")
	if err != nil {
		t.Fatalf("readSubtree root: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("root tree files = %d, want 2", len(got))
	}
}

func TestReadSubtree_FileCountCap(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 5; i++ {
		files["sub/f"+string(rune('a'+i))+".txt"] = "x"
	}
	tree := buildRepo(t, files)
	f := NewFetcher()
	f.MaxFileCount = 3
	_, err := f.readSubtree(tree, "sub")
	if !errors.Is(err, ErrTooManyFiles) {
		t.Errorf("err = %v, want ErrTooManyFiles", err)
	}
}

func TestReadSubtree_TotalSizeCap(t *testing.T) {
	tree := buildRepo(t, map[string]string{
		"sub/big1.txt": strings.Repeat("x", 600),
		"sub/big2.txt": strings.Repeat("y", 600),
	})
	f := NewFetcher()
	f.MaxTotalBytes = 1000 // 600+600 > 1000
	_, err := f.readSubtree(tree, "sub")
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
}

func TestReadSubtree_PerFileSizeCap(t *testing.T) {
	tree := buildRepo(t, map[string]string{
		"sub/huge.txt": strings.Repeat("x", 2000),
	})
	f := NewFetcher()
	f.MaxFileBytes = 1000
	_, err := f.readSubtree(tree, "sub")
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
}

func TestReadSubtree_BinaryFlag(t *testing.T) {
	tree := buildRepo(t, map[string]string{
		"sub/text.md": "plain text",
		"sub/bin.dat": "ok\x00\x01\x02binary",
	})
	f := NewFetcher()
	got, err := f.readSubtree(tree, "sub")
	if err != nil {
		t.Fatalf("readSubtree: %v", err)
	}
	for _, ff := range got {
		switch ff.Path {
		case "text.md":
			if ff.Binary {
				t.Errorf("text.md marked binary")
			}
		case "bin.dat":
			if !ff.Binary {
				t.Errorf("bin.dat not marked binary")
			}
		}
	}
}

// content_sha256 from a fetched subtree must equal what the registry
// computes for the identical files — this is the contract t5's update
// check relies on (compare hashes, not commits).
func TestManifestFromFiles_MatchesRegistry(t *testing.T) {
	tree := buildRepo(t, map[string]string{
		"deploy-helper/SKILL.md": "# deploy-helper\n\nDoes things.",
		"deploy-helper/run.sh":   "#!/bin/sh\necho go\n",
	})
	f := NewFetcher()
	fetched, err := f.readSubtree(tree, "deploy-helper")
	if err != nil {
		t.Fatalf("readSubtree: %v", err)
	}
	manifest, err := manifestFromFiles(fetched)
	if err != nil {
		t.Fatalf("manifestFromFiles: %v", err)
	}
	if manifest.ContentSHA256 == "" {
		t.Fatal("empty ContentSHA256")
	}

	// Publish the SAME files through the registry and compare hashes.
	inmem := make([]registry.InMemoryFile, 0, len(fetched))
	for _, ff := range fetched {
		inmem = append(inmem, registry.InMemoryFile{Path: ff.Path, Content: ff.Content})
	}
	regManifest := registryContentSHA(t, inmem)
	if manifest.ContentSHA256 != regManifest {
		t.Errorf("fetch content_sha256 = %q, registry = %q (must match)", manifest.ContentSHA256, regManifest)
	}
}

// registryContentSHA materialises files the way registry does and returns
// the content_sha256 skill.Generate produces, so the test compares apples
// to apples without standing up a full registry.Store.
func registryContentSHA(t *testing.T, files []registry.InMemoryFile) string {
	t.Helper()
	tmp := t.TempDir()
	for _, f := range files {
		dest := filepath.Join(tmp, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, f.Content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := skill.Generate(tmp)
	if err != nil {
		t.Fatalf("skill.Generate: %v", err)
	}
	return m.ContentSHA256
}
