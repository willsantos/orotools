package delivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// OfficialTemplateID is the only generation template id this generator
// consumes. It must match the entry registered in standards/manifest.json
// (generationTemplates) of the standards distribution being used (ADR-0004).
const OfficialTemplateID = "oroborus-delivery"

// OfficialSource is an immutable snapshot of the official generation inputs
// taken from a verified clean Git checkout of the engineering standards
// distribution. Callers obtain the checkout revision by verifying the checkout
// (byte/mode equality against HEAD, no symlinks); this package never runs Git
// and trusts the verified revision it receives. Treat all fields as read-only.
type OfficialSource struct {
	// Revision is the full 40-hex SHA of the verified checkout HEAD.
	Revision string
	// DistributionVersion is manifest distributionVersion: the SemVer identity
	// of the distribution and the target of the generated pins.
	DistributionVersion string
	// TemplateID and TemplatePath identify the registered official template.
	TemplateID   string
	TemplatePath string
	// TemplateBytes is the exact template content pinned at Revision.
	TemplateBytes []byte
	// ConfigSchemaPath and SchemaBytes locate and carry the JSON Schema the
	// rendered .oroborus/delivery.yaml must validate against.
	ConfigSchemaPath string
	SchemaBytes      []byte
}

// ErrOfficialSourceUnavailable is the typed failure for every refused official
// source load: missing or malformed manifest fields, unknown template id,
// unsafe paths, non-regular files, or an unverified revision. Errors wrapping
// it carry an actionable message.
var ErrOfficialSourceUnavailable = errors.New("official delivery source unavailable")

var (
	revisionPattern          = regexp.MustCompile(`^[0-9a-f]{40}$`)
	distributionVersionRe    = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-(?:rc|alpha|beta)\.[1-9][0-9]*)?$`)
	templateIDPattern        = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	officialTemplatePathRoot = "pipelines/azure-devops/templates/"
	officialConfigSchemaRoot = "schemas/"
	constManifestPath        = "standards/manifest.json"
)

// standardsManifest mirrors only the manifest fields this generator consumes.
// Unknown fields are ignored on purpose: the registry is additive (ADR-0004)
// and the distribution may carry fields introduced after this generator.
type standardsManifest struct {
	DistributionVersion string               `json:"distributionVersion"`
	GenerationTemplates []generationTemplate `json:"generationTemplates"`
}

type generationTemplate struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	ConfigSchema string `json:"configSchema"`
	Description  string `json:"description"`
}

// LoadOfficialSource reads the official generation inputs from standardsRoot
// and returns an immutable snapshot pinned at revision. The caller must have
// verified that standardsRoot is a clean Git checkout whose working files are
// byte/mode-equal to revision; a revision that is not a full 40-hex SHA is
// refused so an unverified snapshot can never be built by accident.
func LoadOfficialSource(standardsRoot, revision string) (*OfficialSource, error) {
	if !revisionPattern.MatchString(revision) {
		return nil, fmt.Errorf("%w: a verified full 40-hex standards revision is required; verify the checkout is clean and pass its HEAD commit", ErrOfficialSourceUnavailable)
	}
	info, err := os.Lstat(standardsRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: standards root: %w", ErrOfficialSourceUnavailable, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: standards root must be a real directory", ErrOfficialSourceUnavailable)
	}
	manifestBytes, err := readVerifiedFile(standardsRoot, constManifestPath)
	if err != nil {
		return nil, err
	}
	var manifest standardsManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("%w: %s is malformed: %w", ErrOfficialSourceUnavailable, constManifestPath, err)
	}
	if !distributionVersionRe.MatchString(manifest.DistributionVersion) {
		return nil, fmt.Errorf("%w: %s has missing or malformed distributionVersion %q (want SemVer, optionally -rc.N/-alpha.N/-beta.N)", ErrOfficialSourceUnavailable, constManifestPath, manifest.DistributionVersion)
	}
	entry, err := selectTemplate(&manifest)
	if err != nil {
		return nil, err
	}
	if err := checkRelativePath(entry.Path, officialTemplatePathRoot, ".yml"); err != nil {
		return nil, fmt.Errorf("%w: template %s has unsafe path %q: %w", ErrOfficialSourceUnavailable, entry.ID, entry.Path, err)
	}
	if err := checkRelativePath(entry.ConfigSchema, officialConfigSchemaRoot, ".schema.json"); err != nil {
		return nil, fmt.Errorf("%w: template %s has unsafe config schema path %q: %w", ErrOfficialSourceUnavailable, entry.ID, entry.ConfigSchema, err)
	}
	templateBytes, err := readVerifiedFile(standardsRoot, entry.Path)
	if err != nil {
		return nil, err
	}
	schemaBytes, err := readVerifiedFile(standardsRoot, entry.ConfigSchema)
	if err != nil {
		return nil, err
	}
	return &OfficialSource{
		Revision:            revision,
		DistributionVersion: manifest.DistributionVersion,
		TemplateID:          entry.ID,
		TemplatePath:        entry.Path,
		TemplateBytes:       templateBytes,
		ConfigSchemaPath:    entry.ConfigSchema,
		SchemaBytes:         schemaBytes,
	}, nil
}

// selectTemplate resolves OfficialTemplateID in the optional
// generationTemplates registry, refusing duplicates and unknown ids with the
// registered ids listed for actionability.
func selectTemplate(manifest *standardsManifest) (generationTemplate, error) {
	none := generationTemplate{}
	registered := make(map[string]generationTemplate, len(manifest.GenerationTemplates))
	ids := make([]string, 0, len(manifest.GenerationTemplates))
	for _, entry := range manifest.GenerationTemplates {
		if entry.ID == "" || !templateIDPattern.MatchString(entry.ID) {
			return none, fmt.Errorf("%w: %s generationTemplates has an entry with missing or malformed id %q", ErrOfficialSourceUnavailable, constManifestPath, entry.ID)
		}
		if _, dup := registered[entry.ID]; dup {
			return none, fmt.Errorf("%w: %s generationTemplates registers id %q more than once", ErrOfficialSourceUnavailable, constManifestPath, entry.ID)
		}
		registered[entry.ID] = entry
		ids = append(ids, entry.ID)
	}
	entry, ok := registered[OfficialTemplateID]
	if !ok {
		if len(ids) == 0 {
			return none, fmt.Errorf("%w: %s has no generationTemplates registry: template id %q is not available; use a standards distribution that registers it", ErrOfficialSourceUnavailable, constManifestPath, OfficialTemplateID)
		}
		return none, fmt.Errorf("%w: template id %q is not registered in %s (registered: %s)", ErrOfficialSourceUnavailable, OfficialTemplateID, constManifestPath, strings.Join(ids, ", "))
	}
	return entry, nil
}

// LstatFileInsideRoot resolves the slash-relative path relPath under root
// without following symlinks in ANY component: every intermediate component
// must be a real directory and no component may be a symlink. The FileInfo of
// the final component is returned for the caller to classify (regular file,
// permissions). Exported so the CLI checkout verifier applies exactly the same
// rule to tracked files: os.Lstat and os.ReadFile follow intermediate
// symlinked directories, which would silently read bytes from outside the
// checkout and break the verified-set guarantee ("regular files inside the
// checkout, no symlinks").
func LstatFileInsideRoot(root, relPath string) (os.FileInfo, error) {
	if err := checkRelativePath(relPath, "", ""); err != nil {
		return nil, fmt.Errorf("unsafe checkout path %q: %w", relPath, err)
	}
	components := strings.Split(relPath, "/")
	abs := root
	var info os.FileInfo
	for i, name := range components {
		abs = filepath.Join(abs, name)
		next, err := os.Lstat(abs)
		if err != nil {
			return nil, err
		}
		if next.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("path component %q of %q is a symlink (symlinks are refused inside the checkout)", name, relPath)
		}
		if i < len(components)-1 && !next.IsDir() {
			return nil, fmt.Errorf("intermediate path component %q of %q is not a directory", name, relPath)
		}
		info = next
	}
	return info, nil
}

// readVerifiedFile reads one slash-relative file of the checkout, refusing
// paths that escape the root and any non-regular or symlinked entry in ANY
// path component: the verified-set guarantee (byte equality against HEAD, no
// symlinks) only holds for regular files inside the checkout.
func readVerifiedFile(root, relPath string) ([]byte, error) {
	info, err := LstatFileInsideRoot(root, relPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrOfficialSourceUnavailable, relPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s must be a regular file (symlinks and special files are refused)", ErrOfficialSourceUnavailable, relPath)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrOfficialSourceUnavailable, relPath, err)
	}
	return data, nil
}

// checkRelativePath enforces a clean, slash-separated, escaping-free checkout
// path, optionally rooted at prefix and suffixed with suffix.
func checkRelativePath(relPath, prefix, suffix string) error {
	if relPath == "" || strings.ContainsRune(relPath, '\\') {
		return errors.New("path must be a non-empty slash-separated relative path")
	}
	if path.IsAbs(relPath) || path.Clean(relPath) != relPath {
		return errors.New("path must be clean and relative (no absolute path, .., or . segments)")
	}
	if prefix != "" && !strings.HasPrefix(relPath, prefix) {
		return fmt.Errorf("path must be under %s", prefix)
	}
	if suffix != "" && !strings.HasSuffix(relPath, suffix) {
		return fmt.Errorf("path must end with %s", suffix)
	}
	return nil
}
