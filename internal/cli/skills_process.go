package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/skill"
)

const processTimeout = 45 * time.Second

type processFailure struct {
	code string
	err  error
}

func (f processFailure) Error() string { return f.err.Error() }
func (f processFailure) Unwrap() error { return f.err }

func failProcess(code, message string) error {
	return processFailure{code: code, err: errors.New(message)}
}

func newSkillsProcessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "process",
		Short: "Processa uma requisição JSON versionada de skills",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return writeProcessTransportError(cmd, "unexpected_argument", "process accepts no positional arguments")
			}
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			localRoots, _ := cmd.Flags().GetStringArray("allow-local-root")
			projectRoots, _ := cmd.Flags().GetStringArray("allow-project-root")
			request, decodeErr := skill.DecodeProcessRequest(cmd.InOrStdin())
			response := skill.ProcessResponse{ProtocolVersion: skill.ProcessProtocolVersion, RequestID: request.RequestID}
			var runErr error
			if decodeErr != nil {
				runErr = processFailure{code: "invalid_request", err: decodeErr}
			} else {
				response, runErr = runSkillProcess(cmd.Context(), request, localRoots, projectRoots)
			}
			if runErr != nil {
				response.ProtocolVersion = skill.ProcessProtocolVersion
				response.RequestID = request.RequestID
				response.Status = "error"
				response.Error = &skill.ProcessError{Code: processErrorCode(runErr), Message: runErr.Error()}
			}
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(response); err != nil {
				return fmt.Errorf("write JSON response: %w", err)
			}
			return runErr
		},
	}
	cmd.Flags().StringArray("allow-local-root", nil, "checkout local autorizado (pode ser repetido)")
	cmd.Flags().StringArray("allow-project-root", nil, "raiz de projeto autorizada (caminho canônico exato; pode ser repetido)")
	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		_ = writeProcessTransportError(cmd, "invalid_cli_argument", err.Error())
		return err
	})
	return cmd
}

func processErrorCode(err error) string {
	var failure processFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	switch {
	case errors.Is(err, skill.ErrProjectLocked):
		return "project_locked"
	default:
		return "operation_failed"
	}
}

func runSkillProcess(parent context.Context, request skill.ProcessRequest, localRoots, projectRoots []string) (skill.ProcessResponse, error) {
	response := skill.ProcessResponse{ProtocolVersion: skill.ProcessProtocolVersion, RequestID: request.RequestID}
	if request.Operation == "capabilities" {
		if request.ProjectRoot != "" || request.Agent != "" || request.SkillID != "" || request.PlanToken != "" || request.Source != (skill.ProcessSource{}) {
			return response, failProcess("invalid_request", "capabilities request accepts no project, source, skill, or plan fields")
		}
		response.Status = "ok"
		response.Capabilities = &skill.ProcessCapabilities{
			Operations:      []string{"capabilities", "install.plan", "install.apply"},
			Sources:         []string{"github:willsantos/skills_AI", "authorized-local-checkout"},
			ProjectScope:    true,
			LockPath:        filepath.ToSlash(filepath.Join(skill.LockDir, skill.LockFilename)),
			LockSchema:      1,
			LockKey:         []string{"id", "target"},
			DigestAlgorithm: skill.DigestAlgorithm,
			Migration:       "unsupported; no legacy lock is read or rewritten by this protocol version",
		}
		return response, nil
	}
	if request.Operation != "install.plan" && request.Operation != "install.apply" {
		return response, failProcess("unsupported_operation", fmt.Sprintf("operation %q is unsupported", request.Operation))
	}
	if request.ProjectRoot == "" || request.SkillID == "" || request.Source.Kind == "" {
		return response, failProcess("invalid_request", "projectRoot, skillId, and source.kind are required")
	}
	if request.Operation == "install.apply" && request.PlanToken == "" {
		return response, failProcess("invalid_request", "install.apply requires planToken from install.plan")
	}
	if request.Operation == "install.plan" && request.PlanToken != "" {
		return response, failProcess("invalid_request", "install.plan must not include planToken")
	}
	projectRoot, err := authorizeDirectory(request.ProjectRoot, projectRoots)
	if err != nil {
		return response, processFailure{code: "unauthorized_project", err: err}
	}
	ctx, cancel := context.WithTimeout(parent, processTimeout)
	defer cancel()
	candidate, cleanup, err := loadProcessCandidate(ctx, request, localRoots)
	if err != nil {
		return response, err
	}
	defer cleanup()
	if candidate.ID != request.SkillID {
		return response, failProcess("skill_not_found", fmt.Sprintf("skillId %q not found in selected source", request.SkillID))
	}

	manifestPath, err := skill.SafeProjectPath(projectRoot, manifest.Filename)
	if err != nil {
		return response, processFailure{code: "unsafe_project_path", err: err}
	}
	lockPath, err := skill.SafeProjectPath(projectRoot, filepath.ToSlash(filepath.Join(skill.LockDir, skill.LockFilename)))
	if err != nil {
		return response, processFailure{code: "unsafe_project_path", err: err}
	}
	m, manifestErr := manifest.Read(manifestPath)
	if manifestErr != nil && !errors.Is(manifestErr, os.ErrNotExist) {
		return response, processFailure{code: "invalid_project_manifest", err: manifestErr}
	}
	var agent string
	if m != nil {
		agent = skill.AgentFromManifest(m)
		if request.Agent != "" && request.Agent != agent {
			return response, failProcess("target_conflict", "request agent conflicts with project's orotools.yaml")
		}
	} else {
		if request.Agent == "" {
			return response, failProcess("invalid_request", "agent is required for projects without orotools.yaml")
		}
		agent = request.Agent
	}
	if _, err := skill.SkillsDir(agent); err != nil {
		return response, processFailure{code: "unsupported_agent", err: err}
	}
	lock, err := readLockOrEmpty(lockPath)
	if err != nil {
		return response, processFailure{code: "invalid_lock", err: err}
	}
	service := skill.Service{Base: projectRoot, Agent: agent, ManifestPath: manifestPath, LockPath: lockPath}
	if request.Operation == "install.plan" {
		plan, err := service.PlanInstall(lock, candidate)
		if err != nil {
			return response, processFailure{code: "plan_failed", err: err}
		}
		response.Status, response.Plan, response.Decision = "ok", &plan, plan.Decision
		return response, nil
	}
	if candidate.Revision == "" || request.Source.Revision == "" {
		return response, failProcess("source_revision_required", "install.apply requires the exact source revision returned by install.plan")
	}
	result, decision, err := service.ApplyPlannedInstallContext(ctx, m, lock, candidate, request.PlanToken)
	if err != nil {
		return response, processFailure{code: "apply_rejected", err: err}
	}
	response.Status, response.Decision, response.Result = "ok", decision, &result
	return response, nil
}

func writeProcessTransportError(cmd *cobra.Command, code, message string) error {
	response := skill.ProcessResponse{ProtocolVersion: skill.ProcessProtocolVersion, Status: "error", Error: &skill.ProcessError{Code: code, Message: message}}
	if err := json.NewEncoder(cmd.OutOrStdout()).Encode(response); err != nil {
		return fmt.Errorf("write transport error envelope: %w", err)
	}
	return fmt.Errorf("%s", message)
}

func authorizeDirectory(requested string, allowed []string) (string, error) {
	canonical, err := canonicalDirectory(requested)
	if err != nil {
		return "", err
	}
	for _, candidate := range allowed {
		root, err := canonicalDirectory(candidate)
		if err == nil && root == canonical {
			return canonical, nil
		}
	}
	return "", fmt.Errorf("directory is not in the explicit allowlist: %s", requested)
}

func canonicalDirectory(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", fmt.Errorf("directory must be provided")
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve directory: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve directory: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("directory does not exist or is not a directory: %s", input)
	}
	return filepath.Clean(real), nil
}

func loadProcessCandidate(ctx context.Context, request skill.ProcessRequest, allowedLocalRoots []string) (skill.Candidate, func(), error) {
	none := func() {}
	switch request.Source.Kind {
	case "github":
		if request.Source.Root != "" {
			return skill.Candidate{}, none, failProcess("invalid_request", "github source does not accept root")
		}
		client := skill.HTTPGitHubClient{Token: os.Getenv("GITHUB_TOKEN")}
		revision := request.Source.Revision
		if revision == "" {
			if request.Operation == "install.apply" {
				return skill.Candidate{}, none, failProcess("source_revision_required", "install.apply requires pinned public source revision")
			}
			var err error
			revision, err = client.Resolve(ctx)
			if err != nil {
				return skill.Candidate{}, none, processFailure{code: "source_unavailable", err: err}
			}
		}
		if !regexp.MustCompile(`^[0-9a-fA-F]{40}$`).MatchString(revision) {
			return skill.Candidate{}, none, failProcess("invalid_source_revision", "public source revision must be a full commit SHA")
		}
		snapshot, err := client.Snapshot(ctx, revision)
		if err != nil {
			return skill.Candidate{}, none, processFailure{code: "source_unavailable", err: err}
		}
		candidates, warnings, err := (skill.GitHubCatalog{Snapshot: snapshot}).List(ctx)
		if err != nil {
			snapshot.Cleanup()
			return skill.Candidate{}, none, processFailure{code: "invalid_source_catalog", err: err}
		}
		for _, warning := range warnings {
			fmt.Fprintf(os.Stderr, "oro skills process: skipped %s: %s\n", warning.SkillID, warning.Message)
		}
		candidate, ok := candidateByID(candidates, request.SkillID)
		if !ok {
			snapshot.Cleanup()
			return skill.Candidate{}, none, failProcess("skill_not_found", fmt.Sprintf("skillId %q not found in public catalog at %s", request.SkillID, revision[:12]))
		}
		return candidate, snapshot.Cleanup, nil
	case "local":
		if request.Source.Root == "" {
			return skill.Candidate{}, none, failProcess("invalid_request", "local source requires root")
		}
		root, err := authorizeDirectory(request.Source.Root, allowedLocalRoots)
		if err != nil {
			return skill.Candidate{}, none, processFailure{code: "unauthorized_source", err: err}
		}
		revision, err := inspectCleanGitCheckout(ctx, root)
		if err != nil {
			return skill.Candidate{}, none, processFailure{code: "invalid_local_source", err: err}
		}
		if request.Source.Revision != "" && request.Source.Revision != revision {
			return skill.Candidate{}, none, failProcess("source_revision_changed", "local checkout HEAD differs from requested revision")
		}
		if request.Operation == "install.apply" && request.Source.Revision == "" {
			return skill.Candidate{}, none, failProcess("source_revision_required", "install.apply requires pinned local source revision")
		}
		sum := sha256.Sum256([]byte(root))
		sourceRef := "checkout-" + hex.EncodeToString(sum[:8])
		candidates, warnings, err := (skill.LocalCatalog{Root: root, SourceRef: sourceRef, Revision: revision}).List(ctx)
		if err != nil {
			return skill.Candidate{}, none, processFailure{code: "invalid_source_catalog", err: err}
		}
		for _, warning := range warnings {
			fmt.Fprintf(os.Stderr, "oro skills process: skipped %s: %s\n", warning.SkillID, warning.Message)
		}
		candidate, ok := candidateByID(candidates, request.SkillID)
		if !ok {
			return skill.Candidate{}, none, failProcess("skill_not_found", fmt.Sprintf("skillId %q not found in authorized local catalog", request.SkillID))
		}
		return candidate, none, nil
	default:
		return skill.Candidate{}, none, failProcess("unsupported_source", fmt.Sprintf("source kind %q is unsupported", request.Source.Kind))
	}
}

func candidateByID(candidates []skill.Candidate, id string) (skill.Candidate, bool) {
	for _, candidate := range candidates {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return skill.Candidate{}, false
}

var fullCommitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func inspectCleanGitCheckout(ctx context.Context, root string) (string, error) {
	run := func(args ...string) ([]byte, error) {
		gitArgs := append([]string{"--no-lazy-fetch", "-c", "protocol.allow=never", "-c", "protocol.ext.allow=never", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "core.attributesFile=/dev/null", "-C", root}, args...)
		cmd := exec.CommandContext(ctx, "git", gitArgs...)
		cmd.Env = controlledGitEnv(os.Environ())
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("inspect local Git source: git %s failed: %w", strings.Join(args, " "), err)
		}
		return out, nil
	}
	head, err := run("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	revision := strings.TrimSpace(string(head))
	if !fullCommitSHA.MatchString(revision) {
		return "", fmt.Errorf("local checkout HEAD is not a full commit SHA")
	}
	listing, err := run("ls-tree", "-r", "-z", "--full-tree", revision)
	if err != nil {
		return "", err
	}
	tracked := make(map[string]gitTreeEntry)
	for _, record := range strings.Split(string(listing), "\x00") {
		if record == "" {
			continue
		}
		metadata, name, ok := strings.Cut(record, "\t")
		if !ok {
			return "", fmt.Errorf("malformed HEAD tree entry")
		}
		fields := strings.Fields(metadata)
		if len(fields) != 3 || fields[1] != "blob" {
			return "", fmt.Errorf("unsupported HEAD object entry %q", name)
		}
		if _, exists := tracked[name]; exists {
			return "", fmt.Errorf("duplicate HEAD path %q", name)
		}
		tracked[name] = gitTreeEntry{mode: fields[0], oid: fields[2]}
	}
	rootFromGit, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	gitRoot, err := canonicalDirectory(strings.TrimSpace(string(rootFromGit)))
	if err != nil || gitRoot != root {
		return "", fmt.Errorf("authorized local source must be the exact root of a Git checkout")
	}
	physical := make(map[string]fs.FileInfo)
	err = filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("local checkout contains symlink %q", rel)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("local checkout contains non-regular file %q", rel)
		}
		physical[filepath.ToSlash(rel)] = info
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("inspect local checkout files: %w", err)
	}
	if len(physical) != len(tracked) {
		return "", fmt.Errorf("local checkout file set differs from pinned HEAD")
	}
	for name, entry := range tracked {
		info, ok := physical[name]
		if !ok {
			return "", fmt.Errorf("HEAD file %q is missing from checkout", name)
		}
		if entry.mode != "100644" && entry.mode != "100755" {
			return "", fmt.Errorf("unsupported HEAD file mode %q for %q", entry.mode, name)
		}
		physicalExecutable := info.Mode().Perm()&0o111 != 0
		if physicalExecutable != (entry.mode == "100755") {
			return "", fmt.Errorf("file mode differs from HEAD for %q", name)
		}
		blob, err := run("cat-file", "blob", entry.oid)
		if err != nil {
			return "", err
		}
		working, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return "", err
		}
		if !bytes.Equal(blob, working) {
			return "", fmt.Errorf("working file %q differs from pinned HEAD", name)
		}
	}
	return revision, nil
}

type gitTreeEntry struct{ mode, oid string }

func controlledGitEnv(environment []string) []string {
	filtered := make([]string, 0, len(environment)+6)
	for _, item := range environment {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			continue
		}
		filtered = append(filtered, item)
	}
	return append(filtered,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_ALLOW_PROTOCOL=",
		"GIT_PROTOCOL_FROM_USER=0",
	)
}
