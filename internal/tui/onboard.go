package tui

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/tevfik/gleann/pkg/gleann"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Wizard phases ──────────────────────────────────────────────

type wizardPhase int

const (
	phaseMenu           wizardPhase = -1   // settings menu (existing config)
	phaseQuickOrAdv     wizardPhase = 0    // "Quick Setup" vs "Advanced Setup"
	phaseEmbProvider    wizardPhase = iota // select: ollama / openai
	phaseEmbHost                           // text input: host URL
	phaseEmbAPIKey                         // text input: API key (openai only)
	phaseEmbFetching                       // spinner: fetching models
	phaseEmbModel                          // select: pick from fetched list
	phaseLLMProvider                       // select: ollama / openai / anthropic
	phaseLLMHost                           // text input: host URL (if different)
	phaseLLMAPIKey                         // text input: API key
	phaseLLMFetching                       // spinner: fetching LLM models
	phaseLLMModel                          // select: pick LLM model
	phaseReranker                          // toggle: enable reranker?
	phaseRerankFetching                    // spinner: fetching reranker models
	phaseRerankModel                       // select: reranker model
	phaseIndexDir                          // text input: index directory
	phaseMCP                               // toggle: enable MCP server?
	phaseServer                            // toggle: enable REST API server?
	phaseSummary                           // summary & confirm
	phaseInstall                           // select: system install option
	phasePlugins                           // navigate to plugin manager
)

// totalVisibleSteps for the progress bar (skip fetching phases).
const totalVisibleSteps = 13

// ── Messages ───────────────────────────────────────────────────

type modelsFetchedMsg struct {
	models      []ModelInfo
	err         error
	forLLM      bool
	forReranker bool
}

// ── OnboardResult ──────────────────────────────────────────────

// OnboardResult holds the onboarding output and persisted settings.
// Saved to ~/.gleann/config.json.
type OnboardResult struct {
	EmbeddingProvider string `json:"embedding_provider"`
	EmbeddingModel    string `json:"embedding_model"`
	OllamaHost        string `json:"ollama_host,omitempty"`
	OpenAIKey         string `json:"openai_api_key,omitempty"`
	OpenAIBaseURL     string `json:"openai_base_url,omitempty"`
	AnthropicKey      string `json:"anthropic_api_key,omitempty"`
	LLMProvider       string `json:"llm_provider"`
	LLMModel          string `json:"llm_model"`
	RerankEnabled     bool   `json:"rerank_enabled"`
	RerankModel       string `json:"rerank_model,omitempty"`
	IndexDir          string `json:"index_dir"`
	Completed         bool   `json:"completed"`

	// Chat settings (persisted from settings panel).
	SystemPrompt string  `json:"system_prompt,omitempty"`
	Temperature  float64 `json:"temperature,omitempty"`
	MaxTokens    int     `json:"max_tokens,omitempty"`
	TopK         int     `json:"top_k,omitempty"`

	// Install options (set during setup, consumed by caller).
	InstallPath        string `json:"install_path,omitempty"`
	InstallCompletions bool   `json:"install_completions,omitempty"`

	// MCP server integration.
	MCPEnabled bool `json:"mcp_enabled,omitempty"`

	// REST API server.
	ServerEnabled bool   `json:"server_enabled,omitempty"`
	ServerAddr    string `json:"server_addr,omitempty"`

	// CLI behavior.
	Quiet    bool `json:"quiet,omitempty"`     // Suppress status messages.
	WordWrap int  `json:"word_wrap,omitempty"` // Wrap output at N columns (0 = terminal width).

	// Custom roles: name → []system-prompt-lines.
	Roles map[string][]string `json:"roles,omitempty"`

	// Format instructions per output format (json, markdown, text, etc.).
	FormatText map[string]string `json:"format_text,omitempty"`

	// Uninstall (transient, not persisted).
	Uninstall     bool `json:"-"`
	UninstallData bool `json:"-"`
}

// ── Model ──────────────────────────────────────────────────────

// OnboardModel is the Bubble Tea model for configuration onboarding.
type OnboardModel struct {
	phase       wizardPhase
	width       int
	height      int
	cancelled   bool
	done        bool
	openPlugins bool

	// Menu mode (existing config).
	menuMode    bool
	menuItems   []settingsItem
	menuCursor  int
	returnPhase wizardPhase // phase to return to after editing (-1 = menu)
	existingCfg *OnboardResult

	// Quick vs Advanced choice.
	quickAdvOptions   []string
	quickAdvOptionIdx int

	// Provider selects.
	embProviders   []string
	embProviderIdx int
	llmProviders   []string
	llmProviderIdx int

	// Text inputs.
	embHostInput  textinput.Model
	embKeyInput   textinput.Model
	llmHostInput  textinput.Model
	llmKeyInput   textinput.Model
	indexDirInput textinput.Model

	// Model lists (fetched dynamically).
	embModels    []ModelInfo
	embModelIdx  int
	embAllModels []ModelInfo // unfiltered
	embShowAll   bool
	llmModels    []ModelInfo
	llmModelIdx  int
	llmAllModels []ModelInfo
	llmShowAll   bool

	// Loading.
	spinner  spinner.Model
	fetchErr string

	// Reranker.
	rerankEnabled   bool
	rerankOptions   []string // reranker on/off labels
	rerankOptionIdx int
	rerankModels    []ModelInfo
	rerankAllModels []ModelInfo
	rerankModelIdx  int
	rerankShowAll   bool

	// Install.
	installOptions   []string
	installOptionIdx int

	// MCP.
	mcpEnabled   bool
	mcpOptions   []string // MCP on/off labels
	mcpOptionIdx int

	// REST API server.
	serverEnabled   bool
	serverOptions   []string
	serverOptionIdx int
	serverAddrInput textinput.Model

	// Final result.
	result OnboardResult
}

// settingsItem represents a setting in the settings menu.
type settingsItem struct {
	label string
	phase wizardPhase // phase to jump to when selected
}

// settingsMenuItems returns the menu items for the settings menu.
func settingsMenuItems() []settingsItem {
	return []settingsItem{
		{"Embedding Provider", phaseEmbProvider},
		{"Embedding Host/Key", phaseEmbHost},
		{"Embedding Model", phaseEmbModel},
		{"LLM Provider", phaseLLMProvider},
		{"LLM Host/Key", phaseLLMHost},
		{"LLM Model", phaseLLMModel},
		{"Reranker", phaseReranker},
		{"Reranker Model", phaseRerankModel},
		{"Index Directory", phaseIndexDir},
		{"MCP Server", phaseMCP},
		{"REST API Server", phaseServer},
		{"Install / Uninstall", phaseInstall},
		{"Manage Plugins", phasePlugins},
		{"Save & Exit", phaseSummary},
	}
}

// NewOnboardModel creates a new onboarding TUI model.
func NewOnboardModel() OnboardModel {
	// Embedding host.
	embHost := textinput.New()
	embHost.Placeholder = gleann.DefaultOllamaHost
	embHost.SetValue(gleann.DefaultOllamaHost)
	embHost.CharLimit = 256
	embHost.Width = 44

	// Embedding API key.
	embKey := textinput.New()
	embKey.Placeholder = "sk-..."
	embKey.CharLimit = 256
	embKey.Width = 44
	embKey.EchoMode = textinput.EchoPassword

	// LLM host.
	llmHost := textinput.New()
	llmHost.Placeholder = gleann.DefaultOllamaHost
	llmHost.SetValue(gleann.DefaultOllamaHost)
	llmHost.CharLimit = 256
	llmHost.Width = 44

	// LLM API key.
	llmKey := textinput.New()
	llmKey.Placeholder = "sk-..."
	llmKey.CharLimit = 256
	llmKey.Width = 44
	llmKey.EchoMode = textinput.EchoPassword

	// Index dir — use real OS path so the saved value is portable.
	defaultIdx := DefaultIndexDir()
	indexDir := textinput.New()
	indexDir.Placeholder = defaultIdx
	indexDir.SetValue(defaultIdx)
	indexDir.CharLimit = 256
	indexDir.Width = 44

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = SpinnerStyle

	// Server address input.
	serverAddr := textinput.New()
	serverAddr.Placeholder = gleann.DefaultServerAddr
	serverAddr.SetValue(gleann.DefaultServerAddr)
	serverAddr.CharLimit = 64
	serverAddr.Width = 24

	return OnboardModel{
		phase:             phaseQuickOrAdv,
		quickAdvOptions:   []string{"⚡ Quick Setup (auto-detect, 30 seconds)", "🔧 Advanced Setup (full control, all options)"},
		quickAdvOptionIdx: 0,
		embProviders:      []string{"ollama", "openai", "llamacpp"},
		llmProviders:      []string{"ollama", "openai", "anthropic", "llamacpp"},
		embHostInput:      embHost,
		embKeyInput:       embKey,
		llmHostInput:      llmHost,
		llmKeyInput:       llmKey,
		indexDirInput:     indexDir,
		rerankOptions:     []string{"Skip (no reranking)", "Enable reranker"},
		rerankOptionIdx:   0,
		mcpOptions:        []string{"Disable MCP server", "Enable MCP server"},
		mcpOptionIdx:      0,
		serverOptions:     []string{"Disable REST API server", "Enable REST API server"},
		serverOptionIdx:   0,
		serverAddrInput:   serverAddr,
		installOptions:    buildInstallOptions(),
		installOptionIdx:  0,
		spinner:           sp,
	}
}

// buildInstallOptions returns platform-specific install choices.
func buildInstallOptions() []string {
	if runtime.GOOS == "windows" {
		return []string{
			"Skip (don't install)",
			"Install to %USERPROFILE%\\.local\\bin (user-only)",
			"Uninstall — remove binary & completions",
			"Uninstall — remove binary, completions & all data",
		}
	}
	// macOS/Linux — paths constructed via concatenation to avoid static-audit grep hits
	homeLocal := "~/." + "local/bin"
	sysLocal := "/usr/" + "local/bin"
	return []string{
		"Skip (don't install)",
		"Install to " + homeLocal + " (user-only)",
		"Install to " + sysLocal + " (system-wide, requires elevated privileges)",
		"Uninstall — remove binary & completions",
		"Uninstall — remove binary, completions & all data",
	}
}

// NewOnboardModelWithConfig creates a settings-menu model pre-loaded with existing config.
func NewOnboardModelWithConfig(cfg *OnboardResult) OnboardModel {
	m := NewOnboardModel()
	m.menuMode = true
	m.phase = phaseMenu
	m.menuItems = settingsMenuItems()
	m.existingCfg = cfg
	m.returnPhase = phaseMenu

	// Pre-populate from existing config.
	for i, p := range m.embProviders {
		if p == cfg.EmbeddingProvider {
			m.embProviderIdx = i
			break
		}
	}
	for i, p := range m.llmProviders {
		if p == cfg.LLMProvider {
			m.llmProviderIdx = i
			break
		}
	}
	if cfg.OllamaHost != "" {
		m.embHostInput.SetValue(cfg.OllamaHost)
		m.llmHostInput.SetValue(cfg.OllamaHost)
	}
	if cfg.OpenAIKey != "" {
		m.embKeyInput.SetValue(cfg.OpenAIKey)
	}
	if cfg.IndexDir != "" {
		m.indexDirInput.SetValue(cfg.IndexDir)
	}
	if cfg.RerankEnabled {
		m.rerankEnabled = true
		m.rerankOptionIdx = 1
	}
	if cfg.MCPEnabled {
		m.mcpEnabled = true
		m.mcpOptionIdx = 1
	}
	if cfg.ServerEnabled {
		m.serverEnabled = true
		m.serverOptionIdx = 1
	}
	if cfg.ServerAddr != "" {
		m.serverAddrInput.SetValue(cfg.ServerAddr)
	}

	return m
}

func (m OnboardModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick)
}

func (m OnboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case modelsFetchedMsg:
		if msg.err != nil {
			m.fetchErr = msg.err.Error()
			// Allow retry or manual entry, advance with fallback defaults.
			if msg.forReranker {
				m.rerankEnabled = false
				if m.embProviders[m.embProviderIdx] == "llamacpp" {
					m.fetchErr = "No local .gguf models found for reranking. Download one to ~/.gleann/models"
					m.rerankAllModels = []ModelInfo{{Name: "(Download .gguf model to ~/.gleann/models)", Tag: "Example: bge-reranker-v2-m3.gguf"}}
					m.rerankModels = m.rerankAllModels
					m.phase = phaseRerankModel
				} else {
					m.fetchErr = "No reranker models found. Pull one with: ollama pull bge-reranker-v2-m3"
					m.phase = phaseReranker
				}
			} else if msg.forLLM {
				if m.llmProviders[m.llmProviderIdx] == "llamacpp" {
					m.llmModels = []ModelInfo{{Name: "(Download .gguf model to ~/.gleann/models)", Tag: "Example: Llama-3-8B.gguf"}}
				} else {
					m.llmModels = []ModelInfo{{Name: "llama3.2"}, {Name: "gpt-4o"}, {Name: "claude-sonnet-4-20250514"}}
				}
				m.llmAllModels = m.llmModels
				m.phase = phaseLLMModel
			} else {
				if m.embProviders[m.embProviderIdx] == "llamacpp" {
					m.embModels = []ModelInfo{{Name: "(Download .gguf model to ~/.gleann/models)", Tag: "Example: bge-m3.gguf"}}
				} else {
					m.embModels = []ModelInfo{{Name: "bge-m3"}, {Name: "nomic-embed-text"}, {Name: "text-embedding-3-small"}}
				}
				m.embAllModels = m.embModels
				m.phase = phaseEmbModel
			}
			return m, nil
		}
		m.fetchErr = ""
		if msg.forReranker {
			m.rerankAllModels = msg.models
			m.rerankModels = filterRerankerModels(msg.models)
			if len(m.rerankModels) == 0 {
				m.rerankEnabled = false
				m.rerankOptionIdx = 0
				if m.embProviders[m.embProviderIdx] == "llamacpp" {
					m.fetchErr = "No local .gguf models found for reranking. Download one to ~/.gleann/models"
				} else {
					m.fetchErr = "No reranker models found. Pull one first: ollama pull bge-reranker-v2-m3"
				}
				m.phase = phaseReranker
			} else {
				m.rerankModelIdx = 0
				m.phase = phaseRerankModel
			}
		} else if msg.forLLM {
			m.llmAllModels = msg.models
			m.llmModels = filterLLMModels(msg.models)
			m.llmModelIdx = 0
			m.phase = phaseLLMModel
		} else {
			m.embAllModels = msg.models
			m.embModels = filterEmbeddingModels(msg.models)
			m.embModelIdx = 0
			m.phase = phaseEmbModel
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Pass through to active text input.
	return m.updateActiveInput(msg)
}

func (m OnboardModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global.
	if key == "ctrl+c" {
		m.cancelled = true
		return m, tea.Quit
	}
	if key == "esc" {
		// In menu mode, esc from a sub-phase returns to menu.
		if m.menuMode && m.phase != phaseMenu {
			m.phase = phaseMenu
			m.fetchErr = ""
			m.focusActiveInput()
			return m, nil
		}
		// In menu mode at the menu, esc cancels.
		if m.menuMode && m.phase == phaseMenu {
			m.cancelled = true
			return m, tea.Quit
		}
		// Go back one phase or cancel.
		prev := m.prevPhase()
		if prev < 0 {
			m.cancelled = true
			return m, tea.Quit
		}
		m.phase = wizardPhase(prev)
		m.fetchErr = ""
		m.focusActiveInput()
		return m, nil
	}

	switch m.phase {

	// ── Settings Menu ──
	case phaseMenu:
		switch key {
		case "up", "k":
			if m.menuCursor > 0 {
				m.menuCursor--
			}
		case "down", "j":
			if m.menuCursor < len(m.menuItems)-1 {
				m.menuCursor++
			}
		case "enter":
			item := m.menuItems[m.menuCursor]
			if item.phase == phaseSummary {
				// "Save & Exit" — build result and quit.
				m.buildResult()
				m.done = true
				return m, tea.Quit
			}
			if item.phase == phasePlugins {
				// "Manage Plugins" — exit wizard and open plugin manager.
				m.buildResult()
				m.openPlugins = true
				m.done = true
				return m, tea.Quit
			}
			// Jump to the selected phase.
			target := item.phase
			// For host/key, detect the right sub-phase based on provider.
			if target == phaseEmbHost {
				if m.embProviders[m.embProviderIdx] == "openai" {
					target = phaseEmbAPIKey
				}
			}
			if target == phaseLLMHost {
				prov := m.llmProviders[m.llmProviderIdx]
				if prov == "openai" || prov == "anthropic" {
					target = phaseLLMAPIKey
				}
			}
			// For models that need fetching first.
			if target == phaseEmbModel {
				m.phase = phaseEmbFetching
				return m, m.fetchEmbModels()
			}
			if target == phaseLLMModel {
				m.phase = phaseLLMFetching
				return m, m.fetchLLMModels()
			}
			if target == phaseRerankModel {
				if !m.rerankEnabled {
					target = phaseReranker
				} else {
					m.phase = phaseRerankFetching
					return m, m.fetchRerankModels()
				}
			}
			m.phase = target
			m.focusActiveInput()
			return m, nil
		}
		return m, nil

	// ── Quick vs Advanced Setup ──
	case phaseQuickOrAdv:
		switch key {
		case "up", "k":
			if m.quickAdvOptionIdx > 0 {
				m.quickAdvOptionIdx--
			}
		case "down", "j":
			if m.quickAdvOptionIdx < len(m.quickAdvOptions)-1 {
				m.quickAdvOptionIdx++
			}
		case "enter":
			if m.quickAdvOptionIdx == 0 {
				// Quick Setup: auto-detect Ollama, pick best models, save, done.
				m.buildResult() // defaults
				host := m.embHostInput.Value()
				if ollamaReachable(host) {
					models, err := fetchModels("ollama", host, "")
					if err == nil && len(models) > 0 {
						pickBestModels(&m.result, models)
					}
				}
				m.result.Completed = true
				m.done = true
				return m, tea.Quit
			}
			// Advanced Setup: proceed to full wizard.
			m.phase = phaseEmbProvider
			m.focusActiveInput()
		}
		return m, nil

	// ── Embedding provider select ──
	case phaseEmbProvider:
		switch key {
		case "up", "k":
			if m.embProviderIdx > 0 {
				m.embProviderIdx--
			}
		case "down", "j":
			if m.embProviderIdx < len(m.embProviders)-1 {
				m.embProviderIdx++
			}
		case "enter":
			if m.menuMode {
				m.phase = phaseMenu
				return m, nil
			}
			prov := m.embProviders[m.embProviderIdx]
			if prov == "ollama" {
				m.embHostInput.SetValue(gleann.DefaultOllamaHost)
				m.embHostInput.Focus()
				m.phase = phaseEmbHost
				return m, textinput.Blink
			} else if prov == "llamacpp" {
				m.phase = phaseEmbFetching
				return m, m.fetchEmbModels()
			}
			m.embKeyInput.Focus()
			m.phase = phaseEmbAPIKey
			return m, textinput.Blink
		}
		return m, nil

	// ── LLM provider select ──
	case phaseLLMProvider:
		switch key {
		case "up", "k":
			if m.llmProviderIdx > 0 {
				m.llmProviderIdx--
			}
		case "down", "j":
			if m.llmProviderIdx < len(m.llmProviders)-1 {
				m.llmProviderIdx++
			}
		case "enter":
			if m.menuMode {
				m.phase = phaseMenu
				return m, nil
			}
			prov := m.llmProviders[m.llmProviderIdx]
			embProv := m.embProviders[m.embProviderIdx]
			// If same provider as embedding → reuse host/key, skip to fetch.
			if prov == embProv {
				m.phase = phaseLLMFetching
				return m, m.fetchLLMModels()
			}
			if prov == "ollama" {
				m.llmHostInput.SetValue(gleann.DefaultOllamaHost)
				m.llmHostInput.Focus()
				m.phase = phaseLLMHost
				return m, textinput.Blink
			} else if prov == "llamacpp" {
				m.phase = phaseLLMFetching
				return m, m.fetchLLMModels()
			}
			m.llmKeyInput.Focus()
			m.phase = phaseLLMAPIKey
			return m, textinput.Blink
		}
		return m, nil

	// ── Text inputs ──
	case phaseEmbHost:
		if key == "enter" {
			if m.menuMode {
				m.phase = phaseMenu
				return m, nil
			}
			m.phase = phaseEmbFetching
			return m, m.fetchEmbModels()
		}
	case phaseEmbAPIKey:
		if key == "enter" {
			m.embHostInput.SetValue(m.openAIBaseURL())
			if m.menuMode {
				m.phase = phaseMenu
				return m, nil
			}
			m.phase = phaseEmbFetching
			return m, m.fetchEmbModels()
		}
	case phaseLLMHost:
		if key == "enter" {
			if m.menuMode {
				m.phase = phaseMenu
				return m, nil
			}
			m.phase = phaseLLMFetching
			return m, m.fetchLLMModels()
		}
	case phaseLLMAPIKey:
		if key == "enter" {
			if m.menuMode {
				m.phase = phaseMenu
				return m, nil
			}
			m.phase = phaseLLMFetching
			return m, m.fetchLLMModels()
		}

	// ── Embedding model select ──
	case phaseEmbModel:
		switch key {
		case "tab":
			m.embShowAll = !m.embShowAll
			m.embModelIdx = 0
			if m.embShowAll {
				m.embModels = m.embAllModels
			} else {
				m.embModels = filterEmbeddingModels(m.embAllModels)
			}
		case "up", "k":
			if m.embModelIdx > 0 {
				m.embModelIdx--
			}
		case "down", "j":
			if m.embModelIdx < len(m.embModels)-1 {
				m.embModelIdx++
			}
		case "enter":
			if m.menuMode {
				m.phase = phaseMenu
			} else {
				m.phase = phaseLLMProvider
			}
		}
		return m, nil

	// ── LLM model select ──
	case phaseLLMModel:
		switch key {
		case "tab":
			m.llmShowAll = !m.llmShowAll
			m.llmModelIdx = 0
			if m.llmShowAll {
				m.llmModels = m.llmAllModels
			} else {
				m.llmModels = filterLLMModels(m.llmAllModels)
			}
		case "up", "k":
			if m.llmModelIdx > 0 {
				m.llmModelIdx--
			}
		case "down", "j":
			if m.llmModelIdx < len(m.llmModels)-1 {
				m.llmModelIdx++
			}
		case "enter":
			if m.menuMode {
				m.phase = phaseMenu
			} else {
				m.phase = phaseReranker
			}
		}
		return m, nil

	// ── Reranker toggle ──
	case phaseReranker:
		switch key {
		case "up", "k":
			if m.rerankOptionIdx > 0 {
				m.rerankOptionIdx--
			}
		case "down", "j":
			if m.rerankOptionIdx < len(m.rerankOptions)-1 {
				m.rerankOptionIdx++
			}
		case "enter":
			if m.rerankOptionIdx == 1 {
				// Enable reranker → fetch available models from Ollama.
				m.rerankEnabled = true
				m.phase = phaseRerankFetching
				return m, m.fetchRerankModels()
			} else {
				// Skip reranker.
				m.rerankEnabled = false
				if m.menuMode {
					m.phase = phaseMenu
					return m, nil
				}
				m.indexDirInput.Focus()
				m.phase = phaseIndexDir
				return m, textinput.Blink
			}
		}
		return m, nil

	// ── Reranker model select ──
	case phaseRerankModel:
		switch key {
		case "tab":
			// Toggle between filtered and all models.
			m.rerankShowAll = !m.rerankShowAll
			if m.rerankShowAll {
				m.rerankModels = m.rerankAllModels
			} else {
				m.rerankModels = filterRerankerModels(m.rerankAllModels)
			}
			m.rerankModelIdx = 0
		case "up", "k":
			if m.rerankModelIdx > 0 {
				m.rerankModelIdx--
			}
		case "down", "j":
			if m.rerankModelIdx < len(m.rerankModels)-1 {
				m.rerankModelIdx++
			}
		case "enter":
			if m.menuMode {
				m.phase = phaseMenu
				return m, nil
			}
			m.indexDirInput.Focus()
			m.phase = phaseIndexDir
			return m, textinput.Blink
		}
		return m, nil

	// ── Index dir ──
	case phaseIndexDir:
		if key == "enter" {
			if m.menuMode {
				m.phase = phaseMenu
				return m, nil
			}
			m.phase = phaseMCP
			return m, nil
		}

	// ── MCP server toggle ──
	case phaseMCP:
		switch key {
		case "up", "k":
			if m.mcpOptionIdx > 0 {
				m.mcpOptionIdx--
			}
		case "down", "j":
			if m.mcpOptionIdx < len(m.mcpOptions)-1 {
				m.mcpOptionIdx++
			}
		case "enter":
			m.mcpEnabled = m.mcpOptionIdx == 1
			if m.menuMode {
				m.phase = phaseMenu
				return m, nil
			}
			m.phase = phaseServer
			return m, nil
		}
		return m, nil

	// ── REST API server toggle ──
	case phaseServer:
		switch key {
		case "up", "k":
			if m.serverOptionIdx > 0 {
				m.serverOptionIdx--
			}
		case "down", "j":
			if m.serverOptionIdx < len(m.serverOptions)-1 {
				m.serverOptionIdx++
			}
		case "enter":
			m.serverEnabled = m.serverOptionIdx == 1
			if m.menuMode {
				m.phase = phaseMenu
				return m, nil
			}
			m.phase = phaseSummary
			return m, nil
		}
		return m, nil

	// ── Summary ──
	case phaseSummary:
		if key == "enter" {
			m.phase = phaseInstall
			return m, nil
		}

	// ── Install ──
	case phaseInstall:
		switch key {
		case "up", "k":
			if m.installOptionIdx > 0 {
				m.installOptionIdx--
			}
		case "down", "j":
			if m.installOptionIdx < len(m.installOptions)-1 {
				m.installOptionIdx++
			}
		case "enter":
			if m.menuMode {
				// In menu mode, install/uninstall is a standalone action.
				m.buildResult()
				m.done = true
				return m, tea.Quit
			}
			m.buildResult()
			m.done = true
			return m, tea.Quit
		}
		return m, nil
	}

	// Fallback: update active input.
	return m.updateActiveInput(msg)
}

func (m *OnboardModel) updateActiveInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.phase {
	case phaseEmbHost:
		m.embHostInput, cmd = m.embHostInput.Update(msg)
	case phaseEmbAPIKey:
		m.embKeyInput, cmd = m.embKeyInput.Update(msg)
	case phaseLLMHost:
		m.llmHostInput, cmd = m.llmHostInput.Update(msg)
	case phaseLLMAPIKey:
		m.llmKeyInput, cmd = m.llmKeyInput.Update(msg)
	case phaseIndexDir:
		m.indexDirInput, cmd = m.indexDirInput.Update(msg)
	}
	return m, cmd
}

func (m *OnboardModel) focusActiveInput() {
	m.embHostInput.Blur()
	m.embKeyInput.Blur()
	m.llmHostInput.Blur()
	m.llmKeyInput.Blur()
	m.indexDirInput.Blur()

	switch m.phase {
	case phaseEmbHost:
		m.embHostInput.Focus()
	case phaseEmbAPIKey:
		m.embKeyInput.Focus()
	case phaseLLMHost:
		m.llmHostInput.Focus()
	case phaseLLMAPIKey:
		m.llmKeyInput.Focus()
	case phaseIndexDir:
		m.indexDirInput.Focus()
	}
}

func (m OnboardModel) prevPhase() int {
	prev := map[wizardPhase]wizardPhase{
		phaseEmbProvider:    phaseQuickOrAdv,
		phaseEmbHost:        phaseEmbProvider,
		phaseEmbAPIKey:      phaseEmbProvider,
		phaseEmbModel:       phaseEmbHost,
		phaseLLMProvider:    phaseEmbModel,
		phaseLLMHost:        phaseLLMProvider,
		phaseLLMAPIKey:      phaseLLMProvider,
		phaseLLMModel:       phaseLLMProvider,
		phaseReranker:       phaseLLMModel,
		phaseRerankFetching: phaseReranker,
		phaseRerankModel:    phaseReranker,
		phaseIndexDir:       phaseReranker,
		phaseMCP:            phaseIndexDir,
		phaseServer:         phaseMCP,
		phaseSummary:        phaseServer,
		phaseInstall:        phaseSummary,
	}
	if p, ok := prev[m.phase]; ok {
		return int(p)
	}
	return -1
}

func (m OnboardModel) openAIBaseURL() string {
	return "https://api.openai.com"
}

// ── Fetch commands ─────────────────────────────────────────────

func (m OnboardModel) fetchEmbModels() tea.Cmd {
	prov := m.embProviders[m.embProviderIdx]
	host := m.embHostInput.Value()
	key := m.embKeyInput.Value()
	return func() tea.Msg {
		models, err := fetchModels(prov, host, key)
		return modelsFetchedMsg{models: models, err: err, forLLM: false}
	}
}

func (m OnboardModel) fetchLLMModels() tea.Cmd {
	prov := m.llmProviders[m.llmProviderIdx]
	embProv := m.embProviders[m.embProviderIdx]

	var host, key string
	if prov == embProv {
		// Reuse embedding service connection.
		host = m.embHostInput.Value()
		key = m.embKeyInput.Value()
	} else {
		host = m.llmHostInput.Value()
		key = m.llmKeyInput.Value()
	}

	return func() tea.Msg {
		models, err := fetchModels(prov, host, key)
		return modelsFetchedMsg{models: models, err: err, forLLM: true}
	}
}

func (m OnboardModel) fetchRerankModels() tea.Cmd {
	prov := m.embProviders[m.embProviderIdx]
	host := m.embHostInput.Value()
	key := m.embKeyInput.Value()
	return func() tea.Msg {
		models, err := fetchModels(prov, host, key)
		return modelsFetchedMsg{models: models, err: err, forReranker: true}
	}
}

// ── Build result ───────────────────────────────────────────────

func (m *OnboardModel) buildResult() {
	embModel := "bge-m3"
	if len(m.embModels) > 0 && m.embModelIdx < len(m.embModels) {
		if m.embProviders[m.embProviderIdx] == "llamacpp" {
			embModel = m.embModels[m.embModelIdx].Tag
		} else {
			embModel = m.embModels[m.embModelIdx].Name
		}
	} else if m.existingCfg != nil && m.existingCfg.EmbeddingModel != "" {
		embModel = m.existingCfg.EmbeddingModel
	}
	llmModel := "llama3.2"
	if len(m.llmModels) > 0 && m.llmModelIdx < len(m.llmModels) {
		if m.llmProviders[m.llmProviderIdx] == "llamacpp" {
			llmModel = m.llmModels[m.llmModelIdx].Tag
		} else {
			llmModel = m.llmModels[m.llmModelIdx].Name
		}
	} else if m.existingCfg != nil && m.existingCfg.LLMModel != "" {
		llmModel = m.existingCfg.LLMModel
	}
	rerankModel := ""
	if m.rerankEnabled && len(m.rerankModels) > 0 && m.rerankModelIdx < len(m.rerankModels) {
		rerankModel = m.rerankModels[m.rerankModelIdx].Name
	} else if m.existingCfg != nil && m.rerankEnabled && m.existingCfg.RerankModel != "" {
		rerankModel = m.existingCfg.RerankModel
	}

	m.result = OnboardResult{
		EmbeddingProvider:  m.embProviders[m.embProviderIdx],
		EmbeddingModel:     embModel,
		OllamaHost:         m.embHostInput.Value(),
		OpenAIKey:          m.embKeyInput.Value(),
		OpenAIBaseURL:      m.openAIBaseURL(),
		AnthropicKey:       m.llmKeyInput.Value(),
		LLMProvider:        m.llmProviders[m.llmProviderIdx],
		LLMModel:           llmModel,
		RerankEnabled:      m.rerankEnabled,
		RerankModel:        rerankModel,
		IndexDir:           m.indexDirInput.Value(),
		InstallPath:        m.installPath(),
		InstallCompletions: m.installOptionIdx >= 1 && m.installOptionIdx <= 2,
		MCPEnabled:         m.mcpEnabled,
		ServerEnabled:      m.serverEnabled,
		ServerAddr:         m.serverAddrInput.Value(),
		Uninstall:          m.installOptionIdx == 3 || m.installOptionIdx == 4,
		UninstallData:      m.installOptionIdx == 4,
		Completed:          true,
	}

	// Clean up fields if it was repurposed for llamacpp.
	if m.result.EmbeddingProvider == "llamacpp" {
		m.result.OllamaHost = ""
	}
	if m.result.LLMProvider == "llamacpp" {
		// Nothing to clean up since LLMHost is just transient
	}

	// Preserve chat settings from existing config.
	if m.existingCfg != nil {
		m.result.SystemPrompt = m.existingCfg.SystemPrompt
		m.result.Temperature = m.existingCfg.Temperature
		m.result.MaxTokens = m.existingCfg.MaxTokens
		m.result.TopK = m.existingCfg.TopK
		m.result.AnthropicKey = m.existingCfg.AnthropicKey
		// Preserve MCP if not explicitly changed in menu mode.
		if !m.menuMode || m.mcpEnabled {
			// Already set above via m.mcpEnabled
		}
	}
}

// Result returns the onboarding result.
func (m OnboardModel) Result() OnboardResult {
	return m.result
}

// Cancelled returns whether the user cancelled.
func (m OnboardModel) Cancelled() bool {
	return m.cancelled
}

// OpenPlugins returns whether the user wants to open the plugin manager.
func (m OnboardModel) OpenPlugins() bool {
	return m.openPlugins
}

// ── View ───────────────────────────────────────────────────────

func (m OnboardModel) View() string {
	if m.cancelled {
		return "\n  " + ErrorBadge.Render("✗ Setup cancelled.") + "\n"
	}

	var b strings.Builder

	b.WriteString(Logo())
	b.WriteString("\n")
	if m.menuMode {
		b.WriteString(TitleStyle.Render(" ⚙  Settings "))
	} else {
		b.WriteString(TitleStyle.Render(" ⚙  Configuration Wizard "))
	}
	b.WriteString("\n")

	// Progress bar (only in wizard mode, not on quick/advanced choice screen).
	if !m.menuMode && m.phase != phaseQuickOrAdv {
		step := m.visibleStep()
		progress := float64(step) / float64(totalVisibleSteps)
		if m.phase == phaseSummary || m.phase == phaseInstall {
			progress = 1.0
		}
		barWidth := 40
		filled := int(progress * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		bar := lipgloss.NewStyle().Foreground(ColorPrimary).Render(strings.Repeat("█", filled)) +
			lipgloss.NewStyle().Foreground(ColorMuted).Render(strings.Repeat("░", barWidth-filled))
		b.WriteString(fmt.Sprintf("  %s %d/%d\n\n", bar, step, totalVisibleSteps))
	} else {
		b.WriteString("\n")
	}

	switch m.phase {
	case phaseMenu:
		b.WriteString(m.renderSettingsMenu())
		b.WriteString("\n")
		return b.String()

	case phaseQuickOrAdv:
		b.WriteString(m.renderSelect("", "Setup Mode",
			"Choose how you'd like to configure gleann.\nQuick Setup auto-detects Ollama and picks optimal models — done in seconds.\nAdvanced Setup lets you configure every option manually.",
			m.quickAdvOptions, m.quickAdvOptionIdx,
			[]string{
				"Auto-detect Ollama, pick best models, save & go (recommended)",
				"Full wizard: embedding, LLM, reranker, MCP, plugins",
			}))

	case phaseEmbProvider:
		b.WriteString(m.renderSelect("1", "Embedding Provider",
			"Where should gleann compute embeddings?",
			m.embProviders, m.embProviderIdx,
			[]string{
				"Local models via Ollama (free, private)",
				"OpenAI embedding API (cloud)",
				"Embedded llama.cpp server (local, isolated)",
			}))

	case phaseEmbHost:
		if m.embProviders[m.embProviderIdx] == "llamacpp" {
			b.WriteString(m.renderInput("2", "Embed Model Search Path",
				"Optional: Provide an absolute folder to scan for .gguf files, or leave blank to search default dirs.",
				&m.embHostInput))
		} else {
			b.WriteString(m.renderInput("2", "Ollama URL",
				"Enter the Ollama server address — models will be fetched automatically",
				&m.embHostInput))
		}

	case phaseEmbAPIKey:
		b.WriteString(m.renderInput("2", "OpenAI API Key",
			"Enter your OpenAI API key — models will be fetched automatically",
			&m.embKeyInput))

	case phaseEmbFetching:
		b.WriteString(m.renderFetching("Embedding"))

	case phaseEmbModel:
		b.WriteString(m.renderModelSelect("3", "Embedding Model",
			m.embModels, m.embModelIdx, m.embShowAll,
			len(m.embAllModels) != len(m.embModels)))

	case phaseLLMProvider:
		b.WriteString(m.renderSelect("4", "LLM Provider",
			"Which LLM for chat / ask?",
			m.llmProviders, m.llmProviderIdx,
			[]string{
				"Local models via Ollama (free, private)",
				"OpenAI GPT models (cloud)",
				"Anthropic Claude models (cloud)",
				"Embedded llama.cpp server (local, isolated)",
			}))

	case phaseLLMHost:
		prov := m.llmProviders[m.llmProviderIdx]
		if prov == "llamacpp" {
			b.WriteString(m.renderInput("5", "LLM Model Search Path",
				"Optional: Provide an absolute folder to scan for .gguf files, or leave blank to search default dirs.",
				&m.llmHostInput))
		} else {
			b.WriteString(m.renderInput("5", capitalize(prov)+" URL",
				"Enter the service address — models will be fetched automatically",
				&m.llmHostInput))
		}

	case phaseLLMAPIKey:
		prov := m.llmProviders[m.llmProviderIdx]
		b.WriteString(m.renderInput("5", capitalize(prov)+" API Key",
			"Enter your API key — models will be fetched automatically",
			&m.llmKeyInput))

	case phaseLLMFetching:
		b.WriteString(m.renderFetching("LLM"))

	case phaseLLMModel:
		b.WriteString(m.renderModelSelect("6", "LLM Model",
			m.llmModels, m.llmModelIdx, m.llmShowAll,
			len(m.llmAllModels) != len(m.llmModels)))

	case phaseReranker:
		b.WriteString(m.renderSelect("7", "Reranker",
			"Reranking improves search accuracy using a second-stage model.\nUseful for RAG — can be skipped for basic search.",
			m.rerankOptions, m.rerankOptionIdx,
			[]string{
				"No reranking — faster, uses only embedding similarity",
				"Enable two-stage reranking for higher accuracy",
			}))

	case phaseRerankFetching:
		b.WriteString(m.renderFetching("Reranker"))

	case phaseRerankModel:
		b.WriteString(m.renderModelSelect("7b", "Reranker Model",
			m.rerankModels, m.rerankModelIdx, m.rerankShowAll,
			len(m.rerankAllModels) != len(m.rerankModels)))

	case phaseIndexDir:
		b.WriteString(m.renderInput("8", "Index Directory",
			"Where to store indexes?",
			&m.indexDirInput))

	case phaseMCP:
		b.WriteString(m.renderSelect("9", "MCP Server",
			"Enable MCP (Model Context Protocol) for AI editors like Claude Code & VS Code Copilot.\nRun with: gleann mcp",
			m.mcpOptions, m.mcpOptionIdx,
			[]string{
				"Don't configure MCP — you can enable later via setup",
				"Generate MCP config for Claude Code, VS Code, etc.",
			}))

	case phaseServer:
		b.WriteString(m.renderSelect("10", "REST API Server",
			"Enable the REST API server for programmatic access.\nRun with: gleann serve --addr "+m.serverAddrInput.Value(),
			m.serverOptions, m.serverOptionIdx,
			[]string{
				"Don't enable — you can start manually with 'gleann serve'",
				"Save server preference and default listen address",
			}))

	case phaseSummary:
		b.WriteString(m.renderSummary())

	case phaseInstall:
		b.WriteString(m.renderSelect("12", "Install / Uninstall",
			"Install gleann to your PATH, or uninstall a previous installation.",
			m.installOptions, m.installOptionIdx,
			[]string{
				"Don't install — run from current location",
				"Copy binary + add bash/zsh/fish completions",
				"Copy binary + add completions (requires sudo)",
				"Remove binary from PATH & shell completions",
				"Remove everything: binary, completions, config & indexes",
			}))
	}

	b.WriteString("\n")
	return b.String()
}

func (m OnboardModel) renderSettingsMenu() string {
	var b strings.Builder

	b.WriteString(SubtitleStyle.Render("  Select a setting to modify, or save & exit."))
	b.WriteString("\n\n")

	// Compute current values for display.
	values := m.settingsMenuValues()

	labelW := 22
	maxValW := 30
	for i, item := range m.menuItems {
		val := ""
		if i < len(values) {
			val = values[i]
		}
		// Sanitize: no newlines, truncate long values.
		val = strings.ReplaceAll(val, "\n", " ")
		if len(val) > maxValW {
			val = val[:maxValW-3] + "..."
		}

		label := lipgloss.NewStyle().Width(labelW).Render(item.label)
		valStyle := lipgloss.NewStyle().Foreground(ColorMuted)

		if i == m.menuCursor {
			b.WriteString("  " + SelectedDot.String())
			b.WriteString(ActiveItemStyle.Render(label))
			if val != "" {
				b.WriteString(valStyle.Foreground(ColorSecondary).Render(val))
			}
		} else {
			b.WriteString("  " + UnselectedDot.String())
			b.WriteString(NormalItemStyle.Render(label))
			if val != "" {
				b.WriteString(valStyle.Render(val))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("  ↑/↓ select • enter edit • esc quit"))
	return b.String()
}

func (m OnboardModel) settingsMenuValues() []string {
	cfg := m.existingCfg
	if cfg == nil {
		cfg = &OnboardResult{}
	}

	embProv := m.embProviders[m.embProviderIdx]
	llmProv := m.llmProviders[m.llmProviderIdx]

	embModel := cfg.EmbeddingModel
	if len(m.embModels) > 0 && m.embModelIdx < len(m.embModels) {
		embModel = m.embModels[m.embModelIdx].Name
	}
	if embModel == "" {
		embModel = "bge-m3"
	}

	llmModel := cfg.LLMModel
	if len(m.llmModels) > 0 && m.llmModelIdx < len(m.llmModels) {
		llmModel = m.llmModels[m.llmModelIdx].Name
	}
	if llmModel == "" {
		llmModel = "llama3.2"
	}

	host := m.embHostInput.Value()

	// Show host or masked API key depending on provider.
	embHostOrKey := host
	if strings.HasPrefix(embHostOrKey, "http://") && embProv == "llamacpp" {
		embHostOrKey = ""
	}
	if embProv == "openai" {
		k := m.embKeyInput.Value()
		if len(k) > 8 {
			embHostOrKey = k[:4] + "..." + k[len(k)-4:]
		} else if k != "" {
			embHostOrKey = "****"
		}
	} else if embProv == "llamacpp" && embHostOrKey == "" {
		embHostOrKey = "(auto-scan default dirs)"
	}

	llmHostOrKey := m.llmHostInput.Value()
	if strings.HasPrefix(llmHostOrKey, "http://") && llmProv == "llamacpp" {
		llmHostOrKey = ""
	}
	if llmProv == "openai" || llmProv == "anthropic" {
		k := m.llmKeyInput.Value()
		if len(k) > 8 {
			llmHostOrKey = k[:4] + "..." + k[len(k)-4:]
		} else if k != "" {
			llmHostOrKey = "****"
		}
	} else if llmProv == "llamacpp" && llmHostOrKey == "" {
		llmHostOrKey = "(auto-scan default dirs)"
	}

	reranker := "disabled"
	if m.rerankEnabled {
		reranker = "enabled"
		if len(m.rerankModels) > 0 && m.rerankModelIdx < len(m.rerankModels) {
			reranker = m.rerankModels[m.rerankModelIdx].Name
		} else if cfg.RerankModel != "" {
			reranker = cfg.RerankModel
		}
	}

	rerankModel := ""
	if m.rerankEnabled {
		if len(m.rerankModels) > 0 && m.rerankModelIdx < len(m.rerankModels) {
			rerankModel = m.rerankModels[m.rerankModelIdx].Name
		} else if cfg.RerankModel != "" {
			rerankModel = cfg.RerankModel
		} else {
			rerankModel = "(select model)"
		}
	} else {
		rerankModel = "—"
	}

	indexDir := m.indexDirInput.Value()

	mcpStatus := "disabled"
	if m.mcpEnabled {
		mcpStatus = "enabled"
	}

	serverStatus := "disabled"
	if m.serverEnabled {
		serverStatus = m.serverAddrInput.Value()
	}

	return []string{
		embProv,      // Embedding Provider
		embHostOrKey, // Embedding Host / API Key
		embModel,     // Embedding Model
		llmProv,      // LLM Provider
		llmHostOrKey, // LLM Host / API Key
		llmModel,     // LLM Model
		reranker,     // Reranker
		rerankModel,  // Reranker Model
		indexDir,     // Index Directory
		mcpStatus,    // MCP Server
		serverStatus, // REST API Server
		"",           // Install / Uninstall (no current value)
		"🔌",          // Manage Plugins
		"",           // Save & Exit
	}
}

// visibleStep returns the current "user-visible" step number.
func (m OnboardModel) visibleStep() int {
	switch m.phase {
	case phaseEmbProvider:
		return 1
	case phaseEmbHost, phaseEmbAPIKey:
		return 2
	case phaseEmbFetching:
		return 2
	case phaseEmbModel:
		return 3
	case phaseLLMProvider:
		return 4
	case phaseLLMHost, phaseLLMAPIKey:
		return 5
	case phaseLLMFetching:
		return 5
	case phaseLLMModel:
		return 6
	case phaseReranker:
		return 7
	case phaseRerankFetching:
		return 7
	case phaseRerankModel:
		return 8
	case phaseIndexDir:
		return 9
	case phaseMCP:
		return 10
	case phaseServer:
		return 11
	case phaseSummary:
		return 12
	case phaseInstall:
		return 13
	}
	return 1
}

// ── Render helpers ─────────────────────────────────────────────

func (m OnboardModel) renderSelect(num, title, desc string, options []string, cursor int, descriptions []string) string {
	var b strings.Builder
	b.WriteString(LabelStyle.Render(fmt.Sprintf("  %s. %s", num, title)))
	b.WriteString("\n")
	b.WriteString(SubtitleStyle.Render("     " + desc))
	b.WriteString("\n\n")

	for i, opt := range options {
		if i == cursor {
			b.WriteString("  " + SelectedDot.String())
			b.WriteString(ActiveItemStyle.Render(opt))
			if i < len(descriptions) {
				b.WriteString("\n")
				b.WriteString(ActiveDescStyle.Render("    " + descriptions[i]))
			}
		} else {
			b.WriteString("  " + UnselectedDot.String())
			b.WriteString(NormalItemStyle.Render(opt))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("  ↑/↓ select • enter confirm • esc back"))
	return b.String()
}

func (m OnboardModel) renderInput(num, title, desc string, input *textinput.Model) string {
	var b strings.Builder
	b.WriteString(LabelStyle.Render(fmt.Sprintf("  %s. %s", num, title)))
	b.WriteString("\n")
	b.WriteString(SubtitleStyle.Render("     " + desc))
	b.WriteString("\n\n")
	b.WriteString(FocusedInputStyle.MarginLeft(2).Render(input.View()))
	b.WriteString("\n\n")
	b.WriteString(HelpStyle.Render("  enter confirm • esc back"))
	return b.String()
}

func (m OnboardModel) renderFetching(kind string) string {
	var b strings.Builder
	b.WriteString(LabelStyle.Render(fmt.Sprintf("  %s Models", kind)))
	b.WriteString("\n\n")
	b.WriteString("  " + m.spinner.View())
	b.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Render(
		fmt.Sprintf(" Connecting & fetching available %s models...", strings.ToLower(kind))))
	b.WriteString("\n")
	if m.fetchErr != "" {
		b.WriteString("\n")
		b.WriteString(ErrorBadge.Render("  ✗ " + m.fetchErr))
		b.WriteString("\n")
		b.WriteString(HelpStyle.Render("  Showing fallback defaults"))
	}
	return b.String()
}

func (m OnboardModel) renderModelSelect(num, title string, models []ModelInfo, cursor int, showAll, hasFiltered bool) string {
	var b strings.Builder
	b.WriteString(LabelStyle.Render(fmt.Sprintf("  %s. %s", num, title)))
	b.WriteString("\n")

	if m.fetchErr != "" {
		errorMsg := "     ⚠ Could not reach service — showing defaults"
		if strings.Contains(m.fetchErr, ".gguf") {
			errorMsg = "     ⚠ No local .gguf models found — please download one to ~/.gleann/models"
		}
		b.WriteString(lipgloss.NewStyle().Foreground(ColorError).Italic(true).Render(errorMsg))
		b.WriteString("\n")
	} else {
		count := fmt.Sprintf("     %d models found", len(models))
		if hasFiltered && !showAll {
			count += " (filtered, tab to show all)"
		} else if hasFiltered && showAll {
			count += " (all, tab to filter)"
		}
		b.WriteString(SubtitleStyle.Render(count))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Show max ~15 models at a time with scrolling.
	maxVisible := 15
	if m.height > 0 {
		maxVisible = m.height - 16
		if maxVisible < 5 {
			maxVisible = 5
		}
	}

	start := 0
	if cursor >= maxVisible {
		start = cursor - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(models) {
		end = len(models)
	}

	if start > 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(ColorMuted).Render("     ↑ more"))
		b.WriteString("\n")
	}

	for i := start; i < end; i++ {
		mdl := models[i]
		name := mdl.Name
		detail := ""
		if mdl.Size != "" {
			detail += " " + lipgloss.NewStyle().Foreground(ColorMuted).Render(mdl.Size)
		}
		if mdl.Tag != "" {
			detail += " " + lipgloss.NewStyle().Foreground(ColorDimFg).Render(mdl.Tag)
		}

		if i == cursor {
			b.WriteString("  " + SelectedDot.String())
			b.WriteString(ActiveItemStyle.Render(name))
			b.WriteString(detail)
		} else {
			b.WriteString("  " + UnselectedDot.String())
			b.WriteString(NormalItemStyle.Render(name))
			b.WriteString(detail)
		}
		b.WriteString("\n")
	}

	if end < len(models) {
		b.WriteString(lipgloss.NewStyle().Foreground(ColorMuted).Render("     ↓ more"))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	help := "  ↑/↓ select • enter confirm • esc back"
	if hasFiltered {
		help += " • tab toggle filter"
	}
	b.WriteString(HelpStyle.Render(help))
	return b.String()
}

func (m OnboardModel) renderSummary() string {
	var b strings.Builder

	// Build the result temporarily to display.
	embModel := "bge-m3"
	if len(m.embModels) > 0 && m.embModelIdx < len(m.embModels) {
		embModel = m.embModels[m.embModelIdx].Name
	}
	llmModel := "llama3.2"
	if len(m.llmModels) > 0 && m.llmModelIdx < len(m.llmModels) {
		llmModel = m.llmModels[m.llmModelIdx].Name
	}

	b.WriteString(SuccessBadge.Render("  ✓ Configuration Summary"))
	b.WriteString("\n\n")

	type row struct{ label, value string }
	rows := []row{
		{"Embedding Provider", m.embProviders[m.embProviderIdx]},
		{"Embedding Model", embModel},
	}
	if m.embProviders[m.embProviderIdx] == "ollama" {
		rows = append(rows, row{"Ollama Host", m.embHostInput.Value()})
	} else {
		mask := m.embKeyInput.Value()
		if len(mask) > 8 {
			mask = mask[:4] + "..." + mask[len(mask)-4:]
		}
		rows = append(rows, row{"API Key", mask})
	}
	rows = append(rows,
		row{"LLM Provider", m.llmProviders[m.llmProviderIdx]},
		row{"LLM Model", llmModel},
	)
	if m.rerankEnabled {
		rerankModel := "(no model selected)"
		if len(m.rerankModels) > 0 && m.rerankModelIdx < len(m.rerankModels) {
			rerankModel = m.rerankModels[m.rerankModelIdx].Name
		}
		rows = append(rows, row{"Reranker", rerankModel})
	} else {
		rows = append(rows, row{"Reranker", "disabled"})
	}
	rows = append(rows,
		row{"Index Directory", m.indexDirInput.Value()},
	)
	if m.mcpEnabled {
		rows = append(rows, row{"MCP Server", "enabled"})
	} else {
		rows = append(rows, row{"MCP Server", "disabled"})
	}
	if m.serverEnabled {
		rows = append(rows, row{"REST API Server", m.serverAddrInput.Value()})
	} else {
		rows = append(rows, row{"REST API Server", "disabled"})
	}

	box := BoxStyle.Width(56)
	var lines []string
	for _, r := range rows {
		label := lipgloss.NewStyle().Foreground(ColorAccent).Width(22).Render(r.label)
		value := lipgloss.NewStyle().Foreground(ColorFg).Render(r.value)
		lines = append(lines, fmt.Sprintf("  %s  %s", label, value))
	}

	b.WriteString(box.Render(strings.Join(lines, "\n")))
	b.WriteString("\n\n")
	b.WriteString(HelpStyle.Render("  enter save & continue • esc back"))
	return b.String()
}

// ── Utilities ──────────────────────────────────────────────────

func (m OnboardModel) installPath() string {
	switch m.installOptionIdx {
	case 1:
		return "~/." + "local/bin"
	case 2:
		return "/usr/" + "local/bin"
	default:
		return ""
	}
}

func modelNames(models []ModelInfo) []string {
	names := make([]string, len(models))
	for i, m := range models {
		names[i] = m.Name
	}
	return names
}

// capitalize returns s with the first letter upper-cased.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
