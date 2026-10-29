// Package onboarding plans and applies opt-in, project-scoped Git onboarding.
package onboarding

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	ignoreBegin = "# >>> orotools onboarding v1 >>>"
	ignoreEnd   = "# <<< orotools onboarding v1 <<<"
	managedHook = "orotools-managed-hook-v2"
)

type Options struct {
	EnableProtection  bool
	ProtectedBranches []string
}
type Plan struct {
	Root, GitDir, CommonDir, HooksDir, IgnorePath, HookPath, StatePath string
	Actions, Conflicts                                                 []string
	options                                                            Options
	authorization                                                      [32]byte
}

var planKey = func() []byte {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic("onboarding authorization key unavailable: " + e.Error())
	}
	return b
}()

func sealPlan(p Plan) [32]byte {
	m := hmac.New(sha256.New, planKey)
	_, _ = fmt.Fprintf(m, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%t\x00%q", p.Root, p.GitDir, p.CommonDir, p.HooksDir, p.IgnorePath, p.HookPath, p.StatePath, p.options.EnableProtection, p.options.ProtectedBranches)
	var out [32]byte
	copy(out[:], m.Sum(nil))
	return out
}
func authorized(p Plan) bool { want := sealPlan(p); return hmac.Equal(want[:], p.authorization[:]) }

type state struct {
	Version              int      `json:"version"`
	IgnoreExisted        bool     `json:"ignore_existed"`
	IgnoreMode           uint32   `json:"ignore_mode"`
	IgnoreOriginal       string   `json:"ignore_original"`
	OriginalMetadataHash string   `json:"original_metadata_sha256_v1"`
	IgnoreInstalledHash  string   `json:"ignore_installed_hash"`
	HookContent          string   `json:"hook_content"`
	HookMode             uint32   `json:"hook_mode"`
	Branches             []string `json:"branches"`
}

func git(root string, args ...string) (string, error) {
	c := exec.Command("git", append([]string{"-C", root}, args...)...)
	b, e := c.Output()
	return strings.TrimSpace(string(b)), e
}
func absClean(s string) (string, error) {
	a, e := filepath.Abs(s)
	if e != nil {
		return "", e
	}
	return filepath.Clean(a), nil
}

// Inspect is read-only. Branch protection is opt-in; default protected names
// come only from legacy PRD (main, develop), never inferred from repository state.
func Inspect(root string, opts ...Options) (Plan, error) {
	r, e := absClean(root)
	if e != nil {
		return Plan{}, e
	}
	top, e := git(r, "rev-parse", "--show-toplevel")
	if e != nil {
		return Plan{}, fmt.Errorf("not a Git worktree: %w", e)
	}
	top, e = absClean(top)
	if e != nil {
		return Plan{}, e
	}
	gd, e := git(r, "rev-parse", "--absolute-git-dir")
	if e != nil {
		return Plan{}, e
	}
	gd, e = absClean(gd)
	if e != nil {
		return Plan{}, e
	}
	cd, e := git(r, "rev-parse", "--git-common-dir")
	if e != nil {
		return Plan{}, e
	}
	if !filepath.IsAbs(cd) {
		cd = filepath.Join(r, cd)
	}
	cd, e = absClean(cd)
	if e != nil {
		return Plan{}, e
	}
	o := Options{}
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.EnableProtection && len(o.ProtectedBranches) == 0 {
		o.ProtectedBranches = []string{"main", "develop"}
	}
	o.ProtectedBranches = append([]string(nil), o.ProtectedBranches...)
	sort.Strings(o.ProtectedBranches)
	for i, branch := range o.ProtectedBranches {
		if branch == "" {
			return Plan{}, fmt.Errorf("protected branch name cannot be empty")
		}
		if i > 0 && branch == o.ProtectedBranches[i-1] {
			return Plan{}, fmt.Errorf("duplicate protected branch %q", branch)
		}
		if _, x := git(r, "check-ref-format", "--branch", branch); x != nil {
			return Plan{}, fmt.Errorf("invalid protected branch %q", branch)
		}
	}
	p := Plan{Root: top, GitDir: gd, CommonDir: cd, HooksDir: filepath.Join(gd, "hooks"), IgnorePath: filepath.Join(top, ".gitignore"), StatePath: filepath.Join(gd, "orotools-onboarding-state-v1.json"), options: o, Actions: []string{"append managed .gitignore block", "write ownership state"}}
	p.HookPath = filepath.Join(p.HooksDir, "pre-merge-commit")
	if o.EnableProtection {
		p.Actions = append(p.Actions, "install pre-commit branch protection hook")
		p.HookPath = filepath.Join(p.HooksDir, "pre-merge-commit")
	}
	if !o.EnableProtection {
		p.Actions = []string{"append managed .gitignore block"}
	}
	// Any effective config entry, including an explicitly empty value, blocks.
	if out, err := git(r, "config", "--show-origin", "--show-scope", "--get-all", "core.hooksPath"); err == nil && strings.TrimSpace(out) != "" {
		p.Conflicts = append(p.Conflicts, "core.hooksPath is configured at an effective Git scope; refusing hook routing changes")
	}
	if out, err := git(r, "worktree", "list", "--porcelain"); err == nil {
		lines := strings.Split(out, "\n")
		count := 0
		for _, line := range lines {
			if strings.HasPrefix(line, "worktree ") {
				count++
			}
		}
		if count > 1 {
			p.Conflicts = append(p.Conflicts, "repository has multiple linked worktrees sharing common Git state; hook installation blocked")
		}
	}
	for _, path := range []string{p.IgnorePath, p.HookPath, filepath.Join(gd, "hooks")} {
		if err := validatePath(path); err != nil {
			p.Conflicts = append(p.Conflicts, err.Error())
		}
	}
	owned := false
	var ownedState state
	if sb, se := os.ReadFile(statePath(p)); se == nil {
		if json.Unmarshal(sb, &ownedState) == nil && validateState(ownedState) == nil {
			owned = true
			p.Actions = []string{"update managed .gitignore", "update ownership state"}
			if len(ownedState.Branches) > 0 {
				p.Actions = append(p.Actions, "update managed branch protection hooks")
			}
			desired := []string(nil)
			if o.EnableProtection {
				desired = append(desired, o.ProtectedBranches...)
			}
			if !sameStrings(ownedState.Branches, desired) {
				p.Conflicts = append(p.Conflicts, "onboarding options differ from installed ownership state; uninstall intact onboarding before changing protection")
			}
		} else {
			p.Conflicts = append(p.Conflicts, "onboarding ownership state is invalid or inconsistent")
		}
	} else if !errors.Is(se, fs.ErrNotExist) {
		p.Conflicts = append(p.Conflicts, "cannot read onboarding ownership state: "+se.Error())
	}
	if b, err := os.ReadFile(p.IgnorePath); err == nil {
		if _, found, pe := parseBlock(b); pe != nil {
			p.Conflicts = append(p.Conflicts, pe.Error())
		} else if found && (!owned || hash(b) != ownedState.IgnoreInstalledHash) {
			p.Conflicts = append(p.Conflicts, "managed ignore block exists without trusted onboarding state")
		}
		if owned {
			if st, se := os.Lstat(p.IgnorePath); se != nil || !st.Mode().IsRegular() || st.Mode().Perm() != fs.FileMode(ownedState.IgnoreMode) {
				p.Conflicts = append(p.Conflicts, "managed .gitignore type/mode drift detected")
			} else if hashMustRead(p.IgnorePath) != ownedState.IgnoreInstalledHash {
				p.Conflicts = append(p.Conflicts, "managed .gitignore drift detected; refusing overwrite")
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		p.Conflicts = append(p.Conflicts, err.Error())
	} else if owned {
		p.Conflicts = append(p.Conflicts, "managed .gitignore is missing")
	}
	protectionOwned := owned && len(ownedState.Branches) > 0
	checkManagedHook := func(name string, required bool) {
		path := filepath.Join(p.HooksDir, name)
		if err := validatePath(path); err != nil {
			p.Conflicts = append(p.Conflicts, err.Error())
			return
		}
		st, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			if required && protectionOwned {
				p.Conflicts = append(p.Conflicts, name+" managed hook is missing")
			}
			return
		}
		if err != nil {
			p.Conflicts = append(p.Conflicts, err.Error())
			return
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
			p.Conflicts = append(p.Conflicts, name+" hook destination is not a regular file")
			return
		}
		b, readErr := os.ReadFile(path)
		wantMode := fs.FileMode(0755)
		if name == "pre-merge-commit" && protectionOwned {
			wantMode = fs.FileMode(ownedState.HookMode)
		}
		if !protectionOwned || readErr != nil || !bytes.Equal(b, hookBytes(Options{EnableProtection: name == "pre-commit" || protectionOwned, ProtectedBranches: ownedState.Branches}, name)) || st.Mode().Perm() != wantMode {
			p.Conflicts = append(p.Conflicts, name+" hook exists or drifted; refusing overwrite")
		}
	}
	checkManagedHook("pre-merge-commit", o.EnableProtection || protectionOwned)
	if o.EnableProtection || protectionOwned {
		checkManagedHook("pre-commit", true)
	}
	if err := validatePath(statePath(p)); err != nil {
		p.Conflicts = append(p.Conflicts, err.Error())
	}
	p.authorization = sealPlan(p)
	return p, nil
}

// PlanApply performs read-only preflight for the exact options Apply will use.
func PlanApply(root string, opts Options) (Plan, error) { return Inspect(root, opts) }

// PlanUninstall performs read-only preflight using ownership-recorded options.
func PlanUninstall(root string) (Plan, error) {
	p, err := Inspect(root)
	if err != nil {
		return Plan{}, err
	}
	b, err := os.ReadFile(statePath(p))
	if err != nil {
		p.Conflicts = append(p.Conflicts, "onboarding ownership state unavailable: "+err.Error())
		p.authorization = sealPlan(p)
		return p, nil
	}
	var st state
	if err = json.Unmarshal(b, &st); err != nil || validateState(st) != nil {
		p.Conflicts = append(p.Conflicts, "onboarding ownership state is invalid or inconsistent")
		p.authorization = sealPlan(p)
		return p, nil
	}
	o := Options{EnableProtection: len(st.Branches) > 0, ProtectedBranches: append([]string(nil), st.Branches...)}
	return Inspect(root, o)
}

func validatePath(path string) error {
	a, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	vol := filepath.VolumeName(a)
	cur := vol + string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(a, cur), string(filepath.Separator))
	for _, part := range parts {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		st, e := os.Lstat(cur)
		if errors.Is(e, fs.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in managed path ancestry: %s", cur)
		}
		if cur != a && !st.IsDir() {
			return fmt.Errorf("non-directory in managed path ancestry: %s", cur)
		}
	}
	return nil
}
func statePath(p Plan) string { return filepath.Join(p.GitDir, "orotools-onboarding-state-v1.json") }
func hashMustRead(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return hash(b)
}
func parseBlock(b []byte) ([]byte, bool, error) {
	lines := bytes.Split(b, []byte("\n"))
	start, end := -1, -1
	for i, l := range lines {
		l = bytes.TrimSuffix(l, []byte("\r"))
		if string(l) == ignoreBegin {
			if start >= 0 {
				return nil, false, fmt.Errorf("multiple managed ignore blocks")
			}
			start = i
		}
		if string(l) == ignoreEnd {
			if end >= 0 {
				return nil, false, fmt.Errorf("multiple managed ignore block endings")
			}
			end = i
		}
	}
	if start < 0 && end < 0 {
		return nil, false, nil
	}
	if start < 0 || end != start+2 {
		return nil, false, fmt.Errorf("malformed or drifted managed ignore block")
	}
	if string(bytes.TrimSuffix(lines[start+1], []byte("\r"))) != ".oroborus/" {
		return nil, false, fmt.Errorf("managed ignore block drifted")
	}
	// Return bytes with exact boundary separator removed only when recorded ownership confirms it.
	return nil, true, nil
}

func hookBytes(o Options, name string) []byte {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# " + managedHook + "\n")
	if name == "pre-merge-commit" {
		b.WriteString("# Git invokes this before creating a merge commit; fast-forwards bypass this hook.\n")
	}
	if name == "pre-commit" {
		b.WriteString("# Git invokes this before commit creation; --no-verify bypasses this hook.\n")
	}
	b.WriteString("branch=$(git symbolic-ref --quiet --short HEAD 2>/dev/null) || exit 0\n")
	for _, branch := range o.ProtectedBranches {
		b.WriteString("if [ \"$branch\" = " + shellQuote(branch) + " ]; then echo 'OroTools: direct commit blocked on protected branch' >&2; exit 1; fi\n")
	}
	b.WriteString("exit 0\n")
	return []byte(b.String())
}
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
func renderIgnore(original []byte) []byte {
	eol := []byte("\n")
	if bytes.Contains(original, []byte("\r\n")) {
		eol = []byte("\r\n")
	}
	suffix := []byte(ignoreBegin)
	suffix = append(suffix, eol...)
	suffix = append(suffix, []byte(".oroborus/")...)
	suffix = append(suffix, eol...)
	suffix = append(suffix, []byte(ignoreEnd)...)
	suffix = append(suffix, eol...)
	separator := []byte(nil)
	if len(original) > 0 && !bytes.HasSuffix(original, []byte("\n")) {
		separator = eol
	}
	return append(append(append([]byte(nil), original...), separator...), suffix...)
}
func validateState(s state) error {
	if s.Version != 1 {
		return fmt.Errorf("unsupported ownership state version")
	}
	orig, e := hex.DecodeString(s.IgnoreOriginal)
	if e != nil {
		return fmt.Errorf("invalid original ignore backup encoding")
	}
	if !s.IgnoreExisted && len(orig) != 0 {
		return fmt.Errorf("absent ignore state contains original bytes")
	}
	if !s.IgnoreExisted && s.IgnoreMode != 0644 {
		return fmt.Errorf("absent ignore state has unexpected mode")
	}
	if s.IgnoreMode > 0777 {
		return fmt.Errorf("invalid original ignore mode")
	}
	if s.OriginalMetadataHash != originalMetadataHash(s.IgnoreExisted, s.IgnoreOriginal, s.IgnoreMode) {
		return fmt.Errorf("original ignore ownership metadata checksum mismatch")
	}
	installedHash, e := hex.DecodeString(s.IgnoreInstalledHash)
	if e != nil || len(installedHash) != sha256.Size {
		return fmt.Errorf("invalid installed ignore digest")
	}
	if hash(renderIgnore(orig)) != s.IgnoreInstalledHash {
		return fmt.Errorf("ignore backup and installed digest are inconsistent")
	}
	if len(s.Branches) == 0 {
		if s.HookContent != "" {
			return fmt.Errorf("hook metadata exists without protection branches")
		}
		return nil
	}
	branches := append([]string(nil), s.Branches...)
	sort.Strings(branches)
	for i, b := range branches {
		if b == "" || (i > 0 && branches[i-1] == b) {
			return fmt.Errorf("invalid protected branch metadata")
		}
	}
	hook, e := hex.DecodeString(s.HookContent)
	if e != nil || !bytes.Equal(hook, hookBytes(Options{EnableProtection: true, ProtectedBranches: branches}, "pre-merge-commit")) {
		return fmt.Errorf("hook metadata is inconsistent")
	}
	if s.HookMode != 0755 {
		return fmt.Errorf("invalid hook mode metadata")
	}
	return nil
}
func originalMetadataHash(existed bool, originalHex string, mode uint32) string {
	payload, _ := json.Marshal(struct {
		Version  int    `json:"version"`
		Existed  bool   `json:"existed"`
		Original string `json:"original_hex"`
		Mode     uint32 `json:"mode"`
	}{1, existed, originalHex, mode})
	return hash(append([]byte("orotools-onboarding-original-metadata-v1\x00"), payload...))
}
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func atomicWrite(path string, b []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	f, e := os.CreateTemp(dir, ".orotools-onboarding-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(tmp, path)
}

func Apply(p Plan) error {
	if !authorized(p) {
		return fmt.Errorf("invalid or altered onboarding plan; inspect again")
	}
	q, e := Inspect(p.Root, p.options)
	if e != nil {
		return e
	}
	if len(q.Conflicts) > 0 {
		return fmt.Errorf("onboarding blocked: %s", strings.Join(q.Conflicts, "; "))
	}
	if p.Root != q.Root || p.GitDir != q.GitDir || p.CommonDir != q.CommonDir || p.IgnorePath != q.IgnorePath || p.HooksDir != q.HooksDir || p.HookPath != q.HookPath || p.StatePath != q.StatePath {
		return fmt.Errorf("stale or forged onboarding plan; inspect again")
	}
	p = q
	if sb, se := os.ReadFile(statePath(p)); se == nil {
		var st state
		if json.Unmarshal(sb, &st) != nil {
			return fmt.Errorf("invalid onboarding ownership state; refusing modification")
		}
		if e = validateState(st); e != nil {
			return fmt.Errorf("invalid onboarding ownership state: %w", e)
		}
		desired := []string(nil)
		if p.options.EnableProtection {
			desired = append(desired, p.options.ProtectedBranches...)
		}
		if !sameStrings(st.Branches, desired) {
			return fmt.Errorf("onboarding options differ from installed ownership state; uninstall intact onboarding before changing protection")
		}
		ib, ie := os.ReadFile(p.IgnorePath)
		clean := ie == nil && hash(ib) == st.IgnoreInstalledHash
		if clean {
			fi, x := os.Lstat(p.IgnorePath)
			clean = x == nil && fi.Mode().IsRegular() && fi.Mode().Perm() == fs.FileMode(st.IgnoreMode)
		}
		for _, n := range []string{"pre-merge-commit", "pre-commit"} {
			if len(st.Branches) == 0 {
				break
			}
			hb, he := os.ReadFile(filepath.Join(p.HooksDir, n))
			hs, hse := os.Lstat(filepath.Join(p.HooksDir, n))
			if he != nil || hse != nil || !hs.Mode().IsRegular() || hs.Mode().Perm() != fs.FileMode(st.HookMode) || !bytes.Equal(hb, hookBytes(Options{EnableProtection: true, ProtectedBranches: st.Branches}, n)) {
				clean = false
			}
		}
		if clean {
			return nil
		}
		return fmt.Errorf("onboarding files drifted from ownership state; refusing overwrite")
	} else if !errors.Is(se, fs.ErrNotExist) {
		return se
	}
	if e = validatePath(p.IgnorePath); e != nil {
		return e
	}
	if e = validatePath(p.HooksDir); e != nil {
		return e
	}
	if e = validatePath(p.HookPath); e != nil {
		return e
	}
	if e = os.MkdirAll(p.HooksDir, 0755); e != nil {
		return e
	}
	if _, e = os.Lstat(p.HookPath); e == nil {
		return fmt.Errorf("hook destination appeared after inspection")
	} else if !errors.Is(e, fs.ErrNotExist) {
		return e
	}
	orig, readErr := os.ReadFile(p.IgnorePath)
	existed := readErr == nil
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return readErr
	}
	if existed {
		if _, found, pe := parseBlock(orig); pe != nil || found {
			if pe != nil {
				return pe
			}
			return fmt.Errorf("managed block exists without ownership state")
		}
	}
	ignoreMode := uint32(0644)
	if existed {
		if fi, x := os.Stat(p.IgnorePath); x == nil {
			ignoreMode = uint32(fi.Mode().Perm())
		} else {
			return x
		}
	}
	installed := renderIgnore(orig)
	hooks := map[string][]byte{}
	if p.options.EnableProtection {
		hooks["pre-merge-commit"] = hookBytes(p.options, "pre-merge-commit")
		hooks["pre-commit"] = hookBytes(p.options, "pre-commit")
	}
	for n := range hooks {
		path := filepath.Join(p.HooksDir, n)
		if e = validatePath(path); e != nil {
			return e
		}
		if _, x := os.Lstat(path); x == nil {
			return fmt.Errorf("hook %s appeared after inspection", n)
		} else if !errors.Is(x, fs.ErrNotExist) {
			return x
		}
	}
	// Preflight everything before first write. State is written last; failures roll back only exact bytes written here.
	if e = atomicWrite(p.IgnorePath, installed, fs.FileMode(ignoreMode)); e != nil {
		return e
	}
	rollback := func(cause error) error {
		var rollbackErrors []error
		for f, expected := range hooks {
			path := filepath.Join(p.HooksDir, f)
			cur, x := os.ReadFile(path)
			if x == nil && bytes.Equal(cur, expected) {
				if re := os.Remove(path); re != nil {
					rollbackErrors = append(rollbackErrors, re)
				}
			}
		}
		cur, x := os.ReadFile(p.IgnorePath)
		if x == nil && bytes.Equal(cur, installed) {
			if existed {
				if re := atomicWrite(p.IgnorePath, orig, fs.FileMode(ignoreMode)); re != nil {
					rollbackErrors = append(rollbackErrors, re)
				}
			} else {
				if re := os.Remove(p.IgnorePath); re != nil {
					rollbackErrors = append(rollbackErrors, re)
				}
			}
		}
		if len(rollbackErrors) > 0 {
			return fmt.Errorf("%w; rollback incomplete: %v", cause, errors.Join(rollbackErrors...))
		}
		return cause
	}
	for n, b := range hooks {
		path := filepath.Join(p.HooksDir, n)
		if e = atomicWrite(path, b, 0755); e != nil {
			return rollback(e)
		}
	}
	originalHex := hex.EncodeToString(orig)
	st := state{Version: 1, IgnoreExisted: existed, IgnoreMode: ignoreMode, IgnoreOriginal: originalHex, OriginalMetadataHash: originalMetadataHash(existed, originalHex, ignoreMode), IgnoreInstalledHash: hash(installed), HookContent: hex.EncodeToString(hooks["pre-merge-commit"]), HookMode: 0755}
	if p.options.EnableProtection {
		st.Branches = append([]string(nil), p.options.ProtectedBranches...)
	}
	data, e := json.Marshal(st)
	if e != nil {
		return rollback(e)
	}
	if e = atomicWrite(statePath(p), append(data, '\n'), 0600); e != nil {
		return rollback(e)
	}
	return nil
}

func Uninstall(p Plan) error {
	if !authorized(p) {
		return fmt.Errorf("invalid or altered onboarding plan; inspect again")
	}
	q, e := Inspect(p.Root, p.options)
	if e != nil {
		return e
	}
	if len(q.Conflicts) > 0 {
		return fmt.Errorf("uninstall blocked: %s", strings.Join(q.Conflicts, "; "))
	}
	if p.Root != q.Root || p.GitDir != q.GitDir || p.CommonDir != q.CommonDir || p.IgnorePath != q.IgnorePath || p.HooksDir != q.HooksDir || p.HookPath != q.HookPath || p.StatePath != q.StatePath {
		return fmt.Errorf("stale or forged onboarding plan; inspect again")
	}
	p = q
	if e = validatePath(p.Root); e != nil {
		return e
	}
	if e = validatePath(p.GitDir); e != nil {
		return e
	}
	if e = validatePath(p.IgnorePath); e != nil {
		return e
	}
	if e = validatePath(p.HooksDir); e != nil {
		return e
	}
	stateBytes, e := os.ReadFile(statePath(p))
	if e != nil {
		return fmt.Errorf("onboarding ownership state unavailable: %w", e)
	}
	var st state
	if e = json.Unmarshal(stateBytes, &st); e != nil || st.Version != 1 {
		return fmt.Errorf("invalid onboarding ownership state")
	}
	if e = validateState(st); e != nil {
		return fmt.Errorf("invalid onboarding ownership state: %w", e)
	}
	ignore, e := os.ReadFile(p.IgnorePath)
	if errors.Is(e, fs.ErrNotExist) {
		if st.IgnoreExisted {
			return fmt.Errorf("managed .gitignore missing; preserving hooks")
		}
	} else if e != nil {
		return e
	}
	if !st.IgnoreExisted {
		if hash(ignore) != st.IgnoreInstalledHash {
			return fmt.Errorf(".gitignore drift detected; preserving all files")
		}
	} else if hash(ignore) != st.IgnoreInstalledHash {
		return fmt.Errorf(".gitignore drift detected; preserving all files")
	}
	if si, x := os.Lstat(p.IgnorePath); x != nil || !si.Mode().IsRegular() || si.Mode().Perm() != fs.FileMode(st.IgnoreMode) {
		return fmt.Errorf("managed .gitignore type/mode drift; preserving all files")
	}
	hookNames := []string{}
	if len(st.Branches) > 0 {
		hookNames = append(hookNames, "pre-merge-commit", "pre-commit")
	}
	expected := map[string][]byte{}
	for _, n := range hookNames {
		b, x := os.ReadFile(filepath.Join(p.HooksDir, n))
		if x != nil {
			return fmt.Errorf("managed hook %s unavailable; preserving all files: %w", n, x)
		}
		if !bytes.Equal(b, hookBytes(Options{EnableProtection: n == "pre-commit", ProtectedBranches: st.Branches}, n)) {
			return fmt.Errorf("managed hook %s drift detected; preserving all files", n)
		}
		si, x := os.Lstat(filepath.Join(p.HooksDir, n))
		if x != nil || !si.Mode().IsRegular() || si.Mode().Perm() != 0755 {
			return fmt.Errorf("managed hook %s type/mode drift; preserving all files", n)
		}
		expected[n] = b
	}
	// Revalidate destinations then remove only files whose bytes still match.
	for _, n := range hookNames {
		path := filepath.Join(p.HooksDir, n)
		if e = validatePath(path); e != nil {
			return e
		}
		b, x := os.ReadFile(path)
		if x != nil || !bytes.Equal(b, expected[n]) {
			return fmt.Errorf("hook changed during uninstall preflight; preserving remaining files")
		}
	}
	removed := []string{}
	rollbackHooks := func() []error {
		var failures []error
		for _, n := range removed {
			if re := atomicWrite(filepath.Join(p.HooksDir, n), expected[n], 0755); re != nil {
				failures = append(failures, fmt.Errorf("restore %s: %w", n, re))
			}
		}
		return failures
	}
	for _, n := range hookNames {
		path := filepath.Join(p.HooksDir, n)
		b, x := os.ReadFile(path)
		if x != nil || !bytes.Equal(b, expected[n]) {
			return fmt.Errorf("hook changed during uninstall; ignore restoration completed, hook preserved")
		}
		if e = os.Remove(path); e != nil {
			if failures := rollbackHooks(); len(failures) > 0 {
				return fmt.Errorf("remove hook: %w; rollback incomplete: %v", e, errors.Join(failures...))
			}
			return e
		}
		removed = append(removed, n)
	}
	installedMode := fs.FileMode(0644)
	if st.IgnoreExisted {
		installedMode = fs.FileMode(st.IgnoreMode)
	}
	if st.IgnoreExisted {
		orig, _ := hex.DecodeString(st.IgnoreOriginal)
		if e = atomicWrite(p.IgnorePath, orig, fs.FileMode(st.IgnoreMode)); e != nil {
			if failures := rollbackHooks(); len(failures) > 0 {
				return fmt.Errorf("restore .gitignore: %w; rollback incomplete: %v", e, errors.Join(failures...))
			}
			return e
		}
	} else if e = os.Remove(p.IgnorePath); e != nil && !errors.Is(e, fs.ErrNotExist) {
		if failures := rollbackHooks(); len(failures) > 0 {
			return fmt.Errorf("remove .gitignore: %w; rollback incomplete: %v", e, errors.Join(failures...))
		}
		return e
	}
	if e = os.Remove(statePath(p)); e != nil && !errors.Is(e, fs.ErrNotExist) {
		var failures []error
		if re := atomicWrite(p.IgnorePath, ignore, installedMode); re != nil {
			failures = append(failures, fmt.Errorf("restore installed .gitignore: %w", re))
		}
		failures = append(failures, rollbackHooks()...)
		if len(failures) > 0 {
			return fmt.Errorf("remove ownership state: %w; rollback incomplete: %v", e, errors.Join(failures...))
		}
		return e
	}
	return nil
}
