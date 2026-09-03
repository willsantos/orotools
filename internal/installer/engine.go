package installer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"oroborus.dev/orotools/internal/agent"
	"oroborus.dev/orotools/internal/recipe"
	"oroborus.dev/orotools/internal/repository"
)

// PlannedOp describes one external installer invocation.
type PlannedOp struct {
	InstallerName string
	Label         string
	Command       string
	Args          []string
}

// Options configures external installer execution.
type Options struct {
	Base    string
	Recipe  *recipe.Recipe
	Yes     bool
	DryRun  bool
	Runner  repository.CommandRunner
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
}

type externalEntry struct {
	installer string
	items     []string
	isAgent   bool
}

// Plan builds installer operations from recipe skills.external entries.
func Plan(r *recipe.Recipe) ([]PlannedOp, error) {
	return PlanAll(r, "opencode")
}

// PlanAll builds installer operations from skills.external and agents.external.
func PlanAll(r *recipe.Recipe, canonicalAgent string) ([]PlannedOp, error) {
	if r == nil {
		return nil, fmt.Errorf("installer: recipe is required")
	}
	if canonicalAgent == "" {
		canonicalAgent = "opencode"
	}
	var entries []externalEntry
	for _, ext := range r.Skills.External {
		entries = append(entries, externalEntry{installer: ext.Installer, items: append([]string(nil), ext.Skills...)})
	}
	for _, ext := range r.Agents.External {
		items := append([]string(nil), ext.Agents...)
		if len(items) == 0 {
			items = []string{agent.InstallerAlias(canonicalAgent, ext.Installer)}
		}
		entries = append(entries, externalEntry{installer: ext.Installer, items: items, isAgent: true})
	}
	out := make([]PlannedOp, 0, len(entries))
	for _, entry := range entries {
		inst, ok := r.ExternalInstallers[entry.installer]
		if !ok {
			return nil, fmt.Errorf("installer: unknown external installer %q", entry.installer)
		}
		cmd, args, err := buildCommand(inst, entry.items)
		if err != nil {
			return nil, fmt.Errorf("installer %q: %w", entry.installer, err)
		}
		out = append(out, PlannedOp{
			InstallerName: entry.installer,
			Label:         packageLabel(inst),
			Command:       cmd,
			Args:          args,
		})
	}
	return out, nil
}

// Run executes planned external installers after optional confirmation.
func Run(ctx context.Context, ops []PlannedOp, o Options) error {
	if len(ops) == 0 {
		return nil
	}
	if o.DryRun {
		return nil
	}
	if !o.Yes {
		if err := confirm(o.Stdout, o.Stdin, ops); err != nil {
			return err
		}
	}
	runner := o.Runner
	if runner == nil {
		runner = repository.RealRunner{}
	}
	for _, op := range ops {
		if err := runner.Run(ctx, op.Command, op.Args, o.Base, o.Stdout, o.Stderr); err != nil {
			return fmt.Errorf("%s: %w", op.InstallerName, err)
		}
	}
	return nil
}

func buildCommand(inst recipe.ExternalInstaller, items []string) (string, []string, error) {
	switch inst.Runtime {
	case "npx":
		return buildNpx(inst, items)
	case "command":
		return buildCommandRuntime(inst, items)
	default:
		return "", nil, fmt.Errorf("unsupported runtime %q", inst.Runtime)
	}
}

func buildNpx(inst recipe.ExternalInstaller, items []string) (string, []string, error) {
	pkg := inst.Package.Name
	if inst.Package.Version != "" && inst.Package.Version != "latest" {
		pkg = pkg + "@" + inst.Package.Version
	}
	args := []string{"--yes", pkg}
	if len(inst.Install.Args) > 0 {
		args = append(args, inst.Install.Args...)
	}
	if len(items) > 0 {
		args = append(args, items...)
	}
	return "npx", args, nil
}

func buildCommandRuntime(inst recipe.ExternalInstaller, items []string) (string, []string, error) {
	if inst.Package.Name == "" {
		return "", nil, fmt.Errorf("command runtime requires package.name")
	}
	args := append([]string(nil), inst.Install.Args...)
	args = append(args, items...)
	return inst.Package.Name, args, nil
}

func packageLabel(inst recipe.ExternalInstaller) string {
	if inst.Package.Version != "" && inst.Package.Version != "latest" {
		return inst.Package.Name + "@" + inst.Package.Version
	}
	return inst.Package.Name + "@latest"
}

func confirm(out io.Writer, in io.Reader, ops []PlannedOp) error {
	if in == nil {
		return fmt.Errorf("external installers require --yes in non-interactive mode")
	}
	fmt.Fprintln(out, "External installers")
	fmt.Fprintln(out)
	for _, op := range ops {
		fmt.Fprintf(out, "• %s\n", op.Label)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "These commands may download and execute third-party code.")
	fmt.Fprintln(out)
	fmt.Fprint(out, "Continue? Y/n ")
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !strings.Contains(err.Error(), "EOF") {
		return err
	}
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" || line == "y" || line == "yes" {
		return nil
	}
	return fmt.Errorf("external installers declined")
}
