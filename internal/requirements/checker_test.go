package requirements

import (
	"errors"
	"strings"
	"testing"

	"oroborus.dev/orotools/internal/conditions"
	"oroborus.dev/orotools/internal/recipe"
)

// fakeRunner is a deterministic Runner for tests. lookPath maps command→error
// (nil means found); output maps command→(stdout, error).
type fakeRunner struct {
	lookPath map[string]error
	output   map[string]fakeOut
}

type fakeOut struct {
	stdout []byte
	err    error
}

func (f fakeRunner) LookPath(name string) (string, error) {
	if err, ok := f.lookPath[name]; ok {
		return "", err
	}
	return name, nil
}

func (f fakeRunner) Output(name string, _ []string) ([]byte, error) {
	if o, ok := f.output[name]; ok {
		return o.stdout, o.err
	}
	return nil, errors.New("no fake output for " + name)
}

func anyVar(v any) *any { return &v }

func TestCheck_PresenceOK(t *testing.T) {
	r := recipe.Requirement{Command: "git"}
	res := Checker{Runner: fakeRunner{}}.Check([]recipe.Requirement{r}, conditions.Context{})[0]
	if !res.Found || !res.Satisfied {
		t.Errorf("expected Found+Satisfied, got %+v", res)
	}
}

func TestCheck_PresenceMissing(t *testing.T) {
	r := recipe.Requirement{Command: "ghost-tool"}
	runner := fakeRunner{lookPath: map[string]error{"ghost-tool": errors.New("not found")}}
	res := Checker{Runner: runner}.Check([]recipe.Requirement{r}, conditions.Context{})[0]
	if res.Found || res.Satisfied {
		t.Errorf("expected not found, got %+v", res)
	}
	if !strings.Contains(res.Reason, "not found on PATH") {
		t.Errorf("reason should cite PATH: %q", res.Reason)
	}
}

func TestCheck_VersionSatisfied(t *testing.T) {
	r := recipe.Requirement{Command: "node", Version: ">=24"}
	runner := fakeRunner{output: map[string]fakeOut{
		"node": {stdout: []byte("v24.1.0\n")},
	}}
	res := Checker{Runner: runner}.Check([]recipe.Requirement{r}, conditions.Context{})[0]
	if !res.Satisfied || res.FoundVersion != "24.1.0" {
		t.Errorf("expected satisfied with version 24.1.0, got %+v", res)
	}
}

func TestCheck_VersionTooLow(t *testing.T) {
	r := recipe.Requirement{Command: "node", Version: ">=24"}
	runner := fakeRunner{output: map[string]fakeOut{
		"node": {stdout: []byte("v23.0.0\n")},
	}}
	res := Checker{Runner: runner}.Check([]recipe.Requirement{r}, conditions.Context{})[0]
	if res.Satisfied {
		t.Errorf("expected not satisfied, got %+v", res)
	}
	if !strings.Contains(res.Reason, "found 23.0.0") || !strings.Contains(res.Reason, ">=24") {
		t.Errorf("reason should cite found/requires: %q", res.Reason)
	}
}

func TestCheck_VersionUnsupportedCommand(t *testing.T) {
	r := recipe.Requirement{Command: "weird-tool", Version: ">=1"}
	res := Checker{Runner: fakeRunner{}}.Check([]recipe.Requirement{r}, conditions.Context{})[0]
	if res.Satisfied {
		t.Errorf("expected not satisfied for unsupported command, got %+v", res)
	}
	if !strings.Contains(res.Reason, "not supported") {
		t.Errorf("reason should cite not supported: %q", res.Reason)
	}
}

func TestCheck_ProbeFailure(t *testing.T) {
	r := recipe.Requirement{Command: "node", Version: ">=24"}
	runner := fakeRunner{output: map[string]fakeOut{
		"node": {err: errors.New("exit status 1")},
	}}
	res := Checker{Runner: runner}.Check([]recipe.Requirement{r}, conditions.Context{})[0]
	if res.Satisfied {
		t.Errorf("expected not satisfied, got %+v", res)
	}
	if !strings.Contains(res.Reason, "probe failed") {
		t.Errorf("reason should cite probe failed: %q", res.Reason)
	}
}

func TestCheck_WhenSkips(t *testing.T) {
	r := recipe.Requirement{
		Command: "pnpm",
		When:    &recipe.Condition{Variable: "package_manager", Equals: anyVar("npm")},
	}
	ctx := conditions.Context{Vars: map[string]any{"package_manager": "pnpm"}} // not npm
	res := Checker{Runner: fakeRunner{}}.Check([]recipe.Requirement{r}, ctx)[0]
	if !res.Skipped || !res.Satisfied {
		t.Errorf("expected skipped+satisfied, got %+v", res)
	}
}

func TestCheck_WhenAppliesWhenTrue(t *testing.T) {
	r := recipe.Requirement{
		Command: "pnpm",
		When:    &recipe.Condition{Variable: "package_manager", Equals: anyVar("pnpm")},
	}
	ctx := conditions.Context{Vars: map[string]any{"package_manager": "pnpm"}}
	res := Checker{Runner: fakeRunner{}}.Check([]recipe.Requirement{r}, ctx)[0]
	if res.Skipped {
		t.Errorf("expected NOT skipped, got %+v", res)
	}
	if !res.Satisfied {
		t.Errorf("expected satisfied (present, no version), got %+v", res)
	}
}

func TestCheck_WhenErrorPropagates(t *testing.T) {
	r := recipe.Requirement{
		Command: "pnpm",
		When:    &recipe.Condition{Variable: "missing", Equals: anyVar("x")},
	}
	res := Checker{Runner: fakeRunner{}}.Check([]recipe.Requirement{r}, conditions.Context{Vars: map[string]any{}})[0]
	if res.Satisfied {
		t.Errorf("expected not satisfied when when-evaluation errors, got %+v", res)
	}
	if !strings.Contains(res.Reason, "when:") {
		t.Errorf("reason should cite when error: %q", res.Reason)
	}
}

func TestCheck_MultipleResultsAllReturned(t *testing.T) {
	reqs := []recipe.Requirement{
		{Command: "git"},
		{Command: "ghost"},
		{Command: "node", Version: ">=24"},
	}
	runner := fakeRunner{
		lookPath: map[string]error{"ghost": errors.New("nf")},
		output:   map[string]fakeOut{"node": {stdout: []byte("v24.0.0\n")}},
	}
	results := Checker{Runner: runner}.Check(reqs, conditions.Context{})
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// all three should be present regardless of pass/fail
}
