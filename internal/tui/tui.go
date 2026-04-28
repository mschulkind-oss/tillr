// Package tui implements the post-reset Bubble Tea TUI.
//
// Three-pane layout: sidebar (Features / Personas / Retros tabs),
// main view (detail of selected item), command bar.
//
// MVP is read-only: list, navigate, view. Mutations go through the
// CLI / conductor for now. Vim-flavored bindings (j/k navigate,
// q/esc quit, ?/h help, tab/shift+tab switch tabs).
package tui

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/mschulkind-oss/tillr/internal/persona"
)

type tab int

const (
	tabFeatures tab = iota
	tabPersonas
	tabRetros
	tabCount
)

func (t tab) String() string {
	switch t {
	case tabFeatures:
		return "Features"
	case tabPersonas:
		return "Personas"
	case tabRetros:
		return "Retros"
	}
	return ""
}

type keymap struct {
	NextTab key.Binding
	PrevTab key.Binding
	Down    key.Binding
	Up      key.Binding
	Open    key.Binding
	Refresh key.Binding
	Quit    key.Binding
	Help    key.Binding
}

func defaultKeymap() keymap {
	return keymap{
		NextTab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next tab"),
		),
		PrevTab: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "prev tab"),
		),
		Down: key.NewBinding(
			key.WithKeys("j", "down"),
			key.WithHelp("j", "down"),
		),
		Up: key.NewBinding(
			key.WithKeys("k", "up"),
			key.WithHelp("k", "up"),
		),
		Open: key.NewBinding(
			key.WithKeys("enter", "l", "right"),
			key.WithHelp("enter", "open"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "refresh"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "esc", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
	}
}

func (k keymap) ShortHelp() []key.Binding {
	return []key.Binding{k.NextTab, k.Down, k.Up, k.Open, k.Refresh, k.Quit, k.Help}
}

func (k keymap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.NextTab, k.PrevTab},
		{k.Down, k.Up, k.Open},
		{k.Refresh, k.Quit, k.Help},
	}
}

// Model is the TUI state.
type Model struct {
	db          *sql.DB
	projectRoot string

	tab tab

	features []models.Feature
	personas []persona.Persona
	retros   []persona.Retro

	cursor int

	detail   string
	viewport viewport.Model
	showing  bool // is the right pane showing detail content

	width  int
	height int

	keys keymap
	help help.Model

	err error
}

// New constructs the TUI model.
func New(database *sql.DB, projectRoot string) Model {
	return Model{
		db:          database,
		projectRoot: projectRoot,
		tab:         tabFeatures,
		keys:        defaultKeymap(),
		help:        help.New(),
	}
}

// Run launches the TUI program against stdin/stdout.
func Run(database *sql.DB, projectRoot string) error {
	p := tea.NewProgram(New(database, projectRoot), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// --- tea.Model implementation ---

func (m Model) Init() tea.Cmd {
	return loadAll(m.db, m.projectRoot)
}

type dataMsg struct {
	features []models.Feature
	personas []persona.Persona
	retros   []persona.Retro
	err      error
}

func loadAll(database *sql.DB, root string) tea.Cmd {
	return func() tea.Msg {
		var msg dataMsg
		project, err := db.GetProject(database)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.features, err = db.ListFeatures(database, project.ID, db.ListFeaturesFilter{})
		if err != nil {
			msg.err = err
			return msg
		}
		msg.personas, err = persona.List(root)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.retros, err = persona.ListRetros(root)
		if err != nil {
			msg.err = err
			return msg
		}
		return msg
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dataMsg:
		m.features = msg.features
		m.personas = msg.personas
		m.retros = msg.retros
		m.err = msg.err
		if m.cursor >= m.itemCount() {
			m.cursor = 0
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Right pane width = ~60% of screen
		rightWidth := m.width - 28
		if rightWidth < 20 {
			rightWidth = 20
		}
		m.viewport.Width = rightWidth
		m.viewport.Height = m.height - 4

	case tea.KeyMsg:
		if m.showing && key.Matches(msg, m.keys.Quit) {
			m.showing = false
			return m, nil
		}
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.NextTab):
			m.tab = (m.tab + 1) % tabCount
			m.cursor = 0
			m.showing = false
		case key.Matches(msg, m.keys.PrevTab):
			m.tab = (m.tab + tabCount - 1) % tabCount
			m.cursor = 0
			m.showing = false
		case key.Matches(msg, m.keys.Down):
			if m.cursor < m.itemCount()-1 {
				m.cursor++
			}
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case key.Matches(msg, m.keys.Open):
			m.showing = true
			return m, m.loadDetail()
		case key.Matches(msg, m.keys.Refresh):
			return m, loadAll(m.db, m.projectRoot)
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
		}

	case detailMsg:
		m.detail = msg.body
		m.viewport.SetContent(msg.body)
	}

	if m.showing {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) View() string {
	if m.err != nil {
		return errorStyle.Render(fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err))
	}

	header := m.headerView()
	body := m.bodyView()
	footer := m.help.View(m.keys)

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// --- view helpers ---

func (m Model) headerView() string {
	var tabs []string
	for t := tab(0); t < tabCount; t++ {
		s := " " + t.String() + " "
		if t == m.tab {
			s = activeTabStyle.Render(s)
		} else {
			s = tabStyle.Render(s)
		}
		tabs = append(tabs, s)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

func (m Model) bodyView() string {
	left := m.listView()
	right := m.detailView()
	gap := lipgloss.NewStyle().Width(2).Render(" ")
	return lipgloss.JoinHorizontal(lipgloss.Top, left, gap, right)
}

func (m Model) listView() string {
	var b strings.Builder
	w := 26
	switch m.tab {
	case tabFeatures:
		if len(m.features) == 0 {
			b.WriteString(emptyStyle.Render("(no features)"))
		}
		for i, f := range m.features {
			line := fmt.Sprintf("#%d  %s", f.ID, truncate(f.Title, 18))
			b.WriteString(rowStyle(i == m.cursor, w).Render(line) + "\n")
		}
	case tabPersonas:
		if len(m.personas) == 0 {
			b.WriteString(emptyStyle.Render("(no personas)"))
		}
		for i, p := range m.personas {
			line := fmt.Sprintf("%-12s %dw", p.Name, p.ContextWords)
			b.WriteString(rowStyle(i == m.cursor, w).Render(line) + "\n")
		}
	case tabRetros:
		if len(m.retros) == 0 {
			b.WriteString(emptyStyle.Render("(no retros)"))
		}
		for i, r := range m.retros {
			b.WriteString(rowStyle(i == m.cursor, w).Render(truncate(r.Name, 24)) + "\n")
		}
	}
	return listFrame.Width(w).Height(m.bodyHeight()).Render(b.String())
}

func (m Model) detailView() string {
	rightWidth := m.width - 30
	if rightWidth < 20 {
		rightWidth = 20
	}
	var content string
	if !m.showing {
		content = emptyStyle.Render("\nPress Enter to open the selected item.")
	} else {
		content = m.viewport.View()
	}
	return detailFrame.Width(rightWidth).Height(m.bodyHeight()).Render(content)
}

func (m Model) bodyHeight() int {
	h := m.height - 6
	if h < 5 {
		h = 5
	}
	return h
}

func (m Model) itemCount() int {
	switch m.tab {
	case tabFeatures:
		return len(m.features)
	case tabPersonas:
		return len(m.personas)
	case tabRetros:
		return len(m.retros)
	}
	return 0
}

type detailMsg struct{ body string }

func (m Model) loadDetail() tea.Cmd {
	return func() tea.Msg {
		switch m.tab {
		case tabFeatures:
			if m.cursor >= len(m.features) {
				return detailMsg{body: ""}
			}
			f := m.features[m.cursor]
			comments, _ := db.ListComments(m.db, "feature", fmt.Sprintf("%d", f.ID))
			return detailMsg{body: renderFeature(f, comments)}
		case tabPersonas:
			if m.cursor >= len(m.personas) {
				return detailMsg{body: ""}
			}
			p := m.personas[m.cursor]
			body, _ := persona.ContextRead(m.projectRoot, p.Name)
			return detailMsg{body: renderPersona(p, body)}
		case tabRetros:
			if m.cursor >= len(m.retros) {
				return detailMsg{body: ""}
			}
			r := m.retros[m.cursor]
			body, _ := persona.ReadRetro(m.projectRoot, r.Name)
			return detailMsg{body: body}
		}
		return detailMsg{body: ""}
	}
}

func renderFeature(f models.Feature, comments []models.Comment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# #%d  %s\n\n", f.ID, f.Title)
	fmt.Fprintf(&b, "Status:  %s\n", f.Status)
	if f.TargetPersona != "" {
		fmt.Fprintf(&b, "Persona: %s\n", f.TargetPersona)
	}
	fmt.Fprintf(&b, "\n")
	if f.Description != "" {
		b.WriteString(f.Description + "\n\n")
	}
	if len(comments) == 0 {
		b.WriteString("(no comments)\n")
		return b.String()
	}
	fmt.Fprintf(&b, "## Comments (%d)\n\n", len(comments))
	for _, c := range comments {
		author := c.AuthorType
		if c.AuthorRole != "" {
			author += "/" + c.AuthorRole
		}
		fmt.Fprintf(&b, "[%s — %s]\n%s\n\n", author, c.CreatedAt.Format("2006-01-02 15:04"), c.Body)
	}
	return b.String()
}

func renderPersona(p persona.Persona, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", p.Name)
	fmt.Fprintf(&b, "Definition: %s\n", p.DefinitionPath)
	fmt.Fprintf(&b, "Context:    %s (%d words)\n", p.ContextPath, p.ContextWords)
	if p.UpdatedAt != "" {
		fmt.Fprintf(&b, "Updated:    %s\n", p.UpdatedAt)
	}
	b.WriteString("\n---\n\n")
	if body == "" {
		b.WriteString("(no context yet)\n")
	} else {
		b.WriteString(body)
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 3 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// --- styling ---

var (
	tabStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(lipgloss.Color("245"))
	activeTabStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("63")).
			Bold(true)
	listFrame = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(lipgloss.Color("237")).
			Padding(0, 1)
	detailFrame = lipgloss.NewStyle().
			Padding(0, 1)
	emptyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true)
	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))
)

func rowStyle(active bool, w int) lipgloss.Style {
	s := lipgloss.NewStyle().Width(w - 2)
	if active {
		s = s.Background(lipgloss.Color("63")).Foreground(lipgloss.Color("15")).Bold(true)
	}
	return s
}
