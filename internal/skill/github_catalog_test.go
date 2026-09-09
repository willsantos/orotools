package skill_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"oroborus.dev/orotools/internal/skill"
)

func githubFS(t *testing.T) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		"README.md":                     &fstest.MapFile{Data: []byte("catalog")},
		"local-pr-review/SKILL.md":      &fstest.MapFile{Data: []byte(skillDoc("local-pr-review", "Review PRs locally", "1.0.0"))},
		"local-pr-review/references.md": &fstest.MapFile{Data: []byte("refs")},
		"azure-mermaid/SKILL.md":        &fstest.MapFile{Data: []byte(skillDoc("azure-mermaid-compatible", "Mermaid compatible diagrams", "2.1.0"))},
		"broken-skill/SKILL.md":         &fstest.MapFile{Data: []byte("---\nname: broken\n---\n")},
		"no-skill-dir/notes.txt":        &fstest.MapFile{Data: []byte("not a skill")},
	}
}

func snapshotFor(t *testing.T, fsys fs.FS, rev string) skill.GitHubSnapshot {
	t.Helper()
	return skill.GitHubSnapshot{Revision: rev, Tree: skill.Tree{FS: fsys, Root: "."}, Cleanup: func() {}}
}

func TestGitHubCatalogDiscoversSkills(t *testing.T) {
	cat := skill.GitHubCatalog{Snapshot: snapshotFor(t, githubFS(t), "abc123")}
	cands, warns, err := cat.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(cands), cands)
	}
	// Sorted by name.
	if cands[0].Name != "azure-mermaid-compatible" || cands[1].Name != "local-pr-review" {
		t.Fatalf("order = [%s, %s]", cands[0].Name, cands[1].Name)
	}
	first := cands[0]
	if first.ID != "github:willsantos/skills_AI/azure-mermaid" {
		t.Errorf("ID = %q", first.ID)
	}
	if first.Source != skill.SourceGitHub || first.SourceRef != "willsantos/skills_AI" {
		t.Errorf("source fields = %+v", first)
	}
	if first.Revision != "abc123" {
		t.Errorf("Revision = %q, want the pinned snapshot revision", first.Revision)
	}
	if first.Version.String() != "2.1.0" {
		t.Errorf("Version = %q", first.Version.String())
	}
	// Invalid metadata degrades to warning, not error.
	if len(warns) != 1 || !strings.Contains(warns[0].SkillID, "broken-skill") {
		t.Fatalf("warnings = %+v, want one for broken-skill", warns)
	}
}

func TestGitHubCatalogEntryLimit(t *testing.T) {
	fsys := fstest.MapFS{
		"a/SKILL.md": &fstest.MapFile{Data: []byte(skillDoc("a", "a", "1.0.0"))},
		"b/SKILL.md": &fstest.MapFile{Data: []byte(skillDoc("b", "b", "1.0.0"))},
	}
	cat := skill.GitHubCatalog{
		Snapshot: snapshotFor(t, fsys, "abc"),
		Limits:   skill.SnapshotLimits{MaxEntries: 1, MaxFilesPerSkill: 10, MaxSkillBytes: 1 << 20, MaxFileBytes: 1 << 20, MaxTotalBytes: 1 << 20},
	}
	if _, _, err := cat.List(context.Background()); err == nil {
		t.Fatal("expected error when catalog exceeds MaxEntries")
	}
}

func TestGitHubCatalogPerSkillLimits(t *testing.T) {
	files := fstest.MapFS{"big/SKILL.md": &fstest.MapFile{Data: []byte(skillDoc("big", "b", "1.0.0"))}}
	many := fstest.MapFS{"many/SKILL.md": &fstest.MapFile{Data: []byte(skillDoc("many", "m", "1.0.0"))}}
	cat := skill.GitHubCatalog{
		Snapshot: snapshotFor(t, files, "abc"),
		Limits:   skill.SnapshotLimits{MaxEntries: 10, MaxFilesPerSkill: 10, MaxSkillBytes: 5, MaxFileBytes: 1 << 20, MaxTotalBytes: 1 << 20},
	}
	cands, warns, err := cat.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 || len(warns) != 1 {
		t.Fatalf("cands=%d warns=%d, want oversized skill omitted with warning", len(cands), len(warns))
	}
	_ = many
}

func TestGitHubCatalogDestinationCollision(t *testing.T) {
	fsys := fstest.MapFS{
		"a/SKILL.md": &fstest.MapFile{Data: []byte(skillDoc("same", "a", "1.0.0"))},
		"b/SKILL.md": &fstest.MapFile{Data: []byte(skillDoc("same", "b", "1.0.0"))},
	}
	cat := skill.GitHubCatalog{Snapshot: snapshotFor(t, fsys, "abc")}
	if _, _, err := cat.List(context.Background()); err == nil {
		t.Fatal("expected error for colliding destinations")
	}
}

// --- HTTPGitHubClient ---

type tarEntry struct {
	Name string
	Mode int64
	Data []byte
	Dir  bool
	Link string // symlink target; makes the entry a symlink
}

func tarballBytes(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.Name, Mode: e.Mode}
		switch {
		case e.Link != "":
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = e.Link
		case e.Dir:
			hdr.Typeflag = tar.TypeDir
		default:
			hdr.Typeflag = tar.TypeReg
			hdr.Size = int64(len(e.Data))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write(e.Data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const testTop = "willsantos-skills_AI-abc123"

func githubTestServer(t *testing.T, tarball []byte, token string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/willsantos/skills_AI", func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"default_branch":"main"}`))
	})
	mux.HandleFunc("/repos/willsantos/skills_AI/commits/main", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"sha":"abc123"}`))
	})
	mux.HandleFunc("/repos/willsantos/skills_AI/tarball/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-gzip")
		w.Write(tarball)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPGitHubClientResolveAndSnapshot(t *testing.T) {
	tarball := tarballBytes(t, []tarEntry{
		{Name: testTop + "/local-pr-review", Dir: true},
		{Name: testTop + "/local-pr-review/SKILL.md", Data: []byte(skillDoc("local-pr-review", "d", "1.0.0")), Mode: 0o644},
		{Name: testTop + "/local-pr-review/scripts/run.sh", Data: []byte("#!/bin/sh\n"), Mode: 0o755},
	})
	srv := githubTestServer(t, tarball, "tok")
	client := skill.HTTPGitHubClient{BaseURL: srv.URL, Token: "tok"}
	rev, err := client.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rev != "abc123" {
		t.Fatalf("Resolve = %q, want abc123", rev)
	}
	snap, err := client.Snapshot(context.Background(), rev)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Cleanup()
	data, err := fs.ReadFile(snap.Tree.FS, "local-pr-review/SKILL.md")
	if err != nil {
		t.Fatalf("skill file not extracted: %v", err)
	}
	if !strings.Contains(string(data), "local-pr-review") {
		t.Errorf("unexpected content: %q", data)
	}
	exe, err := os.Stat(filepath.Join(snap.Dir, "local-pr-review", "scripts", "run.sh"))
	if err != nil {
		t.Fatalf("exec file missing: %v", err)
	}
	if exe.Mode()&0o111 == 0 {
		t.Errorf("executable bit not preserved: %v", exe.Mode())
	}
	// Cleanup removes the extraction directory.
	snap.Cleanup()
	if _, err := os.Stat(snap.Dir); !os.IsNotExist(err) {
		t.Errorf("snapshot dir %q still present after cleanup", snap.Dir)
	}
}

func TestHTTPGitHubClientRejectsSymlink(t *testing.T) {
	tarball := tarballBytes(t, []tarEntry{
		{Name: testTop + "/evil", Link: "/etc/passwd", Mode: 0o777},
	})
	srv := githubTestServer(t, tarball, "")
	client := skill.HTTPGitHubClient{BaseURL: srv.URL}
	rev, err := client.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Snapshot(context.Background(), rev); err == nil {
		t.Fatal("expected error for symlink entry")
	}
}

func TestHTTPGitHubClientRejectsTraversal(t *testing.T) {
	tarball := tarballBytes(t, []tarEntry{
		{Name: testTop + "/../../escaped.txt", Data: []byte("nope")},
	})
	srv := githubTestServer(t, tarball, "")
	client := skill.HTTPGitHubClient{BaseURL: srv.URL}
	if _, err := client.Snapshot(context.Background(), "abc123"); err == nil {
		t.Fatal("expected error for traversal entry")
	}
}

func TestHTTPGitHubClientEnforcesFileLimit(t *testing.T) {
	tarball := tarballBytes(t, []tarEntry{
		{Name: testTop + "/big.bin", Data: bytes.Repeat([]byte("a"), 1024), Mode: 0o644},
	})
	srv := githubTestServer(t, tarball, "")
	client := skill.HTTPGitHubClient{
		BaseURL: srv.URL,
		Limits:  skill.SnapshotLimits{MaxFileBytes: 100, MaxTotalBytes: 1 << 20, MaxEntries: 10, MaxFilesPerSkill: 10, MaxSkillBytes: 1 << 20},
	}
	if _, err := client.Snapshot(context.Background(), "abc123"); err == nil {
		t.Fatal("expected error for oversized file")
	}
}

func TestHTTPGitHubClientRateLimitMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	client := skill.HTTPGitHubClient{BaseURL: srv.URL}
	_, err := client.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("error should hint at GITHUB_TOKEN: %v", err)
	}
}
