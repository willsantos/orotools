package ui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
)

// Status renders styled status lines (title/success/error/warn/info) to a writer.
// When the writer is not a TTY, output degrades to plain ASCII without ANSI codes.
type Status struct {
	w          io.Writer
	p          Palette
	tty        bool
	markerGood rune
	markerBad  rune
	markerWarn rune
	markerInfo rune
}

// NewStatus builds a Status that writes to w. TTY detection is performed when
// w is an *os.File; anything else is treated as non-TTY.
func NewStatus(w io.Writer) *Status {
	tty := IsTTY(w)
	s := &Status{w: w, tty: tty, p: NewPalette(tty)}
	if tty {
		s.markerGood, s.markerBad, s.markerWarn, s.markerInfo = '✓', '✗', '!', '•'
	} else {
		s.markerGood, s.markerBad, s.markerWarn, s.markerInfo = '+', 'x', '!', '-'
	}
	return s
}

// Title prints a highlighted title line.
func (s *Status) Title(msg string) {
	fmt.Fprintln(s.w, s.p.Title.Render(msg))
}

// Success prints a success status line.
func (s *Status) Success(msg string) {
	s.line(s.markerGood, s.p.Success, msg)
}

// Error prints an error status line.
func (s *Status) Error(msg string) {
	s.line(s.markerBad, s.p.Error, msg)
}

// Warn prints a warning status line.
func (s *Status) Warn(msg string) {
	s.line(s.markerWarn, s.p.Warn, msg)
}

// Info prints an informational status line.
func (s *Status) Info(msg string) {
	s.line(s.markerInfo, s.p.Info, msg)
}

func (s *Status) line(marker rune, style lipgloss.Style, msg string) {
	body := style.Render(fmt.Sprintf("%c %s", marker, msg))
	fmt.Fprintln(s.w, body)
}
