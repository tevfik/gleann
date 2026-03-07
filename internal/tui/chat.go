package tui

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tevfik/gleann/pkg/gleann"
)

// ── Messages ───────────────────────────────────────────────────

// chatMsg wraps messages displayed in the chat viewport.
type chatMsg struct {
	role    string // "user" | "assistant" | "system"
	content string
}

// answerMsg is sent when the LLM responds.
type answerMsg struct {
	content string
	err     error
}

// ── Settings constants ─────────────────────────────────────────

type settingsField int

const (
	fieldTemperature settingsField = iota
	fieldMaxTokens
	fieldTopK
	fieldLLMModel
	fieldRerankToggle
	fieldRerankModel
	fieldSystemPrompt
	fieldCount // sentinel
)

var temperaturePresets = []float64{
	0.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0,
	1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 1.8, 1.9, 2.0,
}

var maxTokensPresets = []int{
	256, 512, 1024, 2048, 4096, 8192,
}

var topKPresets = []int{3, 5, 10, 15, 20, 30}

// ── ChatModel ──────────────────────────────────────────────────

// ChatModel is the Bubble Tea model for interactive chat.
type ChatModel struct {
	chat      *gleann.LeannChat
	indexName string
	modelName string

	messages []chatMsg
	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model
	waiting  bool
	err      error
	width    int
	height   int
	ready    bool
	quitting bool

	// Settings panel.
	showSettings   bool
	settingsCursor settingsField
	temperature    float64
	maxTokens      int
	topK           int
	systemPrompt   string

	// LLM model selection in settings.
	llmModels   []string
	llmModelIdx int

	// Reranker settings.
	rerankEnabled  bool
	rerankModels   []string
	rerankModelIdx int

	// Embedding info (read-only display).
	embeddingModel    string
	embeddingProvider string

	// Settings edit mode for system prompt.
	editingPrompt bool
	promptInput   textarea.Model
}

// NewChatModel creates a new chat TUI model.
func NewChatModel(chat *gleann.LeannChat, indexName, modelName string) ChatModel {
	ta := textarea.New()
	ta.Placeholder = "Ask a question..."
	ta.Focus()
	ta.CharLimit = 4096
	ta.SetHeight(3)
	ta.SetWidth(80)
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1)
	ta.BlurredStyle.Base = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorMuted).
		Padding(0, 1)

	// Disable Enter in textarea — we handle it ourselves for send.
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter"),
		key.WithHelp("alt+enter", "newline"))

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = SpinnerStyle

	// Prompt editor for settings.
	pi := textarea.New()
	pi.Placeholder = "System prompt..."
	pi.CharLimit = 2048
	pi.SetHeight(5)
	pi.SetWidth(60)
	pi.ShowLineNumbers = false
	pi.FocusedStyle.CursorLine = lipgloss.NewStyle()
	pi.FocusedStyle.Base = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorAccent).
		Padding(0, 1)

	cfg := chat.Config()

	// Build LLM model list from the current model name.
	llmModels := []string{modelName}

	// Load saved settings.
	savedCfg := LoadSavedConfig()

	// Fetch dynamic models based on provider.
	var allModels []ModelInfo
	var err error
	if savedCfg != nil && savedCfg.LLMProvider != "" {
		host := savedCfg.OllamaHost
		key := savedCfg.OpenAIKey
		if savedCfg.LLMProvider == "openai" {
			host = savedCfg.OpenAIBaseURL
		} else if savedCfg.LLMProvider == "anthropic" {
			key = savedCfg.AnthropicKey
		}
		allModels, err = fetchModels(savedCfg.LLMProvider, host, key)
	} else {
		// Fallback to local ollama default.
		allModels, err = fetchModels("ollama", gleann.DefaultOllamaHost, "")
	}

	if err == nil {
		filteredLLMs := filterLLMModels(allModels)
		for _, m := range filteredLLMs {
			// Add if not already the current model
			if m.Name != modelName {
				llmModels = append(llmModels, m.Name)
			}
		}
	} else {
		// Fallback to minimal defaults if unreachable
		for _, common := range []string{"llama3.2", "gpt-4o", "claude-sonnet-4-20250514"} {
			if common != modelName {
				llmModels = append(llmModels, common)
			}
		}
	}
	rerankEnabled := false
	embModel := ""
	embProvider := ""

	// Start with defaults from the chat config provided by caller.
	temperature := cfg.Temperature
	maxTokens := cfg.MaxTokens
	topK := 10
	systemPrompt := cfg.SystemPrompt

	if savedCfg != nil {
		rerankEnabled = savedCfg.RerankEnabled
		embModel = savedCfg.EmbeddingModel
		embProvider = savedCfg.EmbeddingProvider
		// Restore persisted chat settings (override defaults only if set).
		if savedCfg.SystemPrompt != "" {
			systemPrompt = savedCfg.SystemPrompt
		}
		if savedCfg.Temperature > 0 {
			temperature = savedCfg.Temperature
		}
		if savedCfg.MaxTokens > 0 {
			maxTokens = savedCfg.MaxTokens
		}
		if savedCfg.TopK > 0 {
			topK = savedCfg.TopK
		}
	}

	// Apply loaded settings to the chat engine immediately.
	chat.SetTemperature(temperature)
	chat.SetMaxTokens(maxTokens)
	chat.SetSystemPrompt(systemPrompt)

	// Fetch available reranker models using the embedding provider from `savedCfg`.
	rerankModels, rerankModelIdx := fetchRerankModelList(savedCfg)

	var initialMessages []chatMsg
	for _, m := range chat.History() {
		initialMessages = append(initialMessages, chatMsg{role: m.Role, content: m.Content})
	}
	initialMessages = append(initialMessages, chatMsg{
		role:    "system",
		content: fmt.Sprintf("Connected to index %q — model: %s", indexName, modelName),
	})

	m := ChatModel{
		chat:              chat,
		indexName:         indexName,
		modelName:         modelName,
		textarea:          ta,
		spinner:           sp,
		messages:          initialMessages,
		temperature:       temperature,
		maxTokens:         maxTokens,
		topK:              topK,
		systemPrompt:      systemPrompt,
		llmModels:         llmModels,
		llmModelIdx:       0,
		rerankEnabled:     rerankEnabled,
		rerankModels:      rerankModels,
		rerankModelIdx:    rerankModelIdx,
		embeddingModel:    embModel,
		embeddingProvider: embProvider,
		promptInput:       pi,
		width:             80,
		height:            24,
	}
	// Initialize layout with defaults so View renders immediately.
	m = m.relayout()
	return m
}

func (m ChatModel) Init() tea.Cmd {
	return tea.Batch(m.textarea.Focus(), m.spinner.Tick)
}

// ── Update ─────────────────────────────────────────────────────

func (m ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// ── Settings overlay routes ──
	if m.showSettings {
		return m.updateSettings(msg)
	}

	var (
		vpCmd tea.Cmd
		taCmd tea.Cmd
		spCmd tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.relayout()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		case "esc":
			// If textarea has content, clear it first.
			// Only quit on ESC when input is already empty.
			if m.textarea.Value() != "" {
				m.textarea.Reset()
				return m, nil
			}
			m.quitting = true
			return m, tea.Quit

		case "ctrl+u":
			// Clear current line (standard terminal shortcut).
			m.textarea.Reset()
			return m, nil

		// ── Viewport scroll — intercepted before textarea eats them ──
		case "pgup", "ctrl+b":
			m.viewport.HalfViewUp()
			return m, nil

		case "pgdown", "ctrl+d":
			m.viewport.HalfViewDown()
			return m, nil

		case "ctrl+s":
			// Toggle settings panel.
			m.showSettings = true
			m.textarea.Blur()
			return m, nil

		case "enter":
			if m.waiting {
				return m, nil
			}
			input := strings.TrimSpace(m.textarea.Value())
			if input == "" {
				return m, nil
			}

			// Slash commands.
			switch {
			case input == "/quit" || input == "/exit":
				m.quitting = true
				return m, tea.Quit

			case input == "/clear":
				m.messages = m.messages[:1]
				m.chat.ClearHistory()
				m.textarea.Reset()
				m.viewport.SetContent(m.renderMessages())
				m.viewport.GotoBottom()
				return m, nil

			case input == "/settings":
				m.showSettings = true
				m.textarea.Reset()
				m.textarea.Blur()
				return m, nil

			case input == "/help":
				m.messages = append(m.messages, chatMsg{
					role: "system",
					content: "Commands: /clear • /settings • /help • /quit\n" +
						"Shortcuts: ctrl+s settings • pgup/pgdn scroll • esc clear/back",
				})
				m.textarea.Reset()
				m.viewport.SetContent(m.renderMessages())
				m.viewport.GotoBottom()
				return m, nil
			}

			// Send message to LLM.
			m.messages = append(m.messages, chatMsg{role: "user", content: input})
			m.textarea.Reset()
			m.textarea.Blur()
			m.waiting = true

			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()

			return m, tea.Batch(m.spinner.Tick, m.askLLM(input))
		}

	case answerMsg:
		m.waiting = false
		if msg.err != nil {
			m.err = msg.err
			m.messages = append(m.messages, chatMsg{
				role:    "system",
				content: "⚠ Error: " + msg.err.Error(),
			})
		} else {
			m.messages = append(m.messages, chatMsg{
				role:    "assistant",
				content: msg.content,
			})
		}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		// Re-focus textarea and restart blink.
		focusCmd := m.textarea.Focus()
		return m, focusCmd

	case spinner.TickMsg:
		if m.waiting {
			m.spinner, spCmd = m.spinner.Update(msg)
			return m, spCmd
		}
		return m, nil
	}

	// Forward to sub-components.
	if !m.waiting {
		m.textarea, taCmd = m.textarea.Update(msg)
	}
	m.viewport, vpCmd = m.viewport.Update(msg)

	return m, tea.Batch(vpCmd, taCmd, spCmd)
}

// ── Settings Update ────────────────────────────────────────────

func (m ChatModel) updateSettings(msg tea.Msg) (tea.Model, tea.Cmd) {
	// If editing the system prompt, route to prompt textarea.
	if m.editingPrompt {
		return m.updatePromptEdit(msg)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		case "esc", "ctrl+s":
			// Close settings, apply changes.
			m.applySettings()
			m.showSettings = false
			focusCmd := m.textarea.Focus()
			return m, focusCmd

		case "up", "k":
			if m.settingsCursor > 0 {
				m.settingsCursor--
			}

		case "down", "j":
			if m.settingsCursor < fieldCount-1 {
				m.settingsCursor++
			}

		case "left", "h":
			m.adjustSetting(-1)

		case "right", "l":
			m.adjustSetting(1)

		case "enter":
			if m.settingsCursor == fieldSystemPrompt {
				m.editingPrompt = true
				m.promptInput.SetValue(m.systemPrompt)
				m.promptInput.Focus()
				return m, textarea.Blink
			}
		}
	}
	return m, nil
}

func (m ChatModel) updatePromptEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		case "ctrl+s":
			// Save prompt and go back to settings.
			m.systemPrompt = m.promptInput.Value()
			m.editingPrompt = false
			m.promptInput.Blur()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.promptInput, cmd = m.promptInput.Update(msg)
	return m, cmd
}

func (m *ChatModel) adjustSetting(dir int) {
	switch m.settingsCursor {
	case fieldTemperature:
		idx := findClosestFloat(temperaturePresets, m.temperature)
		idx += dir
		if idx < 0 {
			idx = 0
		}
		if idx >= len(temperaturePresets) {
			idx = len(temperaturePresets) - 1
		}
		m.temperature = temperaturePresets[idx]

	case fieldMaxTokens:
		idx := findClosestInt(maxTokensPresets, m.maxTokens)
		idx += dir
		if idx < 0 {
			idx = 0
		}
		if idx >= len(maxTokensPresets) {
			idx = len(maxTokensPresets) - 1
		}
		m.maxTokens = maxTokensPresets[idx]

	case fieldTopK:
		idx := findClosestInt(topKPresets, m.topK)
		idx += dir
		if idx < 0 {
			idx = 0
		}
		if idx >= len(topKPresets) {
			idx = len(topKPresets) - 1
		}
		m.topK = topKPresets[idx]

	case fieldLLMModel:
		m.llmModelIdx += dir
		if m.llmModelIdx < 0 {
			m.llmModelIdx = 0
		}
		if m.llmModelIdx >= len(m.llmModels) {
			m.llmModelIdx = len(m.llmModels) - 1
		}

	case fieldRerankToggle:
		// Toggle on/off.
		m.rerankEnabled = !m.rerankEnabled

	case fieldRerankModel:
		if len(m.rerankModels) > 0 {
			m.rerankModelIdx += dir
			if m.rerankModelIdx < 0 {
				m.rerankModelIdx = 0
			}
			if m.rerankModelIdx >= len(m.rerankModels) {
				m.rerankModelIdx = len(m.rerankModels) - 1
			}
		}
	}
}

func (m *ChatModel) applySettings() {
	m.chat.SetTemperature(m.temperature)
	m.chat.SetMaxTokens(m.maxTokens)
	m.chat.SetSystemPrompt(m.systemPrompt)

	// Apply LLM model change.
	if m.llmModelIdx >= 0 && m.llmModelIdx < len(m.llmModels) {
		m.chat.SetModel(m.llmModels[m.llmModelIdx])
		m.modelName = m.llmModels[m.llmModelIdx]
	}

	// Apply reranker toggle.
	selectedRerankModel := ""
	if len(m.rerankModels) > 0 && m.rerankModelIdx < len(m.rerankModels) {
		selectedRerankModel = m.rerankModels[m.rerankModelIdx]
	}
	if m.rerankEnabled && selectedRerankModel != "" {
		reranker := gleann.NewReranker(gleann.RerankerConfig{
			Provider: gleann.RerankerOllama,
			Model:    selectedRerankModel,
		})
		m.chat.Searcher().SetReranker(reranker)
	} else {
		m.chat.Searcher().SetReranker(nil)
	}

	// Build notification.
	rerankStatus := "off"
	if m.rerankEnabled && selectedRerankModel != "" {
		rerankStatus = selectedRerankModel
	}
	m.messages = append(m.messages, chatMsg{
		role: "system",
		content: fmt.Sprintf("Settings updated — model: %s • temp: %.1f • max tokens: %d • top-k: %d • reranker: %s",
			m.modelName, m.temperature, m.maxTokens, m.topK, rerankStatus),
	})
	if m.ready {
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
	}

	// Persist settings to ~/.gleann/config.json.
	_ = UpdateConfig(func(cfg *OnboardResult) {
		cfg.LLMModel = m.modelName
		cfg.SystemPrompt = m.systemPrompt
		cfg.Temperature = m.temperature
		cfg.MaxTokens = m.maxTokens
		cfg.TopK = m.topK
		cfg.RerankEnabled = m.rerankEnabled
		if selectedRerankModel != "" {
			cfg.RerankModel = selectedRerankModel
		}
	})
}

// ── Commands ───────────────────────────────────────────────────

func (m ChatModel) askLLM(question string) tea.Cmd {
	chat := m.chat
	topK := m.topK
	rerank := m.rerankEnabled
	return func() tea.Msg {
		answer, err := chat.Ask(context.Background(), question,
			gleann.WithTopK(topK),
			gleann.WithReranker(rerank))
		return answerMsg{content: answer, err: err}
	}
}

// ── Layout ─────────────────────────────────────────────────────

func (m ChatModel) relayout() ChatModel {
	headerH := 3
	inputH := 6
	helpH := 2
	vpHeight := m.height - headerH - inputH - helpH
	if vpHeight < 4 {
		vpHeight = 4
	}
	vpWidth := m.width - 4
	if vpWidth < 20 {
		vpWidth = 20
	}

	if !m.ready {
		m.viewport = viewport.New(vpWidth, vpHeight)
		// Restore viewport scroll keybindings so ↑/↓/PgUp/PgDn scroll chat history.
		// We use separate keys here to avoid stealing ↑/↓ from textarea.
		m.viewport.KeyMap = viewport.KeyMap{
			PageDown:     key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdown", "scroll down")),
			PageUp:       key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "scroll up")),
			HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "½ page up")),
			HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "½ page down")),
		}
		m.viewport.SetContent(m.renderMessages())
		m.ready = true
	} else {
		m.viewport.Width = vpWidth
		m.viewport.Height = vpHeight
	}
	m.textarea.SetWidth(vpWidth)
	return m
}

// ── Render ─────────────────────────────────────────────────────

func (m ChatModel) renderMessages() string {
	var b strings.Builder
	maxW := m.width - 12
	if maxW < 30 {
		maxW = 60
	}

	for _, msg := range m.messages {
		switch msg.role {
		case "system":
			sys := lipgloss.NewStyle().
				Foreground(ColorMuted).
				Italic(true).
				Width(maxW).
				Render("  ℹ  " + msg.content)
			b.WriteString(sys + "\n\n")

		case "user":
			label := lipgloss.NewStyle().
				Foreground(ColorPrimary).
				Bold(true).
				Render("  You")
			bubble := UserBubbleStyle.Width(maxW).Render(msg.content)
			b.WriteString(label + "\n" + bubble + "\n\n")

		case "assistant":
			label := lipgloss.NewStyle().
				Foreground(ColorSecondary).
				Bold(true).
				Render("  ⚡ gleann")
			bubble := AssistantBubbleStyle.Width(maxW).Render(msg.content)
			b.WriteString(label + "\n" + bubble + "\n\n")
		}
	}

	if m.waiting {
		b.WriteString("  " + m.spinner.View() + " Thinking...\n")
	}

	return b.String()
}

func (m ChatModel) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "\n  Initializing...\n"
	}

	// Settings overlay.
	if m.showSettings {
		return m.viewSettings()
	}

	var b strings.Builder

	// Header.
	header := lipgloss.NewStyle().
		Foreground(ColorPrimary).
		Bold(true).
		Render("⚡ gleann chat")
	indexBadge := lipgloss.NewStyle().
		Foreground(ColorSecondary).
		Render(fmt.Sprintf(" [%s]", m.indexName))
	modelBadge := lipgloss.NewStyle().
		Foreground(ColorMuted).
		Render(fmt.Sprintf(" • %s", m.modelName))
	tempBadge := lipgloss.NewStyle().
		Foreground(ColorAccent).
		Render(fmt.Sprintf(" • t=%.1f", m.temperature))

	b.WriteString(header + indexBadge + modelBadge + tempBadge + "\n")
	sep := lipgloss.NewStyle().Foreground(ColorMuted).Render(strings.Repeat("─", m.width-2))
	b.WriteString(sep + "\n")

	// Viewport with scrollbar.
	vpContent := m.viewport.View()
	scrollbar := m.renderScrollbar()
	// Join viewport lines with scrollbar column.
	vpLines := strings.Split(vpContent, "\n")
	sbLines := strings.Split(scrollbar, "\n")
	// Ensure equal line count.
	for len(sbLines) < len(vpLines) {
		sbLines = append(sbLines, "│")
	}
	for i, line := range vpLines {
		sb := ""
		if i < len(sbLines) {
			sb = sbLines[i]
		}
		b.WriteString(line + " " + sb + "\n")
	}

	// Input area.
	b.WriteString(m.textarea.View())
	b.WriteString("\n")

	// Help.
	help := HelpStyle.Render("  enter send • pgup/pgdn scroll • ctrl+s settings • esc clear/back")
	b.WriteString(help)

	return b.String()
}

// renderScrollbar draws a minimal vertical scrollbar for the viewport.
func (m ChatModel) renderScrollbar() string {
	h := m.viewport.Height
	if h <= 0 {
		return ""
	}

	totalLines := m.viewport.TotalLineCount()
	visibleLines := h

	thumbStyle := lipgloss.NewStyle().Foreground(ColorPrimary)
	trackStyle := lipgloss.NewStyle().Foreground(ColorMuted)

	// If all content fits, show a full-height thumb.
	if totalLines <= visibleLines {
		lines := make([]string, h)
		for i := range lines {
			lines[i] = trackStyle.Render("│")
		}
		return strings.Join(lines, "\n")
	}

	// Calculate thumb size and position.
	thumbSize := max(1, visibleLines*visibleLines/totalLines)
	scrollOffset := m.viewport.ScrollPercent()
	maxOffset := float64(totalLines - visibleLines)
	thumbTop := 0
	if maxOffset > 0 {
		thumbTop = int(scrollOffset * float64(h-thumbSize))
	}
	thumbBottom := thumbTop + thumbSize

	lines := make([]string, h)
	for i := range lines {
		if i >= thumbTop && i < thumbBottom {
			lines[i] = thumbStyle.Render("▓")
		} else {
			lines[i] = trackStyle.Render("│")
		}
	}
	return strings.Join(lines, "\n")
}

// ── Settings View ──────────────────────────────────────────────

func (m ChatModel) viewSettings() string {
	var b strings.Builder

	// Title.
	b.WriteString("\n")
	b.WriteString(TitleStyle.Render(" ⚙  LLM Settings "))
	b.WriteString("\n\n")

	panelW := 60
	if m.width > 10 && m.width-10 < panelW {
		panelW = m.width - 10
	}

	// Temperature.
	b.WriteString(m.renderSlider(fieldTemperature, "Temperature",
		fmt.Sprintf("%.1f", m.temperature),
		m.temperature/2.0, panelW))
	b.WriteString("\n")

	// Max Tokens.
	b.WriteString(m.renderSlider(fieldMaxTokens, "Max Tokens",
		fmt.Sprintf("%d", m.maxTokens),
		float64(findClosestInt(maxTokensPresets, m.maxTokens))/float64(len(maxTokensPresets)-1), panelW))
	b.WriteString("\n")

	// Top-K.
	b.WriteString(m.renderSlider(fieldTopK, "Search Top-K",
		fmt.Sprintf("%d", m.topK),
		float64(findClosestInt(topKPresets, m.topK))/float64(len(topKPresets)-1), panelW))
	b.WriteString("\n")

	// LLM Model selector.
	{
		active := m.settingsCursor == fieldLLMModel
		var labelStr string
		if active {
			labelStr = ActiveItemStyle.Render("▸ LLM Model")
		} else {
			labelStr = NormalItemStyle.Render("  LLM Model")
		}
		currentModel := m.modelName
		if m.llmModelIdx >= 0 && m.llmModelIdx < len(m.llmModels) {
			currentModel = m.llmModels[m.llmModelIdx]
		}
		valStyle := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
		labelStr += "  " + valStyle.Render(currentModel)
		b.WriteString(labelStr + "\n")
		if active {
			hint := lipgloss.NewStyle().Foreground(ColorDimFg).PaddingLeft(6).
				Render(fmt.Sprintf("← → to switch (%d/%d)", m.llmModelIdx+1, len(m.llmModels)))
			b.WriteString(hint + "\n")
		}
		b.WriteString("\n")
	}

	// Reranker toggle.
	{
		active := m.settingsCursor == fieldRerankToggle
		var labelStr string
		if active {
			labelStr = ActiveItemStyle.Render("▸ Reranker")
		} else {
			labelStr = NormalItemStyle.Render("  Reranker")
		}
		toggle := "OFF"
		toggleColor := ColorMuted
		if m.rerankEnabled {
			toggle = "ON"
			toggleColor = ColorAccent
		}
		valStyle := lipgloss.NewStyle().Foreground(toggleColor).Bold(true)
		labelStr += "  " + valStyle.Render(toggle)
		b.WriteString(labelStr + "\n")
		if active {
			hint := lipgloss.NewStyle().Foreground(ColorDimFg).PaddingLeft(6).
				Render("← → to toggle on/off")
			b.WriteString(hint + "\n")
		}
		b.WriteString("\n")
	}

	// Reranker model selector.
	{
		active := m.settingsCursor == fieldRerankModel
		var labelStr string
		if active {
			labelStr = ActiveItemStyle.Render("▸ Rerank Model")
		} else {
			labelStr = NormalItemStyle.Render("  Rerank Model")
		}
		currentModel := "(none)"
		if len(m.rerankModels) > 0 && m.rerankModelIdx < len(m.rerankModels) {
			currentModel = m.rerankModels[m.rerankModelIdx]
		}
		modelColor := ColorDimFg
		if m.rerankEnabled {
			modelColor = ColorAccent
		}
		valStyle := lipgloss.NewStyle().Foreground(modelColor).Bold(true)
		labelStr += "  " + valStyle.Render(currentModel)
		b.WriteString(labelStr + "\n")
		if active {
			total := len(m.rerankModels)
			if total == 0 {
				hint := lipgloss.NewStyle().Foreground(ColorDimFg).PaddingLeft(6).
					Render("no reranker models found — ollama pull bge-reranker-v2-m3")
				b.WriteString(hint + "\n")
			} else {
				hint := lipgloss.NewStyle().Foreground(ColorDimFg).PaddingLeft(6).
					Render(fmt.Sprintf("← → to switch (%d/%d)", m.rerankModelIdx+1, total))
				b.WriteString(hint + "\n")
			}
		}
		b.WriteString("\n")
	}

	// System Prompt.
	if m.editingPrompt {
		label := LabelStyle.Render("  System Prompt")
		b.WriteString(label + " (editing — ctrl+s to save)\n")
		b.WriteString("  " + m.promptInput.View() + "\n")
	} else {
		active := m.settingsCursor == fieldSystemPrompt
		var label string
		if active {
			label = ActiveItemStyle.Render("▸ System Prompt")
		} else {
			label = NormalItemStyle.Render("  System Prompt")
		}
		b.WriteString(label)
		if active {
			b.WriteString(lipgloss.NewStyle().Foreground(ColorDimFg).Render("  (enter to edit)"))
		}
		b.WriteString("\n")
		preview := m.systemPrompt
		if len(preview) > 80 {
			preview = preview[:77] + "..."
		}
		promptStyle := lipgloss.NewStyle().
			Foreground(ColorDimFg).
			Italic(true).
			PaddingLeft(6).
			Width(panelW)
		b.WriteString(promptStyle.Render(preview) + "\n")
	}

	b.WriteString("\n")

	// Info box with embedding info.
	infoStyle := lipgloss.NewStyle().
		Foreground(ColorDimFg).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorMuted).
		Padding(0, 2).
		MarginLeft(4).
		Width(panelW)
	embInfo := m.embeddingModel
	if embInfo == "" {
		embInfo = "(default)"
	}
	if m.embeddingProvider != "" {
		embInfo += " via " + m.embeddingProvider
	}
	info := fmt.Sprintf("📊 Index: %s\n🤖 Model: %s\n🔤 Embedding: %s\n⚠  Changing embedding model requires index rebuild",
		m.indexName, m.modelName, embInfo)
	b.WriteString(infoStyle.Render(info) + "\n\n")

	// Help.
	help := HelpStyle.Render("  ↑/↓ navigate • ←/→ adjust • enter edit prompt • esc/ctrl+s close")
	b.WriteString(help)

	return b.String()
}

func (m ChatModel) renderSlider(field settingsField, label, value string, ratio float64, panelW int) string {
	active := m.settingsCursor == field

	// Label line.
	var labelStr string
	if active {
		labelStr = ActiveItemStyle.Render(fmt.Sprintf("▸ %s", label))
	} else {
		labelStr = NormalItemStyle.Render(fmt.Sprintf("  %s", label))
	}

	valStyle := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	labelStr += "  " + valStyle.Render(value)

	// Slider bar.
	barW := panelW - 10
	if barW < 10 {
		barW = 10
	}
	filled := int(math.Round(ratio * float64(barW)))
	if filled < 0 {
		filled = 0
	}
	if filled > barW {
		filled = barW
	}
	empty := barW - filled

	filledColor := ColorPrimary
	emptyColor := ColorMuted
	if active {
		filledColor = ColorAccent
	}

	bar := lipgloss.NewStyle().Foreground(filledColor).Render(strings.Repeat("━", filled)) +
		lipgloss.NewStyle().Foreground(emptyColor).Render(strings.Repeat("─", empty))

	arrowL := " "
	arrowR := " "
	if active {
		arrowL = lipgloss.NewStyle().Foreground(ColorPrimary).Render("◂")
		arrowR = lipgloss.NewStyle().Foreground(ColorPrimary).Render("▸")
	}

	sliderLine := fmt.Sprintf("      %s %s %s", arrowL, bar, arrowR)

	return labelStr + "\n" + sliderLine + "\n"
}

// ── Helpers ────────────────────────────────────────────────────

func findClosestFloat(presets []float64, value float64) int {
	best := 0
	bestDist := math.Abs(presets[0] - value)
	for i, p := range presets {
		d := math.Abs(p - value)
		if d < bestDist {
			bestDist = d
			best = i
		}
	}
	return best
}

func findClosestInt(presets []int, value int) int {
	best := 0
	bestDist := intAbs(presets[0] - value)
	for i, p := range presets {
		d := intAbs(p - value)
		if d < bestDist {
			bestDist = d
			best = i
		}
	}
	return best
}

func intAbs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// fetchRerankModelList fetches models from the embedding provider and filters for reranker-capable ones.
// Returns the model name list and the index of the previously-selected model (or 0).
func fetchRerankModelList(savedCfg *OnboardResult) ([]string, int) {
	var allModels []ModelInfo
	var err error
	if savedCfg != nil && savedCfg.EmbeddingProvider != "" {
		host := savedCfg.OllamaHost
		key := savedCfg.OpenAIKey
		if savedCfg.EmbeddingProvider == "openai" {
			host = savedCfg.OpenAIBaseURL
		}
		allModels, err = fetchModels(savedCfg.EmbeddingProvider, host, key)
	} else {
		// Fallback
		allModels, err = fetchModels("ollama", gleann.DefaultOllamaHost, "")
	}
	if err != nil || len(allModels) == 0 {
		// Fallback: if saved config has a model, use that.
		if savedCfg != nil && savedCfg.RerankModel != "" {
			return []string{savedCfg.RerankModel}, 0
		}
		return nil, 0
	}

	filtered := filterRerankerModels(allModels)
	names := make([]string, len(filtered))
	for i, m := range filtered {
		names[i] = m.Name
	}

	// Find saved model in the list.
	selectedIdx := 0
	if savedCfg != nil && savedCfg.RerankModel != "" {
		for i, n := range names {
			if n == savedCfg.RerankModel {
				selectedIdx = i
				break
			}
		}
	}
	return names, selectedIdx
}
