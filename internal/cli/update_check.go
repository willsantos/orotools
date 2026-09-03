package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"oroborus.dev/orotools/internal/ui"
	"oroborus.dev/orotools/internal/updater"
)

const (
	// Intervalo mínimo entre consultas ao canal (DIST-13).
	updateCheckInterval = 24 * time.Hour
	// O aviso nunca atrasa o comando: goroutine fire-and-forget com timeout
	// curto; o processo pode sair antes e interromper o check (NFR-1).
	updateCheckTimeout = 2 * time.Second
	// EnvNoUpdateCheck desativa o aviso passivo.
	EnvNoUpdateCheck = "ORO_NO_UPDATE_CHECK"
)

// updateCheckState is the on-disk cache (update-check.json) that throttles
// the passive check and records releases the user has already been told about.
type updateCheckState struct {
	LastCheck  time.Time `json:"last_check"`
	LatestSeen string    `json:"latest_seen"`
}

func shouldCheckForUpdate(version string, args []string) bool {
	if version == "dev" {
		return false
	}
	if os.Getenv(EnvNoUpdateCheck) != "" {
		return false
	}
	// Comandos que falam de versão/atualização por conta própria.
	if len(args) > 0 {
		switch args[0] {
		case "upgrade", "help", "completion":
			return false
		}
	}
	return true
}

// runPassiveUpdateCheck is the fire-and-forget body: consulta o canal, grava
// o cache e devolve o aviso (vazio = nada a imprimir). Qualquer falha é
// silenciosa — o aviso passivo nunca é um erro para o usuário.
func runPassiveUpdateCheck(version string) {
	cachePath, err := updateCheckCachePath()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()
	if msg := passiveUpdateCheck(ctx, version, cachePath, ""); msg != "" {
		ui.NewStatus(os.Stderr).Info(msg)
	}
}

// passiveUpdateCheck resolves the notice for the current state; baseURL
// injects a test channel (vazio = canal público).
func passiveUpdateCheck(ctx context.Context, version, cachePath, baseURL string) string {
	prev := readUpdateCheckState(cachePath)
	if !prev.LastCheck.IsZero() && time.Since(prev.LastCheck) < updateCheckInterval {
		return ""
	}

	up, err := updater.New(updater.Options{BaseURL: baseURL})
	if err != nil {
		return ""
	}
	rel, err := up.Latest(ctx)
	if err != nil {
		return ""
	}

	writeUpdateCheckState(cachePath, updateCheckState{LastCheck: time.Now().UTC(), LatestSeen: rel.Version})

	newer, err := updater.IsNewer(rel.Version, version)
	if err != nil || !newer {
		return ""
	}
	if prev.LatestSeen == rel.Version {
		// Usuário já foi avisado desta release; não insistir no próximo ciclo.
		return ""
	}
	return fmt.Sprintf("nova versão do oro disponível: %s → %s (rode \"oro upgrade\")",
		version, displayVersion(rel.Version))
}

func updateCheckCachePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "oro", "update-check.json"), nil
}

func readUpdateCheckState(path string) updateCheckState {
	var state updateCheckState
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &state) != nil {
		return updateCheckState{}
	}
	return state
}

func writeUpdateCheckState(path string, state updateCheckState) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
