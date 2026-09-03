package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShouldCheckForUpdate(t *testing.T) {
	t.Setenv(EnvNoUpdateCheck, "")
	cases := []struct {
		name    string
		version string
		args    []string
		want    bool
	}{
		{"comando comum", "0.5.0", []string{"info"}, true},
		{"sem argumentos", "0.5.0", nil, true},
		{"build dev", "dev", []string{"info"}, false},
		{"opt-out por env", "0.5.0", []string{"info"}, false},
		{"upgrade", "0.5.0", []string{"upgrade"}, false},
		{"help", "0.5.0", []string{"help"}, false},
		{"completion", "0.5.0", []string{"completion"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "opt-out por env" {
				t.Setenv(EnvNoUpdateCheck, "1")
			}
			if got := shouldCheckForUpdate(c.version, c.args); got != c.want {
				t.Errorf("shouldCheckForUpdate(%q, %v) = %v, want %v", c.version, c.args, got, c.want)
			}
		})
	}
}

// newNoticeChannel serves a single v0.9.0 release (mais nova que 0.5.0).
func newNoticeChannel(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("GITHUB_TOKEN", "")
	release := map[string]any{
		"tag_name":   "v0.9.0",
		"draft":      false,
		"prerelease": false,
		"assets": []map[string]any{
			{"id": 1, "name": "oro_0.9.0_linux_amd64.tar.gz", "state": "uploaded", "size": 10},
			{"id": 2, "name": "checksums.txt", "state": "uploaded", "size": 10},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/willsantos/orotools/releases", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{release})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestPassiveCheckNotifiesOnce(t *testing.T) {
	channel := newNoticeChannel(t)
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	ctx := context.Background()

	msg := passiveUpdateCheck(ctx, "0.5.0", cachePath, channel.URL)
	if !strings.Contains(msg, "v0.9.0") {
		t.Errorf("primeiro check sem aviso: %q", msg)
	}

	// Release já vista: próximo ciclo (cache vencido) não insiste.
	stale := updateCheckState{LastCheck: time.Now().Add(-25 * time.Hour), LatestSeen: "0.9.0"}
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if msg := passiveUpdateCheck(ctx, "0.5.0", cachePath, channel.URL); msg != "" {
		t.Errorf("aviso repetido para release já vista: %q", msg)
	}
}

func TestPassiveCheckThrottledByFreshCache(t *testing.T) {
	channel := newNoticeChannel(t)
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	fresh := updateCheckState{LastCheck: time.Now().Add(-1 * time.Hour)}
	data, _ := json.Marshal(fresh)
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if msg := passiveUpdateCheck(context.Background(), "0.5.0", cachePath, channel.URL); msg != "" {
		t.Errorf("cache de <24h deveria silenciar, aviso: %q", msg)
	}
}

func TestPassiveCheckSilentOnFailure(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	// Canal inacessível (conexão recusada) e cache inexistente.
	if msg := passiveUpdateCheck(context.Background(), "0.5.0", cachePath, "http://127.0.0.1:1"); msg != "" {
		t.Errorf("falha de rede deveria ser silenciosa, aviso: %q", msg)
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Errorf("falha de rede não deveria gravar cache: %v", err)
	}
}

func TestPassiveCheckUpToDateWritesCache(t *testing.T) {
	channel := newNoticeChannel(t)
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	if msg := passiveUpdateCheck(context.Background(), "0.9.0", cachePath, channel.URL); msg != "" {
		t.Errorf("versão em dia deveria ser silenciosa, aviso: %q", msg)
	}
	var state updateCheckState
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("cache não gravado: %v", err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.LatestSeen != "0.9.0" || state.LastCheck.IsZero() {
		t.Errorf("cache inesperado: %+v", state)
	}
}
