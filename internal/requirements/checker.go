package requirements

import (
	"fmt"
	"os/exec"

	"oroborus.dev/orotools/internal/conditions"
	"oroborus.dev/orotools/internal/recipe"
)

// Runner abstracts command execution so the checker can be tested without
// depending on real tools installed. The zero-value realRunner uses os/exec.
type Runner interface {
	LookPath(name string) (string, error)
	Output(name string, args []string) ([]byte, error)
}

// realRunner is the default Runner backed by the host filesystem and PATH.
type realRunner struct{}

func (realRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

// realRunner.Output executa apenas os binários do registry de probes
// (allowlist): qualquer outro nome é rejeitado antes de chegar ao exec.
func (realRunner) Output(name string, args []string) ([]byte, error) {
	switch name {
	case "node":
		return exec.Command("node", args...).Output()
	case "dotnet":
		return exec.Command("dotnet", args...).Output()
	case "git":
		return exec.Command("git", args...).Output()
	case "go":
		return exec.Command("go", args...).Output()
	case "pnpm":
		return exec.Command("pnpm", args...).Output()
	case "npm":
		return exec.Command("npm", args...).Output()
	case "yarn":
		return exec.Command("yarn", args...).Output()
	case "ruby":
		return exec.Command("ruby", args...).Output()
	default:
		return nil, fmt.Errorf("command %q is not an allowed version probe", name)
	}
}

// Checker runs requirement checks. A nil Runner falls back to realRunner.
type Checker struct {
	Runner Runner
}

func (c Checker) runner() Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return realRunner{}
}

// Result is the outcome of checking one Requirement.
type Result struct {
	Requirement  recipe.Requirement
	Skipped      bool   // when evaluated false; not applicable
	Found        bool   // command present on PATH
	FoundVersion string // parsed version, empty when not checked
	Satisfied    bool   // true when Found and (no constraint or constraint met); also true when Skipped
	Reason       string // human-readable explanation when not Satisfied
}

// Check evaluates every requirement against ctx. Requirements whose when
// evaluates false are skipped (Satisfied=true, Skipped=true). The caller
// decides abort policy based on the returned Results.
func (c Checker) Check(reqs []recipe.Requirement, ctx conditions.Context) []Result {
	out := make([]Result, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, c.checkOne(req, ctx))
	}
	return out
}

func (c Checker) checkOne(req recipe.Requirement, ctx conditions.Context) Result {
	r := Result{Requirement: req}

	// 1. conditional requirement
	if req.When != nil {
		ok, err := conditions.Evaluate(*req.When, ctx)
		if err != nil {
			r.Reason = "when: " + err.Error()
			return r
		}
		if !ok {
			r.Skipped = true
			r.Satisfied = true
			return r
		}
	}

	// 2. presence
	runner := c.runner()
	if _, err := runner.LookPath(req.Command); err != nil {
		r.Reason = req.Command + " not found on PATH"
		return r
	}
	r.Found = true

	// 3. version (if constraint declared)
	if req.Version == "" {
		r.Satisfied = true
		return r
	}
	probe, ok := probes[req.Command]
	if !ok {
		r.Reason = "version check not supported for " + req.Command
		return r
	}
	out, err := runner.Output(req.Command, probe.Args)
	if err != nil {
		r.Reason = "probe failed: " + err.Error()
		return r
	}
	ver, err := probe.Parse(out)
	if err != nil {
		r.Reason = "version parse failed: " + err.Error()
		return r
	}
	r.FoundVersion = ver
	ok, err = satisfies(ver, req.Version)
	if err != nil {
		r.Reason = "constraint error: " + err.Error()
		return r
	}
	if !ok {
		r.Reason = fmt.Sprintf("found %s, requires %s", ver, req.Version)
		return r
	}
	r.Satisfied = true
	return r
}
