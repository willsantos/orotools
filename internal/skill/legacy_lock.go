package skill

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

// Legacy JSON v2 lock contract, recovered from the pre-adapter OroRag
// installer (git a7a9c2b..c67e32e, src/oroborus/skills/installer.py, hash
// algorithm in src/oroborus/skills/discovery.py content_hash) and confirmed
// against nine real lock files in authoring/consumer repositories
// (codeReviewer, design-system, OroRag, OroHarness, Skills, orodocker,
// OroCore, OroTools, oroSkills — all version 2, all with exactly the fields
// below: name, source ("local" in every real file; the installer also wrote
// "oroborus-mcp"), contentHash, installedAt, updatedAt, agents, method
// ("copy", plus "symlink" in OroCore), global=false).
//
// Legacy YAML v1 verdict (M02 investigation, 2026-10-29): NO legacy YAML lock
// exists in the wild. A tree-wide search over the authoring/consumer
// repositories found zero skills.yaml / .orotools/skills.yaml files; YAML v1
// is exclusively the CANONICAL target schema (.orotools/skills.lock.yaml).
// Files at plausible legacy YAML names are recognized and refused
// (report.BlockedPaths) instead of inventing a schema.
//
// Location: <project>/.agents/.skill-lock.json
//
//	{
//	  "version": 2,
//	  "skills": {
//	    "<name>": {
//	      "name": "<name>", "source": "local" | "oroborus-mcp",
//	      "contentHash": "<64 hex chars>",
//	      "installedAt": "<RFC3339>", "updatedAt": "<RFC3339>",
//	      "agents": ["zcode", "claude-code", ...],
//	      "method": "copy" | "symlink", "global": false
//	    }
//	  }
//	}
//
// Entries carry no explicit target: the writer installed each skill at
// <agent-dir>/skills/<name> for every agent listed. The recorded contentHash
// is sha256 over sorted "relpath NUL bytes NUL" pairs, ignoring
// .skill-meta.json — a DIFFERENT algorithm from DigestAlgorithm, never
// compared as equivalent by this migration. (Empirical note: across all nine
// real projects, 0 of 576 installed targets recompute to their recorded
// contentHash — sources evolve after install and some downloader-era writers
// hashed trees that carried a committed .skill-meta.json. Honest drift
// reporting is therefore the expected outcome for real projects, never an
// authorization to replace or download content.)
const (
	LegacyLockRelPath  = ".agents/.skill-lock.json"
	LegacyLockVersion  = 2
	LegacyMetaFilename = ".skill-meta.json"

	// legacyHashAlgorithm is a descriptive label for the legacy writer's
	// implicit hash algorithm (no name existed in the JSON itself).
	legacyHashAlgorithm = "legacy-sha256-sorted-relpath-bytes-v2"

	// LegacyBackupDirRel is where copies of legacy lock files are kept after
	// an apply, relative to .orotools/.
	LegacyBackupDirRel = "legacy-backup"

	// LegacyMigrationMarkerRel is the migration state marker, relative to
	// .orotools/.
	LegacyMigrationMarkerRel = ".legacy-lock-migration.json"
)

// legacyAgentDirs maps the legacy writer's agent ids to the project-relative
// directory that holds that agent's skills (installer.py AGENT_DIRS).
var legacyAgentDirs = map[string]string{
	"zcode":          ".zcode",
	"claude-code":    ".claude",
	"opencode":       ".opencode",
	"cursor":         ".cursor",
	"codex":          ".codex",
	"gemini":         ".gemini",
	"antigravity":    ".antigravity",
	"trae":           ".trae",
	"kiro":           ".kiro",
	"github-copilot": ".github",
}

// legacyAgentOrder keeps reports deterministic (installer.py canonical order).
var legacyAgentOrder = []string{
	"zcode", "claude-code", "opencode", "cursor", "codex", "gemini",
	"antigravity", "trae", "kiro", "github-copilot",
}

// LegacyMigrationDecision classifies one legacy claim (name × agent target).
type LegacyMigrationDecision string

const (
	// LegacyMigratable: target exists, digest recomputed from bytes.
	LegacyMigratable LegacyMigrationDecision = "migratable"
	// LegacyDriftModified: content differs from the legacy record — a local
	// modification, never an authorization to replace or download. Still
	// migratable: migration only converts bookkeeping.
	LegacyDriftModified LegacyMigrationDecision = "drift-modified"
	// LegacyAlreadyPresent: the canonical lock already holds an identical
	// entry for this target; skipped, not rewritten.
	LegacyAlreadyPresent LegacyMigrationDecision = "already-present"
	// LegacyConflict: the same target is disputed (two legacy claims, or the
	// canonical lock already owns the target with different content or id).
	LegacyConflict LegacyMigrationDecision = "conflict"
	// LegacyMissing: target directory absent — reported, no entry fabricated.
	LegacyMissing LegacyMigrationDecision = "missing"
	// LegacyUnsafe: target escapes the project, or a symlinked target,
	// ancestor or inner entry violates engine path conventions.
	LegacyUnsafe LegacyMigrationDecision = "unsafe"
	// LegacyUnversioned: no strict-SemVer version could be recovered from the
	// installed records; the canonical lock requires one and none is invented.
	LegacyUnversioned LegacyMigrationDecision = "unversioned"
	// LegacyUnmappable: entry shape out of the migrated contract (unknown
	// agent id, unsupported method, global scope).
	LegacyUnmappable LegacyMigrationDecision = "unmappable"
)

// blocking classifies decisions that must not produce a lock entry.
func (d LegacyMigrationDecision) blocking() bool {
	switch d {
	case LegacyMigratable, LegacyDriftModified, LegacyAlreadyPresent:
		return false
	}
	return true
}

// LegacyFinding is the report row for one legacy claim.
type LegacyFinding struct {
	LegacyPath         string // project-relative lock file
	Name               string // legacy entry name
	Agent              string // legacy agent id
	Target             string // project-relative destination directory
	ClaimedSource      string // recorded "source" field, as-is
	Origin             string // verifiable origin or "unknown"
	Version            string // resolved strict SemVer (empty when unresolved)
	RecordedHash       string // legacy contentHash, as recorded
	RecordedAlgorithm  string // descriptive label of the legacy algorithm
	RecomputedHash     string // same legacy algorithm recomputed from disk bytes
	LegacyHashVerified bool   // recomputed equals recorded (no drift detected)
	CanonicalDigest    string // orotools-tree-sha256-v1 over actual bytes
	CanonicalAlgorithm string // always DigestAlgorithm
	Decision           LegacyMigrationDecision
	Detail             string
}

// LegacyMigrationReport is the read-only migration survey of one project.
type LegacyMigrationReport struct {
	ProjectRoot     string
	LegacyFiles     []string // project-relative legacy lock files found
	LegacyProblems  []string // unreadable/malformed legacy files (blocking)
	BlockedPaths    []string // defensive recognize-and-refuse hits
	Canonical       string   // "absent" | "valid" | "invalid: <reason>"
	Findings        []LegacyFinding
	AlreadyMigrated bool // marker present: migration already applied here
}

// MigratableCount counts findings whose decision allows a lock entry.
func (r *LegacyMigrationReport) MigratableCount() int {
	n := 0
	for _, f := range r.Findings {
		if !f.Decision.blocking() {
			n++
		}
	}
	return n
}

// ConflictCount counts disputed targets.
func (r *LegacyMigrationReport) ConflictCount() int {
	n := 0
	for _, f := range r.Findings {
		if f.Decision == LegacyConflict {
			n++
		}
	}
	return n
}

// HasConflicts reports whether any target is disputed.
func (r *LegacyMigrationReport) HasConflicts() bool { return r.ConflictCount() > 0 }

// ApplyBlocked explains why --apply must refuse, or "" when allowed.
func (r *LegacyMigrationReport) ApplyBlocked() string {
	switch {
	case r.AlreadyMigrated:
		return ""
	case len(r.LegacyFiles) == 0:
		return ""
	case len(r.LegacyProblems) > 0:
		return "arquivo(s) legado(s) ilegível(is): " + strings.Join(r.LegacyProblems, "; ")
	case len(r.BlockedPaths) > 0:
		return "formato legado não reconhecido (recusado por segurança): " + strings.Join(r.BlockedPaths, "; ")
	case strings.HasPrefix(r.Canonical, "invalid"):
		return "lock canônico inválido: " + r.Canonical
	}
	return ""
}

type legacyEntryJSON struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	ContentHash string   `json:"contentHash"`
	InstalledAt string   `json:"installedAt"`
	UpdatedAt   string   `json:"updatedAt"`
	Agents      []string `json:"agents"`
	Method      string   `json:"method"`
	Global      *bool    `json:"global"`
}

type legacyLockJSON struct {
	Version int                        `json:"version"`
	Skills  map[string]legacyEntryJSON `json:"skills"`
}

// InspectLegacyLocks surveys projectRoot for legacy lock state without
// touching anything. It never downloads, never writes, never deletes: the
// only filesystem operations are reads.
func InspectLegacyLocks(projectRoot string) (*LegacyMigrationReport, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	report := &LegacyMigrationReport{ProjectRoot: absRoot}

	if _, err := os.Lstat(filepath.Join(absRoot, LockDir, LegacyMigrationMarkerRel)); err == nil {
		report.AlreadyMigrated = true
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect migration marker: %w", err)
	}

	// Canonical lock status.
	lockAbs, lockErr := safeJoin(absRoot, filepath.ToSlash(filepath.Join(LockDir, LockFilename)))
	if lockErr != nil {
		return nil, fmt.Errorf("canonical lock path: %w", lockErr)
	}
	if _, statErr := os.Lstat(lockAbs); os.IsNotExist(statErr) {
		report.Canonical = "absent"
	} else if statErr != nil {
		return nil, fmt.Errorf("inspect canonical lock: %w", statErr)
	} else if _, err := ReadLock(lockAbs); err != nil {
		report.Canonical = fmt.Sprintf("invalid: %v", err)
	} else {
		report.Canonical = "valid"
	}

	// Defensive recognize-and-refuse: plausible legacy YAML names. No legacy
	// YAML writer was ever found in the wild (only JSON v2), so any YAML file
	// at these names is refused with a clear error instead of inventing a
	// schema.
	for _, candidate := range []string{
		filepath.ToSlash(filepath.Join(LockDir, "skills.yaml")),
		filepath.ToSlash(filepath.Join(".agents", "skills.yaml")),
	} {
		p, err := safeJoin(absRoot, candidate)
		if err != nil {
			continue // unreachable under the safety rules; nothing to refuse
		}
		if _, statErr := os.Lstat(p); statErr == nil {
			report.BlockedPaths = append(report.BlockedPaths, candidate)
		}
	}

	// The one legacy lock shape that exists in reality.
	legacyAbs, legacyErr := safeJoin(absRoot, LegacyLockRelPath)
	if legacyErr != nil {
		if _, statErr := os.Lstat(filepath.Join(absRoot, filepath.FromSlash(LegacyLockRelPath))); statErr == nil {
			report.LegacyProblems = append(report.LegacyProblems,
				fmt.Sprintf("%s: local inseguro (symlink ou escape): %v", LegacyLockRelPath, legacyErr))
		}
		return report, nil
	}
	if _, statErr := os.Lstat(legacyAbs); os.IsNotExist(statErr) {
		return report, nil
	} else if statErr != nil {
		return nil, fmt.Errorf("inspect legacy lock: %w", statErr)
	}
	report.LegacyFiles = append(report.LegacyFiles, LegacyLockRelPath)

	raw, err := os.ReadFile(legacyAbs)
	if err != nil {
		return nil, fmt.Errorf("read legacy lock: %w", err)
	}
	entries, parseErr := parseLegacyLock(raw)
	if parseErr != nil {
		report.LegacyProblems = append(report.LegacyProblems, fmt.Sprintf("%s: %v", LegacyLockRelPath, parseErr))
		return report, nil
	}

	var canonical *Lock
	if report.Canonical == "valid" {
		canonical, err = ReadLock(lockAbs)
		if err != nil { // re-read; state verified above
			return nil, err
		}
	}

	findings := make([]LegacyFinding, 0, len(entries))
	targetClaims := make(map[string][]string) // target -> legacy claims "name@agent"
	for _, name := range sortedLegacyNames(entries) {
		entry := entries[name]
		// A traversal name would aim the derived target outside the agent
		// skills dir — classified unsafe (security signal), never mapped.
		if !cleanSegment(name) {
			findings = append(findings, LegacyFinding{
				LegacyPath: LegacyLockRelPath, Name: name, ClaimedSource: entry.Source,
				Origin: legacyOrigin(entry.Source), RecordedHash: entry.ContentHash,
				RecordedAlgorithm: legacyHashAlgorithm, Decision: LegacyUnsafe,
				Detail: fmt.Sprintf("nome %q não é um segmento de caminho simples (escape recusado)", name),
			})
			continue
		}
		targets, unmappableReason := legacyEntryTargets(entry)
		if len(targets) == 0 {
			findings = append(findings, LegacyFinding{
				LegacyPath: LegacyLockRelPath, Name: name, ClaimedSource: entry.Source,
				Origin: legacyOrigin(entry.Source), RecordedHash: entry.ContentHash,
				RecordedAlgorithm: legacyHashAlgorithm, Decision: LegacyUnmappable,
				Detail: unmappableReason,
			})
			continue
		}
		for _, agent := range legacyAgentOrder {
			target, ok := targets[agent]
			if !ok {
				continue
			}
			targetClaims[target] = append(targetClaims[target], name+"@"+agent)
			findings = append(findings, inspectLegacyTarget(absRoot, LegacyLockRelPath, name, agent, target, entry, canonical))
		}
	}
	// Same target disputed by two or more legacy claims → all of them conflict.
	for i := range findings {
		f := &findings[i]
		if len(targetClaims[f.Target]) > 1 && f.Decision != LegacyUnmappable {
			f.Decision = LegacyConflict
			f.Detail = fmt.Sprintf("alvo disputado por: %s", strings.Join(targetClaims[f.Target], ", "))
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.LegacyPath < b.LegacyPath
	})
	report.Findings = findings
	return report, nil
}

func parseLegacyLock(raw []byte) (map[string]legacyEntryJSON, error) {
	var parsed legacyLockJSON
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("JSON inválido: %w", err)
	}
	// Fail closed on anything after the JSON document: the legacy writers
	// emitted exactly one object; trailing bytes mean a corrupt or tampered
	// file, not a parseable lock.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("dados após o objeto JSON (arquivo corrompido)")
	}
	if parsed.Version != LegacyLockVersion {
		return nil, fmt.Errorf("versão %d não suportada: a migração conhece apenas o formato JSON v2 (version: %d)", parsed.Version, LegacyLockVersion)
	}
	if parsed.Skills == nil {
		return nil, errors.New("campo \"skills\" ausente")
	}
	for name, entry := range parsed.Skills {
		display := entry.Name
		if display == "" {
			display = name
		}
		switch {
		case entry.Name == "":
			return nil, fmt.Errorf("entrada %q: campo name ausente", name)
		case display != name:
			return nil, fmt.Errorf("entrada %q: campo name %q difere da chave", name, display)
		case entry.Source == "":
			return nil, fmt.Errorf("entrada %q: source ausente", name)
		case len(entry.ContentHash) != 64 || !isHex(entry.ContentHash):
			return nil, fmt.Errorf("entrada %q: contentHash não é um sha256 hexadecimal de 64 caracteres", name)
		}
		// method e agents são validados por entrada (unmappable), não aqui:
		// um método ou agente desconhecido não invalida o arquivo inteiro,
		// apenas aquela reivindicação (matriz de decisões do M02).
	}
	return parsed.Skills, nil
}

func isHex(s string) bool {
	for _, r := range strings.ToLower(s) {
		isDigit := r >= '0' && r <= '9'
		isHexLetter := r >= 'a' && r <= 'f'
		if !isDigit && !isHexLetter {
			return false
		}
	}
	return true
}

func sortedLegacyNames(entries map[string]legacyEntryJSON) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// legacyEntryTargets derives the project-relative target per legacy agent.
// A non-empty unmappable reason means the entry cannot be mapped at all.
func legacyEntryTargets(entry legacyEntryJSON) (map[string]string, string) {
	if entry.Global != nil && *entry.Global {
		return nil, "instalação global (global=true) está fora do escopo do projeto"
	}
	if entry.Name != "" && !cleanSegment(entry.Name) {
		return nil, fmt.Sprintf("nome %q não é um segmento de caminho simples", entry.Name)
	}
	switch {
	case entry.Method != "copy" && entry.Method != "symlink":
		return nil, fmt.Sprintf("method %q não suportado (contrato legado: copy | symlink)", entry.Method)
	case len(entry.Agents) == 0:
		return nil, "lista agents vazia"
	}
	targets := make(map[string]string, len(entry.Agents))
	for _, agent := range entry.Agents {
		dir, ok := legacyAgentDirs[agent]
		if !ok {
			return nil, fmt.Sprintf("agent legado %q desconhecido", agent)
		}
		targets[agent] = path.Join(dir, "skills", entry.Name)
	}
	return targets, ""
}

// legacyOrigin applies the plan rule: never assume a public origin for an
// install performed from an internal checkout or via MCP. Both values the
// legacy writer ever recorded ("local", "oroborus-mcp") are unverifiable.
func legacyOrigin(claimed string) string {
	switch claimed {
	case "github", "recipe":
		return claimed
	default:
		return "unknown"
	}
}

// inspectLegacyTarget classifies one (name, agent, target) claim read-only.
func inspectLegacyTarget(base, legacyPath, name, agent, target string, entry legacyEntryJSON, canonical *Lock) LegacyFinding {
	f := LegacyFinding{
		LegacyPath:         legacyPath,
		Name:               name,
		Agent:              agent,
		Target:             target,
		ClaimedSource:      entry.Source,
		Origin:             legacyOrigin(entry.Source),
		RecordedHash:       entry.ContentHash,
		RecordedAlgorithm:  legacyHashAlgorithm,
		CanonicalAlgorithm: DigestAlgorithm,
	}
	targetAbs, err := safeJoin(base, target)
	if err != nil {
		f.Decision = LegacyUnsafe
		f.Detail = err.Error()
		return f
	}
	info, err := os.Lstat(targetAbs)
	if os.IsNotExist(err) {
		f.Decision = LegacyMissing
		f.Detail = "diretório alvo ausente no disco"
		return f
	}
	if err != nil {
		f.Decision = LegacyUnsafe
		f.Detail = err.Error()
		return f
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		f.Decision = LegacyUnsafe
		f.Detail = "alvo não é um diretório real (symlink rejeitado)"
		return f
	}
	// Canonical digest under the engine's own contract (no filtering): every
	// consumer — update drift check, install plan, install Already path —
	// recomputes Digest over the raw target bytes, so the stored value must be
	// reproducible from disk bytes alone, legacy .skill-meta.json included.
	digest, err := Digest(os.DirFS(targetAbs), ".")
	if err != nil {
		f.Decision = LegacyUnsafe
		f.Detail = fmt.Sprintf("conteúdo do alvo viola as convenções do motor: %v", err)
		return f
	}
	f.CanonicalDigest = digest

	version, versionErr := resolveLegacyVersion(targetAbs)
	if versionErr != nil {
		f.Decision = LegacyUnversioned
		f.Detail = versionErr.Error()
		return f
	}
	f.Version = version

	// Recompute the legacy hash with the legacy writer's own algorithm to
	// detect drift since the install. The two algorithms are never compared
	// to each other.
	recomputed, err := legacyContentHash(targetAbs)
	if err != nil {
		f.Decision = LegacyUnsafe
		f.Detail = fmt.Sprintf("hash legado não recomputável: %v", err)
		return f
	}
	f.RecomputedHash = recomputed
	f.LegacyHashVerified = strings.EqualFold(recomputed, entry.ContentHash)

	// Canonical lock ownership decides between migration and conflict.
	entryID := legacyEntryID(name)
	if canonical != nil {
		if existing, ok := canonical.LookupTarget(entryID, target); ok {
			if existing.ContentSHA256 == digest {
				f.Decision = LegacyAlreadyPresent
				f.Detail = "lock canônico já contém entrada idêntica"
			} else {
				f.Decision = LegacyConflict
				f.Detail = "lock canônico já registra o mesmo alvo com outro conteúdo"
			}
			return f
		}
		for _, owned := range canonical.Skills {
			if owned.Target == target {
				f.Decision = LegacyConflict
				f.Detail = fmt.Sprintf("lock canônico já registra o alvo para %q", owned.ID)
				return f
			}
		}
	}

	f.Decision = LegacyMigratable
	if !f.LegacyHashVerified {
		f.Decision = LegacyDriftModified
		f.Detail = "bytes do alvo divergem do registro legado (modificação local; nenhum download é feito)"
	}
	return f
}

// legacyEntryID maps a legacy skill name to a canonical lock id. The recorded
// source is never a verifiable public origin, so the id is honest about the
// unknown origin (LocalID with the "unknown" sourceRef).
func legacyEntryID(name string) string {
	return LocalID("unknown", name)
}

// legacyLockEntry builds the canonical record for a migratable finding.
func legacyLockEntry(f LegacyFinding) LockEntry {
	return LockEntry{
		ID:              legacyEntryID(f.Name),
		Name:            f.Name,
		Source:          string(SourceLocal),
		SourceRef:       "unknown",
		Path:            f.Name,
		Version:         f.Version,
		Target:          f.Target,
		ContentSHA256:   f.CanonicalDigest,
		DigestAlgorithm: DigestAlgorithm,
	}
}

// resolveLegacyVersion recovers a strict SemVer version from the installed
// records without inventing one: first the writer's own .skill-meta.json,
// then the SKILL.md frontmatter contract.
func resolveLegacyVersion(targetAbs string) (string, error) {
	if raw, err := os.ReadFile(filepath.Join(targetAbs, LegacyMetaFilename)); err == nil {
		var meta struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(raw, &meta) == nil && meta.Version != "" {
			if v, err := parseStrictSemver(meta.Version); err == nil {
				return v, nil
			}
		}
	}
	if file, err := os.Open(filepath.Join(targetAbs, "SKILL.md")); err == nil {
		defer file.Close()
		if meta, err := ParseMetadata(file); err == nil {
			return meta.Version.String(), nil
		}
	}
	return "", errors.New("nenhuma versão SemVer estrita recuperável (.skill-meta.json ou SKILL.md); nenhuma versão é inventada")
}

func parseStrictSemver(raw string) (string, error) {
	if strings.HasPrefix(raw, "v") {
		return "", fmt.Errorf("versão %q não pode ter prefixo \"v\"", raw)
	}
	v, err := semver.StrictNewVersion(raw)
	if err != nil {
		return "", fmt.Errorf("versão %q não é SemVer estrita: %w", raw, err)
	}
	return v.String(), nil
}

// legacyContentHash recomputes the legacy writer's hash: sha256 over sorted
// "relpath NUL bytes NUL" pairs of every regular file, ignoring any file named
// .skill-meta.json (installer.py content_hash). Symlinks and special files are
// an error, consistent with engine conventions.
func legacyContentHash(dirAbs string) (string, error) {
	h := sha256.New()
	var paths []string
	err := fs.WalkDir(os.DirFS(dirAbs), ".", func(p string, d fs.DirEntry, err error) error {
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
			return fmt.Errorf("arquivo %q não é regular", p)
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, p := range paths {
		if path.Base(p) == LegacyMetaFilename {
			continue
		}
		file, err := os.Open(filepath.Join(dirAbs, filepath.FromSlash(p)))
		if err != nil {
			return "", err
		}
		h.Write([]byte(p))
		h.Write([]byte{0})
		if _, err := io.Copy(h, file); err != nil {
			file.Close()
			return "", err
		}
		file.Close()
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// metaFilteredFS was removed on purpose: the canonical digest recorded in the
// lock must be reproducible by every engine consumer (update.go drift check,
// install_plan.go, managed.go) that recomputes Digest over the actual target
// bytes with no filtering. A migrated entry whose tree still carries a legacy
// .skill-meta.json therefore hashes those bytes too — the engine's own
// contract — instead of storing a digest the engine can never reproduce.

// LegacyMigrationMarker records a completed conversion. It is diagnostic state
// for operators and tooling: a legacy writer unaware of this marker is NOT
// technically blocked by it (documented limit — an old binary needs an
// operational or minimum-version block; no impossible promise is made here).
type LegacyMigrationMarker struct {
	Version      int      `json:"version"`
	MigratedFrom []string `json:"migratedFrom"`
	EntryIDs     []string `json:"entryIds"`
	LockSHA256   string   `json:"lockSha256"`
}

// LegacyApplyResult summarizes one --apply run.
type LegacyApplyResult struct {
	NoOp           bool
	Written        int
	AlreadyPresent int
	Skipped        []LegacyFinding
	LockPath       string
	Marker         LegacyMigrationMarker
}

// ApplyLegacyMigration converts migratable legacy claims into the canonical
// lock. Conflict-free claims are required; disputed targets abort without
// writing anything. Other blocking findings (missing/unsafe/unversioned/
// unmappable) are skipped: remaining migratable entries are still written and
// the caller receives an error flagging the incomplete conversion — no marker
// is recorded for a partial apply, so fixing the pending claims and re-running
// completes the conversion idempotently (written targets become
// already-present). Legacy files are preserved in place; byte copies land in
// .orotools/legacy-backup/. prior (optional) is a previous read-only report;
// when it disagrees with the under-lock re-inspection the apply refuses
// (TOCTOU guard).
func ApplyLegacyMigration(projectRoot string, prior *LegacyMigrationReport) (LegacyApplyResult, error) {
	result := LegacyApplyResult{}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return result, fmt.Errorf("resolve project root: %w", err)
	}
	report, err := InspectLegacyLocks(absRoot)
	if err != nil {
		return result, err
	}
	if report.AlreadyMigrated {
		result.NoOp = true
		result.Marker, err = readLegacyMarker(absRoot)
		if err != nil {
			return result, err
		}
		return result, nil
	}
	if len(report.LegacyFiles) == 0 {
		result.NoOp = true
		return result, nil
	}
	if blocked := report.ApplyBlocked(); blocked != "" {
		return result, errors.New("migração bloqueada: " + blocked)
	}
	if prior != nil {
		if err := requireSameFindings(prior, report); err != nil {
			return result, err
		}
	}

	projectLock, err := acquireUnlockedOperation(absRoot)
	if err != nil {
		return result, err
	}
	defer projectLock.Release()

	// Fail closed on a pending transaction: the explicit recovery path owns it.
	if err := (Service{Base: absRoot}).recoverTransaction(); err != nil {
		return result, fmt.Errorf("migração: %w (use `oro skills recover`)", err)
	}

	// Re-inspect under the lock and require the exact same decisions (TOCTOU
	// guard). From here on, every decision is taken from the under-lock
	// inspection and the canonical lock is read under the lock, so a concurrent
	// writer can neither change a verdict nor lose its lock entries.
	locked, err := InspectLegacyLocks(absRoot)
	if err != nil {
		return result, err
	}
	if err := requireSameFindings(report, locked); err != nil {
		return result, err
	}

	// One conflict anywhere aborts the whole conversion before any write.
	if locked.HasConflicts() {
		var details []string
		for _, f := range locked.Findings {
			if f.Decision == LegacyConflict {
				details = append(details, fmt.Sprintf("%s@%s: %s", f.Name, f.Target, f.Detail))
			}
		}
		return result, fmt.Errorf("migração abortada: %d alvo(s) disputado(s), nada foi escrito: %s",
			len(details), strings.Join(details, "; "))
	}

	migratable := make([]LegacyFinding, 0, len(locked.Findings))
	for _, f := range locked.Findings {
		if f.Decision.blocking() {
			result.Skipped = append(result.Skipped, f)
			continue
		}
		if f.Decision == LegacyAlreadyPresent {
			result.AlreadyPresent++
			continue
		}
		migratable = append(migratable, f)
	}

	// Backups first: byte copies of every legacy file, kept even after success
	// (legacy files themselves also stay in place).
	if err := backupLegacyFiles(absRoot, locked.LegacyFiles); err != nil {
		return result, err
	}

	lockAbs, err := safeJoin(absRoot, filepath.ToSlash(filepath.Join(LockDir, LockFilename)))
	if err != nil {
		return result, fmt.Errorf("canonical lock path: %w", err)
	}
	lock, err := readLockOrNew(lockAbs)
	if err != nil {
		return result, err
	}
	for _, f := range migratable {
		lock.Upsert(legacyLockEntry(f))
		result.Written++
	}
	if err := lock.Save(lockAbs); err != nil {
		return result, fmt.Errorf("migração: salvar lock canônico: %w", err)
	}
	result.LockPath = lockAbs

	// The marker is only recorded for a COMPLETE conversion. A partial apply
	// (skipped findings) deliberately stays unmarked so the operator can fix
	// the outstanding claims and re-apply: already-written targets then resolve
	// to already-present and the remainder migrates — still idempotent, and the
	// marker never lies that every claim is covered.
	if len(result.Skipped) == 0 {
		lockBytes, err := os.ReadFile(lockAbs)
		if err != nil {
			return result, fmt.Errorf("migração: ler lock canônico: %w", err)
		}
		sum := sha256.Sum256(lockBytes)
		idSet := make(map[string]bool, len(migratable))
		ids := make([]string, 0, len(migratable))
		for _, f := range migratable {
			id := legacyEntryID(f.Name)
			if !idSet[id] { // one id per skill even when installed at several agents
				idSet[id] = true
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		result.Marker = LegacyMigrationMarker{
			Version:      1,
			MigratedFrom: append([]string(nil), locked.LegacyFiles...),
			EntryIDs:     ids,
			LockSHA256:   "sha256:" + hex.EncodeToString(sum[:]),
		}
		if err := writeLegacyMarker(absRoot, result.Marker); err != nil {
			return result, fmt.Errorf("migração: gravar marcador: %w", err)
		}
	}
	if len(result.Skipped) > 0 {
		return result, fmt.Errorf("migração parcial: %d entrada(s) gravada(s), %d ignorada(s) (ausentes/inesperadas); resolva as pendências e execute --apply novamente",
			result.Written, len(result.Skipped))
	}
	return result, nil
}

func readLockOrNew(lockAbs string) (*Lock, error) {
	lock, err := ReadLock(lockAbs)
	if errors.Is(err, ErrLockNotFound) {
		return NewLock(), nil
	}
	return lock, err
}

// projectAcquireTimeout bounds how long the migration waits for the project
// lock; tests shrink it to keep mutual-exclusion cases fast.
var projectAcquireTimeout = 10 * time.Second

func acquireUnlockedOperation(projectRoot string) (*ProjectLock, error) {
	ctx, cancel := context.WithTimeout(context.Background(), projectAcquireTimeout)
	defer cancel()
	projectLock, err := AcquireProjectLock(ctx, projectRoot)
	if err != nil {
		return nil, fmt.Errorf("migração: project lock: %w", err)
	}
	return projectLock, nil
}

func requireSameFindings(a, b *LegacyMigrationReport) error {
	sameReport := a.AlreadyMigrated == b.AlreadyMigrated &&
		a.Canonical == b.Canonical &&
		strings.Join(a.LegacyFiles, "\x00") == strings.Join(b.LegacyFiles, "\x00") &&
		strings.Join(a.LegacyProblems, "\x00") == strings.Join(b.LegacyProblems, "\x00") &&
		strings.Join(a.BlockedPaths, "\x00") == strings.Join(b.BlockedPaths, "\x00") &&
		len(a.Findings) == len(b.Findings)
	if sameReport {
		for i := range a.Findings {
			if a.Findings[i] != b.Findings[i] {
				sameReport = false
				break
			}
		}
	}
	if !sameReport {
		return errors.New("estado do projeto mudou desde o relatório; execute o relatório novamente")
	}
	return nil
}

func backupLegacyFiles(absRoot string, legacyFiles []string) error {
	for _, rel := range legacyFiles {
		srcAbs, err := safeJoin(absRoot, rel)
		if err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		raw, err := os.ReadFile(srcAbs)
		if err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		backupRel := filepath.ToSlash(filepath.Join(LockDir, LegacyBackupDirRel, rel))
		backupAbs, err := safeJoin(absRoot, backupRel)
		if err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		if err := atomicFile(backupAbs, raw, 0o644); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
	}
	return nil
}

func readLegacyMarker(absRoot string) (LegacyMigrationMarker, error) {
	raw, err := os.ReadFile(filepath.Join(absRoot, LockDir, LegacyMigrationMarkerRel))
	if err != nil {
		return LegacyMigrationMarker{}, fmt.Errorf("ler marcador de migração: %w", err)
	}
	var marker LegacyMigrationMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return LegacyMigrationMarker{}, fmt.Errorf("marcador de migração inválido: %w", err)
	}
	return marker, nil
}

func writeLegacyMarker(absRoot string, marker LegacyMigrationMarker) error {
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return atomicFile(filepath.Join(absRoot, LockDir, LegacyMigrationMarkerRel), append(raw, '\n'), 0o644)
}
