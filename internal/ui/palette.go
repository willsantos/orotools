package ui

import (
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Tokens Oroborus do design system (DESIGN.md; fonte da verdade em
// oroborus/design-system/tokens.css, mapeada em site/index.html). Nenhuma cor
// de UI pode ficar fora desta lista — spec cli-design-system FR-3.
const (
	// ColorPrimary é o violeta Oroborus, a voz ativa: títulos, atalhos,
	// seleção e identificadores.
	ColorPrimary = "#b3a1ff"
	// ColorOnPrimary é o texto sobre a primária (violeta profundo), como no
	// CTA do site.
	ColorOnPrimary = "#310093"
	// ColorAccent é o violeta intenso: estados que pedem mais energia (foco).
	ColorAccent = "#7a53ff"
	// ColorPink é o rosa assinatura: destaque pontual por definição.
	ColorPink = "#ff6a9d"
	// ColorGradientStart e ColorGradientEnd delimitam o gradiente de marca —
	// assinatura rara, exclusiva de momentos de marca (banner da TUI).
	ColorGradientStart = "#5b2eff"
	ColorGradientEnd   = "#ff2e88"

	// ColorSuccess, ColorWarn e ColorError são os estados semânticos.
	ColorSuccess = "#4ade80"
	ColorWarn    = "#fbbf24"
	ColorError   = "#ff6e84"

	// ColorContent é o texto principal e ColorContentSec o secundário.
	ColorContent    = "#f8f5fd"
	ColorContentSec = "#acaab1"
	// ColorMuted é o texto recolhido (oro.neutral.500).
	ColorMuted = "#76747b"
	// ColorBorder é a borda discreta de tabelas e frames (border.subtle).
	ColorBorder = "#48474d"

	// ColorBackground e a escala de superfícies servem à TUI full-screen,
	// onde o fundo é controlável.
	ColorBackground     = "#0e0e13"
	ColorSurfaceLow     = "#131318"
	ColorSurface        = "#19191f"
	ColorSurfaceHigh    = "#1f1f26"
	ColorSurfaceHighest = "#25252c"
)

// Palette holds the shared lipgloss styles used across the CLI output so
// tables and status lines stay visually consistent. The zero value — and any
// palette built for a non-TTY writer or under NO_COLOR — renders plain text
// without ANSI codes.
type Palette struct {
	Title   lipgloss.Style
	Success lipgloss.Style
	Error   lipgloss.Style
	Warn    lipgloss.Style
	Info    lipgloss.Style
	Muted   lipgloss.Style
	// Key highlights identifiers (atalhos, nomes de recipe) next to regular text.
	Key lipgloss.Style
	// Accent aplica o violeta intenso em estados que pedem mais energia.
	Accent lipgloss.Style
	// Pink é o rosa assinatura; uso pontual, nunca como cor corrente.
	Pink lipgloss.Style
	// Border estiliza bordas de tabelas e frames.
	Border lipgloss.Style
	// Content é o texto principal para superfícies controladas (TUI).
	Content lipgloss.Style
}

// NewPalette builds the shared palette; without a TTY — or under NO_COLOR —
// every style degrades to plain rendering.
func NewPalette(tty bool) Palette {
	if !tty || os.Getenv("NO_COLOR") != "" {
		return Palette{}
	}
	fg := func(c string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(c))
	}
	return Palette{
		Title:   fg(ColorPrimary).Bold(true),
		Success: fg(ColorSuccess),
		Error:   fg(ColorError),
		Warn:    fg(ColorWarn),
		Info:    fg(ColorContentSec),
		Muted:   fg(ColorMuted),
		Key:     fg(ColorPrimary).Bold(true),
		Accent:  fg(ColorAccent),
		Pink:    fg(ColorPink),
		Border:  fg(ColorBorder),
		Content: fg(ColorContent),
	}
}

// IsTTY reports whether w is an interactive terminal.
func IsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// PaletteFor returns the shared palette for a writer, degrading to plain
// styles when the writer is not a TTY.
func PaletteFor(w io.Writer) Palette {
	return NewPalette(IsTTY(w))
}
