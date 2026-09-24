package skill_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/skill"
)

func sampleLock() *skill.Lock {
	lock := skill.NewLock()
	lock.Upsert(skill.LockEntry{
		ID:            "recipe:dotnet/base/code-review",
		Name:          "code-review",
		Source:        "recipe",
		SourceRef:     "dotnet",
		Path:          "base/code-review",
		Version:       "1.0.0",
		Target:        ".opencode/skills/code-review",
		ContentSHA256: "sha256:aaaa",
	})
	lock.Upsert(skill.LockEntry{
		ID:            "github:willsantos/skills_AI/local-pr-review",
		Name:          "local-pr-review",
		Source:        "github",
		SourceRef:     "willsantos/skills_AI",
		Path:          "local-pr-review",
		Version:       "1.1.0",
		Revision:      "abc123",
		Target:        ".opencode/skills/local-pr-review",
		ContentSHA256: "sha256:bbbb",
	})
	return lock
}

func TestLockRoundTripByteIdentical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, skill.LockDir, skill.LockFilename)
	if err := sampleLock().Save(path); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := skill.ReadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Save(path); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatalf("lock round-trip not byte-identical:\n%s\nvs\n%s", first, second)
	}
	// Records are sorted by ID in the file.
	a := strings.Index(string(second), "github:willsantos")
	b := strings.Index(string(second), "recipe:dotnet")
	if a == -1 || b == -1 || a > b {
		t.Errorf("records not sorted by id:\n%s", second)
	}
}

func TestLockSaveCreatesDirAndNoTempLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, skill.LockDir, skill.LockFilename)
	if err := sampleLock().Save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("lock mode = %v, want 0644", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestLockUpsertAndLookup(t *testing.T) {
	lock := skill.NewLock()
	entry := skill.LockEntry{ID: "recipe:r/a", Name: "a", Source: "recipe", SourceRef: "r", Path: "a", Version: "1.0.0", Target: ".opencode/skills/a", ContentSHA256: "sha256:x"}
	lock.Upsert(entry)
	got, ok := lock.Lookup("recipe:r/a")
	if !ok || got != entry {
		t.Fatalf("Lookup = %+v, %v", got, ok)
	}
	entry.Version = "2.0.0"
	lock.Upsert(entry)
	if len(lock.Skills) != 1 || lock.Skills[0].Version != "2.0.0" {
		t.Fatalf("Upsert did not replace: %+v", lock.Skills)
	}
}

func TestLockUpsertKeepsSortedByID(t *testing.T) {
	lock := skill.NewLock()
	mk := func(id, name string) skill.LockEntry {
		return skill.LockEntry{ID: id, Name: name, Source: "recipe", SourceRef: "r", Path: name, Version: "1.0.0", Target: ".opencode/skills/" + name, ContentSHA256: "sha256:x"}
	}
	lock.Upsert(mk("recipe:r/zeta", "zeta"))
	lock.Upsert(mk("recipe:r/alpha", "alpha"))
	lock.Upsert(mk("github:willsantos/skills_AI/mid", "mid"))
	for i := 1; i < len(lock.Skills); i++ {
		if lock.Skills[i-1].ID >= lock.Skills[i].ID {
			t.Fatalf("not sorted: %v", lock.Skills)
		}
	}
}

func TestLockReadMissing(t *testing.T) {
	_, err := skill.ReadLock(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil || !strings.Contains(err.Error(), skill.ErrLockNotFound.Error()) {
		t.Fatalf("err = %v, want ErrLockNotFound", err)
	}
}

func TestParseLockStrict(t *testing.T) {
	valid := `version: 1
skills:
    - id: recipe:r/a
      name: a
      source: recipe
      source_ref: r
      path: a
      version: 1.0.0
      target: .opencode/skills/a
      content_sha256: sha256:aa
`
	if _, err := skill.ParseLock([]byte(valid)); err != nil {
		t.Fatalf("valid lock rejected: %v", err)
	}

	cases := map[string]string{
		"unknown field":    strings.Replace(valid, "content_sha256: sha256:aa", "content_sha256: sha256:aa\n      extra: x", 1),
		"duplicate id":     valid + "    - " + strings.Repeat("", 0) + dupEntry(),
		"bad version":      strings.Replace(valid, "version: 1", "version: 2", 1),
		"missing target":   strings.Replace(valid, "      target: .opencode/skills/a\n", "", 1),
		"absolute target":  strings.Replace(valid, "target: .opencode/skills/a", "target: /abs/skills/a", 1),
		"traversal target": strings.Replace(valid, "target: .opencode/skills/a", "target: ../skills/a", 1),
		"bad semver":       strings.Replace(valid, "version: 1.0.0\n", "version: 1.0\n", 1),
	}
	for label, src := range cases {
		t.Run(label, func(t *testing.T) {
			if _, err := skill.ParseLock([]byte(src)); err == nil {
				t.Fatalf("expected error for %s", label)
			}
		})
	}
}

func dupEntry() string {
	return `id: recipe:r/a
      name: a
      source: recipe
      source_ref: r
      path: a
      version: 1.0.0
      target: .opencode/skills/a
      content_sha256: sha256:bb
`
}

// Multi-agent (skills-multi-agent FR-11/FR-12): a mesma skill em agents
// distintos são entradas distintas; o par (id, target) é que precisa ser único.
func TestLockMultiAgentSameIDDistinctTargets(t *testing.T) {
	lock := skill.NewLock()
	mk := func(target string) skill.LockEntry {
		return skill.LockEntry{ID: "github:willsantos/skills_AI/a", Name: "a", Source: "github",
			SourceRef: "willsantos/skills_AI", Path: "a", Version: "1.0.0",
			Target: target, ContentSHA256: "sha256:x"}
	}
	lock.Upsert(mk(".codex/skills/a"))
	lock.Upsert(mk(".opencode/skills/a"))
	if len(lock.Skills) != 2 {
		t.Fatalf("entries = %d, want 2 (uma por agent)", len(lock.Skills))
	}
	// Ordenação por (id, target): .codex antes de .opencode.
	if lock.Skills[0].Target != ".codex/skills/a" || lock.Skills[1].Target != ".opencode/skills/a" {
		t.Errorf("ordem por (id, target) incorreta: %+v", lock.Skills)
	}
	// Upsert substitui apenas o par correspondente.
	upd := mk(".codex/skills/a")
	upd.Version = "2.0.0"
	lock.Upsert(upd)
	if len(lock.Skills) != 2 {
		t.Fatalf("upsert multi-agent criou entrada: %+v", lock.Skills)
	}
	if e, _ := lock.LookupTarget("github:willsantos/skills_AI/a", ".codex/skills/a"); e.Version != "2.0.0" {
		t.Errorf("LookupTarget .codex = %+v", e)
	}
	if e, ok := lock.LookupTarget("github:willsantos/skills_AI/a", ".cursor/skills/a"); ok {
		t.Errorf("LookupTarget de target inexistente devolveu %+v", e)
	}

	// Round-trip preserva as duas entradas.
	dir := t.TempDir()
	path := filepath.Join(dir, skill.LockDir, skill.LockFilename)
	if err := lock.Save(path); err != nil {
		t.Fatal(err)
	}
	reloaded, err := skill.ReadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Skills) != 2 {
		t.Fatalf("lock recarregado = %d entradas, want 2", len(reloaded.Skills))
	}
}

func TestParseLockRejectsDuplicateIDTargetPair(t *testing.T) {
	valid := `version: 1
skills:
    - id: recipe:r/a
      name: a
      source: recipe
      source_ref: r
      path: a
      version: 1.0.0
      target: .opencode/skills/a
      content_sha256: sha256:aa
`
	if _, err := skill.ParseLock([]byte(valid + "    - " + dupEntry())); err == nil {
		t.Fatal("par id/target duplicado deveria ser rejeitado")
	}
	// Mesmo id em target distinto é válido.
	other := `    - id: recipe:r/a
      name: a
      source: recipe
      source_ref: r
      path: a
      version: 1.0.0
      target: .codex/skills/a
      content_sha256: sha256:bb
`
	if _, err := skill.ParseLock([]byte(valid + other)); err != nil {
		t.Fatalf("mesmo id em targets distintos deveria ser aceito: %v", err)
	}
}

func TestAgentFromSkillsDir(t *testing.T) {
	for _, agent := range skill.AllAgents {
		dir, err := skill.SkillsDir(agent)
		if err != nil {
			t.Fatal(err)
		}
		got, err := skill.AgentFromSkillsDir(dir)
		if err != nil || got != agent {
			t.Errorf("AgentFromSkillsDir(%q) = %q, %v; want %q", dir, got, err, agent)
		}
	}
	if _, err := skill.AgentFromSkillsDir(".windsurf/skills"); err == nil {
		t.Error("diretório desconhecido deveria falhar")
	}
}
