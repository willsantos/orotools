package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func pickerFixture() []skillOption {
	return []skillOption{
		{ID: "recipe:demo/skills/code-review", Label: "code-review 1.0.0 recipe:demo — revisão de código",
			Description: "revisão de código assistida", Version: "1.0.0",
			Source: "recipe", SourceRef: "demo"},
		{ID: "recipe:demo/skills/aspnet", Label: "aspnet 1.1.0 recipe:demo — convenções ASP.NET",
			Description: "convenções ASP.NET", Version: "1.1.0",
			Source: "recipe", SourceRef: "demo"},
		{ID: "github:willsantos/skills_AI/architecture", Label: "architecture 0.2.0 github:willsantos/skills_AI — análise",
			Description: "análise arquitetural de componentes", Version: "0.2.0",
			Source: "github", SourceRef: "willsantos/skills_AI"},
	}
}

func TestGroupSkillOptionsOrdersRecipeFirst(t *testing.T) {
	groups := groupSkillOptions(pickerFixture())
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].title != "Recipe demo" || groups[1].title != "GitHub willsantos/skills_AI" {
		t.Errorf("titles = %q, %q; want recipe antes de github", groups[0].title, groups[1].title)
	}
	if len(groups[0].entries) != 2 || len(groups[1].entries) != 1 {
		t.Errorf("entries por grupo = %d, %d; want 2, 1", len(groups[0].entries), len(groups[1].entries))
	}
}

func TestPickerRowsCollapsedAndExpanded(t *testing.T) {
	m := newSkillPicker("Selecione", pickerFixture(), "1.2.3")
	// Colapsado: só cabeçalhos de grupo.
	if len(m.rows) != 2 {
		t.Fatalf("rows colapsado = %d, want 2", len(m.rows))
	}
	// Expandir o primeiro grupo com espaço.
	m.cursor = 0
	m.toggleCursor()
	if len(m.rows) != 4 { // cabeçalho 1 + 2 entradas + cabeçalho 2
		t.Fatalf("rows expandido = %d, want 4", len(m.rows))
	}
	if !m.groups[0].expanded {
		t.Error("grupo 0 deveria estar expandido")
	}
}

func TestPickerToggleSelectionAndIDs(t *testing.T) {
	m := newSkillPicker("Selecione", pickerFixture(), "1.2.3")
	m.groups[0].expanded = true
	m.rebuildRows()
	// cursor na primeira entrada do grupo 0 (row 1).
	m.cursor = 1
	m.toggleCursor()
	if got := m.selectedIDs(); len(got) != 1 || got[0] != "recipe:demo/skills/code-review" {
		t.Errorf("selectedIDs = %v; want apenas code-review", got)
	}
	m.toggleCursor()
	if got := m.selectedIDs(); len(got) != 0 {
		t.Errorf("selectedIDs após desmarcar = %v; want vazio", got)
	}
}

func TestPickerFilter(t *testing.T) {
	m := newSkillPicker("Selecione", pickerFixture(), "1.2.3")
	m.filtering = true
	out, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("arq")})
	m = out.(skillPicker)
	if m.filter != "arq" {
		t.Fatalf("filter = %q; want arq (digitação vira filtro)", m.filter)
	}
	// "arq" não casa com nada (descrições em PT: revisão/convenções/análise)...
	// navegação deve ficar desabilitada no modo filtro.
	out, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = out.(skillPicker)
	if m.cursor != 0 {
		t.Error("setas não devem mover o cursor enquanto filtra")
	}
	out, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(skillPicker)
	if m.filtering {
		t.Fatal("enter deve encerrar o modo filtro")
	}
	if m.appliedFilter != "arq" {
		t.Fatalf("appliedFilter = %q; want arq", m.appliedFilter)
	}
	// filtro sem correspondência: nenhuma linha de entrada.
	if len(m.rows) != 0 {
		t.Errorf("rows com filtro sem match = %d; want 0", len(m.rows))
	}
	// limpar com esc restaura todas as linhas.
	out, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = out.(skillPicker)
	if m.appliedFilter != "" {
		t.Errorf("appliedFilter após esc = %q; want vazio", m.appliedFilter)
	}
	if len(m.rows) != 2 {
		t.Errorf("rows sem filtro = %d; want 2 (colapsado)", len(m.rows))
	}
}

func TestPickerFilterMatchesDescription(t *testing.T) {
	m := newSkillPicker("Selecione", pickerFixture(), "1.2.3")
	m.appliedFilter = "ASP.NET"
	m.rebuildRows()
	// grupo recipe (cabeçalho) + entrada aspnet apenas.
	if len(m.rows) != 2 {
		t.Fatalf("rows filtradas = %d; want 2", len(m.rows))
	}
	if e, ok := m.entryAt(1); !ok || e.ID != "recipe:demo/skills/aspnet" {
		t.Errorf("entrada filtrada = %+v; want aspnet", e)
	}
}

func TestPickerEnsureVisibleKeepsCursorInWindow(t *testing.T) {
	opts := make([]skillOption, 0, 20)
	for i := 0; i < 10; i++ {
		opts = append(opts, skillOption{
			ID: "github:willsantos/skills_AI/skill-" + string(rune('a'+i)),
			Label: "skill-" + string(rune('a'+i)),
			Description: "descrição", Source: "github", SourceRef: "willsantos/skills_AI",
		})
	}
	m := newSkillPicker("Selecione", opts, "1.0.0")
	m.width, m.height = 80, 12
	m.groups[0].expanded = true
	m.rebuildRows()
	// pular para o fim e garantir que a janela acompanha.
	m.cursor = len(m.rows) - 1
	m.ensureVisible()
	if m.cursor < m.offset || m.cursor >= m.offset+m.visibleRowCount() {
		t.Errorf("cursor %d fora da janela [%d, %d)", m.cursor, m.offset, m.offset+m.visibleRowCount())
	}
}

func TestPickerViewSmoke(t *testing.T) {
	m := newSkillPicker("Selecione as skills para instalar", pickerFixture(), "1.2.3")
	m.width, m.height = 100, 30
	view := m.View()
	for _, want := range []string{"███████╗", "VERSION 1.2.3", "Recipe demo",
		"GitHub willsantos/skills_AI", "espaço", "enter", "filtrar", "esc"} {
		if !strings.Contains(view, want) {
			t.Errorf("view não contém %q", want)
		}
	}
	// Largura pequena: banner degrada para título simples, sem pânico.
	m.width, m.height = 50, 12
	view = m.View()
	if !strings.Contains(view, "ORO SKILLS") {
		t.Error("view estreita deve conter o título simples")
	}
}

func TestPickerCancelAndConfirm(t *testing.T) {
	m := newSkillPicker("Selecione", pickerFixture(), "1.2.3")
	out, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !out.(skillPicker).cancel {
		t.Error("esc deve marcar cancelamento")
	}
	m2 := newSkillPicker("Selecione", pickerFixture(), "1.2.3")
	m2.groups[0].expanded = true
	m2.rebuildRows()
	m2.cursor = 1
	m2.toggleCursor()
	out2, _ := m2.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := out2.(skillPicker)
	if got.cancel {
		t.Error("enter não é cancelamento")
	}
	if ids := got.selectedIDs(); len(ids) != 1 || ids[0] != "recipe:demo/skills/code-review" {
		t.Errorf("selectedIDs = %v; want code-review", ids)
	}
}

func TestWrapAndTruncate(t *testing.T) {
	if got := truncateRunes("abcdef", 4); got != "abc…" {
		t.Errorf("truncateRunes = %q; want abc…", got)
	}
	if got := truncateRunes("ab", 4); got != "ab" {
		t.Errorf("truncateRunes = %q; want ab", got)
	}
	lines := wrapText("uma frase bem comprida para quebrar", 10)
	if len(lines) < 2 {
		t.Errorf("wrapText = %v; want múltiplas linhas", lines)
	}
	for _, l := range lines {
		if len([]rune(l)) > 10 {
			t.Errorf("linha %q excede a largura 10", l)
		}
	}
}
