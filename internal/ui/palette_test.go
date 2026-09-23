package ui

import (
	"bytes"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// plainFields lista todos os estilos da Palette para varredura uniforme nos
// testes de degradação.
func plainFields(p Palette) map[string]lipgloss.Style {
	return map[string]lipgloss.Style{
		"Title":   p.Title,
		"Success": p.Success,
		"Error":   p.Error,
		"Warn":    p.Warn,
		"Info":    p.Info,
		"Muted":   p.Muted,
		"Key":     p.Key,
		"Accent":  p.Accent,
		"Pink":    p.Pink,
		"Border":  p.Border,
		"Content": p.Content,
	}
}

func TestPlainPaletteRendersWithoutANSI(t *testing.T) {
	pal := NewPalette(false)
	for name, style := range plainFields(pal) {
		if got := style.Render("abc"); got != "abc" {
			t.Errorf("%s: estilo plain alterou a saída: %q", name, got)
		}
	}
}

func TestNoColorEnvDegradesTTYPalette(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	pal := NewPalette(true)
	for name, style := range plainFields(pal) {
		if got := style.Render("abc"); got != "abc" {
			t.Errorf("%s: NO_COLOR deveria degradar para texto puro, got %q", name, got)
		}
	}
}

func TestTTYPaletteUsesOroborusTokens(t *testing.T) {
	pal := NewPalette(true)
	want := map[string]struct {
		style lipgloss.Style
		color string
		bold  bool
	}{
		"Title":   {pal.Title, ColorPrimary, true},
		"Key":     {pal.Key, ColorPrimary, true},
		"Success": {pal.Success, ColorSuccess, false},
		"Error":   {pal.Error, ColorError, false},
		"Warn":    {pal.Warn, ColorWarn, false},
		"Info":    {pal.Info, ColorContentSec, false},
		"Muted":   {pal.Muted, ColorMuted, false},
		"Accent":  {pal.Accent, ColorAccent, false},
		"Pink":    {pal.Pink, ColorPink, false},
		"Border":  {pal.Border, ColorBorder, false},
		"Content": {pal.Content, ColorContent, false},
	}
	for name, tc := range want {
		if fg := tc.style.GetForeground(); fg != lipgloss.Color(tc.color) {
			t.Errorf("%s: foreground = %v, want %s", name, fg, tc.color)
		}
		if got := tc.style.GetBold(); got != tc.bold {
			t.Errorf("%s: bold = %v, want %v", name, got, tc.bold)
		}
	}
}

func TestPlainPaletteHasNoColors(t *testing.T) {
	pal := NewPalette(false)
	for name, style := range plainFields(pal) {
		if fg := style.GetForeground(); fg != (lipgloss.NoColor{}) {
			t.Errorf("%s: paleta plain não deveria ter foreground, got %v", name, fg)
		}
	}
}

func TestPaletteForNonFileWriterIsPlain(t *testing.T) {
	var buf bytes.Buffer
	pal := PaletteFor(&buf)
	if got := pal.Title.Render("abc"); got != "abc" {
		t.Errorf("PaletteFor(bytes.Buffer) deveria degradar para texto puro, got %q", got)
	}
}
