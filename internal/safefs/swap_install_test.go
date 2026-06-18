package safefs

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

// openTestRoot makes a temp dir, populates it via setup, and returns an
// open *os.Root over it plus the dir path. The root is closed on cleanup.
func openTestRoot(t *testing.T) (*os.Root, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := OpenAllowedRoot(dir)
	if err != nil {
		t.Fatalf("OpenAllowedRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(t *testing.T, dir, rel string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// --- OpenAllowedRoot -------------------------------------------------

func TestOpenAllowedRoot_RejectsNonDir(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAllowedRoot(f); err == nil {
		t.Error("opened a file as an allowed root")
	}
}

// --- staging containment --------------------------------------------

// TestStageFiles_RejectsEscapes is the containment guard at the staging
// layer: a plan file path that tries to escape (../, absolute, drive,
// control char) is refused before any write.
func TestStageFiles_RejectsEscapes(t *testing.T) {
	root, _ := openTestRoot(t)
	stRel, _, err := CreateStaging(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../escape", "/abs/path", "a/../../b", "x\x00y"} {
		err := StageFiles(root, stRel, []StagedFile{{Path: bad, Content: []byte("x")}})
		if err == nil {
			t.Errorf("StageFiles accepted escaping path %q", bad)
		}
	}
}

func TestStageFiles_WritesTreeWithExecBit(t *testing.T) {
	root, dir := openTestRoot(t)
	stRel, _, err := CreateStaging(root)
	if err != nil {
		t.Fatal(err)
	}
	files := []StagedFile{
		{Path: "SKILL.md", Content: []byte("# skill")},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Exec: true},
	}
	if err := StageFiles(root, stRel, files); err != nil {
		t.Fatalf("StageFiles: %v", err)
	}
	if got := readFile(t, dir, stRel+"/SKILL.md"); got != "# skill" {
		t.Errorf("content = %q", got)
	}
	// Exec bit honoured (skip on platforms without unix perms).
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(stRel+"/scripts/run.sh")))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&0o100 == 0 {
			t.Error("exec bit not set on staged script")
		}
	}
}

// TestStageFiles_SymlinkInRootCannotEscape proves os.Root containment:
// even if a symlink named "out" inside the root points at an external
// dir, staging a file "out/evil" does NOT write outside the root —
// os.Root refuses to traverse the symlink.
func TestStageFiles_SymlinkInRootCannotEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root, dir := openTestRoot(t)
	external := t.TempDir()
	// Create a symlink inside the root pointing at the external dir.
	if err := os.Symlink(external, filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	stRel, _, err := CreateStaging(root)
	if err != nil {
		t.Fatal(err)
	}
	// Staging path "out/evil" — if os.Root followed the symlink this would
	// land in `external`. It must not.
	_ = StageFiles(root, stRel, []StagedFile{{Path: "out/evil", Content: []byte("pwned")}})
	if exists(t, external, "evil") {
		t.Fatal("write escaped the root through a symlink — containment broken")
	}
}

// --- Reconcile ------------------------------------------------------

// TestReconcile_CarriesExtras: a hand-added file (in neither old marker
// nor new manifest) is classified as an extra to carry; a file the old
// install owned but the new version drops is stale.
func TestReconcile_CarriesExtras(t *testing.T) {
	root, dir := openTestRoot(t)
	// On-disk skill dir: managed SKILL.md + old-managed legacy.txt +
	// user's note.txt (unmanaged).
	writeFile(t, dir, "deploy/SKILL.md", "v2")
	writeFile(t, dir, "deploy/legacy.txt", "old")
	writeFile(t, dir, "deploy/note.txt", "user data")

	oldMarker := []string{"SKILL.md", "legacy.txt"}
	newManifest := []string{"SKILL.md"} // legacy dropped

	extras, stale, err := Reconcile(root, "deploy", oldMarker, newManifest)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(extras) != 1 || extras[0] != "note.txt" {
		t.Errorf("extras = %v, want [note.txt]", extras)
	}
	if len(stale) != 1 || stale[0] != "legacy.txt" {
		t.Errorf("stale = %v, want [legacy.txt]", stale)
	}
}

func TestReconcile_FirstInstallEmpty(t *testing.T) {
	root, _ := openTestRoot(t)
	extras, stale, err := Reconcile(root, "newskill", nil, []string{"SKILL.md"})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(extras) != 0 || len(stale) != 0 {
		t.Errorf("first install: extras=%v stale=%v, want empty", extras, stale)
	}
}

// --- DeleteManaged --------------------------------------------------

// TestDeleteManaged_NeverTouchesUnmanaged is the load-bearing §9.4
// guard: DeleteManaged removes only files in the managed set; a file
// present on disk but NOT in the set survives untouched.
func TestDeleteManaged_NeverTouchesUnmanaged(t *testing.T) {
	root, dir := openTestRoot(t)
	writeFile(t, dir, "deploy/SKILL.md", "managed")
	writeFile(t, dir, "deploy/note.txt", "UNMANAGED user data")

	managed := []string{"SKILL.md"} // note.txt deliberately absent
	deleted, _, err := DeleteManaged(root, "deploy", managed)
	if err != nil {
		t.Fatalf("DeleteManaged: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "SKILL.md" {
		t.Errorf("deleted = %v, want [SKILL.md]", deleted)
	}
	if !exists(t, dir, "deploy/note.txt") {
		t.Fatal("unmanaged note.txt was deleted — §9.4 violated")
	}
	if exists(t, dir, "deploy/SKILL.md") {
		t.Error("managed SKILL.md not deleted")
	}
}

// TestDeleteManaged_KeepsSymlinkSlot: if a managed slot is occupied by a
// symlink (a tampered tree), DeleteManaged keeps it (records in kept)
// rather than following/unlinking it.
func TestDeleteManaged_KeepsSymlinkSlot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root, dir := openTestRoot(t)
	external := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(external, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A managed slot "SKILL.md" is actually a symlink to an external file.
	if err := os.Symlink(external, filepath.Join(dir, "deploy", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	deleted, kept, err := DeleteManaged(root, "deploy", []string{"SKILL.md"})
	if err != nil {
		t.Fatalf("DeleteManaged: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("deleted a symlink slot: %v", deleted)
	}
	if len(kept) != 1 {
		t.Errorf("kept = %v, want [SKILL.md]", kept)
	}
	if _, err := os.Lstat(external); err != nil {
		t.Error("external symlink target was removed — followed a link out of tree")
	}
}

// --- backup / restore round-trip ------------------------------------

// TestBackupRestore_RoundTrip: a backup captures the install (including
// a local edit + the marker), and after the live dir is mutated, restore
// brings back the exact original bytes.
func TestBackupRestore_RoundTrip(t *testing.T) {
	root, dir := openTestRoot(t)
	writeFile(t, dir, "deploy/SKILL.md", "ORIGINAL with local edit")
	writeFile(t, dir, "deploy/scripts/run.sh", "echo original")
	writeFile(t, dir, "deploy/"+MarkerName, `{"managed_by":"skillfleet"}`)

	backupDir := filepath.Join(t.TempDir(), "bk")
	ref, err := BackupInstall(root, "deploy", backupDir)
	if err != nil {
		t.Fatalf("BackupInstall: %v", err)
	}
	if ref.Empty {
		t.Fatal("backup reported empty for a populated dir")
	}
	wantFiles := []string{"SKILL.md", "scripts/run.sh"}
	sort.Strings(ref.Files)
	if len(ref.Files) != 2 || ref.Files[0] != wantFiles[0] || ref.Files[1] != wantFiles[1] {
		t.Errorf("backup files = %v, want %v (+marker separately)", ref.Files, wantFiles)
	}
	if len(ref.MarkerJSON) == 0 {
		t.Error("marker not captured in backup")
	}

	// Mutate the live install (simulate a bad swap).
	writeFile(t, dir, "deploy/SKILL.md", "CORRUPTED")
	_ = os.Remove(filepath.Join(dir, "deploy", "scripts", "run.sh"))

	if err := RestoreBackup(root, "deploy", ref); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if got := readFile(t, dir, "deploy/SKILL.md"); got != "ORIGINAL with local edit" {
		t.Errorf("after restore SKILL.md = %q, want original", got)
	}
	if got := readFile(t, dir, "deploy/scripts/run.sh"); got != "echo original" {
		t.Errorf("after restore run.sh = %q", got)
	}
	if !exists(t, dir, "deploy/"+MarkerName) {
		t.Error("marker not restored")
	}
}

func TestBackupInstall_EmptyForMissingDir(t *testing.T) {
	root, _ := openTestRoot(t)
	ref, err := BackupInstall(root, "ghost", filepath.Join(t.TempDir(), "bk"))
	if err != nil {
		t.Fatalf("BackupInstall: %v", err)
	}
	if !ref.Empty {
		t.Error("missing dir should back up as Empty")
	}
}

// TestRestoreBackup_EmptyUninstalls: restoring an Empty backup removes
// the install dir (rolling back a first-time install).
func TestRestoreBackup_EmptyUninstalls(t *testing.T) {
	root, dir := openTestRoot(t)
	writeFile(t, dir, "deploy/SKILL.md", "installed")
	if err := RestoreBackup(root, "deploy", BackupRef{Empty: true}); err != nil {
		t.Fatalf("RestoreBackup empty: %v", err)
	}
	if exists(t, dir, "deploy") {
		t.Error("restoring an empty backup should remove the install dir")
	}
}

// --- atomic replace swap --------------------------------------------

// TestSwap_CommitReplacesLive: a forward swap + commit makes the staged
// tree the live install and discards the old.
func TestSwap_CommitReplacesLive(t *testing.T) {
	root, dir := openTestRoot(t)
	writeFile(t, dir, "deploy/SKILL.md", "OLD")

	stRel, _, err := CreateStaging(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := StageFiles(root, stRel, []StagedFile{{Path: "SKILL.md", Content: []byte("NEW")}}); err != nil {
		t.Fatal(err)
	}
	sw, err := BeginSwap(root, "deploy", stRel)
	if err != nil {
		t.Fatalf("BeginSwap: %v", err)
	}
	if got := readFile(t, dir, "deploy/SKILL.md"); got != "NEW" {
		t.Errorf("after swap live = %q, want NEW", got)
	}
	if err := sw.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := readFile(t, dir, "deploy/SKILL.md"); got != "NEW" {
		t.Errorf("after commit live = %q, want NEW", got)
	}
	// Staging dir gone (renamed into place).
	if exists(t, dir, stRel) {
		t.Error("staging dir survived a committed swap")
	}
}

// TestSwap_Rollback restores the original live dir after a swap that
// must be undone (e.g. a failed post-install rescan).
func TestSwap_Rollback(t *testing.T) {
	root, dir := openTestRoot(t)
	writeFile(t, dir, "deploy/SKILL.md", "OLD")

	stRel, _, err := CreateStaging(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := StageFiles(root, stRel, []StagedFile{{Path: "SKILL.md", Content: []byte("NEW")}}); err != nil {
		t.Fatal(err)
	}
	sw, err := BeginSwap(root, "deploy", stRel)
	if err != nil {
		t.Fatalf("BeginSwap: %v", err)
	}
	// Simulate a later failure → roll the swap back.
	if err := sw.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := readFile(t, dir, "deploy/SKILL.md"); got != "OLD" {
		t.Errorf("after rollback live = %q, want OLD restored", got)
	}
}

// TestSwap_FirstInstallNoOld: swapping into a name with no prior live
// dir works, and rollback removes the install (back to nothing).
func TestSwap_FirstInstallNoOld(t *testing.T) {
	root, dir := openTestRoot(t)
	stRel, _, err := CreateStaging(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := StageFiles(root, stRel, []StagedFile{{Path: "SKILL.md", Content: []byte("NEW")}}); err != nil {
		t.Fatal(err)
	}
	sw, err := BeginSwap(root, "fresh", stRel)
	if err != nil {
		t.Fatalf("BeginSwap: %v", err)
	}
	if got := readFile(t, dir, "fresh/SKILL.md"); got != "NEW" {
		t.Errorf("first install live = %q", got)
	}
	if err := sw.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if exists(t, dir, "fresh") {
		t.Error("rollback of a first install should leave nothing")
	}
}

func TestSwap_DoubleCommitGuarded(t *testing.T) {
	root, dir := openTestRoot(t)
	writeFile(t, dir, "deploy/SKILL.md", "OLD")
	stRel, _, _ := CreateStaging(root)
	_ = StageFiles(root, stRel, []StagedFile{{Path: "SKILL.md", Content: []byte("NEW")}})
	sw, err := BeginSwap(root, "deploy", stRel)
	if err != nil {
		t.Fatal(err)
	}
	if err := sw.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := sw.Commit(); err != ErrSwapAlreadyDone {
		t.Errorf("double commit: err = %v, want ErrSwapAlreadyDone", err)
	}
}
