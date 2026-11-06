package tui

import (
	"fmt"
	"os"
	"os/exec"
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
	imStorage                         // vector storage mode: confirm a switch
)

// deleteResultMsg is sent after an async delete completes.
type deleteResultMsg struct {
	name string
	err  error
}

// storageResultMsg is sent when an async storage-mode switch finishes.
type storageResultMsg struct {
	name, target, out string
	err               error
}

// storageRunner switches an index's vector storage by running
// `gleann index storage <name> --set <mode>` (a separate process keeps the TUI
// responsive and reuses the CLI's rollback-on-failure logic). Tests replace it.
var storageRunner = func(indexDir, name, target string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	out, err := exec.Command(exe, "index", "storage", name, "--set", target, "--index-dir", indexDir).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func otherStorageMode(mode string) string {
	if mode == gleann.StorageRecompute {
		return gleann.StorageStore
	}
	return gleann.StorageRecompute
}

func fmtBytes(n int64) string {
	const k = 1024.0
	f := float64(n)
	switch {
	case f < k:
		return fmt.Sprintf("%d B", n)
	case f < k*k:
		return fmt.Sprintf("%.1f KB", f/k)
	case f < k*k*k:
		return fmt.Sprintf("%.1f MB", f/k/k)
	}
	return fmt.Sprintf("%.2f GB", f/k/k/k)
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
	busy     string // non-empty while a long operation runs ("Switching x ...")
	// defaultMode is the storage mode used for new indexes ("store"/"recompute").
	defaultMode string
}

// NewIndexManageModel creates the index-management screen.
func NewIndexManageModel(indexDir string) IndexManageModel {
	indexes, err := gleann.ListIndexes(indexDir)
	mode := gleann.StorageStore
	if cfg := LoadSavedConfig(); cfg != nil && cfg.StorageMode != "" {
		mode = cfg.StorageMode
	}
	return IndexManageModel{
		indexDir:    indexDir,
		indexes:     indexes,
		err:         err,
		defaultMode: mode,
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

	case storageResultMsg:
		m.busy = ""
		if msg.err != nil {
			m.status = fmt.Sprintf("⚠ Could not switch %q to %s: %v", msg.name, msg.target, firstLine(msg.out, msg.err))
		} else {
			m.status = fmt.Sprintf("✓ %q now uses %s storage", msg.name, msg.target)
		}
		indexes, err := gleann.ListIndexes(m.indexDir)
		m.indexes, m.err = indexes, err
		m.state = imList
		return m, nil

	case tea.KeyPressMsg:
		if m.busy != "" {
			if msg.String() == "ctrl+c" {
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil // ignore keys while converting
		}
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
		case imStorage:
			return m.updateStorage(msg)
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

	case "s":
		if len(m.indexes) > 0 {
			return m.beginStorageSwitch()
		}

	case "g":
		m.defaultMode = otherStorageMode(m.defaultMode)
		mode := m.defaultMode
		if err := UpdateConfig(func(c *OnboardResult) { c.StorageMode = mode }); err != nil {
			m.status = fmt.Sprintf("⚠ Could not save default: %v", err)
		} else {
			m.status = fmt.Sprintf("✓ New indexes will use %s storage", mode)
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

	case "s":
		return m.beginStorageSwitch()
	}

	return m, nil
}

// beginStorageSwitch opens the confirmation for the selected index, or
// explains why it cannot be switched.
func (m IndexManageModel) beginStorageSwitch() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.indexes) {
		return m, nil
	}
	idx := m.indexes[m.cursor]
	vs := gleann.InspectVectorStorage(m.indexDir, idx)
	if !vs.Convertible {
		m.status = fmt.Sprintf("⚠ %q cannot be switched: %s", idx.Name, vs.Note)
		m.state = imList
		return m, nil
	}
	m.state = imStorage
	return m, nil
}

func (m IndexManageModel) updateStorage(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "y", "Y":
		idx := m.indexes[m.cursor]
		vs := gleann.InspectVectorStorage(m.indexDir, idx)
		target := otherStorageMode(vs.Mode)
		dir, name := m.indexDir, idx.Name
		m.busy = fmt.Sprintf("Switching %q to %s storage — re-indexing from stored passages…", name, target)
		return m, func() tea.Msg {
			out, err := storageRunner(dir, name, target)
			return storageResultMsg{name: name, target: target, out: out, err: err}
		}
	case "n", "N", "esc", "q":
		m.state = imList
	}
	return m, nil
}

func firstLine(out string, err error) string {
	if i := strings.LastIndex(strings.TrimSpace(out), "\n"); i >= 0 {
		return strings.TrimSpace(out)[i+1:]
	}
	if out != "" {
		return out
	}
	return err.Error()
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

	if m.busy != "" {
		return tea.NewView("\n" + TitleStyle.Render(" 📚 Index Manager ") + "\n\n  ⏳ " + m.busy + "\n\n" + HelpStyle.Render("  please wait (ctrl+c to quit)") + "\n")
	}
	switch m.state {
	case imStorage:
		return tea.NewView(m.viewStorage())
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
			Render("  No indexes found. Build one first:\n\n    gleann index build my-docs --docs ./documents/")
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

		b.WriteString(cursor + name + info + age + m.storageBadge(idx) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(ColorMuted).
		Render(fmt.Sprintf("  New indexes use %s storage", m.defaultMode)) + "\n")
	help := HelpStyle.Render("  ↑/↓ navigate • enter details • s switch vector storage • g default for new • d delete • r refresh • esc back")
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
	vs := gleann.InspectVectorStorage(m.indexDir, idx)
	rows = append(rows,
		struct{ label, value string }{"Vector storage", vs.Mode + " — " + vs.Note},
		struct{ label, value string }{"Index file", fmtBytes(vs.IndexBytes) + "  (with stored vectors: " + fmtBytes(vs.FullBytes) + ")"},
		struct{ label, value string }{"Passage store", fmtBytes(vs.PassageBytes)},
	)

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

	help := HelpStyle.Render("  ← back • s switch vector storage • d delete • esc quit")
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

// storageBadge is the per-index vector storage tag shown in the list.
func (m IndexManageModel) storageBadge(idx gleann.IndexMeta) string {
	vs := gleann.InspectVectorStorage(m.indexDir, idx)
	switch vs.Mode {
	case gleann.StorageStore:
		return lipgloss.NewStyle().Foreground(ColorSuccess).Render("  ▣ vectors stored " + fmtBytes(vs.IndexBytes))
	case gleann.StorageRecompute:
		return lipgloss.NewStyle().Foreground(ColorAccent).Render("  ▢ recompute " + fmtBytes(vs.IndexBytes) + " (slow search)")
	case "none":
		return lipgloss.NewStyle().Foreground(ColorMuted).Render("  ▫ keyword only")
	}
	return ""
}

func (m IndexManageModel) viewStorage() string {
	if m.cursor >= len(m.indexes) {
		return ""
	}
	idx := m.indexes[m.cursor]
	vs := gleann.InspectVectorStorage(m.indexDir, idx)
	target := otherStorageMode(vs.Mode)

	var what string
	if target == gleann.StorageStore {
		what = fmt.Sprintf("Search becomes fast (embeds only the query).\n"+
			"Index file grows from %s to about %s.", fmtBytes(vs.IndexBytes), fmtBytes(vs.FullBytes+vs.IndexBytes))
	} else {
		what = fmt.Sprintf("Index file shrinks from %s to a few %% of that.\n"+
			"Every search will re-embed hundreds of passages: expect seconds per query\n"+
			"and a running embedding model at search time.", fmtBytes(vs.IndexBytes))
	}
	panelW := 64
	if m.width > 10 && m.width-10 < panelW {
		panelW = m.width - 10
	}
	var b strings.Builder
	b.WriteString("\n" + TitleStyle.Render(" 🔁 Switch vector storage ") + "\n\n")
	b.WriteString(lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorAccent).
		Padding(1, 2).MarginLeft(4).Width(panelW).Render(
		lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%s: %s → %s", idx.Name, vs.Mode, target))+"\n\n"+
			lipgloss.NewStyle().Foreground(ColorDimFg).Render(what+
				"\n\nThe vector index is rebuilt from the stored passages (source files are not\n"+
				"needed; the embedding cache is reused). If it fails the old index is restored.")) + "\n\n")
	b.WriteString(fmt.Sprintf("  %s to switch • %s to cancel\n",
		lipgloss.NewStyle().Foreground(ColorAccent).Bold(true).Render("[y]es"),
		lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("[n]o / esc")))
	return b.String()
}
