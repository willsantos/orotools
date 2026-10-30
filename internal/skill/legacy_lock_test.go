package skill_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/skill"
)

// legacyJSON mounts a legacy lock document with one entry per spec. The shape
// mirrors the nine real lock files: version 2 and the fixed field set.
type legacySpec struct {
	name    string
	source  string
	hash    string
	agents  []string
	method  string
	global  *bool
	dropped bool // omit the field entirely (e.g. name-less entries)
}

func (s legacySpec) entry(t *testing.T) map[string]any {
	t.Helper()
	e := map[string]any{}
	if !s.dropped {
		e["name"] = s.name
	}
	if s.source != "" {
		e["source"] = s.source
	}
	if s.hash != "" {
		e["contentHash"] = s.hash
	}
	if s.agents != nil {
		e["agents"] = s.agents
	}
	if s.method != "" {
		e["method"] = s.method
	}
	if s.global != nil {
		e["global"] = *s.global
	}
	return e
}

func legacyDoc(t *testing.T, version any, specs []legacySpec) []byte {
	t.Helper()
	skills := map[string]any{}
	for _, s := range specs {
		skills[s.name] = s.entry(t)
	}
	doc := map[string]any{"version": version, "skills": skills}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func globalTrue() *bool { b := true; return &b }

func randomHex(t *testing.T, seed string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// writeLegacySkill writes an installed skill tree: SKILL.md with a strict
// version, one reference file and (optionally) the legacy bookkeeping file.
func writeLegacySkill(t *testing.T, root, agentDir, name, version string, withMeta bool) string {
	t.Helper()
	dir := filepath.Join(root, agentDir, "skills", name)
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	skillMd := fmt.Sprintf("---\nname: %s\ndescription: skill %s\nmetadata:\n  version: %q\n---\n\ncorpo\n", name, name, version)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMd), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "guide.md"), []byte("# guia "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if withMeta {
		meta := fmt.Sprintf("{\n  \"name\": %q,\n  \"version\": %q,\n  \"installedAt\": \"2026-04-07T00:09:56Z\",\n  \"updatedAt\": \"2026-04-07T00:09:56Z\"\n}\n", name, version)
		if err := os.WriteFile(filepath.Join(dir, skill.LegacyMetaFilename), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// legacyHashOf recomputes the legacy writer's algorithm INDEPENDENTLY of the
// implementation under test (installer.py content_hash): sha256 over sorted
// "relpath NUL bytes NUL" pairs of regular files, skipping any file named
// .skill-meta.json.
func legacyHashOf(t *testing.T, dir string) string {
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
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("fixture escalation: %q não é regular", p)
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

// fixtureProject mounts a legacy project and returns its root.
type fixtureProject struct {
	root string
}

func newFixtureProject(t *testing.T) fixtureProject {
	t.Helper()
	return fixtureProject{root: t.TempDir()}
}

func (f fixtureProject) install(t *testing.T, name, version, agentDir string, withMeta bool) {
	t.Helper()
	writeLegacySkill(t, f.root, agentDir, name, version, withMeta)
}

func (f fixtureProject) writeLock(t *testing.T, specs []legacySpec) {
	t.Helper()
	path := filepath.Join(f.root, ".agents", ".skill-lock.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacyDoc(t, 2, specs), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f fixtureProject) legacyPath() string {
	return filepath.Join(f.root, ".agents", ".skill-lock.json")
}

func (f fixtureProject) inspect(t *testing.T) *skill.LegacyMigrationReport {
	t.Helper()
	report, err := skill.InspectLegacyLocks(f.root)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	return report
}

func findingFor(t *testing.T, report *skill.LegacyMigrationReport, name, agent string) skill.LegacyFinding {
	t.Helper()
	for _, f := range report.Findings {
		if f.Name == name && f.Agent == agent {
			return f
		}
	}
	t.Fatalf("finding %s@%s não encontrado em %+v", name, agent, report.Findings)
	return skill.LegacyFinding{}
}

func TestParseLegacyLockValidAndInvalid(t *testing.T) {
	tests := []struct {
		name    string
		version any
		specs   []legacySpec
		raw     string // when set, replaces the generated document
		wantErr string
	}{
		{
			name:    "valid minimal contract",
			version: 2,
			specs:   []legacySpec{{name: "alpha", source: "local", hash: randomHex(t, "a"), agents: []string{"opencode"}, method: "copy"}},
		},
		{
			name:    "symlink method and github-copilot agent are valid",
			version: 2,
			specs:   []legacySpec{{name: "beta", source: "oroborus-mcp", hash: randomHex(t, "b"), agents: []string{"github-copilot", "kiro"}, method: "symlink"}},
		},
		{
			name:    "invalid JSON",
			version: 2,
			specs:   nil,
			raw:     "{not json",
			wantErr: "JSON inválido",
		},
		{
			name:    "unknown version 1",
			version: 1,
			specs:   []legacySpec{{name: "alpha", source: "local", hash: randomHex(t, "a"), agents: []string{"opencode"}, method: "copy"}},
			wantErr: "versão 1 não suportada",
		},
		{
			name:    "unknown version 3",
			version: 3,
			specs:   []legacySpec{{name: "alpha", source: "local", hash: randomHex(t, "a"), agents: []string{"opencode"}, method: "copy"}},
			wantErr: "versão 3 não suportada",
		},
		{
			name:    "missing skills field",
			version: 2,
			specs:   nil,
			raw:     `{"version": 2}`,
			wantErr: `"skills" ausente`,
		},
		{
			name:    "name differs from key",
			version: 2,
			specs:   []legacySpec{{name: "other", source: "local", hash: randomHex(t, "a"), agents: []string{"opencode"}, method: "copy"}},
			raw:     `{"version": 2, "skills": {"alpha": {"name": "other", "source": "local", "contentHash": "` + randomHex(t, "a") + `", "agents": ["opencode"], "method": "copy"}}}`,
			wantErr: "difere da chave",
		},
		{
			name:    "missing name field",
			version: 2,
			specs:   []legacySpec{{name: "alpha", dropped: true, source: "local", hash: randomHex(t, "a"), agents: []string{"opencode"}, method: "copy"}},
			wantErr: "campo name ausente",
		},
		{
			name:    "missing source",
			version: 2,
			specs:   []legacySpec{{name: "alpha", source: "", hash: randomHex(t, "a"), agents: []string{"opencode"}, method: "copy"}},
			wantErr: "source ausente",
		},
		{
			name:    "short contentHash",
			version: 2,
			specs:   []legacySpec{{name: "alpha", source: "local", hash: "abc123", agents: []string{"opencode"}, method: "copy"}},
			wantErr: "sha256 hexadecimal de 64 caracteres",
		},
		{
			name:    "non-hex contentHash",
			version: 2,
			specs:   []legacySpec{{name: "alpha", source: "local", hash: strings.Repeat("z", 64), agents: []string{"opencode"}, method: "copy"}},
			wantErr: "sha256 hexadecimal de 64 caracteres",
		},
		{
			name:    "trailing data after the JSON document",
			version: 2,
			specs:   []legacySpec{{name: "alpha", source: "local", hash: randomHex(t, "a"), agents: []string{"opencode"}, method: "copy"}},
			raw:     "{\"version\": 2, \"skills\": {\"alpha\": {\"name\": \"alpha\", \"source\": \"local\", \"contentHash\": \"" + randomHex(t, "a") + "\", \"agents\": [\"opencode\"], \"method\": \"copy\"}}}\nGARBAGE\n",
			wantErr: "dados após o objeto JSON",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := tt.raw
			if raw == "" {
				raw = string(legacyDoc(t, tt.version, tt.specs))
			}
			f := newFixtureProject(t)
			if err := os.MkdirAll(filepath.Join(f.root, ".agents"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.legacyPath(), []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			report := f.inspect(t)
			if tt.wantErr == "" {
				if len(report.LegacyProblems) != 0 {
					t.Fatalf("unexpected problems: %v", report.LegacyProblems)
				}
				return
			}
			if len(report.LegacyProblems) == 0 {
				t.Fatalf("expected problem %q, got none", tt.wantErr)
			}
			if !strings.Contains(report.LegacyProblems[0], tt.wantErr) {
				t.Fatalf("problem %q does not contain %q", report.LegacyProblems[0], tt.wantErr)
			}
			if blocked := report.ApplyBlocked(); blocked == "" {
				t.Fatal("unreadable legacy file must block apply")
			}
		})
	}
}

func TestParseLegacyLockToleratesUnknownFields(t *testing.T) {
	f := newFixtureProject(t)
	f.install(t, "alpha", "1.0.0", ".opencode", false)
	if err := os.MkdirAll(filepath.Join(f.root, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"version": 2, "writer": "python-installer", "skills": {"alpha": {"name": "alpha", "source": "local", "contentHash": "` + randomHex(t, "a") + `", "installedAt": "2026-04-07T00:09:56Z", "updatedAt": "2026-04-07T00:09:56Z", "agents": ["opencode"], "method": "copy", "global": false, "extra": 42}}}`
	if err := os.WriteFile(f.legacyPath(), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	report := f.inspect(t)
	if len(report.LegacyProblems) != 0 {
		t.Fatalf("unknown fields must be tolerated: %v", report.LegacyProblems)
	}
	if len(report.Findings) != 1 || report.Findings[0].Decision != skill.LegacyDriftModified {
		// hash aleatório no fixture: drift-modified é a decisão esperada
		t.Fatalf("findings = %+v", report.Findings)
	}
}

func TestInspectLegacyLocksDecisionMatrix(t *testing.T) {
	oppHash := randomHex(t, "opponent")
	tests := []struct {
		name     string
		mount    func(t *testing.T, f fixtureProject)
		target   string // "<name>@<agent>" to inspect
		decision skill.LegacyMigrationDecision
		detail   string
	}{
		{
			name: "migratable with matching legacy hash",
			mount: func(t *testing.T, f fixtureProject) {
				f.install(t, "alpha", "1.0.0", ".opencode", true)
				f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"}})
			},
			target:   "alpha@opencode",
			decision: skill.LegacyMigratable,
		},
		{
			name: "drift-modified after byte change",
			mount: func(t *testing.T, f fixtureProject) {
				f.install(t, "alpha", "1.0.0", ".opencode", true)
				f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"}})
				// local edit AFTER the record
				if err := os.WriteFile(filepath.Join(f.root, ".opencode/skills/alpha/references/guide.md"), []byte("# editado\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			target:   "alpha@opencode",
			decision: skill.LegacyDriftModified,
			detail:   "bytes do alvo divergem",
		},
		{
			name: "missing target",
			mount: func(t *testing.T, f fixtureProject) {
				f.writeLock(t, []legacySpec{{name: "ghost", source: "local", hash: randomHex(t, "g"), agents: []string{"opencode"}, method: "copy"}})
			},
			target:   "ghost@opencode",
			decision: skill.LegacyMissing,
		},
		{
			name: "symlinked target is unsafe",
			mount: func(t *testing.T, f fixtureProject) {
				outside := filepath.Join(t.TempDir(), "outside-skill")
				if err := os.MkdirAll(outside, 0o755); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(f.root, ".opencode", "skills", "linked")
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, target); err != nil {
					t.Fatal(err)
				}
				f.writeLock(t, []legacySpec{{name: "linked", source: "local", hash: randomHex(t, "l"), agents: []string{"opencode"}, method: "symlink"}})
			},
			target:   "linked@opencode",
			decision: skill.LegacyUnsafe,
			detail:   "symlink",
		},
		{
			name: "traversal name is unsafe",
			mount: func(t *testing.T, f fixtureProject) {
				f.writeLock(t, []legacySpec{{name: "../escape", source: "local", hash: randomHex(t, "e"), agents: []string{"opencode"}, method: "copy"}})
			},
			target:   "../escape@",
			decision: skill.LegacyUnsafe,
			detail:   "segmento de caminho simples",
		},
		{
			name: "global scope is unmappable",
			mount: func(t *testing.T, f fixtureProject) {
				f.install(t, "alpha", "1.0.0", ".opencode", false)
				f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: randomHex(t, "a"), agents: []string{"opencode"}, method: "copy", global: globalTrue()}})
			},
			target:   "alpha@",
			decision: skill.LegacyUnmappable,
			detail:   "global=true",
		},
		{
			name: "unknown legacy agent is unmappable",
			mount: func(t *testing.T, f fixtureProject) {
				f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: randomHex(t, "a"), agents: []string{"windsurf"}, method: "copy"}})
			},
			target:   "alpha@",
			decision: skill.LegacyUnmappable,
			detail:   "windsurf",
		},
		{
			name: "unsupported method is unmappable per entry",
			mount: func(t *testing.T, f fixtureProject) {
				f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: randomHex(t, "a"), agents: []string{"opencode"}, method: "hardlink"}})
			},
			target:   "alpha@",
			decision: skill.LegacyUnmappable,
			detail:   "hardlink",
		},
		{
			name: "empty agents list is unmappable per entry",
			mount: func(t *testing.T, f fixtureProject) {
				f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: randomHex(t, "a"), agents: []string{}, method: "copy"}})
			},
			target:   "alpha@",
			decision: skill.LegacyUnmappable,
			detail:   "agents vazia",
		},
		{
			name: "unversioned target without recoverable version",
			mount: func(t *testing.T, f fixtureProject) {
				dir := filepath.Join(f.root, ".opencode", "skills", "raw")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: raw\ndescription: sem versão\n---\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				f.writeLock(t, []legacySpec{{name: "raw", source: "local", hash: randomHex(t, "r"), agents: []string{"opencode"}, method: "copy"}})
			},
			target:   "raw@opencode",
			decision: skill.LegacyUnversioned,
			detail:   "nenhuma versão",
		},
		{
			name: "non-strict version in meta falls back and stays unversioned",
			mount: func(t *testing.T, f fixtureProject) {
				dir := filepath.Join(f.root, ".codex", "skills", "loose")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: loose\ndescription: d\nmetadata:\n  version: \"1.0\"\n---\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				meta := "{\n  \"version\": \"v2.0.0\"\n}\n"
				if err := os.WriteFile(filepath.Join(dir, skill.LegacyMetaFilename), []byte(meta), 0o644); err != nil {
					t.Fatal(err)
				}
				f.writeLock(t, []legacySpec{{name: "loose", source: "local", hash: randomHex(t, "lo"), agents: []string{"codex"}, method: "copy"}})
			},
			target:   "loose@codex",
			decision: skill.LegacyUnversioned,
		},
		{
			name: "conflict with canonical lock owning target under another id",
			mount: func(t *testing.T, f fixtureProject) {
				f.install(t, "alpha", "1.0.0", ".opencode", false)
				f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"}})
				lock := skill.NewLock()
				lock.Upsert(skill.LockEntry{
					ID: "github:willsantos/skills_AI/alpha", Name: "alpha", Source: "github",
					SourceRef: "willsantos/skills_AI", Path: "alpha", Version: "1.0.0",
					Target:        ".opencode/skills/alpha",
					ContentSHA256: mustDigest(t, filepath.Join(f.root, ".opencode/skills/alpha")),
				})
				if err := lock.Save(filepath.Join(f.root, skill.LockDir, skill.LockFilename)); err != nil {
					t.Fatal(err)
				}
			},
			target:   "alpha@opencode",
			decision: skill.LegacyConflict,
			detail:   "github:willsantos/skills_AI/alpha",
		},
		{
			name: "conflict with canonical lock recording different content",
			mount: func(t *testing.T, f fixtureProject) {
				f.install(t, "alpha", "1.0.0", ".opencode", false)
				f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"}})
				lock := skill.NewLock()
				lock.Upsert(skill.LockEntry{
					ID: "local:unknown/alpha", Name: "alpha", Source: "local",
					SourceRef: "unknown", Path: "alpha", Version: "1.0.0",
					Target:        ".opencode/skills/alpha",
					ContentSHA256: "sha256:" + oppHash,
				})
				if err := lock.Save(filepath.Join(f.root, skill.LockDir, skill.LockFilename)); err != nil {
					t.Fatal(err)
				}
			},
			target:   "alpha@opencode",
			decision: skill.LegacyConflict,
			detail:   "outro conteúdo",
		},
		{
			name: "already-present with identical canonical entry",
			mount: func(t *testing.T, f fixtureProject) {
				f.install(t, "alpha", "1.0.0", ".opencode", true)
				f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"}})
				lock := skill.NewLock()
				lock.Upsert(skill.LockEntry{
					ID: "local:unknown/alpha", Name: "alpha", Source: "local",
					SourceRef: "unknown", Path: "alpha", Version: "1.0.0",
					Target:          ".opencode/skills/alpha",
					ContentSHA256:   mustDigest(t, filepath.Join(f.root, ".opencode/skills/alpha")),
					DigestAlgorithm: skill.DigestAlgorithm,
				})
				if err := lock.Save(filepath.Join(f.root, skill.LockDir, skill.LockFilename)); err != nil {
					t.Fatal(err)
				}
			},
			target:   "alpha@opencode",
			decision: skill.LegacyAlreadyPresent,
			detail:   "idêntica",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixtureProject(t)
			tt.mount(t, f)
			report := f.inspect(t)
			name, agent, _ := strings.Cut(tt.target, "@")
			got := findingFor(t, report, name, agent)
			if got.Decision != tt.decision {
				t.Fatalf("decision = %s (%s), want %s", got.Decision, got.Detail, tt.decision)
			}
			if tt.detail != "" && !strings.Contains(got.Detail, tt.detail) {
				t.Fatalf("detail %q does not contain %q", got.Detail, tt.detail)
			}
			// Invariants that hold for every row of the matrix.
			if got.Origin != "unknown" && got.Origin != "github" && got.Origin != "recipe" {
				t.Fatalf("origin %q inesperada", got.Origin)
			}
			if got.RecordedAlgorithm == "" {
				t.Fatalf("algoritmo legado não rotulado: %+v", got)
			}
			if got.CanonicalDigest != "" && got.CanonicalAlgorithm != skill.DigestAlgorithm {
				t.Fatalf("digest canônico %s sem rótulo do algoritmo (%q)", got.CanonicalDigest, got.CanonicalAlgorithm)
			}
		})
	}
}

func TestInspectLegacyLocksDigestAlgorithmsNeverEquated(t *testing.T) {
	f := newFixtureProject(t)
	f.install(t, "alpha", "1.2.3", ".opencode", true)
	f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"}})
	report := f.inspect(t)
	got := findingFor(t, report, "alpha", "opencode")
	if got.Decision != skill.LegacyMigratable {
		t.Fatalf("decision = %s (%s)", got.Decision, got.Detail)
	}
	if got.LegacyHashVerified != true {
		t.Fatal("fixture should verify against the legacy algorithm")
	}
	if !strings.HasPrefix(got.CanonicalDigest, "sha256:") {
		t.Fatalf("canonical digest %q sem prefixo", got.CanonicalDigest)
	}
	// The two algorithms hash different byte streams: the legacy one is a bare
	// hex sha256 over relpath+bytes pairs; the canonical one includes exec bit
	// and length. Same tree, different values — never comparable.
	if got.RecomputedHash == strings.TrimPrefix(got.CanonicalDigest, "sha256:") {
		t.Fatal("algoritmos distintos produziram o mesmo valor; a comparação estaria viciada")
	}
}

func TestInspectLegacyLocksCanonicalDigestIncludesLegacyMeta(t *testing.T) {
	// The migrated entry must be verifiable by the engine, whose drift check
	// digests every byte on disk — including the legacy .skill-meta.json.
	f := newFixtureProject(t)
	f.install(t, "alpha", "1.0.0", ".opencode", true)
	f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"}})
	report := f.inspect(t)
	got := findingFor(t, report, "alpha", "opencode")
	want := mustDigest(t, filepath.Join(f.root, ".opencode/skills/alpha"))
	if got.CanonicalDigest != want {
		t.Fatalf("canonical digest = %s, want plain engine digest %s", got.CanonicalDigest, want)
	}
}

func TestInspectLegacyLocksReportIsDeterministic(t *testing.T) {
	f := newFixtureProject(t)
	f.install(t, "alpha", "1.0.0", ".opencode", true)
	f.install(t, "alpha", "1.0.0", ".claude", true)
	f.install(t, "beta", "0.9.1", ".codex", false)
	f.install(t, "gamma", "2.0.0", ".zcode", false)
	f.writeLock(t, []legacySpec{
		{name: "gamma", source: "local", hash: randomHex(t, "g"), agents: []string{"zcode"}, method: "copy"},
		{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"claude-code", "opencode"}, method: "copy"},
		{name: "beta", source: "local", hash: randomHex(t, "b"), agents: []string{"codex"}, method: "copy"},
		{name: "ghost", source: "local", hash: randomHex(t, "gh"), agents: []string{"cursor"}, method: "copy"},
	})
	first := f.inspect(t)
	second := f.inspect(t)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("duas inspeções do mesmo estado produziram relatórios diferentes")
	}
	// Findings are ordered by target, then name.
	var targets []string
	for _, fd := range first.Findings {
		targets = append(targets, fd.Target)
	}
	if !sort.StringsAreSorted(targets) {
		t.Fatalf("findings fora de ordem: %v", targets)
	}
	if first.MigratableCount() != 4 { // alpha×2 migratable + beta e gamma drift-modified (não-bloqueantes)
		t.Fatalf("migratable = %d, want 4", first.MigratableCount())
	}
	if first.HasConflicts() {
		t.Fatal("projeto sem disputas reportou conflito")
	}
	if first.Canonical != "absent" {
		t.Fatalf("canonical = %q", first.Canonical)
	}
}

func TestInspectLegacyLocksRecognizesAndRefusesLegacyYAMLNames(t *testing.T) {
	// Verdict of the M02 investigation: no legacy YAML lock exists in the wild
	// (YAML v1 is only the canonical target schema). Any file at a plausible
	// legacy YAML name is recognized and REFUSED, never parsed.
	for _, candidate := range []string{
		filepath.Join(skill.LockDir, "skills.yaml"),
		filepath.Join(".agents", "skills.yaml"),
	} {
		f := newFixtureProject(t)
		f.install(t, "alpha", "1.0.0", ".opencode", false)
		f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: randomHex(t, "a"), agents: []string{"opencode"}, method: "copy"}})
		path := filepath.Join(f.root, candidate)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("version: 2\nskills: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		report := f.inspect(t)
		found := false
		for _, blocked := range report.BlockedPaths {
			if blocked == filepath.ToSlash(candidate) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s não reportado em BlockedPaths: %+v", candidate, report.BlockedPaths)
		}
		if blocked := report.ApplyBlocked(); blocked == "" {
			t.Fatalf("%s deveria bloquear --apply", candidate)
		}
		// The read-only report still renders; nothing was parsed as a lock.
		if len(report.Findings) != 1 {
			t.Fatalf("findings = %+v", report.Findings)
		}
	}
}

func TestInspectLegacyLocksEmptyProject(t *testing.T) {
	f := newFixtureProject(t)
	report := f.inspect(t)
	if len(report.LegacyFiles) != 0 || len(report.Findings) != 0 || report.AlreadyMigrated {
		t.Fatalf("report = %+v", report)
	}
	if report.Canonical != "absent" {
		t.Fatalf("canonical = %q", report.Canonical)
	}
}

func TestInspectLegacyLocksUnreadableLegacyFileBlocksApply(t *testing.T) {
	f := newFixtureProject(t)
	if err := os.MkdirAll(filepath.Join(f.root, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.legacyPath(), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := f.inspect(t)
	if len(report.LegacyProblems) == 0 {
		t.Fatal("JSON raiz inválido deveria virar problema")
	}
}

func TestInspectLegacyLocksInvalidCanonicalLockIsReported(t *testing.T) {
	f := newFixtureProject(t)
	f.install(t, "alpha", "1.0.0", ".opencode", false)
	f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: randomHex(t, "a"), agents: []string{"opencode"}, method: "copy"}})
	if err := os.MkdirAll(filepath.Join(f.root, skill.LockDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, skill.LockDir, skill.LockFilename), []byte("version: 9\nskills: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := f.inspect(t)
	if !strings.HasPrefix(report.Canonical, "invalid") {
		t.Fatalf("canonical = %q, want invalid", report.Canonical)
	}
	if blocked := report.ApplyBlocked(); blocked == "" {
		t.Fatal("lock canônico inválido deveria bloquear apply")
	}
}

func mustDigest(t *testing.T, dir string) string {
	t.Helper()
	parent := filepath.Dir(dir)
	d, err := skill.Digest(os.DirFS(parent), filepath.Base(dir))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestApplyLegacyMigrationHappyPath(t *testing.T) {
	f := newFixtureProject(t)
	f.install(t, "alpha", "1.0.0", ".opencode", true)
	f.install(t, "alpha", "1.0.0", ".claude", true)
	f.install(t, "beta", "0.9.1", ".codex", false)
	legacyBytes := legacyDoc(t, 2, []legacySpec{
		{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"claude-code", "opencode"}, method: "copy"},
		{name: "beta", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".codex/skills/beta")), agents: []string{"codex"}, method: "copy"},
	})
	f.writeLock(t, nil)
	if err := os.WriteFile(f.legacyPath(), legacyBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := skill.ApplyLegacyMigration(f.root, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.NoOp || result.Written != 3 || len(result.Skipped) != 0 {
		t.Fatalf("result = %+v", result)
	}

	lock, err := skill.ReadLock(filepath.Join(f.root, skill.LockDir, skill.LockFilename))
	if err != nil {
		t.Fatal(err)
	}
	if lock.Version != 1 {
		t.Fatalf("lock version = %d", lock.Version)
	}
	for _, want := range []struct{ id, target string }{
		{"local:unknown/alpha", ".opencode/skills/alpha"},
		{"local:unknown/alpha", ".claude/skills/alpha"},
		{"local:unknown/beta", ".codex/skills/beta"},
	} {
		entry, ok := lock.LookupTarget(want.id, want.target)
		if !ok {
			t.Fatalf("entrada %s em %s ausente: %+v", want.id, want.target, lock.Skills)
		}
		if entry.Source != "local" || entry.SourceRef != "unknown" || entry.DigestAlgorithm != skill.DigestAlgorithm {
			t.Fatalf("entrada = %+v", entry)
		}
		if entry.Version == "" {
			t.Fatalf("entrada %s sem versão", want.id)
		}
		dirAbs := filepath.Join(f.root, filepath.FromSlash(want.target))
		if entry.ContentSHA256 != mustDigest(t, dirAbs) {
			t.Fatalf("digest de %s não confere com os bytes do disco", want.target)
		}
	}

	// Legacy files are preserved byte-identical...
	after, err := os.ReadFile(f.legacyPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(legacyBytes) {
		t.Fatal("arquivo legado foi alterado")
	}
	// ...and backed up before the new lock was written.
	backup, err := os.ReadFile(filepath.Join(f.root, skill.LockDir, skill.LegacyBackupDirRel, ".agents", ".skill-lock.json"))
	if err != nil {
		t.Fatalf("backup ausente: %v", err)
	}
	if string(backup) != string(legacyBytes) {
		t.Fatal("backup difere do arquivo legado")
	}

	// Marker written after the lock and recording the lock digest.
	rawMarker, err := os.ReadFile(filepath.Join(f.root, skill.LockDir, skill.LegacyMigrationMarkerRel))
	if err != nil {
		t.Fatalf("marcador ausente: %v", err)
	}
	var marker skill.LegacyMigrationMarker
	if err := json.Unmarshal(rawMarker, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.Version != 1 || len(marker.EntryIDs) != 2 {
		t.Fatalf("marker = %+v", marker)
	}
	lockBytes, _ := os.ReadFile(filepath.Join(f.root, skill.LockDir, skill.LockFilename))
	sum := sha256.Sum256(lockBytes)
	if marker.LockSHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatal("marker.LockSHA256 não corresponde ao lock gravado")
	}
	if !reflect.DeepEqual(marker, result.Marker) {
		t.Fatalf("marker retornado = %+v, no disco = %+v", result.Marker, marker)
	}
}

func TestApplyLegacyMigrationIsIdempotent(t *testing.T) {
	f := newFixtureProject(t)
	f.install(t, "alpha", "1.0.0", ".opencode", true)
	f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"}})

	if _, err := skill.ApplyLegacyMigration(f.root, nil); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(f.root, skill.LockDir, skill.LockFilename)
	before, _ := os.ReadFile(lockPath)
	markerBefore, _ := os.ReadFile(filepath.Join(f.root, skill.LockDir, skill.LegacyMigrationMarkerRel))

	second, err := skill.ApplyLegacyMigration(f.root, nil)
	if err != nil {
		t.Fatalf("segunda aplicação deve ser no-op com sucesso: %v", err)
	}
	if !second.NoOp || second.Written != 0 {
		t.Fatalf("segunda aplicação = %+v", second)
	}
	after, _ := os.ReadFile(lockPath)
	if string(before) != string(after) {
		t.Fatal("lock mudou na re-aplicação")
	}
	markerAfter, _ := os.ReadFile(filepath.Join(f.root, skill.LockDir, skill.LegacyMigrationMarkerRel))
	if string(markerBefore) != string(markerAfter) {
		t.Fatal("marcador mudou na re-aplicação")
	}
}

func TestApplyLegacyMigrationWithoutMarkerStillIdempotent(t *testing.T) {
	// Marker deleted by the operator: re-apply must converge via
	// already-present decisions, not duplicate or error.
	f := newFixtureProject(t)
	f.install(t, "alpha", "1.0.0", ".opencode", true)
	f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"}})
	if _, err := skill.ApplyLegacyMigration(f.root, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.root, skill.LockDir, skill.LegacyMigrationMarkerRel)); err != nil {
		t.Fatal(err)
	}
	lockBefore, _ := os.ReadFile(filepath.Join(f.root, skill.LockDir, skill.LockFilename))

	result, err := skill.ApplyLegacyMigration(f.root, nil)
	if err != nil {
		t.Fatalf("re-apply sem marcador: %v", err)
	}
	if result.NoOp || result.Written != 0 || result.AlreadyPresent != 1 {
		t.Fatalf("result = %+v", result)
	}
	lockAfter, _ := os.ReadFile(filepath.Join(f.root, skill.LockDir, skill.LockFilename))
	if string(lockBefore) != string(lockAfter) {
		t.Fatal("lock mudou na re-convergência")
	}
	if _, err := os.Stat(filepath.Join(f.root, skill.LockDir, skill.LegacyMigrationMarkerRel)); err != nil {
		t.Fatal("marcador não foi regravado")
	}
}

func TestApplyLegacyMigrationConflictAbortsWritingNothing(t *testing.T) {
	f := newFixtureProject(t)
	f.install(t, "disputed", "1.0.0", ".opencode", false)
	f.install(t, "clean", "2.0.0", ".opencode", false)
	f.writeLock(t, []legacySpec{
		{name: "disputed", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/disputed")), agents: []string{"opencode"}, method: "copy"},
		{name: "clean", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/clean")), agents: []string{"opencode"}, method: "copy"},
	})
	// The canonical lock owns the disputed target under a different id.
	lock := skill.NewLock()
	lock.Upsert(skill.LockEntry{
		ID: "github:willsantos/skills_AI/disputed", Name: "disputed", Source: "github",
		SourceRef: "willsantos/skills_AI", Path: "disputed", Version: "1.0.0",
		Target:        ".opencode/skills/disputed",
		ContentSHA256: mustDigest(t, filepath.Join(f.root, ".opencode/skills/disputed")),
	})
	lockPath := filepath.Join(f.root, skill.LockDir, skill.LockFilename)
	if err := lock.Save(lockPath); err != nil {
		t.Fatal(err)
	}
	lockBefore, _ := os.ReadFile(lockPath)
	legacyBefore, _ := os.ReadFile(f.legacyPath())

	result, err := skill.ApplyLegacyMigration(f.root, nil)
	if err == nil {
		t.Fatalf("conflito deveria abortar: %+v", result)
	}
	if !strings.Contains(err.Error(), "nada foi escrito") || !strings.Contains(err.Error(), "disputed") {
		t.Fatalf("erro = %v", err)
	}
	lockAfter, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(lockBefore) != string(lockAfter) {
		t.Fatal("lock canônico foi alterado num apply abortado")
	}
	legacyAfter, _ := os.ReadFile(f.legacyPath())
	if string(legacyBefore) != string(legacyAfter) {
		t.Fatal("arquivo legado foi alterado num apply abortado")
	}
	// Atomicity: no backups, no marker — nothing new under .orotools beyond
	// the pre-existing lock.
	entries, _ := os.ReadDir(filepath.Join(f.root, skill.LockDir))
	for _, e := range entries {
		switch e.Name() {
		case skill.LockFilename, ".skills-operation.lock":
		default:
			t.Fatalf("apply abortado escreveu %s", e.Name())
		}
	}
}

func TestApplyLegacyMigrationPartialSkipsAndStaysRetryable(t *testing.T) {
	f := newFixtureProject(t)
	f.install(t, "alpha", "1.0.0", ".opencode", false)
	// beta is claimed but its target is gone; gamma has no recoverable version.
	rawDir := filepath.Join(f.root, ".claude", "skills", "gamma")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rawDir, "SKILL.md"), []byte("---\nname: gamma\ndescription: sem versão\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.writeLock(t, []legacySpec{
		{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"},
		{name: "beta", source: "local", hash: randomHex(t, "b"), agents: []string{"codex"}, method: "copy"},
		{name: "gamma", source: "local", hash: legacyHashOf(t, rawDir), agents: []string{"claude-code"}, method: "copy"},
	})

	result, err := skill.ApplyLegacyMigration(f.root, nil)
	if err == nil {
		t.Fatalf("conversão parcial deveria sinalizar erro: %+v", result)
	}
	if result.Written != 1 || len(result.Skipped) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(f.root, skill.LockDir, skill.LegacyMigrationMarkerRel)); err == nil {
		t.Fatal("apply parcial não deve gravar marcador: a pendência precisaria de nova execução")
	}

	// The operator fixes the pending claims and re-applies: alpha becomes
	// already-present, the fixed entries migrate, conversion completes.
	writeLegacySkill(t, f.root, ".codex", "beta", "1.0.0", false)
	writeLegacySkill(t, f.root, ".claude", "gamma", "3.0.0", false)

	retry, err := skill.ApplyLegacyMigration(f.root, nil)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retry.Written != 2 || retry.AlreadyPresent != 1 || len(retry.Skipped) != 0 {
		t.Fatalf("retry = %+v", retry)
	}
	if _, err := os.Stat(filepath.Join(f.root, skill.LockDir, skill.LegacyMigrationMarkerRel)); err != nil {
		t.Fatal("conversão completa deve gravar o marcador")
	}
}

func TestApplyLegacyMigrationNoLegacyIsNoOp(t *testing.T) {
	f := newFixtureProject(t)
	result, err := skill.ApplyLegacyMigration(f.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoOp {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(f.root, skill.LockDir)); err == nil {
		t.Fatal("nenhum arquivo deveria ter sido criado")
	}
}

func TestApplyLegacyMigrationAlreadyMigratedIsNoOpSuccess(t *testing.T) {
	f := newFixtureProject(t)
	f.install(t, "alpha", "1.0.0", ".opencode", false)
	f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"}})
	if _, err := skill.ApplyLegacyMigration(f.root, nil); err != nil {
		t.Fatal(err)
	}
	lockBefore, _ := os.ReadFile(filepath.Join(f.root, skill.LockDir, skill.LockFilename))

	result, err := skill.ApplyLegacyMigration(f.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NoOp || result.Marker.LockSHA256 == "" {
		t.Fatalf("result = %+v", result)
	}
	lockAfter, _ := os.ReadFile(filepath.Join(f.root, skill.LockDir, skill.LockFilename))
	if string(lockBefore) != string(lockAfter) {
		t.Fatal("lock mudou num no-op")
	}
}

func TestApplyLegacyMigrationRefusesStalePriorReport(t *testing.T) {
	f := newFixtureProject(t)
	f.install(t, "alpha", "1.0.0", ".opencode", false)
	f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"}})
	prior := f.inspect(t)
	// State changes after the read-only report.
	if err := os.RemoveAll(filepath.Join(f.root, ".opencode", "skills", "alpha")); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.ApplyLegacyMigration(f.root, prior); err == nil {
		t.Fatal("apply deveria recusar relatório desatualizado (TOCTOU)")
	} else if !strings.Contains(err.Error(), "estado do projeto mudou") {
		t.Fatalf("erro = %v", err)
	}
}

func TestApplyLegacyMigrationFailClosedOnPendingTransaction(t *testing.T) {
	f := newFixtureProject(t)
	f.install(t, "alpha", "1.0.0", ".opencode", false)
	f.writeLock(t, []legacySpec{{name: "alpha", source: "local", hash: legacyHashOf(t, filepath.Join(f.root, ".opencode/skills/alpha")), agents: []string{"opencode"}, method: "copy"}})
	if err := os.MkdirAll(filepath.Join(f.root, skill.LockDir), 0o755); err != nil {
		t.Fatal(err)
	}
	journal := `{"target":".opencode/skills/alpha","hadManifest":false,"hadLock":false}`
	if err := os.WriteFile(filepath.Join(f.root, skill.LockDir, ".skills-transaction.json"), []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
	lockBefore, _ := os.ReadFile(f.legacyPath())
	if _, err := skill.ApplyLegacyMigration(f.root, nil); err == nil {
		t.Fatal("apply deveria falhar com transação pendente")
	} else if !strings.Contains(err.Error(), "oro skills recover") {
		t.Fatalf("erro = %v", err)
	}
	lockAfter, _ := os.ReadFile(f.legacyPath())
	if string(lockBefore) != string(lockAfter) {
		t.Fatal("nada deveria ser escrito com transação pendente")
	}
}
