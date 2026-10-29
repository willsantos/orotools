package onboarding

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repo(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", d}, args...)...)
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %s %v", args, b, e)
		}
	}
	run("init")
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "Test")
	return d
}

func TestApplyPreservesIgnoreBytesAndIsIdempotent(t *testing.T) {
	d := repo(t)
	original := []byte("# local\r\n/node_modules/\r\n")
	if e := os.WriteFile(filepath.Join(d, ".gitignore"), original, 0644); e != nil {
		t.Fatal(e)
	}
	p, e := Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = Apply(p); e != nil {
		t.Fatal(e)
	}
	first, _ := os.ReadFile(p.IgnorePath)
	if !strings.HasPrefix(string(first), string(original)) {
		t.Fatal("existing bytes changed")
	}
	if e = Apply(p); e != nil {
		t.Fatal(e)
	}
	second, _ := os.ReadFile(p.IgnorePath)
	if string(first) != string(second) {
		t.Fatal("second apply changed files")
	}
	if e = Uninstall(p); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(p.IgnorePath)
	if string(after) != string(original) {
		t.Fatalf("ignore restore mismatch: %q", after)
	}
}

func TestBlocksExistingHookAndCustomHooksPath(t *testing.T) {
	d := repo(t)
	p, e := Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(p.HooksDir, 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p.HookPath, []byte("#!/bin/sh\necho local\n"), 0755); e != nil {
		t.Fatal(e)
	}
	p, e = Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Conflicts) == 0 {
		t.Fatal("expected hook conflict")
	}
	if e = Apply(p); e == nil {
		t.Fatal("apply must block")
	}
	d = repo(t)
	custom := filepath.Join(d, ".custom-hooks")
	c := exec.Command("git", "-C", d, "config", "--local", "core.hooksPath", custom)
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%s %v", b, e)
	}
	p, e = Inspect(d)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Conflicts) == 0 {
		t.Fatal("expected hooksPath conflict")
	}
}

func TestWorktreeIsBlocked(t *testing.T) {
	d := repo(t)
	c := exec.Command("git", "-C", d, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%s %v", b, e)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	c = exec.Command("git", "-C", d, "worktree", "add", wt)
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%s %v", b, e)
	}
	p, e := Inspect(wt)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Conflicts) == 0 {
		t.Fatal("worktree must be blocked")
	}
}

func TestUninstallPreservesDrift(t *testing.T) {
	d := repo(t)
	p, e := Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = Apply(p); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p.HookPath, []byte("local edit"), 0755); e != nil {
		t.Fatal(e)
	}
	if e = Uninstall(p); e == nil {
		t.Fatal("expected drift error")
	}
	b, _ := os.ReadFile(p.HookPath)
	if string(b) != "local edit" {
		t.Fatal("drift was lost")
	}
}

func TestRejectsSymlinksForgedPlansAndSharedWorktrees(t *testing.T) {
	d := repo(t)
	outside := filepath.Join(t.TempDir(), "ignore")
	_ = os.WriteFile(outside, []byte("keep"), 0644)
	_ = os.Symlink(outside, filepath.Join(d, ".gitignore"))
	p, err := Inspect(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Conflicts) == 0 {
		t.Fatal("ignore symlink not rejected")
	}
	if err = Apply(p); err == nil {
		t.Fatal("symlink apply accepted")
	}
	d = repo(t)
	p, err = Inspect(d)
	if err != nil {
		t.Fatal(err)
	}
	forged := t.TempDir()
	p.IgnorePath = filepath.Join(forged, "ignore")
	if err = Apply(p); err == nil {
		t.Fatal("forged plan accepted")
	}
	if _, err = os.Stat(p.IgnorePath); !os.IsNotExist(err) {
		t.Fatal("forged destination touched")
	}
	d = repo(t)
	c := exec.Command("git", "-C", d, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "init")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%s %v", b, e)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	c = exec.Command("git", "-C", d, "worktree", "add", "-b", "other", wt)
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%s %v", b, e)
	}
	p, err = Inspect(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Conflicts) == 0 {
		t.Fatal("main worktree sharing hooks not rejected")
	}
}

func TestIgnoreExactRoundTripAndDriftBlocksBothOperations(t *testing.T) {
	for _, original := range [][]byte{nil, []byte("local-without-newline"), []byte("CRLF\r\nentry\r\n")} {
		t.Run(fmt.Sprintf("%q", original), func(t *testing.T) {
			d := repo(t)
			p, e := Inspect(d)
			if e != nil {
				t.Fatal(e)
			}
			if len(original) > 0 {
				_ = os.WriteFile(p.IgnorePath, original, 0644)
			}
			p, e = Inspect(d)
			if e != nil {
				t.Fatal(e)
			}
			if e = Apply(p); e != nil {
				t.Fatal(e)
			}
			if e = Uninstall(p); e != nil {
				t.Fatal(e)
			}
			b, e := os.ReadFile(p.IgnorePath)
			if len(original) == 0 {
				if !os.IsNotExist(e) {
					t.Fatalf("formerly absent file remains: %q", b)
				}
			} else if e != nil || !bytes.Equal(b, original) {
				t.Fatalf("roundtrip: %q %v", b, e)
			}
		})
	}
	d := repo(t)
	p, e := Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = Apply(p); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p.IgnorePath)
	b = bytes.Replace(b, []byte(".oroborus/"), []byte("user-important-entry"), 1)
	_ = os.WriteFile(p.IgnorePath, b, 0644)
	if e = Apply(p); e == nil {
		t.Fatal("apply accepted drift")
	}
	if e = Uninstall(p); e == nil {
		t.Fatal("uninstall accepted drift")
	}
	if _, e = os.Stat(p.HookPath); e != nil {
		t.Fatal("drift handling removed hook")
	}
}

func TestEffectiveHooksPathBlockedAtAllScopes(t *testing.T) {
	for _, scope := range []string{"local", "global", "worktree", "empty"} {
		t.Run(scope, func(t *testing.T) {
			d := repo(t)
			switch scope {
			case "global":
				g := filepath.Join(t.TempDir(), "config")
				_ = os.WriteFile(g, []byte("[core]\n hooksPath = .hooks\n"), 0600)
				t.Setenv("GIT_CONFIG_GLOBAL", g)
			case "worktree":
				for _, a := range [][]string{{"config", "extensions.worktreeConfig", "true"}, {"config", "--worktree", "core.hooksPath", ".hooks"}} {
					c := exec.Command("git", append([]string{"-C", d}, a...)...)
					if b, e := c.CombinedOutput(); e != nil {
						t.Fatalf("%s %v", b, e)
					}
				}
			default:
				value := ".hooks"
				if scope == "empty" {
					value = ""
				}
				c := exec.Command("git", "-C", d, "config", "--local", "core.hooksPath", value)
				if b, e := c.CombinedOutput(); e != nil {
					t.Fatalf("%s %v", b, e)
				}
			}
			p, e := Inspect(d)
			if e != nil {
				t.Fatal(e)
			}
			if len(p.Conflicts) == 0 {
				t.Fatal("effective hooksPath not blocked")
			}
		})
	}
}

func TestHookSymlinkAndModeDriftAreBlocked(t *testing.T) {
	d := repo(t)
	p, e := Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	outside := t.TempDir()
	if e = os.Rename(p.HooksDir, p.HooksDir+"-saved"); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, p.HooksDir); e != nil {
		t.Fatal(e)
	}
	p, e = Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Conflicts) == 0 {
		t.Fatal("hooks directory symlink not rejected")
	}
	if e = Apply(p); e == nil {
		t.Fatal("symlink hook destination accepted")
	}
	if _, e = os.Stat(filepath.Join(outside, "pre-merge-commit")); !os.IsNotExist(e) {
		t.Fatal("external hook written")
	}
	d = repo(t)
	p, e = Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = Apply(p); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(p.HookPath, 0644); e != nil {
		t.Fatal(e)
	}
	p, e = Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Conflicts) == 0 {
		t.Fatal("hook mode drift not detected")
	}
	if e = Uninstall(p); e == nil {
		t.Fatal("uninstall accepted mode drift")
	}
}

func TestHookPreflightPreventsIgnorePartialWrite(t *testing.T) {
	d := repo(t)
	p, e := Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(p.HookPath, 0755); e != nil {
		t.Fatal(e)
	}
	original := []byte("keep\r\n")
	if e = os.WriteFile(p.IgnorePath, original, 0644); e != nil {
		t.Fatal(e)
	}
	p, e = Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = Apply(p); e == nil {
		t.Fatal("hook directory conflict accepted")
	}
	after, e := os.ReadFile(p.IgnorePath)
	if e != nil || !bytes.Equal(after, original) {
		t.Fatalf("partial ignore write: %q %v", after, e)
	}
}

func TestBranchNameCannotInjectShellCommands(t *testing.T) {
	d := repo(t)
	branch := "safe$(touch${IFS}review-injected)"
	p, e := Inspect(d, Options{EnableProtection: true, ProtectedBranches: []string{branch}})
	if e != nil {
		t.Fatal(e)
	}
	if e = Apply(p); e != nil {
		t.Fatal(e)
	}
	c := exec.Command("git", "-C", d, "symbolic-ref", "HEAD", "refs/heads/feature")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%s %v", b, e)
	}
	c = exec.Command("git", "-C", d, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "feature")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("feature commit failed: %s %v", b, e)
	}
	if _, e = os.Stat(filepath.Join(d, "review-injected")); !os.IsNotExist(e) {
		t.Fatal("branch name executed shell command")
	}
}

func TestOwnershipMetadataTamperingFailsClosed(t *testing.T) {
	for _, mutate := range []struct {
		name   string
		change func(*state)
	}{{"invalid-hex", func(s *state) { s.IgnoreOriginal = "zz" }}, {"backup-mismatch", func(s *state) { s.IgnoreOriginal = hex.EncodeToString([]byte("replacement")) }}, {"false-existence", func(s *state) { s.IgnoreExisted = false }}, {"empty-digest", func(s *state) { s.IgnoreInstalledHash = "" }}, {"mode", func(s *state) { s.IgnoreMode = 010000 }}} {
		t.Run(mutate.name, func(t *testing.T) {
			d := repo(t)
			p, e := Inspect(d, Options{EnableProtection: true})
			if e != nil {
				t.Fatal(e)
			}
			original := []byte("user-important\r\n")
			if e = os.WriteFile(p.IgnorePath, original, 0600); e != nil {
				t.Fatal(e)
			}
			p, e = Inspect(d, Options{EnableProtection: true})
			if e != nil {
				t.Fatal(e)
			}
			if e = Apply(p); e != nil {
				t.Fatal(e)
			}
			b, e := os.ReadFile(statePath(p))
			if e != nil {
				t.Fatal(e)
			}
			var s state
			if e = json.Unmarshal(b, &s); e != nil {
				t.Fatal(e)
			}
			mutate.change(&s)
			b, _ = json.Marshal(s)
			if e = os.WriteFile(statePath(p), b, 0600); e != nil {
				t.Fatal(e)
			}
			installed, _ := os.ReadFile(p.IgnorePath)
			hook, _ := os.ReadFile(p.HookPath)
			if e = Uninstall(p); e == nil {
				t.Fatal("tampered state accepted")
			}
			after, e := os.ReadFile(p.IgnorePath)
			if e != nil || !bytes.Equal(after, installed) {
				t.Fatalf("ignore changed: %q %v", after, e)
			}
			afterHook, e := os.ReadFile(p.HookPath)
			if e != nil || !bytes.Equal(afterHook, hook) {
				t.Fatalf("hook changed: %v", e)
			}
		})
	}
}

func TestProtectionOptionsChangesFailWithoutMutation(t *testing.T) {
	t.Run("enable-after-ignore-only", func(t *testing.T) {
		d := repo(t)
		p, _ := Inspect(d)
		if e := Apply(p); e != nil {
			t.Fatal(e)
		}
		before, _ := os.ReadFile(p.IgnorePath)
		enabled, _ := Inspect(d, Options{EnableProtection: true})
		if e := Apply(enabled); e == nil {
			t.Fatal("enabled protection silently ignored")
		}
		after, _ := os.ReadFile(p.IgnorePath)
		if !bytes.Equal(before, after) {
			t.Fatal("ignore changed")
		}
		if _, e := os.Stat(enabled.HookPath); !os.IsNotExist(e) {
			t.Fatal("hook partially installed")
		}
	})
	t.Run("branch-list-change", func(t *testing.T) {
		d := repo(t)
		old, _ := Inspect(d, Options{EnableProtection: true})
		if e := Apply(old); e != nil {
			t.Fatal(e)
		}
		hook, _ := os.ReadFile(old.HookPath)
		next, _ := Inspect(d, Options{EnableProtection: true, ProtectedBranches: []string{"release"}})
		if e := Apply(next); e == nil {
			t.Fatal("branch change silently applied")
		}
		after, _ := os.ReadFile(old.HookPath)
		if !bytes.Equal(hook, after) {
			t.Fatal("existing hook changed")
		}
	})
}

func TestIgnoreInstallPreservesOriginalMode(t *testing.T) {
	d := repo(t)
	p, _ := Inspect(d)
	_ = os.WriteFile(p.IgnorePath, []byte("keep"), 0600)
	p, _ = Inspect(d)
	if e := Apply(p); e != nil {
		t.Fatal(e)
	}
	fi, e := os.Stat(p.IgnorePath)
	if e != nil {
		t.Fatal(e)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("mode changed: %v", fi.Mode().Perm())
	}
}

func TestOriginalMetadataChecksumRejectsIsolatedTampering(t *testing.T) {
	for _, tc := range []struct {
		name     string
		original []byte
		mode     fs.FileMode
		mutate   func(*state)
	}{
		{"existing-empty-marked-absent", []byte{}, 0644, func(s *state) { s.IgnoreExisted = false }},
		{"newline-removed-from-backup", []byte("user\n"), 0644, func(s *state) { s.IgnoreOriginal = hex.EncodeToString([]byte("user")) }},
		{"mode-broadened", []byte("private"), 0600, func(s *state) { s.IgnoreMode = 0644 }},
		{"forged-backup-and-installed-digest", []byte("original\r\n"), 0600, func(s *state) {
			s.IgnoreOriginal = hex.EncodeToString([]byte("forged"))
			s.IgnoreInstalledHash = hash(renderIgnore([]byte("forged")))
		}}} {
		t.Run(tc.name, func(t *testing.T) {
			d := repo(t)
			p, e := Inspect(d, Options{EnableProtection: true})
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(p.IgnorePath, tc.original, tc.mode); e != nil {
				t.Fatal(e)
			}
			p, e = Inspect(d, Options{EnableProtection: true})
			if e != nil {
				t.Fatal(e)
			}
			if e = Apply(p); e != nil {
				t.Fatal(e)
			}
			installed, _ := os.ReadFile(p.IgnorePath)
			hook, _ := os.ReadFile(p.HookPath)
			raw, e := os.ReadFile(statePath(p))
			if e != nil {
				t.Fatal(e)
			}
			var s state
			if e = json.Unmarshal(raw, &s); e != nil {
				t.Fatal(e)
			}
			tc.mutate(&s)
			raw, _ = json.Marshal(s)
			if e = os.WriteFile(statePath(p), raw, 0600); e != nil {
				t.Fatal(e)
			}
			if e = Apply(p); e == nil {
				t.Fatal("Apply accepted altered ownership state")
			}
			if e = Uninstall(p); e == nil {
				t.Fatal("Uninstall accepted altered ownership state")
			}
			actual, e := os.ReadFile(p.IgnorePath)
			if e != nil || !bytes.Equal(actual, installed) {
				t.Fatalf("ignore changed: %v", e)
			}
			actual, e = os.ReadFile(p.HookPath)
			if e != nil || !bytes.Equal(actual, hook) {
				t.Fatalf("hook changed: %v", e)
			}
		})
	}
}

func TestInstalledIgnoreModeDriftBlocksApplyAndUninstall(t *testing.T) {
	d := repo(t)
	p, e := Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p.IgnorePath, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	p, e = Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = Apply(p); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(p.IgnorePath)
	if e = os.Chmod(p.IgnorePath, 0644); e != nil {
		t.Fatal(e)
	}
	if e = Apply(p); e == nil {
		t.Fatal("Apply accepted installed mode drift")
	}
	if e = Uninstall(p); e == nil {
		t.Fatal("Uninstall accepted installed mode drift")
	}
	after, _ := os.ReadFile(p.IgnorePath)
	if !bytes.Equal(before, after) {
		t.Fatal("mode drift handling changed bytes")
	}
	if _, e = os.Stat(p.HookPath); e != nil {
		t.Fatal("hook was removed")
	}
}

func TestOptInBranchHooksEnforceWithGitLimitations(t *testing.T) {
	d := repo(t)
	c := exec.Command("git", "-C", d, "symbolic-ref", "HEAD", "refs/heads/main")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%s %v", b, e)
	}
	p, e := Inspect(d, Options{EnableProtection: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = Apply(p); e != nil {
		t.Fatal(e)
	}
	commit := exec.Command("git", "-C", d, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "blocked")
	if b, e := commit.CombinedOutput(); e == nil || !strings.Contains(string(b), "direct commit blocked") {
		t.Fatalf("protected commit passed: %s %v", b, e)
	}
	c = exec.Command("git", "-C", d, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--no-verify", "--allow-empty", "-m", "initial")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("initial commit: %s %v", b, e)
	}
	c = exec.Command("git", "-C", d, "checkout", "-b", "feature")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%s %v", b, e)
	}
	c = exec.Command("git", "-C", d, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "feature")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("feature commit blocked: %s %v", b, e)
	}
	c = exec.Command("git", "-C", d, "checkout", "main")
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("%s %v", b, e)
	}
	c = exec.Command("git", "-C", d, "merge", "--no-ff", "--no-edit", "feature")
	if b, e := c.CombinedOutput(); e == nil || !strings.Contains(string(b), "direct commit blocked") {
		t.Fatalf("protected merge passed: %s %v", b, e)
	}
}
