package cli

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"oroborus.dev/orotools/internal/skill"
)

// errPickerCancel marca a saída do picker sem confirmação (esc/ctrl+c); o
// chamador trata como cancelamento do wizard.
var errPickerCancel = errors.New("seleção cancelada")

// tuiSkillSelector é o selector de produção: TUI full-screen em alt-screen no
// estilo de instaladores de skills (banner, grupos colapsáveis por origem,
// filtro, detalhe e barra de atalhos).
type tuiSkillSelector struct {
	version string
}

func (s tuiSkillSelector) Select(title string, options []skillOption) ([]string, error) {
	p := tea.NewProgram(newSkillPicker(title, options, s.version),
		tea.WithAltScreen(), tea.WithMouseCellMotion())
	out, err := p.Run()
	if err != nil {
		return nil, err
	}
	m, ok := out.(skillPicker)
	if !ok {
		return nil, fmt.Errorf("skills: modelo inesperado do picker: %T", out)
	}
	if m.cancel {
		return nil, errPickerCancel
	}
	return m.selectedIDs(), nil
}

// --- estilos ---

var (
	pickerBannerColors = []string{"#2E5BE6", "#3773EE", "#3F8CF3", "#43A5F5", "#47BCF7", "#4BD3F9"}

	pickerAccent      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4D9FFF"))
	pickerGroupArrow  = lipgloss.NewStyle().Foreground(lipgloss.Color("#3F8CF3"))
	pickerGroupTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4D9FFF"))
	pickerCount       = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	pickerHint        = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	pickerName        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	pickerNameOn      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7CC4FF"))
	pickerMarkerOff   = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	pickerMarkerOn    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#43E5A0"))
	pickerDesc        = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	pickerCursorBg    = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	pickerTagline     = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("241"))
	pickerTip         = lipgloss.NewStyle().Foreground(lipgloss.Color("#4D9FFF"))
	pickerDivider     = lipgloss.NewStyle().Foreground(lipgloss.Color("239"))
	pickerDim         = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	pickerSelectedSum = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#43E5A0"))
	pickerBarBorder   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238"))
)

const (
	// bannerMinWidth é a largura mínima para o banner ASCII; abaixo disso o
	// cabeçalho degrada para o título simples.
	bannerMinWidth = 80
)

// --- modelo ---

type pickerEntry struct {
	skillOption
	selected bool
}

type pickerGroup struct {
	title    string
	expanded bool
	entries  []pickerEntry
}

// pickerRow achata grupos+skills em linhas navegáveis; entry -1 é o cabeçalho
// do grupo.
type pickerRow struct {
	group int
	entry int
}

type skillPicker struct {
	title   string
	version string
	groups  []pickerGroup
	rows    []pickerRow
	cursor  int
	offset  int
	width   int
	height  int

	filtering     bool   // modo de digitação do filtro
	filter        string // buffer enquanto digita
	appliedFilter string // filtro em vigor (aplicado com enter)

	detail bool
	help   bool
	cancel bool
}

func newSkillPicker(title string, options []skillOption, version string) skillPicker {
	if version == "" {
		version = "dev"
	}
	m := skillPicker{
		title:   title,
		version: version,
		groups:  groupSkillOptions(options),
		width:   80,
		height:  24,
	}
	m.rebuildRows()
	return m
}

// groupSkillOptions agrupa por origem preservando a ordem determinística dos
// catálogos (recipe antes de GitHub, depois nome).
func groupSkillOptions(options []skillOption) []pickerGroup {
	var groups []pickerGroup
	index := make(map[string]int)
	for _, o := range options {
		key := o.Source + ":" + o.SourceRef
		gi, ok := index[key]
		if !ok {
			groups = append(groups, pickerGroup{title: groupTitle(o)})
			gi = len(groups) - 1
			index[key] = gi
		}
		if o.Name == "" {
			// Fallback para chamadas legadas que só preenchem Label/ID.
			o.Name = o.ID
			if i := strings.LastIndex(o.Name, "/"); i >= 0 {
				o.Name = o.Name[i+1:]
			}
		}
		groups[gi].entries = append(groups[gi].entries, pickerEntry{skillOption: o})
	}
	return groups
}

func groupTitle(o skillOption) string {
	switch skill.SourceKind(o.Source) {
	case skill.SourceRecipe:
		return "Recipe " + o.SourceRef
	case skill.SourceGitHub:
		return "GitHub " + o.SourceRef
	}
	return strings.TrimSpace(o.Source + " " + o.SourceRef)
}

// rebuildRows reconstrói a lista achatada respeitando expansão dos grupos e o
// filtro aplicado (com filtro, os grupos aparecem sempre expandidos).
func (m *skillPicker) rebuildRows() {
	m.rows = m.rows[:0]
	f := strings.ToLower(strings.TrimSpace(m.appliedFilter))
	for gi := range m.groups {
		g := &m.groups[gi]
		var visible []int
		for ei, e := range g.entries {
			if f == "" || entryMatches(e.skillOption, f) {
				visible = append(visible, ei)
			}
		}
		if len(visible) == 0 {
			continue
		}
		m.rows = append(m.rows, pickerRow{group: gi, entry: -1})
		if f != "" || g.expanded {
			for _, ei := range visible {
				m.rows = append(m.rows, pickerRow{group: gi, entry: ei})
			}
		}
	}
	m.clampCursor()
}

func entryMatches(o skillOption, filterLower string) bool {
	return strings.Contains(strings.ToLower(o.Label+" "+o.SourceRef), filterLower)
}

func (m *skillPicker) rowAt(i int) (pickerRow, bool) {
	if i < 0 || i >= len(m.rows) {
		return pickerRow{}, false
	}
	return m.rows[i], true
}

func (m *skillPicker) entryAt(i int) (pickerEntry, bool) {
	r, ok := m.rowAt(i)
	if !ok || r.entry < 0 {
		return pickerEntry{}, false
	}
	return m.groups[r.group].entries[r.entry], true
}

func (m *skillPicker) clampCursor() {
	if len(m.rows) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor > len(m.rows)-1 {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *skillPicker) selectedIDs() []string {
	var ids []string
	for _, g := range m.groups {
		for _, e := range g.entries {
			if e.selected {
				ids = append(ids, e.ID)
			}
		}
	}
	return ids
}

func (m *skillPicker) selectedCount() int {
	n := 0
	for _, g := range m.groups {
		for _, e := range g.entries {
			if e.selected {
				n++
			}
		}
	}
	return n
}

// --- update ---

// Init não agenda comandos: a primeira WindowSizeMsg chega do programa.
func (m skillPicker) Init() tea.Cmd {
	return nil
}

func (m skillPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampCursor()
		m.ensureVisible()
		return m, nil
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.move(-3)
			case tea.MouseButtonWheelDown:
				m.move(3)
			}
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m skillPicker) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		m.cancel = true
		return m, tea.Quit
	}

	// Ajuda modal: qualquer tecla fecha.
	if m.help {
		m.help = false
		return m, nil
	}

	// No modo filtro o teclado é texto; navegação só volta após enter/esc.
	if m.filtering {
		switch key {
		case "esc":
			if m.filter == "" {
				m.filtering = false
			} else {
				m.filter = ""
			}
		case "enter":
			m.filtering = false
			m.appliedFilter = m.filter
			m.rebuildRows()
			m.offset = 0
			m.ensureVisible()
		case "backspace":
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.filter += string(msg.Runes)
			}
		}
		return m, nil
	}

	switch key {
	case "esc":
		switch {
		case m.appliedFilter != "":
			m.appliedFilter = ""
			m.rebuildRows()
			m.offset = 0
			m.ensureVisible()
		case m.detail:
			m.detail = false
		default:
			m.cancel = true
			return m, tea.Quit
		}
	case "enter":
		return m, tea.Quit
	case "/":
		m.filtering = true
	case "tab":
		m.detail = !m.detail
	case "?":
		m.help = true
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-m.visibleRowCount())
	case "pgdown":
		m.move(m.visibleRowCount())
	case "home", "g":
		m.cursor = 0
		m.ensureVisible()
	case "end", "G":
		m.cursor = len(m.rows) - 1
		m.ensureVisible()
	case "left", "h":
		if r, ok := m.rowAt(m.cursor); ok && r.entry == -1 && m.groups[r.group].expanded {
			m.groups[r.group].expanded = false
			m.rebuildRows()
			m.ensureVisible()
		}
	case "right", "l":
		if r, ok := m.rowAt(m.cursor); ok && r.entry == -1 && !m.groups[r.group].expanded {
			m.groups[r.group].expanded = true
			m.rebuildRows()
			m.ensureVisible()
		}
	case " ", "space":
		m.toggleCursor()
	}
	return m, nil
}

func (m *skillPicker) toggleCursor() {
	r, ok := m.rowAt(m.cursor)
	if !ok {
		return
	}
	if r.entry == -1 {
		m.groups[r.group].expanded = !m.groups[r.group].expanded
		m.rebuildRows()
		m.ensureVisible()
		return
	}
	e := &m.groups[r.group].entries[r.entry]
	e.selected = !e.selected
}

func (m *skillPicker) move(d int) {
	m.cursor += d
	m.clampCursor()
	m.ensureVisible()
}

// --- layout ---

func (m skillPicker) rowHeight(r pickerRow) int {
	if r.entry == -1 {
		return 2 // linha em branco + cabeçalho
	}
	return 2 // nome + descrição
}

// listBudget é o número de linhas de terminal disponíveis para a lista.
func (m skillPicker) listBudget() int {
	h := m.height
	if h <= 0 {
		h = 24
	}
	n := h - lipgloss.Height(m.headerView()) - lipgloss.Height(m.footerView())
	if n < 3 {
		n = 3
	}
	return n
}

func (m skillPicker) visibleRowCount() int {
	return m.visibleRowCountFrom(m.offset)
}

func (m skillPicker) visibleRowCountFrom(offset int) int {
	budget := m.listBudget()
	used, n := 0, 0
	for i := offset; i < len(m.rows); i++ {
		h := m.rowHeight(m.rows[i])
		if used+h > budget {
			break
		}
		used += h
		n++
	}
	return n
}

// ensureVisible ajusta o offset para que a linha do cursor esteja na janela.
func (m *skillPicker) ensureVisible() {
	if m.offset < 0 {
		m.offset = 0
	}
	for m.cursor < m.offset && m.offset > 0 {
		m.offset--
	}
	for m.offset < len(m.rows) && m.cursor >= m.offset+m.visibleRowCountFrom(m.offset) {
		m.offset++
	}
	if m.offset >= len(m.rows) {
		m.offset = max(0, len(m.rows)-1)
	}
}

// --- view ---

func (m skillPicker) View() string {
	if m.help {
		return m.helpView()
	}
	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n")
	b.WriteString(m.listView())
	b.WriteString("\n")
	if m.detail {
		b.WriteString(m.detailView())
		b.WriteString("\n")
	}
	b.WriteString(m.footerView())
	return b.String()
}

func (m skillPicker) headerView() string {
	var lines []string
	if m.width >= bannerMinWidth {
		lines = append(lines, buildBanner()...)
	} else {
		lines = append(lines, pickerAccent.Render("ORO SKILLS"))
	}
	lines = append(lines,
		"",
		pickerDim.Render(fmt.Sprintf("────── VERSION %s ──────", m.version)),
		pickerTagline.Render(m.title),
		pickerTip.Render("ⓘ dica: `oro skills update` mantém as skills gerenciadas atualizadas"),
		"",
		pickerDivider.Render(strings.Repeat("─", min(m.width-4, 64))),
	)
	if m.filtering {
		lines = append(lines, "", pickerAccent.Render("filtro › ")+pickerName.Render(m.filter)+"█")
	} else if m.appliedFilter != "" {
		lines = append(lines, "", pickerDim.Render(fmt.Sprintf("filtro: %q — esc para limpar", m.appliedFilter)))
	}
	return lipgloss.JoinVertical(lipgloss.Center, lines...)
}

func (m skillPicker) listView() string {
	budget := m.listBudget()
	var lines []string
	used := 0
	for i := m.offset; i < len(m.rows); i++ {
		h := m.rowHeight(m.rows[i])
		if used+h > budget {
			break
		}
		used += h
		lines = append(lines, m.renderRow(i)...)
	}
	if len(lines) == 0 {
		lines = append(lines, pickerDim.Render("nada corresponde ao filtro"))
	}
	return strings.Join(lines, "\n")
}

func (m skillPicker) renderRow(i int) []string {
	r := m.rows[i]
	g := &m.groups[r.group]
	if r.entry == -1 {
		arrow, expanded := "▸", false
		if g.expanded {
			arrow, expanded = "▾", true
		}
		f := strings.ToLower(strings.TrimSpace(m.appliedFilter))
		count := len(g.entries)
		if f != "" {
			count = 0
			for _, e := range g.entries {
				if entryMatches(e.skillOption, f) {
					count++
				}
			}
		}
		line := pickerGroupArrow.Render(arrow) + " " + pickerGroupTitle.Render(g.title) +
			pickerCount.Render(fmt.Sprintf(" (%d)", count))
		if m.cursor == i {
			hint := "espaço recolhe"
			if !expanded {
				hint = "espaço expande"
			}
			if m.filtering {
				hint = ""
			}
			if hint != "" {
				line += pickerHint.Render("  ·  " + hint)
			}
		}
		return []string{"", line}
	}

	e := &g.entries[r.entry]
	marker, markerStyle := "□", pickerMarkerOff
	nameStyle := pickerName
	if e.selected {
		marker, markerStyle = "■", pickerMarkerOn
		nameStyle = pickerNameOn
	}
	nameLine := markerStyle.Render(marker) + " " + nameStyle.Render(e.Name)
	if m.cursor == i {
		pad := m.width - lipgloss.Width(nameLine)
		if pad > 0 {
			nameLine = pickerCursorBg.Render(nameLine + strings.Repeat(" ", pad))
		}
	}
	desc := "  " + pickerDesc.Render(truncateRunes(e.Description, max(0, m.width-2)))
	return []string{nameLine, desc}
}

func (m skillPicker) detailView() string {
	e, ok := m.entryAt(m.cursor)
	if !ok {
		return ""
	}
	w := max(20, m.width-2)
	title := " " + pickerName.Render(e.Name) + "  " +
		pickerDim.Render(fmt.Sprintf("%s  ·  %s:%s", e.Version, e.Source, e.SourceRef))
	id := " " + pickerDim.Render("id: "+e.ID)
	desc := " " + pickerDesc.Render(strings.Join(wrapText(e.Description, w-4), "\n"))
	body := title + "\n" + id + "\n" + desc
	return pickerBarBorder.Width(w).Render(body)
}

func (m skillPicker) footerView() string {
	var parts []string
	for _, kv := range [][2]string{
		{"espaço", "marcar"},
		{"enter", "instalar"},
		{"/", "filtrar"},
		{"tab", "detalhes"},
		{"esc", "sair"},
		{"?", "ajuda"},
	} {
		parts = append(parts, pickerAccent.Render(kv[0])+pickerDim.Render(" "+kv[1]))
	}
	left := strings.Join(parts, pickerDim.Render(" · "))
	sep := m.width - lipgloss.Width(left) - lipgloss.Width(rightSummary(m)) - 4
	if sep < 3 {
		return pickerBarBorder.Width(max(0, m.width-2)).Render(left)
	}
	content := left + strings.Repeat(" ", sep) + rightSummary(m)
	return pickerBarBorder.Width(max(0, m.width-2)).Render(content)
}

func rightSummary(m skillPicker) string {
	switch n := m.selectedCount(); n {
	case 0:
		return pickerDim.Render("nenhuma marcada")
	case 1:
		return pickerSelectedSum.Render("1 marcada")
	default:
		return pickerSelectedSum.Render(fmt.Sprintf("%d marcadas", n))
	}
}

func (m skillPicker) helpView() string {
	var lines []string
	for _, kv := range [][2]string{
		{"↑/↓ · j/k", "mover o cursor"},
		{"espaço", "expandir/recolher grupo · marcar skill"},
		{"enter", "instalar as skills marcadas"},
		{"/", "filtrar por nome, descrição ou origem"},
		{"tab", "painel de detalhes da skill sob o cursor"},
		{"esc", "sair sem instalar"},
		{"?", "abrir/fechar esta ajuda"},
	} {
		lines = append(lines, pickerAccent.Render(fmt.Sprintf("%-12s", kv[0]))+pickerDim.Render(kv[1]))
	}
	lines = append(lines, "", pickerHint.Render("qualquer tecla fecha"))
	body := pickerBarBorder.Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}

// --- banner ---

// bannerLetters desenha "ORO SKILLS" em fonte bloco (ANSI Shadow) para o
// cabeçalho do picker.
var bannerLetters = map[rune][]string{
	'O': {
		" ██████╗ ",
		"██╔═══██╗",
		"██║   ██║",
		"██║   ██║",
		"╚██████╔╝",
		" ╚═════╝ ",
	},
	'R': {
		"██████╗ ",
		"██╔══██╗",
		"██████╔╝",
		"██╔══██╗",
		"██║  ██║",
		"╚═╝  ╚═╝",
	},
	'S': {
		"███████╗",
		"██╔════╝",
		"███████╗",
		"╚════██║",
		"███████║",
		"╚══════╝",
	},
	'K': {
		"██╗  ██╗",
		"██║ ██╔╝",
		"█████╔╝ ",
		"██╔═██╗ ",
		"██║  ██╗",
		"╚═╝  ╚═╝",
	},
	'I': {
		"██╗",
		"██║",
		"██║",
		"██║",
		"██║",
		"╚═╝",
	},
	'L': {
		"██╗     ",
		"██║     ",
		"██║     ",
		"██║     ",
		"███████╗",
		"╚══════╝",
	},
}

func bannerWord(word string) [6]string {
	var rows [6][]string
	for _, r := range word {
		gl, ok := bannerLetters[r]
		if !ok {
			continue
		}
		for j := 0; j < 6; j++ {
			rows[j] = append(rows[j], gl[j])
		}
	}
	var out [6]string
	for j := range out {
		out[j] = strings.Join(rows[j], "")
	}
	return out
}

// buildBanner devolve as 6 linhas do banner com gradiente azul→ciano.
func buildBanner() []string {
	a := bannerWord("ORO")
	b := bannerWord("SKILLS")
	rows := make([]string, 6)
	for j := 0; j < 6; j++ {
		line := strings.TrimRight(a[j], " ") + "   " + strings.TrimLeft(b[j], " ")
		rows[j] = lipgloss.NewStyle().
			Foreground(lipgloss.Color(pickerBannerColors[j])).
			Render(line)
	}
	return rows
}

// --- utilitários de texto ---

func truncateRunes(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

func wrapText(s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, word := range words[1:] {
			if lipgloss.Width(line)+1+lipgloss.Width(word) <= w {
				line += " " + word
			} else {
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}
