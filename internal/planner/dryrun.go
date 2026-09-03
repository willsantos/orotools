package planner

import (
	"fmt"
	"strings"
)

// DryRun renders the plan as human-readable text following spec section 25.
// Empty plans render as "No operations.".
func (p *Plan) DryRun() string {
	if p == nil || (len(p.Directories) == 0 && len(p.Files) == 0 && len(p.Commands) == 0 && len(p.Messages) == 0 && len(p.BundledSkills) == 0 && len(p.BundledAgents) == 0 && len(p.ExternalInstallers) == 0) {
		return "PLAN\n\nNo operations.\n"
	}

	var b strings.Builder
	b.WriteString("PLAN\n")

	if len(p.Directories) > 0 {
		b.WriteString("\nDirectories\n")
		for _, d := range p.Directories {
			fmt.Fprintf(&b, "+ %s\n", d)
		}
	}

	if len(p.Files) > 0 {
		b.WriteString("\nFiles\n")
		for _, f := range p.Files {
			fmt.Fprintf(&b, "+ %s (%s)\n", f.Destination, f.Kind)
		}
	}

	if len(p.Commands) > 0 {
		b.WriteString("\nCommands\n")
		for _, c := range p.Commands {
			fmt.Fprintf(&b, "> %s%s\n", c.Command, formatArgs(c.Args))
		}
	}

	if len(p.Messages) > 0 {
		b.WriteString("\nMessages\n")
		for _, m := range p.Messages {
			fmt.Fprintf(&b, "%s\n", m)
		}
	}

	if len(p.BundledSkills) > 0 {
		b.WriteString("\nBundled skills\n")
		for _, s := range p.BundledSkills {
			fmt.Fprintf(&b, "+ %s\n", s)
		}
	}

	if len(p.BundledAgents) > 0 {
		b.WriteString("\nBundled agents\n")
		for _, a := range p.BundledAgents {
			fmt.Fprintf(&b, "+ %s\n", a)
		}
	}

	if len(p.ExternalInstallers) > 0 {
		b.WriteString("\nExternal installers\n")
		for _, op := range p.ExternalInstallers {
			fmt.Fprintf(&b, "> %s%s\n", op.Command, formatArgs(op.Args))
		}
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "Local operations       %d\n", p.LocalOps)
	fmt.Fprintf(&b, "External operations    %d\n", p.NetworkOps)
	net := "no"
	if p.NetworkRequired {
		net = "yes"
	}
	fmt.Fprintf(&b, "Network required       %s\n", net)

	return b.String()
}

// formatArgs renders command args as a space-joined suffix; empty when no args.
// Quoting is deferred to the real executor (dry-run is informational only).
func formatArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	var b strings.Builder
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(a)
	}
	return b.String()
}
