package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/modules/settings"
)

// SettingsModel edits gleann's settings one at a time. Its rows come from
// Settings(), the registry the web UI's settings form (/api/settings) is built
// from, so both screens show the same fields, options, defaults and checks.
// Every change is saved at once.
type SettingsModel struct {
	reg    *settings.Registry[OnboardResult]
	cfg    *OnboardResult
	cursor int
	height int

	editing bool   // typing a text, path, secret or number
	picking bool   // choosing among a select's options
	input   string // typed text, or the picker filter
	pick    int    // highlighted option in the picker

	downloading string // catalog model being downloaded
	notice      string
	err         error
	done        bool
}

// NewSettingsModel loads the current settings.
func NewSettingsModel() SettingsModel {
	reg := Settings()
	cfg, err := reg.Load()
	if cfg == nil {
		cfg = &OnboardResult{}
	}
	return SettingsModel{reg: reg, cfg: cfg, err: err}
}

// rows are the keys of the visible fields; the last row is Back ("").
func (m SettingsModel) rows() []string {
	var keys []string
	for _, f := range m.reg.Fields {
		if m.reg.Visible(m.cfg, f.Key) {
			keys = append(keys, f.Key)
		}
	}
	return append(keys, "")
}

// Done reports that the user left the screen.
func (m SettingsModel) Done() bool { return m.done }

func (m SettingsModel) Init() tea.Cmd { return nil }

type settingsDownloadedMsg struct {
	name string
	err  error
}

func (m SettingsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil
	case settingsDownloadedMsg:
		m.downloading = ""
		ResetModelOptionsCache()
		if msg.err != nil {
			m.err, m.notice = msg.err, ""
		} else {
			m.err, m.notice = nil, "Downloaded "+msg.name+"."
		}
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.done = true
			return m, tea.Quit
		}
		switch {
		case m.picking:
			return m.updatePicking(msg)
		case m.editing:
			return m.updateEditing(msg)
		}
		return m.updateBrowsing(msg)
	}
	return m, nil
}

func (m SettingsModel) updateBrowsing(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	rows := m.rows()
	cur := rows[m.cursor]
	switch key.String() {
	case "esc", "q":
		m.done = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(rows)-1 {
			m.cursor++
		}
	case "left", "h", "right", "l":
		if cur != "" && steppable(m.field(cur)) {
			dir := 1
			if s := key.String(); s == "left" || s == "h" {
				dir = -1
			}
			m.change(cur, func(c *OnboardResult) error { return m.reg.Step(c, cur, dir) })
		}
	case "delete", "backspace":
		if cur != "" {
			m.change(cur, func(c *OnboardResult) error { return m.reg.Reset(c, cur) })
		}
	case "d":
		if e, ok := m.missingCatalogModel(cur); ok && m.downloading == "" {
			m.downloading, m.notice, m.err = e.Name, "", nil
			return m, downloadCatalogModel(e)
		}
	case "enter", " ":
		if cur == "" {
			m.done = true
			return m, tea.Quit
		}
		f := m.field(cur)
		switch {
		case f.Type == settings.Select && len(m.options(cur)) > 0:
			m.picking, m.input, m.notice, m.err = true, "", "", nil
			m.pick = 0
			for i, o := range m.filtered(cur) {
				if o.Value == fmt.Sprint(m.reg.Value(m.cfg, cur)) {
					m.pick = i
				}
			}
		case typed(f):
			m.editing, m.notice, m.err, m.input = true, "", nil, ""
			if f.Type != settings.Secret {
				m.input = fmt.Sprint(m.reg.Value(m.cfg, cur))
			}
		default:
			m.change(cur, func(c *OnboardResult) error { return m.reg.Step(c, cur, 1) })
		}
	}
	return m, nil
}

func (m SettingsModel) updateEditing(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	cur := m.rows()[m.cursor]
	switch key.String() {
	case "esc":
		m.editing = false
	case "enter":
		v := m.input
		m.change(cur, func(c *OnboardResult) error { return m.reg.Set(c, cur, v) })
		if m.err == nil {
			m.editing = false
		}
	default:
		m.input = editText(m.input, key)
	}
	return m, nil
}

// updatePicking handles the option list of a select: typing filters it,
// enter takes the highlighted option (or, for a field that accepts custom
// values, the typed text when nothing matches).
func (m SettingsModel) updatePicking(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	cur := m.rows()[m.cursor]
	opts := m.filtered(cur)
	switch key.String() {
	case "esc":
		m.picking = false
	case "up":
		if m.pick > 0 {
			m.pick--
		}
	case "down":
		if m.pick < len(opts)-1 {
			m.pick++
		}
	case "enter":
		var v string
		switch {
		case len(opts) > 0:
			v = opts[m.pick].Value
		case m.field(cur).AllowCustom && strings.TrimSpace(m.input) != "":
			v = strings.TrimSpace(m.input)
		default:
			return m, nil
		}
		m.change(cur, func(c *OnboardResult) error { return m.reg.Set(c, cur, v) })
		if m.err == nil {
			m.picking = false
			if e, ok := m.missingCatalogModel(cur); ok {
				m.notice = e.Name + " is not downloaded yet: press d to download it (" + settings.HumanSize(e.Bytes) + ")."
			}
		}
	default:
		m.input = editText(m.input, key)
		m.pick = 0
	}
	return m, nil
}

func editText(s string, key tea.KeyPressMsg) string {
	switch key.String() {
	case "backspace":
		if r := []rune(s); len(r) > 0 {
			return string(r[:len(r)-1])
		}
		return s
	case "ctrl+u":
		return ""
	}
	if t := key.Key().Text; t != "" {
		return s + t
	}
	return s
}

func (m SettingsModel) field(key string) *settings.Field[OnboardResult] {
	f, _ := m.reg.Field(key)
	return f
}

func (m SettingsModel) options(key string) []settings.Option {
	f := m.field(key)
	opts := append([]settings.Option(nil), f.Options...)
	if f.OptionsFrom != nil {
		more, _ := f.OptionsFrom(m.cfg)
		for _, o := range more {
			dup := false
			for _, p := range opts {
				dup = dup || p.Value == o.Value
			}
			if !dup {
				opts = append(opts, o)
			}
		}
	}
	return opts
}

// filtered are the options whose value or label contains the picker filter.
func (m SettingsModel) filtered(key string) []settings.Option {
	q := strings.ToLower(strings.TrimSpace(m.input))
	var out []settings.Option
	for _, o := range m.options(key) {
		if q == "" || strings.Contains(strings.ToLower(o.Value), q) || strings.Contains(strings.ToLower(o.Label), q) {
			out = append(out, o)
		}
	}
	return out
}

// missingCatalogModel returns the catalog model a field names when it is not
// on disk yet.
func (m SettingsModel) missingCatalogModel(key string) (modelcatalog.Entry, bool) {
	if key == "" {
		return modelcatalog.Entry{}, false
	}
	e, ok := catalogEntryFor(fmt.Sprint(m.reg.Value(m.cfg, key)))
	return e, ok && !e.Installed()
}

func downloadCatalogModel(e modelcatalog.Entry) tea.Cmd {
	return func() tea.Msg {
		return settingsDownloadedMsg{name: e.Name, err: modelcatalog.Download(context.Background(), e, nil)}
	}
}

// steppable fields change with ←/→.
func steppable(f *settings.Field[OnboardResult]) bool {
	switch f.Type {
	case settings.Bool, settings.Select:
		return true
	case settings.Number:
		return f.Step != nil || len(f.Options) > 0
	}
	return false
}

// typed fields open a text input on enter.
func typed(f *settings.Field[OnboardResult]) bool {
	switch f.Type {
	case settings.Text, settings.Path, settings.Secret, settings.Number:
		return true
	case settings.Select:
		return f.AllowCustom
	}
	return false
}

// change applies one edit to the config on disk (re-read first, so edits made
// meanwhile from the web UI are kept) and saves it.
func (m *SettingsModel) change(key string, edit func(*OnboardResult) error) {
	cfg, err := m.reg.Load()
	if err == nil {
		err = edit(cfg)
	}
	if err == nil && m.reg.Validate != nil {
		err = m.reg.Validate(cfg)
	}
	if err == nil {
		if m.reg.BeforeSave != nil {
			m.reg.BeforeSave(cfg)
		}
		err = m.reg.Save(cfg)
	}
	if err != nil {
		m.err, m.notice = fmt.Errorf("%s: %w", m.field(key).Label, err), ""
		return
	}
	m.cfg, m.err, m.notice = cfg, nil, "Saved."
	if m.reg.NeedsRestart([]string{key}) {
		m.notice = "Saved. " + m.reg.Note
	}
	if n := len(m.rows()); m.cursor >= n {
		m.cursor = n - 1
	}
}

func (m SettingsModel) View() tea.View { return tea.NewView(m.render()) }

func (m SettingsModel) render() string {
	var b strings.Builder
	b.WriteString("\n" + TitleStyle.Render("  ⚙  Settings") + "\n")
	b.WriteString(SubtitleStyle.Render("  Each change is saved at once • the same settings as the web UI (System → Settings)") + "\n\n")

	rows := m.rows()
	if m.picking {
		return b.String() + m.viewPicker(rows[m.cursor])
	}

	// One line per row plus a group header; keep the cursor in view.
	lines, cursorLine, group := []string{}, 0, ""
	for i, key := range rows {
		if key == "" {
			lines = append(lines, "")
			if i == m.cursor {
				cursorLine = len(lines)
				lines = append(lines, ActiveItemStyle.Render("▸ ← Back"))
			} else {
				lines = append(lines, NormalItemStyle.Render("  ← Back"))
			}
			continue
		}
		f := m.field(key)
		if f.Group != group {
			group = f.Group
			lines = append(lines, DescStyle.Render(strings.ToUpper(group)))
		}
		val := m.reg.Display(m.cfg, key)
		if e, ok := m.missingCatalogModel(key); ok {
			val += "  ⬇ not downloaded (d)"
			_ = e
		}
		if steppable(f) && i == m.cursor {
			val = "◂ " + val + " ▸"
		}
		if m.reg.IsDefault(m.cfg, key) && f.Default != nil {
			val += "  (default)"
		}
		line := fmt.Sprintf("%-24s %s", f.Label, val)
		switch {
		case i != m.cursor:
			lines = append(lines, NormalItemStyle.Render("  "+line))
		case m.editing:
			cursorLine = len(lines)
			lines = append(lines, ActiveItemStyle.Render(fmt.Sprintf("▸ %-24s ", f.Label))+
				lipgloss.NewStyle().Foreground(ColorAccent).Render(m.input+"█"))
		default:
			cursorLine = len(lines)
			lines = append(lines, ActiveItemStyle.Render("▸ "+line))
		}
		if i == m.cursor && f.Help != "" {
			lines = append(lines, ActiveDescStyle.Render("  "+f.Help))
		}
	}
	b.WriteString(strings.Join(window(lines, cursorLine, m.height-9), "\n") + "\n\n")

	switch {
	case m.downloading != "":
		b.WriteString(lipgloss.NewStyle().Foreground(ColorAccent).Render("  ⬇ Downloading "+m.downloading+"…") + "\n")
	case m.notice != "":
		b.WriteString(lipgloss.NewStyle().Foreground(ColorSuccess).Render("  "+m.notice) + "\n")
	}
	if m.err != nil {
		b.WriteString(ErrorBadge.Render("  "+m.err.Error()) + "\n")
	}
	b.WriteString(StatusBarStyle.Render(m.hint(rows[m.cursor])) + "\n")
	return b.String()
}

func (m SettingsModel) viewPicker(key string) string {
	f := m.field(key)
	var b strings.Builder
	b.WriteString(ActiveItemStyle.Render("  "+f.Label) + "\n")
	if f.Help != "" {
		b.WriteString(ActiveDescStyle.Render("  "+f.Help) + "\n")
	}
	b.WriteString("\n  " + lipgloss.NewStyle().Foreground(ColorAccent).Render("filter: "+m.input+"█") + "\n\n")
	opts := m.filtered(key)
	cur := fmt.Sprint(m.reg.Value(m.cfg, key))
	var lines []string
	for i, o := range opts {
		mark := "  "
		if o.Value == cur {
			mark = "● "
		}
		label := o.Label
		if label != o.Value && !strings.Contains(label, o.Value) {
			label += "  [" + o.Value + "]"
		}
		if i == m.pick {
			lines = append(lines, ActiveItemStyle.Render("▸ "+mark+label))
		} else {
			lines = append(lines, NormalItemStyle.Render("  "+mark+label))
		}
	}
	if len(opts) == 0 {
		if f.AllowCustom && strings.TrimSpace(m.input) != "" {
			lines = append(lines, NormalItemStyle.Render("  enter: use \""+strings.TrimSpace(m.input)+"\""))
		} else {
			lines = append(lines, NormalItemStyle.Render("  (no match)"))
		}
	}
	b.WriteString(strings.Join(window(lines, m.pick, m.height-12), "\n") + "\n\n")
	if m.err != nil {
		b.WriteString(ErrorBadge.Render("  "+m.err.Error()) + "\n")
	}
	h := "  ↑/↓ choose • type to filter • enter select • esc cancel"
	if f.AllowCustom {
		h = "  ↑/↓ choose • type to filter or enter any name • enter select • esc cancel"
	}
	b.WriteString(StatusBarStyle.Render(h) + "\n")
	return b.String()
}

// window returns at most n lines of lines around line cur (all when n < 5).
func window(lines []string, cur, n int) []string {
	if n < 5 || len(lines) <= n {
		return lines
	}
	start := cur - n/2
	if start < 0 {
		start = 0
	}
	if start+n > len(lines) {
		start = len(lines) - n
	}
	return lines[start : start+n]
}

func (m SettingsModel) hint(key string) string {
	switch {
	case m.editing:
		return "  enter save • ctrl+u clear • esc cancel"
	case key == "":
		return "  ↑/↓ navigate • enter back • esc back"
	}
	f := m.field(key)
	h := "  ↑/↓ navigate"
	if steppable(f) {
		h += " • ←/→ change"
	}
	switch {
	case f.Type == settings.Select && len(m.options(key)) > 0:
		h += " • enter choose"
	case typed(f):
		h += " • enter type a value"
	}
	if _, ok := m.missingCatalogModel(key); ok {
		h += " • d download"
	}
	if f.Default != nil || f.Type != settings.Bool {
		h += " • del reset"
	}
	return h + " • esc back"
}
