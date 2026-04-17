package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tevfik/gleann/pkg/gleann"
)

// indexManageState tracks sub-views within the index manager.
type indexManageState int

const (
	imList    indexManageState = iota // browsing indexes
	imDetail                          // viewing one index's details
	imConfirm                         // delete confirmation
)

// deleteResultMsg is sent after an async delete completes.
type deleteResultMsg struct {
	name string
	err  error
}

// IndexManageModel lets users browse, inspect, and delete indexes.
type IndexManageModel struct {
	indexDir string
	indexes  []gleann.IndexMeta
	cursor   int
	state    indexManageState
	width    int
	height   int
	quitting bool
	err      error
	status   string // transient status message ("Deleted ...")
}

// NewIndexManageModel creates the index-management screen.
func NewIndexManageModel(indexDir string) IndexManageModel {
	indexes, err := gleann.ListIndexes(indexDir)
	return IndexManageModel{
		indexDir: indexDir,
		indexes:  indexes,
		err:      err,
	}
}

func (m IndexManageModel) Init() tea.Cmd { return nil }

// ── Update ─────────────────────────────────────────────────────

func (m IndexManageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case deleteResultMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("⚠ Error deleting %q: %v", msg.name, msg.err)
		} else {
			m.status = fmt.Sprintf("✓ Deleted index %q", msg.name)
		}
		// Refresh list.
		indexes, err := gleann.ListIndexes(m.indexDir)
		m.indexes = indexes
		m.err = err
		if m.cursor >= len(m.indexes) && m.cursor > 0 {
			m.cursor--
		}
		m.state = imList
		return m, nil

	case tea.KeyPressMsg:
		// Clear transient status on any key.
		if m.status != "" && m.state == imList {
			m.status = ""
		}

		switch m.state {
		case imList:
			return m.updateList(msg)
		case imDetail:
			return m.updateDetail(msg)
		case imConfirm:
			return m.updateConfirm(msg)
		}
	}

	return m, nil
}

func (m IndexManageModel) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc", "q":
		m.quitting = true
		return m, tea.Quit

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "j":
		if m.cursor < len(m.indexes)-1 {
			m.cursor++
		}

	case "enter", "right", "l":
		if len(m.indexes) > 0 {
			m.state = imDetail
		}

	case "d", "backspace", "delete":
		if len(m.indexes) > 0 {
			m.state = imConfirm
		}

	case "r":
		// Refresh.
		indexes, err := gleann.ListIndexes(m.indexDir)
		m.indexes = indexes
		m.err = err
		if m.cursor >= len(m.indexes) && m.cursor > 0 {
			m.cursor--
		}
		m.status = "↻ Refreshed"
	}

	return m, nil
}

func (m IndexManageModel) updateDetail(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "esc", "q", "left", "h":
		m.state = imList

	case "d", "backspace", "delete":
		m.state = imConfirm
	}

	return m, nil
}

func (m IndexManageModel) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "y", "Y":
		name := m.indexes[m.cursor].Name
		dir := m.indexDir
		return m, func() tea.Msg {
			err := gleann.RemoveIndex(dir, name)
			return deleteResultMsg{name: name, err: err}
		}

	case "n", "N", "esc", "q":
		m.state = imList
	}

	return m, nil
}

// Quitting returns whether the user wants to leave.
func (m IndexManageModel) Quitting() bool {
	return m.quitting
}

// ── View ───────────────────────────────────────────────────────

func (m IndexManageModel) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	switch m.state {
	case imDetail:
		return tea.NewView(m.viewDetail())
	case imConfirm:
		return tea.NewView(m.viewConfirm())
	default:
		return tea.NewView(m.viewList())
	}
}

// ── List view ──────────────────────────────────────────────────

func (m IndexManageModel) viewList() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(TitleStyle.Render(" 📚 Index Manager "))
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(ErrorBadge.Render(fmt.Sprintf("  Error: %v", m.err)))
		b.WriteString("\n")
		b.WriteString(HelpStyle.Render("  esc back"))
		return b.String()
	}

	if len(m.indexes) == 0 {
		empty := lipgloss.NewStyle().Foreground(ColorMuted).
			Render("  No indexes found. Build one first:\n\n    gleann build my-docs --docs ./documents/")
		b.WriteString(empty)
		b.WriteString("\n\n")
		b.WriteString(HelpStyle.Render("  esc back"))
		return b.String()
	}

	// Status message.
	if m.status != "" {
		statusStyle := lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true)
		if strings.HasPrefix(m.status, "⚠") {
			statusStyle = statusStyle.Foreground(ColorError)
		}
		b.WriteString("  " + statusStyle.Render(m.status) + "\n\n")
	}

	for i, idx := range m.indexes {
		cursor := "  "
		nameStyle := NormalItemStyle
		detailStyle := lipgloss.NewStyle().Foreground(ColorDimFg)

		if i == m.cursor {
			cursor = ActiveItemStyle.Render("▸ ")
			nameStyle = ActiveItemStyle
			detailStyle = lipgloss.NewStyle().Foreground(ColorSecondary)
		}

		name := nameStyle.Render(idx.Name)
		info := detailStyle.Render(
			fmt.Sprintf("  %d passages • %s • dim=%d",
				idx.NumPassages, idx.EmbeddingModel, idx.Dimensions),
		)
		age := lipgloss.NewStyle().Foreground(ColorMuted).
			Render(fmt.Sprintf("  %s", timeAgo(idx.CreatedAt)))

		b.WriteString(cursor + name + info + age + "\n")
	}

	b.WriteString("\n")
	help := HelpStyle.Render("  ↑/↓ navigate • enter details • d delete • r refresh • esc back")
	b.WriteString(help + "\n")

	return b.String()
}

// ── Detail view ────────────────────────────────────────────────

func (m IndexManageModel) viewDetail() string {
	if m.cursor >= len(m.indexes) {
		return ""
	}
	idx := m.indexes[m.cursor]

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(TitleStyle.Render(fmt.Sprintf(" 📋 Index: %s ", idx.Name)))
	b.WriteString("\n\n")

	panelW := 56
	if m.width > 10 && m.width-10 < panelW {
		panelW = m.width - 10
	}

	labelSt := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true).Width(18)
	valSt := lipgloss.NewStyle().Foreground(ColorFg)

	rows := []struct{ label, value string }{
		{"Name", idx.Name},
		{"Backend", idx.Backend},
		{"Embedding Model", idx.EmbeddingModel},
		{"Dimensions", fmt.Sprintf("%d", idx.Dimensions)},
		{"Passages", fmt.Sprintf("%d", idx.NumPassages)},
		{"Version", idx.Version},
		{"Created", idx.CreatedAt.Format("2006-01-02 15:04:05")},
		{"Updated", idx.UpdatedAt.Format("2006-01-02 15:04:05")},
	}

	for _, r := range rows {
		b.WriteString("  " + labelSt.Render(r.label) + valSt.Render(r.value) + "\n")
	}

	b.WriteString("\n")

	// Danger zone.
	dangerBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorError).
		Padding(0, 2).
		MarginLeft(4).
		Width(panelW).
		Render(
			lipgloss.NewStyle().Foreground(ColorError).Bold(true).Render("⚠ Danger Zone") + "\n" +
				lipgloss.NewStyle().Foreground(ColorDimFg).Render("Press d to permanently delete this index."),
		)
	b.WriteString(dangerBox + "\n\n")

	help := HelpStyle.Render("  ← back • d delete • esc quit")
	b.WriteString(help + "\n")

	return b.String()
}

// ── Confirm view ───────────────────────────────────────────────

func (m IndexManageModel) viewConfirm() string {
	if m.cursor >= len(m.indexes) {
		return ""
	}
	idx := m.indexes[m.cursor]

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(TitleStyle.Render(" ⚠  Delete Index "))
	b.WriteString("\n\n")

	panelW := 56
	if m.width > 10 && m.width-10 < panelW {
		panelW = m.width - 10
	}

	warnBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorError).
		Padding(1, 2).
		MarginLeft(4).
		Width(panelW).
		Render(
			lipgloss.NewStyle().Foreground(ColorError).Bold(true).
				Render(fmt.Sprintf("Delete %q?", idx.Name)) + "\n\n" +
				lipgloss.NewStyle().Foreground(ColorDimFg).
					Render(fmt.Sprintf("This will permanently remove:\n"+
						"  • %d passages\n"+
						"  • All embeddings & index files\n\n"+
						"This action cannot be undone.",
						idx.NumPassages)),
		)
	b.WriteString(warnBox + "\n\n")

	yStyle := lipgloss.NewStyle().Foreground(ColorError).Bold(true)
	nStyle := lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true)
	b.WriteString(fmt.Sprintf("  %s to confirm • %s to cancel\n",
		yStyle.Render("[y]es"),
		nStyle.Render("[n]o / esc"),
	))

	return b.String()
}

// ── Helpers ────────────────────────────────────────────────────

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 2, 2006")
	}
}
