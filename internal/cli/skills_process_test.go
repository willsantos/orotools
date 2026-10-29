package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/skill"
)

func TestSkillsProcessCapabilitiesIsSingleJSONDocument(t *testing.T) {
	root := newRoot("dev")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(`{"protocolVersion":1,"requestId":"caps-1","operation":"capabilities"}`))
	root.SetArgs([]string{"skills", "process"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var response skill.ProcessResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("stdout is not JSON: %q: %v", stdout.String(), err)
	}
	if response.Status != "ok" || response.RequestID != "caps-1" || response.Capabilities == nil {
		t.Fatalf("response = %+v", response)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestSkillsProcessErrorsRemainJSONAndAuthorizeProject(t *testing.T) {
	project := t.TempDir()
	root := newRoot("dev")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	request := `{"protocolVersion":1,"requestId":"req-unauth","operation":"install.plan","projectRoot":` + quoteJSON(project) + `,"skillId":"local:x/y","agent":"opencode","source":{"kind":"local","root":"/tmp"}}`
	root.SetIn(strings.NewReader(request))
	root.SetArgs([]string{"skills", "process"})
	if err := root.Execute(); err == nil {
		t.Fatal("unauthorized project accepted")
	}
	var response skill.ProcessResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("error stdout is not JSON: %q: %v", stdout.String(), err)
	}
	if response.Status != "error" || response.Error == nil || response.Error.Code != "unauthorized_project" {
		t.Fatalf("response = %+v", response)
	}
	if stderr.Len() != 0 {
		t.Fatalf("Cobra leaked diagnostics to stderr: %q", stderr.String())
	}
}

func TestLocalProcessSourceRequiresExplicitCheckoutAndProjectAllowlist(t *testing.T) {
	project, checkout := t.TempDir(), t.TempDir()
	request := skill.ProcessRequest{ProtocolVersion: 1, RequestID: "local-1", Operation: "install.plan", ProjectRoot: project, Agent: "opencode", SkillID: "local:checkout/test", Source: skill.ProcessSource{Kind: "local", Root: checkout}}
	if _, err := runSkillProcess(context.Background(), request, nil, []string{project}); err == nil {
		t.Fatal("local root without allowlist accepted")
	}
	if _, err := runSkillProcess(context.Background(), request, []string{checkout}, nil); err == nil {
		t.Fatal("project root without allowlist accepted")
	}
}

func TestAuthorizeDirectoryRejectsSymlinkAlias(t *testing.T) {
	root, alias := t.TempDir(), filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := authorizeDirectory(alias, []string{root}); err != nil {
		t.Fatal("canonical exact allowlist should permit symlink alias resolving to same root:", err)
	}
	if _, err := authorizeDirectory(root, []string{t.TempDir()}); err == nil {
		t.Fatal("unlisted project root accepted")
	}
}

func TestSkillsProcessCLIValidationEmitsJSONEnvelope(t *testing.T) {
	for _, args := range [][]string{{"skills", "process", "unexpected"}, {"skills", "process", "--bogus"}} {
		root := newRoot("dev")
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetIn(strings.NewReader(`{"protocolVersion":1,"requestId":"cli-validation","operation":"capabilities"}`))
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Fatalf("args %v unexpectedly succeeded", args)
		}
		var response skill.ProcessResponse
		if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
			t.Fatalf("args %v stdout not JSON: %q (%v)", args, stdout.String(), err)
		}
		if response.Status != "error" || response.Error == nil {
			t.Fatalf("args %v response = %+v", args, response)
		}
		if stderr.Len() != 0 {
			t.Fatalf("args %v leaked stderr: %q", args, stderr.String())
		}
	}
}

func TestInspectCleanGitCheckoutDisablesConfiguredFSMonitor(t *testing.T) {
	root := t.TempDir()
	gitTestCommand(t, root, "init", "-q")
	gitTestCommand(t, root, "config", "user.email", "test@example.invalid")
	gitTestCommand(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, root, "add", "tracked.txt")
	gitTestCommand(t, root, "commit", "-qm", "initial")
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	script := filepath.Join(t.TempDir(), "fsmonitor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch \"$FS_MONITOR_MARKER\"\necho token\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, root, "config", "core.fsmonitor", script)
	t.Setenv("FS_MONITOR_MARKER", marker)
	if _, err := inspectCleanGitCheckout(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("configured fsmonitor executed: %v", err)
	}
}

func TestInspectCleanGitCheckoutRejectsIgnoredCatalogFiles(t *testing.T) {
	root := t.TempDir()
	gitTestCommand(t, root, "init", "-q")
	gitTestCommand(t, root, "config", "user.email", "test@example.invalid")
	gitTestCommand(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored-skill/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, root, "add", ".gitignore", "tracked.txt")
	gitTestCommand(t, root, "commit", "-qm", "initial")
	ignored := filepath.Join(root, "ignored-skill")
	if err := os.Mkdir(ignored, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignored, "SKILL.md"), []byte("ignored payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectCleanGitCheckout(context.Background(), root); err == nil {
		t.Fatal("ignored payload outside pinned HEAD accepted")
	}
}

func TestInspectGitTreeDoesNotRunCleanFiltersAndDetectsAssumeUnchanged(t *testing.T) {
	root := t.TempDir()
	gitTestCommand(t, root, "init", "-q")
	gitTestCommand(t, root, "config", "user.email", "test@example.invalid")
	gitTestCommand(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.txt filter=payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("HEAD bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, root, "add", ".gitattributes", "tracked.txt")
	gitTestCommand(t, root, "commit", "-qm", "initial")
	marker := filepath.Join(t.TempDir(), "clean-filter-ran")
	script := filepath.Join(t.TempDir(), "clean-filter.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch \"$CLEAN_FILTER_MARKER\"\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, root, "config", "filter.payload.clean", script)
	t.Setenv("CLEAN_FILTER_MARKER", marker)
	if _, err := inspectCleanGitCheckout(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("clean filter executed: %v", err)
	}
	gitTestCommand(t, root, "update-index", "--assume-unchanged", "tracked.txt")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("changed, index says unchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectCleanGitCheckout(context.Background(), root); err == nil {
		t.Fatal("assume-unchanged worktree bytes accepted")
	}
}

func TestInspectPartialCloneMissingBlobCannotRunExtPromisor(t *testing.T) {
	root := t.TempDir()
	gitTestCommand(t, root, "init", "-q")
	gitTestCommand(t, root, "config", "user.email", "test@example.invalid")
	gitTestCommand(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("blob in HEAD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, root, "add", "tracked.txt")
	gitTestCommand(t, root, "commit", "-qm", "initial")
	oid := strings.TrimSpace(string(gitTestOutput(t, root, "rev-parse", "HEAD:tracked.txt")))
	blobPath := filepath.Join(root, ".git", "objects", oid[:2], oid[2:])
	if err := os.Remove(blobPath); err != nil {
		t.Fatalf("remove loose blob object %s: %v", oid, err)
	}
	marker := filepath.Join(t.TempDir(), "promisor-helper-ran")
	helper := filepath.Join(t.TempDir(), "remote-helper.sh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ntouch \"$PROMISOR_MARKER\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, root, "config", "extensions.partialClone", "origin")
	gitTestCommand(t, root, "config", "remote.origin.promisor", "true")
	gitTestCommand(t, root, "config", "remote.origin.url", "ext::"+helper)
	t.Setenv("PROMISOR_MARKER", marker)
	if _, err := inspectCleanGitCheckout(context.Background(), root); err == nil {
		t.Fatal("missing promisor blob accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("ext promisor helper executed or network transport attempted: %v", err)
	}
}

func gitTestOutput(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = controlledGitEnv(os.Environ())
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return output
}

func gitTestCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = controlledGitEnv(os.Environ())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
