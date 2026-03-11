package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tevfik/gleann/pkg/gleann"
)

// IndexListModel shows available indexes for selection (used before chat).
type IndexListModel struct {
	indexes  []gleann.IndexMeta
	cursor   int
	width    int
	height   int
	selected string
	quitting bool
	skipped  bool // user chose "no index" (pure LLM mode)
	err      error
}

// NewIndexListModel creates an index list model.
func NewIndexListModel(indexDir string) IndexListModel {
	indexes, err := gleann.ListIndexes(indexDir)
	return IndexListModel{
		indexes: indexes,
		err:     err,
	}
}

func (m IndexListModel) Init() tea.Cmd { return nil }

func (m IndexListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.quitting = true
			return m, tea.Quit

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "enter", " ":
			// Cursor past all real indexes → "no index" option.
			if m.cursor >= len(m.indexes) {
				m.skipped = true
			} else if len(m.indexes) > 0 {
				m.selected = m.indexes[m.cursor].Name
			} else {
				// no indexes at all, Enter also skips to LLM mode
				m.skipped = true
			}
			return m, tea.Quit
		case "down", "j":
			// +1 for the "no index" row
			if m.cursor < len(m.indexes) {
				m.cursor++
			}
		}
	}
	return m, nil
}

// Selected returns the chosen index name (empty if cancelled or skipped).
func (m IndexListModel) Selected() string {
	if m.quitting {
		return ""
	}
	return m.selected
}

// Quitting returns true when the user pressed esc/q/ctrl+c to cancel.
func (m IndexListModel) Quitting() bool {
	return m.quitting
}

// Skipped returns true when the user chose "Continue without index" (pure LLM).
func (m IndexListModel) Skipped() bool {
	return m.skipped
}

func (m IndexListModel) View() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(TitleStyle.Render(" 📚 Select an Index "))
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(ErrorBadge.Render(fmt.Sprintf("  Error: %v", m.err)))
		b.WriteString("\n")
		return b.String()
	}

	if len(m.indexes) == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(ColorMuted).Render("  No indexes found."))
		b.WriteString("\n")
		noIdxStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#7C9EFF"))
		b.WriteString(ActiveItemStyle.Render("▸ ") + noIdxStyle.Render("Continue without index (pure LLM)"))
		b.WriteString("\n\n")
		b.WriteString(HelpStyle.Render("  enter continue • esc back"))
		return b.String()
	}

	// Show the "no index" option at the bottom.
	noIdxRow := len(m.indexes)
	for i, idx := range m.indexes {
		cursor := "  "
		nameStyle := NormalItemStyle
		if i == m.cursor {
			cursor = ActiveItemStyle.Render("▸ ")
			nameStyle = ActiveItemStyle
		}
		name := nameStyle.Render(idx.Name)
		info := lipgloss.NewStyle().Foreground(ColorDimFg).Render(
			fmt.Sprintf("  %d passages • %s • %s", idx.NumPassages, idx.Backend, idx.EmbeddingModel),
		)
		b.WriteString(cursor + name + info + "\n")
	}

	// "No index" row.
	b.WriteString("\n")
	noIdxStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#7C9EFF"))
	if m.cursor == noIdxRow {
		b.WriteString(ActiveItemStyle.Render("▸ ") + noIdxStyle.Render("Continue without index (pure LLM)") + "\n")
	} else {
		b.WriteString("   " + noIdxStyle.Render("Continue without index (pure LLM)") + "\n")
	}

	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("  ↑/↓ navigate • enter select • esc back"))
	b.WriteString("\n")
	return b.String()
}
