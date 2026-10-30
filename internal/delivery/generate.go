// Package delivery plans and writes official, pinned project delivery files
// from a verified standards checkout: stack detection (M05.3), generation of
// .oroborus/delivery.yaml plus the consumer azure-pipelines.yml with the pin
// contract of ADR-0004 (M05.4), and guarded, conflict-refusing writes (M05.6).
// Generation never deploys, never executes build commands, and never
// overwrites existing CI or delivery configuration.
package delivery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	errNoEvidence = errors.New("insufficient project evidence to infer stack; specify the build command explicitly")
	errNoRoot     = errors.New("project root must be a real directory")
	// identifierPattern mirrors schemas/project-delivery.schema.json $defs/identifier.
	identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
)

// ErrUnsupportedProvider is returned for CI destinations without an
// implemented official adapter.
var ErrUnsupportedProvider = errors.New("unsupported delivery destination")

// stackBuildCommands maps a detected stack to its informative default build
// command. The mapping documents conventional commands per ecosystem; the
// result is written as informative config (the schema itself states the
// command "não deve ser executado automaticamente a partir deste YAML") and is
// NEVER executed by generation.
var stackBuildCommands = map[string]string{
	"dotnet": "dotnet publish -c Release",
	"go":     "go build ./...",
	"node":   "npm ci && npm run build",
	"php":    "composer install --no-dev",
	"python": "python -m build",
}

// DetectStack is evidence-based detection. Evidence must be a regular,
// non-symlink manifest directly under root. Exactly one distinct stack is
// reported; mixed evidence is not classified; no evidence fails with
// errNoEvidence so callers ask for data instead of inventing commands (M05.3).
func DetectStack(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	checks := []struct{ file, stack string }{{"go.mod", "go"}, {"*.sln", "dotnet"}, {"*.csproj", "dotnet"}, {"pyproject.toml", "python"}, {"requirements.txt", "python"}, {"composer.json", "php"}, {"package.json", "node"}, {"pnpm-lock.yaml", "node"}, {"yarn.lock", "node"}}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	found := make(map[string]bool)
	for _, check := range checks {
		for _, entry := range entries {
			matched := entry.Name() == check.file
			if strings.Contains(check.file, "*") {
				matched, _ = filepath.Match(check.file, entry.Name())
			}
			if matched && regularEvidence(root, entry.Name()) {
				found[check.stack] = true
			}
		}
	}
	stacks := make([]string, 0, len(found))
	for stack := range found {
		stacks = append(stacks, stack)
	}
	sort.Strings(stacks)
	switch len(stacks) {
	case 0:
		return "", errNoEvidence
	case 1:
		return stacks[0], nil
	default:
		return "", fmt.Errorf("mixed project evidence (%s); stack is not classified: specify the build command explicitly", strings.Join(stacks, ", "))
	}
}

// Options carries the explicit inputs of one generation. Fields the schema
// requires but no evidence can supply (deployment provider, promotion mode,
// environment targets) must be provided by the caller: missing data is asked
// for, never invented (M05.3).
type Options struct {
	// Application is the delivery application identifier (schema
	// $defs/identifier). Required.
	Application string
	// Component names the single generated component; defaults to "app".
	Component string
	// ComponentPath is the component path; defaults to ".".
	ComponentPath string
	// Provider is the CI provider of the generated pipeline. Only
	// "azure-devops" is implemented.
	Provider string
	// DeployProvider, PromotionMode and the environment targets follow the
	// official schema enums. DeployProvider, PromotionMode, EnvDev and EnvPrd
	// are required; EnvStg is optional.
	DeployProvider string
	PromotionMode  string
	EnvDev         string
	EnvPrd         string
	EnvStg         string
	// BuildCommand overrides the stack-derived informative command.
	BuildCommand string
	// StandardsRepoName is the Azure DevOps `project/repo` of the standards
	// repository resource. Defaults to the official example's value.
	StandardsRepoName string
}

// Pin is the ADR-0004 pin recorded in both generated files: the distribution
// version from standards/manifest.json, the registered official template
// identity, and the verified standards revision. The five version identities
// of the compatibility matrix stay distinct: nothing here substitutes the
// norma, schema, skills, or operational versions.
type Pin struct {
	DistributionVersion string
	TemplateID          string
	TemplatePath        string
	StandardsRevision   string
}

// Plan is the complete, validated result of one generation: what would be
// written, with which pin, and what conflicts exist. Preview prints a Plan;
// --write applies exactly its Files.
type Plan struct {
	ProjectRoot string
	// Stack is the detected stack or "" when evidence is missing or mixed.
	Stack string
	// StackNote explains an unclassified stack; empty when classified.
	StackNote string
	// BuildCommand is the informative command emitted ("" when omitted) and
	// BuildCommandSource says where it came from ("stack:<name>" or "flag").
	BuildCommand       string
	BuildCommandSource string

	Pin          Pin
	DeliveryYAML []byte
	PipelineYAML []byte
	// Files lists the relative outputs a write would create.
	Files []string
	// Conflicts lists relative outputs that already exist; a write refuses.
	Conflicts []string
}

// Generated output paths and defaults.
const (
	outputDeliveryPath = ".oroborus/delivery.yaml"
	outputPipelinePath = "azure-pipelines.yml"
	schemaVersion      = "1.0"
	defaultComponent   = "app"
	defaultRepoName    = "Engenharia/oroborus-engineering-standards"
)

// PlanGeneration builds the validated plan for one official generation from a
// verified OfficialSource. It writes nothing. The rendered delivery config is
// validated against the official schema BEFORE the plan is returned, so a
// schema-invalid result can never reach a write. Fail-closed postures:
//
//   - src == nil: official generation is unavailable without a verified
//     standards checkout;
//   - missing required explicit data: the exact missing fields are listed;
//   - schema-invalid rendering: refused with the validator's message.
func PlanGeneration(projectRoot string, src *OfficialSource, opts Options) (*Plan, error) {
	if src == nil {
		return nil, errors.New("delivery generation unavailable: a verified official standards source is required (clean checkout of the standards distribution via --standards-root)")
	}
	if opts.Provider != "azure-devops" {
		return nil, fmt.Errorf("%w %q: only azure-devops has an official generation template", ErrUnsupportedProvider, opts.Provider)
	}
	if !identifierPattern.MatchString(opts.Application) {
		return nil, fmt.Errorf("--application must match %s (got %q)", identifierPattern, opts.Application)
	}
	component := opts.Component
	if component == "" {
		component = defaultComponent
	}
	if !identifierPattern.MatchString(component) {
		return nil, fmt.Errorf("--component must match %s (got %q)", identifierPattern, component)
	}
	componentPath := opts.ComponentPath
	if componentPath == "" {
		componentPath = "."
	}
	if missing := missingRequiredData(opts); len(missing) > 0 {
		return nil, fmt.Errorf("missing required delivery data (project evidence cannot supply it, so it must be explicit): %s", strings.Join(missing, ", "))
	}

	stack, stackNote, err := classifyStack(projectRoot)
	if err != nil {
		return nil, err
	}
	buildCommand, buildSource := resolveBuildCommand(stack, opts.BuildCommand)

	config := renderDeliveryConfig(renderInput{
		application:    opts.Application,
		component:      component,
		componentPath:  componentPath,
		version:        src.DistributionVersion,
		ciProvider:     opts.Provider,
		deployProvider: opts.DeployProvider,
		promotionMode:  opts.PromotionMode,
		envDev:         opts.EnvDev,
		envPrd:         opts.EnvPrd,
		envStg:         opts.EnvStg,
		buildCommand:   buildCommand,
	})
	deliveryYAML, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("render delivery config: %w", err)
	}
	// Pin contract (ADR-0004 item 4): validate the rendered config against the
	// official schema before anything else happens.
	if err := validateDeliveryConfig(src.SchemaBytes, deliveryYAML); err != nil {
		return nil, err
	}

	pipelineYAML, err := renderPipelineYAML(pipelineInput{
		version:        src.DistributionVersion,
		templateID:     src.TemplateID,
		templatePath:   src.TemplatePath,
		revision:       src.Revision,
		application:    opts.Application,
		component:      component,
		repoName:       opts.StandardsRepoName,
		deployProvider: opts.DeployProvider,
		promotionMode:  opts.PromotionMode,
		envDev:         opts.EnvDev,
	})
	if err != nil {
		return nil, err
	}

	return &Plan{
		ProjectRoot:        projectRoot,
		Stack:              stack,
		StackNote:          stackNote,
		BuildCommand:       buildCommand,
		BuildCommandSource: buildSource,
		Pin: Pin{
			DistributionVersion: src.DistributionVersion,
			TemplateID:          src.TemplateID,
			TemplatePath:        src.TemplatePath,
			StandardsRevision:   src.Revision,
		},
		DeliveryYAML: deliveryYAML,
		PipelineYAML: pipelineYAML,
		Files:        []string{outputDeliveryPath, outputPipelinePath},
		Conflicts:    existingConflicts(projectRoot),
	}, nil
}

// missingRequiredData lists the schema-required explicit inputs that are
// absent, so the caller can ask once for everything.
func missingRequiredData(opts Options) []string {
	var missing []string
	for _, item := range []struct{ flag, value string }{
		{"--deploy-provider", opts.DeployProvider},
		{"--promotion-mode", opts.PromotionMode},
		{"--env-dev", opts.EnvDev},
		{"--env-prd", opts.EnvPrd},
	} {
		if strings.TrimSpace(item.value) == "" {
			missing = append(missing, item.flag)
		}
	}
	return missing
}

// classifyStack detects the stack, translating "no evidence" and "mixed
// evidence" into an unclassified result with an explanatory note instead of an
// error: generation proceeds without an invented build command (M05.3).
func classifyStack(projectRoot string) (stack, note string, err error) {
	if projectRoot == "" {
		return "", "", errNoRoot
	}
	root, absErr := filepath.Abs(projectRoot)
	if absErr != nil {
		return "", "", absErr
	}
	info, lstatErr := os.Lstat(root)
	if lstatErr != nil {
		return "", "", lstatErr
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errNoRoot
	}
	stack, detectErr := DetectStack(root)
	if detectErr == nil {
		return stack, "", nil
	}
	if errors.Is(detectErr, errNoEvidence) || strings.HasPrefix(detectErr.Error(), "mixed project evidence") {
		return "", detectErr.Error(), nil
	}
	return "", "", detectErr
}

// resolveBuildCommand prefers the explicit override, then the stack-derived
// informative default, then omits the command entirely.
func resolveBuildCommand(stack, override string) (command, source string) {
	if strings.TrimSpace(override) != "" {
		return override, "flag"
	}
	if command, ok := stackBuildCommands[stack]; ok {
		return command, "stack:" + stack
	}
	return "", ""
}

// existingConflicts returns the generated targets that already exist under
// projectRoot. Any conflict means a write must refuse: adopting existing CI or
// delivery configuration is a manual decision (M05.6).
func existingConflicts(projectRoot string) []string {
	if projectRoot == "" {
		return nil
	}
	var conflicts []string
	for _, rel := range []string{outputDeliveryPath, outputPipelinePath} {
		if _, err := os.Lstat(filepath.Join(projectRoot, filepath.FromSlash(rel))); err == nil {
			conflicts = append(conflicts, rel)
		}
	}
	return conflicts
}

// Write applies the plan with the WritePreview safety model (exclusive
// creation, symlink-safe parents, rollback of only files it created). It
// refuses before touching the filesystem when the plan recorded conflicts:
// existing CI is never overwritten (M05.6).
func (p *Plan) Write(projectRoot string) error {
	if len(p.Conflicts) > 0 {
		return fmt.Errorf("refusing to write: existing files would be overwritten (%s); adopting existing CI/delivery config is manual — review and integrate them by hand", strings.Join(p.Conflicts, ", "))
	}
	return WritePreview(projectRoot, p.DeliveryYAML, p.PipelineYAML)
}

// --- rendering -------------------------------------------------------------

type renderInput struct {
	application, component, componentPath, version string
	ciProvider, deployProvider, promotionMode      string
	envDev, envPrd, envStg                         string
	buildCommand                                   string
}

type deliveryConfigYAML struct {
	SchemaVersion string                  `yaml:"schemaVersion"`
	Application   string                  `yaml:"application"`
	Standards     deliveryStandardsYAML   `yaml:"standards"`
	CI            deliveryCIYAML          `yaml:"ci"`
	Components    []deliveryComponentYAML `yaml:"components"`
}

type deliveryStandardsYAML struct {
	Version string `yaml:"version"`
}

type deliveryCIYAML struct {
	Provider string `yaml:"provider"`
}

type deliveryComponentYAML struct {
	Name       string                 `yaml:"name"`
	Path       string                 `yaml:"path"`
	Build      *deliveryBuildYAML     `yaml:"build,omitempty"`
	Deployment deliveryDeploymentYAML `yaml:"deployment"`
}

type deliveryBuildYAML struct {
	Command string `yaml:"command,omitempty"`
}

type deliveryDeploymentYAML struct {
	Provider      string                   `yaml:"provider"`
	PromotionMode string                   `yaml:"promotionMode,omitempty"`
	Environments  deliveryEnvironmentsYAML `yaml:"environments"`
}

type deliveryEnvironmentsYAML struct {
	Dev string `yaml:"dev"`
	Prd string `yaml:"prd"`
	Stg string `yaml:"stg,omitempty"`
}

func renderDeliveryConfig(in renderInput) deliveryConfigYAML {
	component := deliveryComponentYAML{
		Name: in.component,
		Path: in.componentPath,
		Deployment: deliveryDeploymentYAML{
			Provider:      in.deployProvider,
			PromotionMode: in.promotionMode,
			Environments: deliveryEnvironmentsYAML{
				Dev: in.envDev,
				Prd: in.envPrd,
				Stg: in.envStg,
			},
		},
	}
	if in.buildCommand != "" {
		component.Build = &deliveryBuildYAML{Command: in.buildCommand}
	}
	return deliveryConfigYAML{
		SchemaVersion: schemaVersion,
		Application:   in.application,
		Standards:     deliveryStandardsYAML{Version: in.version},
		CI:            deliveryCIYAML{Provider: in.ciProvider},
		Components:    []deliveryComponentYAML{component},
	}
}

type pipelineInput struct {
	version, templateID, templatePath, revision string
	application, component, repoName            string
	deployProvider, promotionMode, envDev       string
}

// renderPipelineYAML emits the consumer pipeline mirroring the official
// example pipelines/azure-devops/examples/oroborus-delivery-ci.yml: a pinned
// `standards` repository resource (refs/tags/v<distributionVersion>, never a
// moving branch), a single stage extending the registered official template,
// and identical standardsVersion. expectedPipelineId and azureOrganization are
// left as explicit fill-in placeholders like the official example; provider
// buildSteps/deploySteps are intentionally not invented and stay empty.
func renderPipelineYAML(in pipelineInput) ([]byte, error) {
	repoName := in.repoName
	if strings.TrimSpace(repoName) == "" {
		repoName = defaultRepoName
	}
	for _, item := range []struct{ what, value string }{
		{"standards repo name", repoName},
		{"deploy provider", in.deployProvider},
		{"promotion mode", in.promotionMode},
		{"dev environment", in.envDev},
	} {
		if strings.ContainsAny(item.value, "\r\n\x00") {
			return nil, fmt.Errorf("%s must be a single line", item.what)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by OroTools from a verified standards checkout. Do not edit the pin by hand.\n")
	fmt.Fprintf(&b, "# PIN: distributionVersion %s | template id %s | %s | standards revision %s\n", in.version, in.templateID, in.templatePath, in.revision)
	fmt.Fprintf(&b, "# Fill in expectedPipelineId (real authoritative CI pipeline ID) and azureOrganization\n")
	fmt.Fprintf(&b, "# before running. PRD promotion is a SEPARATE pipeline extending promote-prd.yml (ADR-0002).\n")
	fmt.Fprintf(&b, "# buildSteps/deploySteps are intentionally empty: provider-specific steps must be reviewed\n")
	fmt.Fprintf(&b, "# and approved manually; see the official example\n")
	fmt.Fprintf(&b, "# pipelines/azure-devops/examples/oroborus-delivery-ci.yml in the standards distribution.\n")
	fmt.Fprintf(&b, "name: $(Date:yyyy.MM.dd).$(Rev:r)\n")
	fmt.Fprintf(&b, "trigger:\n  branches:\n    include: [main]\n")
	fmt.Fprintf(&b, "pr: none\n")
	fmt.Fprintf(&b, "resources:\n  repositories:\n")
	fmt.Fprintf(&b, "  - repository: standards\n    type: git\n    name: %s\n    ref: refs/tags/v%s\n", yamlQuote(repoName), in.version)
	fmt.Fprintf(&b, "stages:\n")
	fmt.Fprintf(&b, "- template: %s@standards\n", in.templatePath)
	fmt.Fprintf(&b, "  parameters:\n")
	fmt.Fprintf(&b, "    standardsVersion: %s\n", yamlQuote(in.version))
	fmt.Fprintf(&b, "    application: %s\n", yamlQuote(in.application))
	fmt.Fprintf(&b, "    component: %s\n", yamlQuote(in.component))
	fmt.Fprintf(&b, "    expectedPipelineId: '' # preencher: ID REAL da pipeline CI autoritativa\n")
	fmt.Fprintf(&b, "    azureOrganization: '' # preencher: slug da organização Azure DevOps\n")
	fmt.Fprintf(&b, "    deploymentProvider: %s\n", yamlQuote(in.deployProvider))
	fmt.Fprintf(&b, "    promotionMode: %s\n", yamlQuote(in.promotionMode))
	fmt.Fprintf(&b, "    devEnvironment: %s\n", yamlQuote(in.envDev))
	return []byte(b.String()), nil
}

// yamlQuote renders a YAML-safe single-quoted scalar for hand-built output.
func yamlQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// --- guarded writing (safety model unchanged) -------------------------------

// WritePreview writes files under root; it is not a dry-run. It exclusively
// creates targets and removes files it created if a later write fails.
func WritePreview(root string, config, refs []byte) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("project root must be a real directory")
	}
	secureRoot, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer secureRoot.Close()
	dir := filepath.Join(root, ".oroborus")
	if err := ensureRealDir(dir); err != nil {
		return err
	}
	paths := []struct {
		path string
		data []byte
	}{{filepath.Join(".oroborus", "delivery.yaml"), config}, {"azure-pipelines.yml", refs}}
	created := make([]createdFile, 0, len(paths))
	for _, item := range paths {
		if err := ensureSafeParents(root, filepath.Join(root, item.path)); err != nil {
			rollbackCreatedRoot(secureRoot, created)
			return err
		}
		f, err := secureRoot.OpenFile(item.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			rollbackCreatedRoot(secureRoot, created)
			return fmt.Errorf("refusing to overwrite or follow unsafe path %s: %w", item.path, err)
		}
		created = append(created, createdFile{item.path, fileInfo(f)})
		_, err = f.Write(item.data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			rollbackCreatedRoot(secureRoot, created)
			return err
		}
	}
	return nil
}

func regularEvidence(root, name string) bool {
	info, err := os.Lstat(filepath.Join(root, name))
	return err == nil && info.Mode().IsRegular()
}

func ensureRealDir(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err = os.Mkdir(path, 0755); err == nil {
			return nil
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New(".oroborus must be a real directory, not a symlink")
	}
	return nil
}

func ensureSafeParents(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("output path escapes project root")
	}
	for dir := filepath.Dir(path); dir != root && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe output parent %s", dir)
		}
	}
	return nil
}

type createdFile struct {
	path string
	info os.FileInfo
}

func fileInfo(f *os.File) os.FileInfo { info, _ := f.Stat(); return info }
func rollbackCreatedRoot(root *os.Root, paths []createdFile) {
	for i := len(paths) - 1; i >= 0; i-- {
		current, err := root.Lstat(paths[i].path)
		if err == nil && current.Mode()&os.ModeSymlink == 0 && paths[i].info != nil && os.SameFile(current, paths[i].info) {
			_ = root.Remove(paths[i].path)
		}
	}
}
