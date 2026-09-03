package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"oroborus.dev/orotools/internal/manifest"
	"oroborus.dev/orotools/internal/ui"
)

// runInfo prints the project state from ./orotools.yaml (spec section 58).
func runInfo(out io.Writer, manifestPath string) error {
	m, err := manifest.Read(manifestPath)
	if err != nil {
		return fmt.Errorf("info: %w", err)
	}
	status := ui.NewStatus(out)
	pal := ui.PaletteFor(out)
	status.Title("Orotools — Projeto")
	fmt.Fprintln(out)

	printField(out, pal, "Projeto", m.Project.Name)
	printField(out, pal, "Recipe", m.Recipe.Name)
	printField(out, pal, "Repositório", m.Repository.Provider)
	printField(out, pal, "Pipeline", m.Pipeline.Provider)
	printField(out, pal, "IA", m.AI.Agent)
	if len(m.Variables) > 0 {
		printField(out, pal, "Variáveis", formatVars(m.Variables))
	}
	if len(m.Skills.Bundled) > 0 || len(m.Skills.External) > 0 {
		printField(out, pal, "Skills", formatList(append(append([]string{}, m.Skills.Bundled...), m.Skills.External...)))
	}
	if len(m.Agents.Bundled) > 0 || len(m.Agents.External) > 0 {
		printField(out, pal, "Agents", formatList(append(append([]string{}, m.Agents.Bundled...), m.Agents.External...)))
	}
	return nil
}

func printField(out io.Writer, pal ui.Palette, label, value string) {
	if value == "" {
		value = "-"
	}
	pad := 12 - lipgloss.Width(label)
	if pad < 1 {
		pad = 1
	}
	fmt.Fprintf(out, "%s %s\n", pal.Key.Render(label+strings.Repeat(" ", pad)), value)
}

func formatVars(vars map[string]any) string {
	parts := make([]string, 0, len(vars))
	for k, v := range vars {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, ", ")
}

func formatList(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	return strings.Join(items, ", ")
}
