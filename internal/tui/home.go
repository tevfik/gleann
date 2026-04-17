package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Screen represents the active screen.
type Screen int

const (
	ScreenHome Screen = iota
	ScreenOnboard
	ScreenChat
	ScreenIndexes
	ScreenPlugins
)

// menuItem defines a main-menu option.
type menuItem struct {
	title string
	desc  string
	icon  string
}

var menuItems = []menuItem{
	{title: "Setup", desc: "Configure providers, models & paths", icon: "⚙ "},
	{title: "Chat", desc: "Interactive RAG-powered Q&A", icon: "💬"},
	{title: "Indexes", desc: "List & manage your indexes", icon: "📚"},
	{title: "Plugins", desc: "Install & manage plugins", icon: "🔌"},
	{title: "Quit", desc: "Exit gleann", icon: "👋"},
}

// HomeModel is the main hub of the TUI.
type HomeModel struct {
	cursor    int
	width     int
	height    int
	quitting  bool
	indexList []string // pre-fetched index names
	chosen    Screen
}

// NewHomeModel creates a new home screen model.
func NewHomeModel() HomeModel {
	return HomeModel{}
}

func (m HomeModel) Init() tea.Cmd {
	return nil
}

func (m HomeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down", "j":
			if m.cursor < len(menuItems)-1 {
				m.cursor++
			}

		case "enter":
			switch m.cursor {
			case 0: // Setup
				m.chosen = ScreenOnboard
				return m, tea.Quit
			case 1: // Chat
				m.chosen = ScreenChat
				return m, tea.Quit
			case 2: // Indexes
				m.chosen = ScreenIndexes
				return m, tea.Quit
			case 3: // Plugins
				m.chosen = ScreenPlugins
				return m, tea.Quit
			case 4: // Quit
				m.quitting = true
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

// Chosen returns the screen the user picked.
func (m HomeModel) Chosen() Screen {
	return m.chosen
}

// Quitting returns whether the user wants to quit.
func (m HomeModel) Quitting() bool {
	return m.quitting
}

func (m HomeModel) View() tea.View {
	if m.quitting {
		return tea.NewView("\n  " + lipgloss.NewStyle().Foreground(ColorMuted).Render("Bye! 👋") + "\n")
	}

	var b strings.Builder

	b.WriteString(Logo())
	b.WriteString("\n")
	b.WriteString(SubtitleStyle.Render("  Lightweight Vector Database with Graph-Based Recomputation"))
	b.WriteString("\n\n")

	// Menu.
	for i, item := range menuItems {
		cursor := "  "
		style := NormalItemStyle
		descSt := DescStyle

		if i == m.cursor {
			cursor = ActiveItemStyle.Render("▸ ")
			style = ActiveItemStyle
			descSt = ActiveDescStyle
		}

		title := style.Render(fmt.Sprintf("%s %s", item.icon, item.title))
		desc := descSt.Render(item.desc)

		b.WriteString(cursor + title + "\n")
		b.WriteString("    " + desc + "\n\n")
	}

	// Footer.
	ver := lipgloss.NewStyle().Foreground(ColorMuted).Render(fmt.Sprintf("v%s", "1.0.0"))
	help := HelpStyle.Render("  ↑/↓ navigate • enter select • q quit")
	b.WriteString(help + "  " + ver + "\n")

	return tea.NewView(b.String())
}
