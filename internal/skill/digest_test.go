package skill_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/skill"
)

func TestDigestDeterministicAcrossSources(t *testing.T) {
	// Same tree content via MapFS and via a real directory must produce the
	// same digest, regardless of creation order.
	mapFS := fstest.MapFS{
		"skill/b.txt": &fstest.MapFile{Data: []byte("bbb")},
		"skill/a.txt": &fstest.MapFile{Data: []byte("aaa")},
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill", "b.txt"), []byte("bbb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill", "a.txt"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	fromMap, err := skill.Digest(mapFS, "skill")
	if err != nil {
		t.Fatal(err)
	}
	fromDisk, err := skill.Digest(os.DirFS(dir), "skill")
	if err != nil {
		t.Fatal(err)
	}
	if fromMap != fromDisk {
		t.Fatalf("digest mismatch: %q vs %q", fromMap, fromDisk)
	}
	if want := "sha256:"; !strings.HasPrefix(fromMap, want) {
		t.Fatalf("digest %q lacks %q prefix", fromMap, want)
	}
}

func treeWith(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

func TestDigestDetectsDrift(t *testing.T) {
	base := map[string]string{
		"skill/a.txt":     "aaa",
		"skill/sub/c.txt": "ccc",
	}
	reference, err := skill.Digest(treeWith(base), "skill")
	if err != nil {
		t.Fatal(err)
	}

	drifts := map[string]map[string]string{
		"modified": {"skill/a.txt": "zzz", "skill/sub/c.txt": "ccc"},
		"removed":  {"skill/a.txt": "aaa"},
		"added":    {"skill/a.txt": "aaa", "skill/sub/c.txt": "ccc", "skill/new.txt": "n"},
	}
	for label, files := range drifts {
		d, err := skill.Digest(treeWith(files), "skill")
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if d == reference {
			t.Errorf("%s: digest did not change", label)
		}
	}
}

func TestDigestIgnoresDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skill", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill", "a.txt"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	d1, err := skill.Digest(os.DirFS(dir), "skill")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "skill", "empty2", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	d2, err := skill.Digest(os.DirFS(dir), "skill")
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Errorf("empty directories changed the digest: %q vs %q", d1, d2)
	}
}

func TestDigestRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill", "a.txt"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "skill", "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := skill.Digest(os.DirFS(dir), "skill"); err == nil {
		t.Fatal("expected error for symlink in tree")
	}
}

func TestDigestDetectsExecBitChange(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "skill", "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d1, err := skill.Digest(os.DirFS(dir), "skill")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	d2, err := skill.Digest(os.DirFS(dir), "skill")
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d2 {
		t.Error("executable bit change did not alter the digest")
	}
}
