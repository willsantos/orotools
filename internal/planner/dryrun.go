package planner

import (
	"fmt"
	"io"
	"strings"

	"oroborus.dev/orotools/internal/ui"
)

// DryRun renders the plan as human-readable text following spec section 25.
// Empty plans render as "No operations.".
func (p *Plan) DryRun() string {
	return p.dryRun(ui.Palette{})
}

// FprintDryRun writes the dry-run to w applying the Oroborus palette when w
// is a TTY; otherwise the output is byte-identical to DryRun().
func (p *Plan) FprintDryRun(w io.Writer) {
	fmt.Fprint(w, p.dryRun(ui.PaletteFor(w)))
}

func (p *Plan) dryRun(pal ui.Palette) string {
	if p == nil || (len(p.Directories) == 0 && len(p.Files) == 0 && len(p.Commands) == 0 && len(p.Messages) == 0 && len(p.BundledSkills) == 0 && len(p.BundledAgents) == 0 && len(p.ExternalInstallers) == 0) {
		return "PLAN\n\nNo operations.\n"
	}

	var b strings.Builder
	b.WriteString(pal.Title.Render("PLAN") + "\n")

	if len(p.Directories) > 0 {
		b.WriteString("\n" + pal.Info.Render("Directories") + "\n")
		for _, d := range p.Directories {
			fmt.Fprintf(&b, "%s %s\n", pal.Success.Render("+"), d)
		}
	}

	if len(p.Files) > 0 {
		b.WriteString("\n" + pal.Info.Render("Files") + "\n")
		for _, f := range p.Files {
			fmt.Fprintf(&b, "%s %s (%s)\n", pal.Success.Render("+"), f.Destination, f.Kind)
		}
	}

	if len(p.Commands) > 0 {
		b.WriteString("\n" + pal.Info.Render("Commands") + "\n")
		for _, c := range p.Commands {
			fmt.Fprintf(&b, "%s %s%s\n", pal.Key.Render(">"), c.Command, formatArgs(c.Args))
		}
	}

	if len(p.Messages) > 0 {
		b.WriteString("\n" + pal.Info.Render("Messages") + "\n")
		for _, m := range p.Messages {
			fmt.Fprintf(&b, "%s\n", m)
		}
	}

	if len(p.BundledSkills) > 0 {
		b.WriteString("\n" + pal.Info.Render("Bundled skills") + "\n")
		for _, s := range p.BundledSkills {
			fmt.Fprintf(&b, "%s %s\n", pal.Success.Render("+"), s)
		}
	}

	if len(p.BundledAgents) > 0 {
		b.WriteString("\n" + pal.Info.Render("Bundled agents") + "\n")
		for _, a := range p.BundledAgents {
			fmt.Fprintf(&b, "%s %s\n", pal.Success.Render("+"), a)
		}
	}

	if len(p.ExternalInstallers) > 0 {
		b.WriteString("\n" + pal.Info.Render("External installers") + "\n")
		for _, op := range p.ExternalInstallers {
			fmt.Fprintf(&b, "%s %s%s\n", pal.Key.Render(">"), op.Command, formatArgs(op.Args))
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
