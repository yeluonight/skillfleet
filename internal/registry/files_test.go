package registry

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestPublishFromFiles_AndReadBack(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	v, err := s.PublishFromFiles(ctx, []InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: fromfiles\ndescription: d\n---\n# x\n")},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n")},
		{Path: "中文/说明.md", Content: []byte("你好\n")},
	}, PublishParams{Name: "fromfiles", Kind: KindManual}, time.UnixMilli(1))
	if err != nil {
		t.Fatal(err)
	}
	if v.Manifest.FileCount != 3 {
		t.Errorf("file count = %d, want 3", v.Manifest.FileCount)
	}

	// ReadVersionFiles returns all files sorted, content intact.
	files, err := s.ReadVersionFiles(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("read %d files, want 3", len(files))
	}
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = string(f.Content)
	}
	if byPath["中文/说明.md"] != "你好\n" {
		t.Errorf("unicode content = %q", byPath["中文/说明.md"])
	}

	// ReadVersionFile fetches a single file.
	one, err := s.ReadVersionFile(ctx, v, "scripts/run.sh")
	if err != nil {
		t.Fatal(err)
	}
	if string(one) != "#!/bin/sh\n" {
		t.Errorf("single file = %q", one)
	}
}

func TestReadVersionFile_NotInPackage(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	v, err := s.PublishFromFiles(ctx, []InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: x\n---\nx\n")},
	}, PublishParams{Name: "x", Kind: KindManual}, time.UnixMilli(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadVersionFile(ctx, v, "missing.md"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}

func TestReadVersionFile_RejectsBadPath(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	v, _ := s.PublishFromFiles(ctx, []InMemoryFile{
		{Path: "SKILL.md", Content: []byte("---\nname: x\n---\nx\n")},
	}, PublishParams{Name: "x", Kind: KindManual}, time.UnixMilli(1))
	if _, err := s.ReadVersionFile(ctx, v, "../escape"); err == nil {
		t.Error("expected path-escape rejection")
	}
}

func TestPublishFromFiles_RejectsBadPath(t *testing.T) {
	s := newStore(t)
	_, err := s.PublishFromFiles(context.Background(), []InMemoryFile{
		{Path: "../evil", Content: []byte("x")},
	}, PublishParams{Name: "x", Kind: KindManual}, time.UnixMilli(1))
	if err == nil {
		t.Error("expected path-escape rejection")
	}
}

func TestListSkills_AggregatesByName(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// Two versions of "a", one of "b".
	s.PublishFromFiles(ctx, []InMemoryFile{{Path: "SKILL.md", Content: []byte("---\nname: a\n---\nA1\n")}},
		PublishParams{Name: "a", Kind: KindManual}, time.UnixMilli(1))
	s.PublishFromFiles(ctx, []InMemoryFile{{Path: "SKILL.md", Content: []byte("---\nname: a\n---\nA2\n")}},
		PublishParams{Name: "a", Kind: KindDraftPublish}, time.UnixMilli(2))
	s.PublishFromFiles(ctx, []InMemoryFile{{Path: "SKILL.md", Content: []byte("---\nname: b\n---\nB1\n")}},
		PublishParams{Name: "b", Kind: KindManual}, time.UnixMilli(3))

	skills, err := s.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("skills = %d, want 2", len(skills))
	}
	// Newest-updated first → "b" (t=3) before "a" (latest t=2).
	if skills[0].Name != "b" {
		t.Errorf("first = %q, want b (most recently updated)", skills[0].Name)
	}
	for _, sk := range skills {
		if sk.Name == "a" && sk.VersionCount != 2 {
			t.Errorf("a version_count = %d, want 2", sk.VersionCount)
		}
	}
}

func TestSkillExists(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ok, err := s.SkillExists(ctx, "ghost")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("ghost should not exist")
	}
	s.PublishFromFiles(ctx, []InMemoryFile{{Path: "SKILL.md", Content: []byte("---\nname: real\n---\nx\n")}},
		PublishParams{Name: "real", Kind: KindManual}, time.UnixMilli(1))
	ok, _ = s.SkillExists(ctx, "real")
	if !ok {
		t.Error("real should exist after publish")
	}
}

func TestListSkills_Empty(t *testing.T) {
	s := newStore(t)
	skills, err := s.ListSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Errorf("empty registry returned %d skills", len(skills))
	}
}
