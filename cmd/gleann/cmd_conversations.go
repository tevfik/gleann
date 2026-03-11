package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tevfik/gleann/pkg/conversations"
)

func cmdConversations(args []string) {
	store := conversations.DefaultStore()

	switch {
	case hasFlag(args, "--list"):
		listConversations(store)
	case hasFlag(args, "--pick"):
		pickConversation(store, args)
	case hasFlag(args, "--show-last"):
		showLastConversation(store)
	case getFlag(args, "--show") != "":
		showConversation(store, getFlag(args, "--show"))
	case len(getDeleteArgs(args)) > 0:
		deleteConversations(store, getDeleteArgs(args))
	case getFlag(args, "--delete-older-than") != "":
		deleteOlderThan(store, getFlag(args, "--delete-older-than"))
	default:
		// Default: list
		listConversations(store)
	}
}

// ─── Interactive conversation picker (bubbletea) ─────────────────────────────

type pickerModel struct {
	items    []conversations.Conversation
	cursor   int
	selected *conversations.Conversation
	quitting bool
}

func newPickerModel(items []conversations.Conversation) pickerModel {
	return pickerModel{items: items}
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter", " ":
			if len(m.items) > 0 {
				c := m.items[m.cursor]
				m.selected = &c
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m pickerModel) View() string {
	if m.quitting {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("💬 Select a conversation (↑/↓ or j/k to move, Enter to select, q to cancel):\n\n")
	for i, c := range m.items {
		age := time.Since(c.UpdatedAt).Round(time.Second)
		msgs := c.MessageCount()
		title := truncate(c.Title, 48)
		if title == "" {
			title = "(untitled)"
		}
		cursor := "  "
		if i == m.cursor {
			cursor = "▶ "
		}
		sb.WriteString(fmt.Sprintf("%s%s  %-48s  %d msgs  %s ago\n",
			cursor, conversations.ShortID(c.ID), title, msgs, formatAge(age)))
	}
	sb.WriteString("\n")
	return sb.String()
}

func pickConversation(store *conversations.Store, args []string) {
	convs, err := store.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(convs) == 0 {
		fmt.Println("No saved conversations.")
		return
	}

	m := newPickerModel(convs)
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Build filtered args (--pick removed) for reuse below.
	filtered := make([]string, 0, len(args)+2)
	for _, a := range args {
		if a != "--pick" {
			filtered = append(filtered, a)
		}
	}

	result, ok := finalModel.(pickerModel)
	if !ok || result.selected == nil {
		// User cancelled → start a fresh chat (index picker / TUI flow).
		cmdChat(filtered)
		return
	}

	conv := result.selected
	fmt.Fprintf(os.Stderr, "📎 Loading conversation %s (%q)\n", conversations.ShortID(conv.ID), conv.Title)

	// Continue the picked conversation in interactive TUI chat.
	// (filtered was already built above with --pick stripped)
	filtered = append(filtered, "--continue", conv.ID)
	cmdChat(filtered)
}

func listConversations(store *conversations.Store) {
	convs, err := store.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(convs) == 0 {
		fmt.Println("No saved conversations.")
		return
	}

	fmt.Printf("💬 Conversations (%d):\n\n", len(convs))
	for _, c := range convs {
		age := time.Since(c.UpdatedAt).Round(time.Second)
		msgs := c.MessageCount()
		idx := c.IndexLabel()
		if idx == "" {
			idx = "-"
		}
		fmt.Printf("  %s  %-40s  %d msgs  index=%s  %s ago\n",
			conversations.ShortID(c.ID), truncate(c.Title, 40), msgs, idx, formatAge(age))
	}
}

func showConversation(store *conversations.Store, idOrTitle string) {
	conv, err := store.Load(idOrTitle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	printConversation(conv)
}

func showLastConversation(store *conversations.Store) {
	conv, err := store.Latest()
	if err != nil || conv == nil {
		fmt.Fprintln(os.Stderr, "no saved conversations")
		os.Exit(1)
	}
	printConversation(conv)
}

func printConversation(conv *conversations.Conversation) {
	fmt.Printf("📎 Conversation: %s\n", conversations.ShortID(conv.ID))
	fmt.Printf("   Title:   %s\n", conv.Title)
	fmt.Printf("   Indexes: %s\n", conv.IndexLabel())
	fmt.Printf("   Model:   %s\n", conv.Model)
	fmt.Printf("   Created: %s\n", conv.CreatedAt.Format(time.RFC3339))
	fmt.Printf("   Updated: %s\n", conv.UpdatedAt.Format(time.RFC3339))
	fmt.Printf("   Messages: %d\n\n", conv.MessageCount())

	for _, m := range conv.Messages {
		switch m.Role {
		case "system":
			fmt.Printf("  [system] %s\n\n", truncate(m.Content, 120))
		case "user":
			fmt.Printf("  You: %s\n\n", m.Content)
		case "assistant":
			fmt.Printf("  Assistant: %s\n\n", m.Content)
		}
	}
}

func deleteConversations(store *conversations.Store, ids []string) {
	for _, id := range ids {
		if err := store.Delete(id); err != nil {
			fmt.Fprintf(os.Stderr, "error deleting %q: %v\n", id, err)
			continue
		}
		fmt.Printf("🗑  Deleted conversation %s\n", id)
	}
}

func deleteOlderThan(store *conversations.Store, durationStr string) {
	d, err := time.ParseDuration(durationStr)
	if err != nil {
		// Try common formats: "7d", "30d", "1w"
		d, err = parseFriendlyDuration(durationStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid duration %q (examples: 24h, 7d, 30d)\n", durationStr)
			os.Exit(1)
		}
	}
	count, err := store.DeleteOlderThan(d)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("🗑  Deleted %d conversation(s) older than %s\n", count, durationStr)
}

// getDeleteArgs collects all values after --delete flags.
func getDeleteArgs(args []string) []string {
	var ids []string
	for i, a := range args {
		if a == "--delete" && i+1 < len(args) {
			ids = append(ids, args[i+1])
		}
	}
	return ids
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

func formatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func parseFriendlyDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	if strings.HasSuffix(s, "w") {
		var weeks int
		if _, err := fmt.Sscanf(s, "%dw", &weeks); err == nil {
			return time.Duration(weeks) * 7 * 24 * time.Hour, nil
		}
	}
	return 0, fmt.Errorf("unsupported duration: %s", s)
}
