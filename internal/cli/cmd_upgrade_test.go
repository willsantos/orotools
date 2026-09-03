package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newUpgradeChannel serves a single v0.5.0 release over a fake GitHub API.
func newUpgradeChannel(t *testing.T, sumsOverride string) *httptest.Server {
	t.Helper()
	t.Setenv("GITHUB_TOKEN", "")

	version := "0.5.0"
	assetName := fmt.Sprintf("oro_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	payload := []byte("fake-oro-binary")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "oro", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	asset := buf.Bytes()

	sums := sumsOverride
	if sums == "" {
		sums = fmt.Sprintf("%x  %s\n", sha256.Sum256(asset), assetName)
	}
	release := map[string]any{
		"tag_name":   "v" + version,
		"draft":      false,
		"prerelease": false,
		"assets": []map[string]any{
			{"id": 1, "name": assetName, "state": "uploaded", "size": len(asset)},
			{"id": 2, "name": "checksums.txt", "state": "uploaded", "size": len(sums)},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/willsantos/orotools/releases", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{release})
	})
	mux.HandleFunc("GET /api/v3/repos/willsantos/orotools/releases/assets/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		switch filepath.Base(r.URL.Path) {
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

func TestUpgradeCheckNewer(t *testing.T) {
	out := &bytes.Buffer{}
	err := runUpgrade(context.Background(), out, upgradeOptions{
		Current:   "0.4.0",
		CheckOnly: true,
		BaseURL:   newUpgradeChannel(t, "").URL,
	})
	if err != nil {
		t.Fatalf("--check com versão nova deve sair 0, err = %v", err)
	}
	for _, want := range []string{"nova versão disponível", "v0.4.0", "v0.5.0", "oro upgrade"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("saída do --check sem %q:\n%s", want, out.String())
		}
	}
}

func TestUpgradeCheckUpToDate(t *testing.T) {
	out := &bytes.Buffer{}
	err := runUpgrade(context.Background(), out, upgradeOptions{
		Current:   "0.5.0",
		CheckOnly: true,
		BaseURL:   newUpgradeChannel(t, "").URL,
	})
	if err != nil {
		t.Fatalf("--check em dia deve sair 0, err = %v", err)
	}
	if !strings.Contains(out.String(), "última versão") {
		t.Errorf("saída do --check em dia sem \"última versão\":\n%s", out.String())
	}
}

func TestUpgradeCheckDevVersion(t *testing.T) {
	out := &bytes.Buffer{}
	err := runUpgrade(context.Background(), out, upgradeOptions{
		Current:   "dev",
		CheckOnly: true,
		BaseURL:   newUpgradeChannel(t, "").URL,
	})
	if err != nil {
		t.Fatalf("--check com build dev deve sair 0, err = %v", err)
	}
	if !strings.Contains(out.String(), "não há como comparar") {
		t.Errorf("saída do --check com dev sem aviso de comparação:\n%s", out.String())
	}
}

func TestUpgradeApplyReplacesBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "oro")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	err := runUpgrade(context.Background(), out, upgradeOptions{
		Current:    "0.4.0",
		Executable: exe,
		BaseURL:    newUpgradeChannel(t, "").URL,
	})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("fake-oro-binary")) {
		t.Errorf("binário após upgrade = %q", got)
	}
	if !strings.Contains(out.String(), "atualizado para v0.5.0") {
		t.Errorf("saída do upgrade sem confirmação:\n%s", out.String())
	}
}

func TestUpgradeAlreadyUpToDateDoesNotTouchBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "oro")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	err := runUpgrade(context.Background(), out, upgradeOptions{
		Current:    "0.5.0",
		Executable: exe,
		BaseURL:    newUpgradeChannel(t, "").URL,
	})
	if err != nil {
		t.Fatalf("upgrade em dia deve sair 0, err = %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("old-binary")) {
		t.Errorf("binário alterado sem necessidade: %q", got)
	}
	if !strings.Contains(out.String(), "nada a fazer") {
		t.Errorf("saída sem \"nada a fazer\":\n%s", out.String())
	}
}

func TestUpgradeOfflineFailsWithManualURL(t *testing.T) {
	out := &bytes.Buffer{}
	err := runUpgrade(context.Background(), out, upgradeOptions{
		Current:   "0.4.0",
		CheckOnly: true,
		BaseURL:   "http://127.0.0.1:1",
	})
	if err == nil {
		t.Fatal("upgrade sem rede deve falhar")
	}
	if !strings.Contains(err.Error(), "github.com/willsantos/orotools/releases") {
		t.Errorf("erro sem a URL manual das releases: %v", err)
	}
}

func TestUpgradeUnknownTargetVersion(t *testing.T) {
	out := &bytes.Buffer{}
	err := runUpgrade(context.Background(), out, upgradeOptions{
		Current:    "0.4.0",
		Target:     "v9.9.9",
		Executable: filepath.Join(t.TempDir(), "oro"),
		BaseURL:    newUpgradeChannel(t, "").URL,
	})
	if err == nil {
		t.Fatal("upgrade para versão inexistente deve falhar")
	}
}
