package executor

import (
	"io"
	"io/fs"
	"os"

	"oroborus.dev/orotools/internal/conditions"
	"oroborus.dev/orotools/internal/planner"
	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/variables"
)

// Context carries the data the executor needs to run a plan: project metadata,
// resolved variables, the target base directory, the recipe-asset FS for
// copy/template sources, and output streams.
type Context struct {
	Project variables.Project
	Vars    map[string]any
	Base    string
	Sources fs.FS      // nil → os.DirFS(Base)
	Stdout  io.Writer
	Stderr  io.Writer
}

func (c Context) sources() fs.FS {
	if c.Sources != nil {
		return c.Sources
	}
	return os.DirFS(c.Base)
}

func (c Context) stdout() io.Writer {
	if c.Stdout != nil {
		return c.Stdout
	}
	return os.Stdout
}

func (c Context) stderr() io.Writer {
	if c.Stderr != nil {
		return c.Stderr
	}
	return os.Stderr
}

// ExecRunner abstracts running an external command for the exec step.
type ExecRunner interface {
	Run(dir, name string, args []string, stdout, stderr io.Writer) error
}

// Severity classifies a step failure so callers can decide abort policy
// (spec section 31 — Fatal vs Recoverable).
type Severity int

const (
	SevNone Severity = iota
	SevRecoverable
	SevFatal
)

// Action describes what the executor did with a step.
type Action string

const (
	ActCreated     Action = "created"
	ActOverwritten Action = "overwritten"
	ActSkipped     Action = "skipped"
	ActRan         Action = "ran"
	ActMessaged    Action = "messaged"
)

// Result is the outcome of executing one step.
type Result struct {
	Step     recipe.Step
	Action   Action
	Severity Severity
	Error    error
}

// Executor runs planned steps. A nil Runner falls back to realExecRunner.
type Executor struct {
	Runner ExecRunner
}

func (e Executor) runner() ExecRunner {
	if e.Runner != nil {
		return e.Runner
	}
	return realExecRunner{}
}

// Execute runs each step of plan in order. It does NOT abort on error: a Result
// with Error set is appended and execution continues (spec section 30). The
// caller decides whether a fatal result aborts the whole pipeline.
func (e Executor) Execute(plan *planner.Plan, ctx Context) []Result {
	if plan == nil {
		return nil
	}
	results := make([]Result, 0, len(plan.Steps))
	condCtx := conditions.Context{Vars: ctx.Vars, Base: ctx.Base}

	for i := range plan.Steps {
		step := plan.Steps[i].Step
		r := Result{Step: step}

		if step.SkipIf != nil {
			ok, err := conditions.Evaluate(*step.SkipIf, condCtx)
			if err != nil {
				r.Error = err
				r.Severity = SevFatal
				results = append(results, r)
				continue
			}
			if ok {
				r.Action = ActSkipped
				results = append(results, r)
				continue
			}
		}

		switch step.Type {
		case "mkdir":
			execMkdir(&r, step, ctx)
		case "copy":
			execCopy(&r, step, ctx)
		case "template":
			execTemplate(&r, step, ctx)
		case "exec":
			execExec(&r, step, ctx, e.runner())
		case "message":
			execMessage(&r, step, ctx)
		default:
			r.Error = errUnknownStep(step.Type)
			r.Severity = SevFatal
		}

		results = append(results, r)
	}
	return results
}

// HasFatal reports whether any Result has a Fatal error.
func HasFatal(results []Result) bool {
	for _, r := range results {
		if r.Error != nil && r.Severity == SevFatal {
			return true
		}
	}
	return false
}

// HasRecoverable reports whether any Result has a Recoverable error.
func HasRecoverable(results []Result) bool {
	for _, r := range results {
		if r.Error != nil && r.Severity == SevRecoverable {
			return true
		}
	}
	return false
}
