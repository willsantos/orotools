package ui

import (
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Palette holds the shared lipgloss styles used across the CLI output so
// tables and status lines stay visually consistent. The zero value — and any
// palette built for a non-TTY writer — renders plain text without ANSI codes.
type Palette struct {
	Title   lipgloss.Style
	Success lipgloss.Style
	Error   lipgloss.Style
	Warn    lipgloss.Style
	Info    lipgloss.Style
	Muted   lipgloss.Style
	// Key highlights identifiers (atalhos, nomes de recipe) next to regular text.
	Key lipgloss.Style
}

// NewPalette builds the shared palette; without a TTY every style degrades
// to plain rendering.
func NewPalette(tty bool) Palette {
	if !tty {
		return Palette{}
	}
	fg := func(c string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(c))
	}
	return Palette{
		Title:   fg("63").Bold(true),
		Success: fg("34"),
		Error:   fg("196"),
		Warn:    fg("214"),
		Info:    fg("245"),
		Muted:   fg("240"),
		Key:     fg("63").Bold(true),
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
