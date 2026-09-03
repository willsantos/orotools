package executor

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"

	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/safeexec"
)

func errUnknownStep(typ string) error {
	return fmt.Errorf("unknown step type %q", typ)
}

// execMkdir creates the directory at step.Path under Base, recursively. If the
// directory already exists it is reported as skipped (idempotent continue,
// spec section 28).
func execMkdir(r *Result, step recipe.Step, ctx Context) {
	target, err := safeJoin(ctx.Base, step.Path)
	if err != nil {
		r.Error = err
		r.Severity = SevFatal
		return
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		r.Action = ActSkipped
		return
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		r.Error = fmt.Errorf("mkdir %s: %w", target, err)
		r.Severity = SevFatal
		return
	}
	r.Action = ActCreated
}

// execCopy copies a recipe asset (step.Source from Sources FS) to step.Destination
// under Base. copy follows create-only idempotency: existing destinations are
// skipped (spec section 29 — mode applies to template; copy never overwrites).
func execCopy(r *Result, step recipe.Step, ctx Context) {
	src, err := fs.ReadFile(ctx.sources(), step.Source)
	if err != nil {
		r.Error = fmt.Errorf("copy read source %q: %w", step.Source, err)
		r.Severity = SevFatal
		return
	}
	dest, err := safeJoin(ctx.Base, step.Destination)
	if err != nil {
		r.Error = err
		r.Severity = SevFatal
		return
	}
	if _, err := os.Stat(dest); err == nil {
		r.Action = ActSkipped
		return
	}
	if err := writeFile(dest, src); err != nil {
		r.Error = fmt.Errorf("copy write %q: %w", dest, err)
		r.Severity = SevFatal
		return
	}
	r.Action = ActCreated
}

// execTemplate renders a recipe asset (step.Source) as a text/template against
// {Project, Vars} and writes to step.Destination. mode controls create vs
// overwrite (default create). Spec sections 18 and 29.
func execTemplate(r *Result, step recipe.Step, ctx Context) {
	tmplBytes, err := fs.ReadFile(ctx.sources(), step.Source)
	if err != nil {
		r.Error = fmt.Errorf("template read source %q: %w", step.Source, err)
		r.Severity = SevFatal
		return
	}
	tmpl, err := template.New("step").Parse(string(tmplBytes))
	if err != nil {
		r.Error = fmt.Errorf("template parse %q: %w", step.Source, err)
		r.Severity = SevFatal
		return
	}
	dest, err := safeJoin(ctx.Base, step.Destination)
	if err != nil {
		r.Error = err
		r.Severity = SevFatal
		return
	}

	mode := step.Mode
	if mode == "" {
		mode = "create"
	}
	exists := false
	if _, err := os.Stat(dest); err == nil {
		exists = true
	}
	if exists && mode == "create" {
		r.Action = ActSkipped
		return
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct {
		Project any
		Vars    map[string]any
	}{ctx.Project, ctx.Vars}); err != nil {
		r.Error = fmt.Errorf("template execute %q: %w", step.Source, err)
		r.Severity = SevFatal
		return
	}
	if err := writeFile(dest, buf.Bytes()); err != nil {
		r.Error = fmt.Errorf("template write %q: %w", dest, err)
		r.Severity = SevFatal
		return
	}
	if exists {
		r.Action = ActOverwritten
	} else {
		r.Action = ActCreated
	}
}

// execExec runs an external command with separated args (spec section 19 —
// never a shell string). working_directory, when set, is resolved under Base.
func execExec(r *Result, step recipe.Step, ctx Context, runner ExecRunner) {
	dir := ctx.Base
	if step.WorkingDirectory != "" {
		joined, err := safeJoin(ctx.Base, step.WorkingDirectory)
		if err != nil {
			r.Error = err
			r.Severity = SevFatal
			return
		}
		dir = joined
	}
	// Exec steps rodam comandos declarados na recipe (autores confiáveis),
	// mas o nome ainda passa por validação de caracteres antes do runner —
	// args seguem como lista separada, nunca shell string.
	if err := safeexec.ValidateName(step.Command); err != nil {
		r.Error = err
		r.Severity = SevFatal
		return
	}
	if err := runner.Run(dir, step.Command, step.Args, ctx.stdout(), ctx.stderr()); err != nil {
		r.Error = fmt.Errorf("exec %s %v: %w", step.Command, step.Args, err)
		r.Severity = SevFatal
		return
	}
	r.Action = ActRan
}

// execMessage prints step.Text to stdout. Spec section 20.
func execMessage(r *Result, step recipe.Step, ctx Context) {
	fmt.Fprintln(ctx.stdout(), step.Text)
	r.Action = ActMessaged
}

// writeFile writes data to dest, creating parent directories as needed.
func writeFile(dest string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}
