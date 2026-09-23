package cli

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"oroborus.dev/orotools/internal/ui"
)

// oroTheme adapta o tema base do huh aos tokens Oroborus: lavanda como voz
// ativa (seleção, cursor, indicadores), foco em violeta intenso, superfícies
// discretas e o CTA primário com texto sobre a primária como no site
// (spec cli-design-system FR-8).
func oroTheme() *huh.Theme {
	t := huh.ThemeBase()
	fg := func(c string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(c))
	}

	f := &t.Focused
	// A borda esquerda espessa do base vira o anel de foco em violeta intenso.
	f.Base = f.Base.BorderForeground(lipgloss.Color(ui.ColorAccent))
	f.Title = fg(ui.ColorContent).Bold(true)
	f.Description = fg(ui.ColorContentSec)
	f.ErrorIndicator = f.ErrorIndicator.Foreground(lipgloss.Color(ui.ColorError)).Bold(true)
	f.ErrorMessage = f.ErrorMessage.Foreground(lipgloss.Color(ui.ColorError))
	f.SelectSelector = f.SelectSelector.Foreground(lipgloss.Color(ui.ColorPrimary)).Bold(true)
	f.MultiSelectSelector = f.MultiSelectSelector.Foreground(lipgloss.Color(ui.ColorPrimary)).Bold(true)
	f.Option = fg(ui.ColorContent)
	f.NextIndicator = f.NextIndicator.Foreground(lipgloss.Color(ui.ColorPrimary))
	f.PrevIndicator = f.PrevIndicator.Foreground(lipgloss.Color(ui.ColorPrimary))
	f.SelectedOption = fg(ui.ColorPrimary).Bold(true)
	// O marcador [•] usa o verde de sucesso, igual aos marcadores da CLI.
	f.SelectedPrefix = f.SelectedPrefix.Foreground(lipgloss.Color(ui.ColorSuccess)).Bold(true)
	f.UnselectedOption = fg(ui.ColorContentSec)
	f.UnselectedPrefix = f.UnselectedPrefix.Foreground(lipgloss.Color(ui.ColorMuted))
	f.TextInput.Cursor = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ui.ColorBackground)).
		Background(lipgloss.Color(ui.ColorPrimary))
	f.TextInput.CursorText = fg(ui.ColorPrimary)
	f.TextInput.Placeholder = fg(ui.ColorMuted)
	f.TextInput.Prompt = fg(ui.ColorPrimary).Bold(true)
	f.TextInput.Text = fg(ui.ColorContent)
	// CTA primário como o botão do site: fundo lavanda, texto violeta profundo.
	f.FocusedButton = f.FocusedButton.
		Foreground(lipgloss.Color(ui.ColorOnPrimary)).
		Background(lipgloss.Color(ui.ColorPrimary))
	f.BlurredButton = f.BlurredButton.
		Foreground(lipgloss.Color(ui.ColorContentSec)).
		Background(lipgloss.Color(ui.ColorSurfaceLow))
	f.NoteTitle = fg(ui.ColorPrimary).Bold(true)
	f.Next = fg(ui.ColorPrimary)
	f.Card = f.Card.BorderForeground(lipgloss.Color(ui.ColorBorder))

	b := &t.Blurred
	b.Base = b.Base.BorderForeground(lipgloss.Color(ui.ColorBorder))
	b.Title = fg(ui.ColorContentSec)
	b.Description = fg(ui.ColorMuted)

	return t
}

// newOroForm cria um form huh com o tema Oroborus — todo wizard da CLI passa
// por aqui para manter a identidade consistente.
func newOroForm(groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).WithTheme(oroTheme())
}
