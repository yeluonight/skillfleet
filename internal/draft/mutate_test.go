package draft

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPutFile_CreateAndReplace(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, err := ds.Create(ctx, CreateParams{Name: "edit-me"}, time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}

	// Create a new text file.
	f, err := ds.PutFile(ctx, d.ID, "notes.md", []byte("first\n"), time.UnixMilli(2000))
	if err != nil {
		t.Fatal(err)
	}
	if f.IsBinary || f.Size != 6 {
		t.Errorf("file = %+v", f)
	}

	// Replace it.
	if _, err := ds.PutFile(ctx, d.ID, "notes.md", []byte("second version\n"), time.UnixMilli(3000)); err != nil {
		t.Fatal(err)
	}
	content, _, err := ds.ReadFile(ctx, d.ID, "notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "second version\n" {
		t.Errorf("content = %q, want replaced", content)
	}

	// updated_at advanced.
	loaded, _ := ds.Load(ctx, d.ID)
	if !loaded.UpdatedAt.After(loaded.CreatedAt) {
		t.Error("updated_at not advanced after edit")
	}
	// SKILL.md + notes.md = 2 files (replace didn't duplicate).
	if len(loaded.Files) != 2 {
		t.Errorf("files = %d, want 2", len(loaded.Files))
	}
}

func TestPutFile_BinaryGoesToBlob(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "bin"}, time.UnixMilli(1))

	f, err := ds.PutFile(ctx, d.ID, "data.bin", []byte{0x00, 0x01, 0x02, 0xff}, time.UnixMilli(2))
	if err != nil {
		t.Fatal(err)
	}
	if !f.IsBinary {
		t.Error("expected binary classification")
	}
	// Round-trips from the on-disk blob.
	content, isBin, err := ds.ReadFile(ctx, d.ID, "data.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !isBin || len(content) != 4 {
		t.Errorf("blob read = %v (binary=%v)", content, isBin)
	}
}

func TestPutFile_RejectsBadPath(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "x"}, time.UnixMilli(1))
	if _, err := ds.PutFile(ctx, d.ID, "../escape", []byte("x"), time.UnixMilli(2)); err == nil {
		t.Error("expected path-escape rejection")
	}
}

func TestDeleteFile(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "x"}, time.UnixMilli(1))
	ds.PutFile(ctx, d.ID, "gone.md", []byte("bye\n"), time.UnixMilli(2))

	if err := ds.DeleteFile(ctx, d.ID, "gone.md", time.UnixMilli(3)); err != nil {
		t.Fatal(err)
	}
	if err := ds.DeleteFile(ctx, d.ID, "gone.md", time.UnixMilli(4)); err != ErrFileNotFound {
		t.Errorf("second delete err = %v, want ErrFileNotFound", err)
	}
}

func TestDelete_DraftAndCascade(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "doomed"}, time.UnixMilli(1))

	if err := ds.Delete(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ds.Load(ctx, d.ID); err != ErrNotFound {
		t.Errorf("load after delete err = %v, want ErrNotFound", err)
	}
	if err := ds.Delete(ctx, d.ID); err != ErrNotFound {
		t.Errorf("re-delete err = %v, want ErrNotFound", err)
	}
}

func TestPutFile_UnknownDraft(t *testing.T) {
	ds, _ := newStores(t)
	if _, err := ds.PutFile(context.Background(), "dft_ghost", "x.md", []byte("y"), time.UnixMilli(1)); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestReadFile_NotFound(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "x"}, time.UnixMilli(1))
	if _, _, err := ds.ReadFile(ctx, d.ID, "nope.md"); err != ErrFileNotFound {
		t.Errorf("err = %v, want ErrFileNotFound", err)
	}
}

func TestPutFile_OversizeTextOffInline(t *testing.T) {
	ds, _ := newStores(t)
	ctx := context.Background()
	d, _ := ds.Create(ctx, CreateParams{Name: "big"}, time.UnixMilli(1))

	big := strings.Repeat("a", 2<<20) // 2 MiB text, over the editable limit
	if _, err := ds.PutFile(ctx, d.ID, "big.txt", []byte(big), time.UnixMilli(2)); err != nil {
		t.Fatal(err)
	}
	// Still readable (from blob), content intact.
	content, _, err := ds.ReadFile(ctx, d.ID, "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != len(big) {
		t.Errorf("oversize read = %d bytes, want %d", len(content), len(big))
	}
}
