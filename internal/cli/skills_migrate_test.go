package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/skill"
)

// migrateFixture mounts a project with legacy installs and a JSON v2 lock,
// enough for the command-level flows.
type migrateFixture struct {
	root        string
	legacyBytes []byte
}

func newMigrateFixture(t *testing.T) migrateFixture {
	t.Helper()
	root := t.TempDir()
	writeCliSkillTree(t, root, ".opencode", "alpha", "1.0.0", true)
	writeCliSkillTree(t, root, ".claude", "alpha", "1.0.0", true)
	writeCliSkillTree(t, root, ".codex", "beta", "0.9.1", false)

	hashOf := func(agentDir, name string) string {
		t.Helper()
		return cliLegacyHash(t, filepath.Join(root, agentDir, "skills", name))
	}
	doc := map[string]any{
		"version": 2,
		"skills": map[string]any{
			"alpha": map[string]any{
				"name": "alpha", "source": "local",
				"contentHash": hashOf(".opencode", "alpha"),
				"installedAt": "2026-04-07T00:09:56Z",
				"updatedAt":   "2026-04-07T00:09:56Z",
				"agents":      []string{"claude-code", "opencode"},
				"method":      "copy",
				"global":      false,
			},
			"beta": map[string]any{
				"name": "beta", "source": "local",
				"contentHash": hashOf(".codex", "beta"),
				"installedAt": "2026-04-07T00:09:56Z",
				"updatedAt":   "2026-04-07T00:09:56Z",
				"agents":      []string{"codex"},
				"method":      "copy",
				"global":      false,
			},
		},
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	legacyBytes := append(raw, '\n')
	if err := os.MkdirAll(filepath.Join(root, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agents", ".skill-lock.json"), legacyBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	return migrateFixture{root: root, legacyBytes: legacyBytes}
}

func writeCliSkillTree(t *testing.T, root, agentDir, name, version string, withMeta bool) {
	t.Helper()
	dir := filepath.Join(root, agentDir, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMd := fmt.Sprintf("---\nname: %s\ndescription: skill %s\nmetadata:\n  version: %q\n---\n\ncorpo\n", name, name, version)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMd), 0o644); err != nil {
		t.Fatal(err)
	}
	if withMeta {
		meta := fmt.Sprintf("{\n  \"name\": %q,\n  \"version\": %q,\n  \"installedAt\": \"2026-04-07T00:09:56Z\",\n  \"updatedAt\": \"2026-04-07T00:09:56Z\"\n}\n", name, version)
		if err := os.WriteFile(filepath.Join(dir, skill.LegacyMetaFilename), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// cliLegacyHash recomputes the legacy writer's algorithm independently
// (installer.py content_hash) for fixtures.
func cliLegacyHash(t *testing.T, dir string) string {
	t.Helper()
	var rels []string
	bodies := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if filepath.Base(rel) == ".skill-meta.json" {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rels = append(rels, filepath.ToSlash(rel))
		bodies[filepath.ToSlash(rel)] = body
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, rel := range rels {
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(bodies[rel])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fsSnapshot captures every path with mode + content hash, for the zero-write
// invariant of the read-only default.
func fsSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel) + " " + info.Mode().String()
		if d.IsDir() {
			snap[key] = "<dir>"
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		snap[key] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func runMigrate(t *testing.T, root string, apply bool) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := runSkillsMigrate(skillsMigrateOptions{
		manifestPath: filepath.Join(root, "orotools.yaml"),
		apply:        apply,
		out:          &out,
		errOut:       &errOut,
	})
	if errOut.Len() > 0 {
		t.Fatalf("stderr inesperado: %q", errOut.String())
	}
	return out.String(), err
}

func TestRunSkillsMigrateDefaultIsReadOnly(t *testing.T) {
	f := newMigrateFixture(t)
	// A local edit after the record: drift-modified, never a download.
	if err := os.WriteFile(filepath.Join(f.root, ".codex", "skills", "beta", "SKILL.md"), []byte("---\nname: beta\ndescription: editada\nmetadata:\n  version: \"0.9.1\"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := fsSnapshot(t, f.root)
	out, err := runMigrate(t, f.root, false)
	if err != nil {
		t.Fatalf("relatório deveria sair sem erro: %v", err)
	}
	after := fsSnapshot(t, f.root)
	if len(before) != len(after) {
		t.Fatalf("arquivos mudaram no modo somente leitura: %d -> %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("conteúdo mudou no modo somente leitura: %s", k)
		}
	}
	for _, want := range []string{"[migratable]", "[drift-modified]", "orotools-tree-sha256-v1", "somente leitura"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output sem %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, ".opencode/skills/alpha") || !strings.Contains(out, ".claude/skills/alpha") {
		t.Fatalf("alvos por agent ausentes:\n%s", out)
	}
}

func TestRunSkillsMigrateApplyWritesCanonicalLock(t *testing.T) {
	f := newMigrateFixture(t)
	out, err := runMigrate(t, f.root, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !strings.Contains(out, "3 entrada(s) nova(s)") {
		t.Fatalf("output sem resumo do apply:\n%s", out)
	}
	lock, err := skill.ReadLock(filepath.Join(f.root, skill.LockDir, skill.LockFilename))
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Skills) != 3 {
		t.Fatalf("lock = %+v", lock.Skills)
	}
	for _, id := range []string{"local:unknown/alpha", "local:unknown/beta"} {
		if _, ok := lock.Lookup(id); !ok {
			t.Fatalf("entrada %s ausente: %+v", id, lock.Skills)
		}
	}
	after, err := os.ReadFile(filepath.Join(f.root, ".agents", ".skill-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(f.legacyBytes) {
		t.Fatal("arquivo legado foi alterado pelo apply")
	}
}

func TestRunSkillsMigrateApplyConflictFailsAndPreservesCanonical(t *testing.T) {
	f := newMigrateFixture(t)
	// The canonical lock owns beta's target under a different id: disputed.
	lock := skill.NewLock()
	lock.Upsert(skill.LockEntry{
		ID: "github:willsantos/skills_AI/beta", Name: "beta", Source: "github",
		SourceRef: "willsantos/skills_AI", Path: "beta", Version: "0.9.1",
		Target:        ".codex/skills/beta",
		ContentSHA256: mustDigestOf(t, filepath.Join(f.root, ".codex", "skills", "beta")),
	})
	lockPath := filepath.Join(f.root, skill.LockDir, skill.LockFilename)
	if err := lock.Save(lockPath); err != nil {
		t.Fatal(err)
	}
	lockBefore, _ := os.ReadFile(lockPath)

	out, err := runMigrate(t, f.root, true)
	if err == nil {
		t.Fatalf("apply com conflito deveria falhar (exit != 0):\n%s", out)
	}
	if !strings.Contains(out, "[conflict]") {
		t.Fatalf("relatório deveria marcar a disputa:\n%s", out)
	}
	if !strings.Contains(err.Error(), "nada foi escrito") {
		t.Fatalf("erro = %v", err)
	}
	lockAfter, _ := os.ReadFile(lockPath)
	if string(lockBefore) != string(lockAfter) {
		t.Fatal("lock canônico foi alterado num apply com conflito")
	}
	if _, err := os.Stat(filepath.Join(f.root, skill.LockDir, skill.LegacyMigrationMarkerRel)); err == nil {
		t.Fatal("marcador não deveria existir num apply abortado")
	}
}

func TestRunSkillsMigrateReportsAlreadyMigrated(t *testing.T) {
	f := newMigrateFixture(t)
	if _, err := runMigrate(t, f.root, true); err != nil {
		t.Fatal(err)
	}
	out, err := runMigrate(t, f.root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "já aplicada") || !strings.Contains(out, skill.LegacyMigrationMarkerRel) {
		t.Fatalf("output:\n%s", out)
	}
}

func TestRunSkillsMigrateNoLegacyIsCleanSuccess(t *testing.T) {
	root := t.TempDir()
	out, err := runMigrate(t, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nenhum lock legado") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestSkillsMigrateAndRecoverCommandsAreRegistered(t *testing.T) {
	f := newMigrateFixture(t)
	root := newRoot("dev")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"skills", "migrate", "--manifest", filepath.Join(f.root, "orotools.yaml")})
	if err := root.Execute(); err != nil {
		t.Fatalf("migrate via cobra: %v", err)
	}
	if !strings.Contains(stdout.String(), "Relatório de migração") {
		t.Fatalf("stdout:\n%s", stdout.String())
	}
	if stderr.Len() > 0 {
		t.Fatalf("stderr: %q", stderr.String())
	}

	root2 := newRoot("dev")
	var stdout2, stderr2 bytes.Buffer
	root2.SetOut(&stdout2)
	root2.SetErr(&stderr2)
	root2.SetArgs([]string{"skills", "recover", "--manifest", filepath.Join(f.root, "orotools.yaml")})
	if err := root2.Execute(); err != nil {
		t.Fatalf("recover via cobra: %v", err)
	}
	if !strings.Contains(stdout2.String(), "Inspeção de recuperação") {
		t.Fatalf("stdout:\n%s", stdout2.String())
	}
}
