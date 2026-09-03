package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const (
	fakeVersion = "0.5.0"
	fakeTag     = "v" + fakeVersion
)

// newFakeBinary returns a plausible payload for the "oro" binary inside the
// release asset.
func newFakeBinary(t *testing.T) []byte {
	t.Helper()
	return []byte("fake-oro-binary-" + fakeTag)
}

// buildTarGz packs a single executable named name (what DecompressCommand
// looks for inside the asset).
func buildTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// newFakeChannel serves a single-release GitHub API (list + asset download)
// over httptest, mirroring the routes go-github hits behind an enterprise
// base URL. sumsOverride replaces checksums.txt contents (bad checksum tests).
func newFakeChannel(t *testing.T, sumsOverride string) *httptest.Server {
	t.Helper()
	t.Setenv("GITHUB_TOKEN", "")

	assetName := fmt.Sprintf("oro_%s_%s_%s.tar.gz", fakeVersion, runtime.GOOS, runtime.GOARCH)
	asset := buildTarGz(t, "oro", newFakeBinary(t))
	sums := sumsOverride
	if sums == "" {
		sums = fmt.Sprintf("%x  %s\n", sha256.Sum256(asset), assetName)
	}

	release := map[string]any{
		"tag_name":   fakeTag,
		"name":       "oro " + fakeTag,
		"draft":      false,
		"prerelease": false,
		"assets": []map[string]any{
			{
				"id":                   1,
				"name":                 assetName,
				"browser_download_url": "UNUSED",
				"content_type":         "application/gzip",
				"state":                "uploaded",
				"size":                 len(asset),
			},
			{
				"id":                   2,
				"name":                 "checksums.txt",
				"browser_download_url": "UNUSED",
				"content_type":         "text/plain",
				"state":                "uploaded",
				"size":                 len(sums),
			},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/willsantos/orotools/releases", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{release})
	})
	mux.HandleFunc("GET /api/v3/repos/willsantos/orotools/releases/assets/", func(w http.ResponseWriter, r *http.Request) {
		id := filepath.Base(r.URL.Path)
		w.Header().Set("Content-Type", "application/octet-stream")
		switch id {
		case "1":
			_, _ = w.Write(asset)
		case "2":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func newTestUpdater(t *testing.T, baseURL string) *Updater {
	t.Helper()
	u, err := New(Options{BaseURL: baseURL + "/", OS: runtime.GOOS, Arch: runtime.GOARCH})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return u
}

func TestLatest(t *testing.T) {
	u := newTestUpdater(t, newFakeChannel(t, "").URL)
	rel, err := u.Latest(t.Context())
	if err != nil {
		t.Fatalf("Latest(): %v", err)
	}
	if rel.Version != fakeVersion {
		t.Errorf("Latest().Version = %q, want %q", rel.Version, fakeVersion)
	}
}

func TestLatestEmptyChannel(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/willsantos/orotools/releases", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	u := newTestUpdater(t, server.URL)
	if _, err := u.Latest(t.Context()); !errors.Is(err, ErrNoRelease) {
		t.Errorf("Latest() em canal vazio: err = %v, want ErrNoRelease", err)
	}
}

func TestVersion(t *testing.T) {
	u := newTestUpdater(t, newFakeChannel(t, "").URL)
	ctx := t.Context()

	if rel, err := u.Version(ctx, fakeTag); err != nil {
		t.Errorf("Version(%q): %v", fakeTag, err)
	} else if rel.Version != fakeVersion {
		t.Errorf("Version(%q).Version = %q", fakeTag, rel.Version)
	}
	if _, err := u.Version(ctx, "v9.9.9"); !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("Version(v9.9.9): err = %v, want ErrVersionNotFound", err)
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		release, current string
		want             bool
		wantErr          error
	}{
		{fakeVersion, "0.4.0", true, nil},
		{fakeVersion, "0.5.0", false, nil},
		{fakeVersion, "v0.4.9", true, nil},
		{fakeVersion, "0.6.0", false, nil},
		{"dev", "dev", false, ErrUnknownCurrentVersion},
		{fakeVersion, "dev", false, ErrUnknownCurrentVersion},
	}
	for _, c := range cases {
		got, err := IsNewer(c.release, c.current)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("IsNewer(%q, %q) err = %v, want %v", c.release, c.current, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.release, c.current, got, c.want)
		}
	}
}

func TestApplyReplacesBinary(t *testing.T) {
	u := newTestUpdater(t, newFakeChannel(t, "").URL)
	ctx := t.Context()

	dir := t.TempDir()
	exe := filepath.Join(dir, "oro")
	old := []byte("old-binary")
	if err := os.WriteFile(exe, old, 0o755); err != nil {
		t.Fatal(err)
	}

	rel, err := u.Version(ctx, fakeTag)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Apply(ctx, rel, exe); err != nil {
		t.Fatalf("Apply(): %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newFakeBinary(t)) {
		t.Errorf("binário após Apply = %q, want %q", got, newFakeBinary(t))
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("modo do binário após Apply = %v, sem bit de execução", info.Mode())
	}
}

// checksums.txt sem a entrada do asset instalado (só arquivos auxiliares):
// o update precisa falhar sem tocar no binário — bloqueia o cenário apontado
// no review da PR 188 contra o updater antigo (manifesto incompleto).
func TestApplyChecksumManifestMissingAssetKeepsBinary(t *testing.T) {
	missingSums := fmt.Sprintf("%064x  outro-arquivo_auxiliar.zip\n", 0xdeadbeef)
	u := newTestUpdater(t, newFakeChannel(t, missingSums).URL)
	ctx := t.Context()

	dir := t.TempDir()
	exe := filepath.Join(dir, "oro")
	old := []byte("old-binary")
	if err := os.WriteFile(exe, old, 0o755); err != nil {
		t.Fatal(err)
	}

	rel, err := u.Version(ctx, fakeTag)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Apply(ctx, rel, exe); err == nil {
		t.Fatal("Apply() com checksums.txt sem o asset instalado deveria falhar")
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, old) {
		t.Errorf("binário alterado com manifesto incompleto: %q", got)
	}
}

func TestApplyBadChecksumKeepsBinary(t *testing.T) {
	badSums := fmt.Sprintf("%064x  oro_%s_%s_%s.tar.gz\n",
		0xdeadbeef, fakeVersion, runtime.GOOS, runtime.GOARCH)
	u := newTestUpdater(t, newFakeChannel(t, badSums).URL)
	ctx := t.Context()

	dir := t.TempDir()
	exe := filepath.Join(dir, "oro")
	old := []byte("old-binary")
	if err := os.WriteFile(exe, old, 0o755); err != nil {
		t.Fatal(err)
	}

	rel, err := u.Version(ctx, fakeTag)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Apply(ctx, rel, exe); err == nil {
		t.Fatal("Apply() com checksum inválido deveria falhar")
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, old) {
		t.Errorf("binário alterado após falha de checksum: %q", got)
	}
}

func TestApplyThroughSymlinkReplacesTarget(t *testing.T) {
	u := newTestUpdater(t, newFakeChannel(t, "").URL)
	ctx := t.Context()

	dir := t.TempDir()
	exe := filepath.Join(dir, "oro")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "oro-link")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}

	rel, err := u.Version(ctx, fakeTag)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Apply(ctx, rel, link); err != nil {
		t.Fatalf("Apply() via symlink: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newFakeBinary(t)) {
		t.Errorf("alvo do symlink não foi substituído: %q", got)
	}
	if info, err := os.Lstat(link); err != nil {
		t.Fatal(err)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink foi substituído por arquivo regular")
	}
}

func TestApplyNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("rodando como root: permissão de escrita não se aplica")
	}
	u := newTestUpdater(t, newFakeChannel(t, "").URL)

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	exe := filepath.Join(dir, "oro")

	err := u.Apply(t.Context(), &Release{Version: fakeVersion}, exe)
	if !errors.Is(err, ErrNotWritable) {
		t.Errorf("Apply() em dir sem escrita: err = %v, want ErrNotWritable", err)
	}
}
