package tui

import (
	"errors"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// okey builds a key press from a name the model's handlers know.
func okey(name string) tea.KeyPressMsg {
	switch name {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}
	return tea.KeyPressMsg{Code: []rune(name)[0], Text: name}
}

// step sends the keys one by one and returns the resulting model and the
// command of the last key.
func step(t *testing.T, m OnboardModel, keys ...string) (OnboardModel, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var r tea.Model
		r, cmd = m.Update(okey(k))
		m = r.(OnboardModel)
	}
	return m, cmd
}

func feed(t *testing.T, m OnboardModel, msg tea.Msg) OnboardModel {
	t.Helper()
	r, _ := m.Update(msg)
	return r.(OnboardModel)
}

func models(names ...string) []ModelInfo {
	var out []ModelInfo
	for _, n := range names {
		out = append(out, ModelInfo{Name: n})
	}
	return out
}

func TestOnboardAdvancedWizardWalkthrough(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	m := NewOnboardModel()
	m, _ = step(t, m, "down", "enter")
	if m.phase != phaseEmbProvider {
		t.Fatalf("advanced setup starts at the embedding provider, got %d", m.phase)
	}
	m, cmd := step(t, m, "enter") // ollama
	if m.phase != phaseEmbHost || cmd == nil {
		t.Fatalf("ollama asks for the host: phase=%d", m.phase)
	}
	m.embHostInput.SetValue("11435") // a bare port becomes a localhost URL
	m, _ = step(t, m, "enter")
	if m.phase != phaseEmbOllamaKey || m.embHostInput.Value() != "http://localhost:11435" {
		t.Fatalf("phase=%d host=%q", m.phase, m.embHostInput.Value())
	}
	if m.llmHostInput.Value() != "http://localhost:11435" {
		t.Fatalf("the LLM host follows when it is also ollama, got %q", m.llmHostInput.Value())
	}
	m.ollamaKeyInput.SetValue("  secret  ")
	m, cmd = step(t, m, "enter")
	if m.phase != phaseEmbFetching || cmd == nil {
		t.Fatalf("after the key models are fetched: phase=%d", m.phase)
	}
	if m.ollamaKeyInput.Value() != "secret" {
		t.Fatalf("the key is trimmed: %q", m.ollamaKeyInput.Value())
	}

	m = feed(t, m, modelsFetchedMsg{models: models("bge-m3", "nomic-embed-text", "llama3.2:3b")})
	if m.phase != phaseEmbModel || m.fetchErr != "" {
		t.Fatalf("phase=%d err=%q", m.phase, m.fetchErr)
	}
	m, _ = step(t, m, "enter", "enter", "enter") // emb model, code model, docs model
	if m.phase != phaseLLMProvider {
		t.Fatalf("phase = %d, want LLM provider", m.phase)
	}
	m, cmd = step(t, m, "enter") // ollama == embedding provider: skip host/key
	if m.phase != phaseLLMFetching || cmd == nil {
		t.Fatalf("phase=%d", m.phase)
	}
	m = feed(t, m, modelsFetchedMsg{models: models("llama3.2:3b", "qwen2.5:32b"), forLLM: true})
	if m.phase != phaseLLMModel {
		t.Fatalf("phase=%d", m.phase)
	}
	m, _ = step(t, m, "enter") // LLM model
	if m.phase != phaseReranker {
		t.Fatalf("phase=%d", m.phase)
	}
	m, _ = step(t, m, "enter") // skip reranking
	if m.phase != phaseIndexDir || m.rerankEnabled {
		t.Fatalf("phase=%d rerank=%v", m.phase, m.rerankEnabled)
	}
	m, _ = step(t, m, "enter", "enter") // index dir, backend (hnsw)
	if m.phase != phaseMCP {
		t.Fatalf("phase=%d", m.phase)
	}
	m, _ = step(t, m, "down", "enter") // enable MCP
	if m.phase != phaseServer || !m.mcpEnabled {
		t.Fatalf("phase=%d mcp=%v", m.phase, m.mcpEnabled)
	}
	m, _ = step(t, m, "down", "enter") // enable REST server
	if m.phase != phaseServerAddr || !m.serverEnabled {
		t.Fatalf("phase=%d", m.phase)
	}
	m.serverAddrInput.SetValue("9090")
	m, _ = step(t, m, "enter")
	if m.phase != phaseSummary || m.serverAddrInput.Value() != ":9090" {
		t.Fatalf("phase=%d addr=%q", m.phase, m.serverAddrInput.Value())
	}
	m, _ = step(t, m, "enter")
	if m.phase != phaseInstall {
		t.Fatalf("phase=%d", m.phase)
	}
	m, cmd = step(t, m, "enter") // skip install
	if !m.done || cmd == nil {
		t.Fatal("the wizard must finish")
	}
	res := m.Result()
	if res.EmbeddingProvider != "ollama" || res.EmbeddingModel != "bge-m3" || res.LLMModel != "llama3.2:3b" ||
		!res.MCPEnabled || !res.ServerEnabled || res.ServerAddr != ":9090" || res.OllamaAPIKey != "secret" || !res.Completed {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestOnboardEscWalksBackAndCancels(t *testing.T) {
	m := NewOnboardModel()
	m, _ = step(t, m, "down", "enter", "enter") // EmbHost
	if m.phase != phaseEmbHost {
		t.Fatal("setup")
	}
	m.fetchErr = "old"
	m, _ = step(t, m, "esc")
	if m.phase != phaseEmbProvider || m.fetchErr != "" {
		t.Fatalf("esc goes back and clears the error: phase=%d err=%q", m.phase, m.fetchErr)
	}
	m, _ = step(t, m, "esc")
	if m.phase != phaseQuickOrAdv {
		t.Fatalf("phase=%d", m.phase)
	}
	m, cmd := step(t, m, "esc")
	if !m.Cancelled() || cmd == nil {
		t.Fatal("esc at the first phase cancels")
	}
}

func TestOnboardQuickSetupFinishesWithDefaults(t *testing.T) {
	// Nothing listens on the default host in test mode: quick setup falls back
	// to the defaults instead of failing.
	t.Setenv("HOME", t.TempDir())
	m := NewOnboardModel()
	m.embHostInput.SetValue("http://127.0.0.1:1") // closed port, refused at once
	m, cmd := step(t, m, "enter")
	if !m.done || !m.Result().Completed || cmd == nil {
		t.Fatalf("quick setup must complete: %+v", m.Result())
	}
}

func TestOnboardProviderKeyPaths(t *testing.T) {
	tests := []struct {
		name      string
		start     wizardPhase
		prov      string // provider to select (embedding or LLM per start)
		wantPhase wizardPhase
		wantCmd   bool
	}{
		{"emb openai asks the key", phaseEmbProvider, "openai", phaseEmbAPIKey, true},
		{"emb llamacpp goes fetching", phaseEmbProvider, "llamacpp", phaseEmbFetching, true},
		{"llm openai asks the key", phaseLLMProvider, "openai", phaseLLMAPIKey, true},
		{"llm anthropic asks the key", phaseLLMProvider, "anthropic", phaseLLMAPIKey, true},
		{"llm llamacpp goes fetching", phaseLLMProvider, "llamacpp", phaseLLMFetching, true},
		{"llm ollama asks the host", phaseLLMProvider, "ollama", phaseLLMHost, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewOnboardModel()
			m.phase = tc.start
			// Pick a different embedding provider so llm==emb does not shortcut.
			if tc.start == phaseLLMProvider {
				m.embProviderIdx = provIdx(m.embProviders, "llamacpp")
				if tc.prov == "llamacpp" {
					m.embProviderIdx = provIdx(m.embProviders, "ollama")
				}
				m.llmProviderIdx = provIdx(m.llmProviders, tc.prov)
			} else {
				m.embProviderIdx = provIdx(m.embProviders, tc.prov)
			}
			m, cmd := step(t, m, "enter")
			if m.phase != tc.wantPhase || (cmd != nil) != tc.wantCmd {
				t.Fatalf("phase=%d cmd=%v", m.phase, cmd != nil)
			}
		})
	}
}

func provIdx(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return 0
}

func TestOnboardProviderListNavigation(t *testing.T) {
	m := NewOnboardModel()
	m.phase = phaseEmbProvider
	m, _ = step(t, m, "up")
	if m.embProviderIdx != 0 {
		t.Fatal("up stops at the top")
	}
	m, _ = step(t, m, "j", "j", "k")
	if m.embProviderIdx != 1 {
		t.Fatalf("idx = %d", m.embProviderIdx)
	}
	for range m.embProviders {
		m, _ = step(t, m, "down")
	}
	if m.embProviderIdx != len(m.embProviders)-1 {
		t.Fatal("down stops at the bottom")
	}
	m.phase = phaseLLMProvider
	for range m.llmProviders {
		m, _ = step(t, m, "down")
	}
	m, _ = step(t, m, "k")
	if m.llmProviderIdx != len(m.llmProviders)-2 {
		t.Fatalf("llm idx = %d", m.llmProviderIdx)
	}
	m.menuMode = true
	m, _ = step(t, m, "enter")
	if m.phase != phaseMenu {
		t.Fatal("in menu mode enter returns to the menu")
	}
}

func TestOnboardHostAndKeyInputs(t *testing.T) {
	m := NewOnboardModel()
	// LLM host: host:port gets http://, then asks for the key.
	m.phase = phaseLLMHost
	m.llmHostInput.SetValue("box.lan:11434")
	m, _ = step(t, m, "enter")
	if m.phase != phaseLLMOllamaKey || m.llmHostInput.Value() != "http://box.lan:11434" {
		t.Fatalf("phase=%d host=%q", m.phase, m.llmHostInput.Value())
	}
	if m.embHostInput.Value() != "http://box.lan:11434" {
		t.Fatalf("the embedding host follows an ollama LLM host: %q", m.embHostInput.Value())
	}
	m, cmd := step(t, m, "enter")
	if m.phase != phaseLLMFetching || cmd == nil {
		t.Fatalf("phase=%d", m.phase)
	}
	// In menu mode each input returns to the menu instead of fetching.
	for _, ph := range []wizardPhase{phaseEmbHost, phaseEmbOllamaKey, phaseEmbAPIKey, phaseLLMHost, phaseLLMOllamaKey, phaseLLMAPIKey, phaseIndexDir} {
		mm := NewOnboardModel()
		mm.menuMode = true
		mm.phase = ph
		if ph == phaseEmbAPIKey || ph == phaseLLMAPIKey {
			mm.embProviderIdx = provIdx(mm.embProviders, "openai")
			mm.llmProviderIdx = provIdx(mm.llmProviders, "openai")
		}
		mm, _ = step(t, mm, "enter")
		wantMenu := mm.phase == phaseMenu
		// Host steps of ollama go on to the key step first.
		if !wantMenu && (ph == phaseEmbHost || ph == phaseLLMHost) && (mm.phase == phaseEmbOllamaKey || mm.phase == phaseLLMOllamaKey) {
			continue
		}
		if !wantMenu {
			t.Errorf("phase %d: enter in menu mode must return to the menu, got %d", ph, mm.phase)
		}
	}
	// API key steps fetch the list in wizard mode.
	m = NewOnboardModel()
	m.phase = phaseEmbAPIKey
	m, cmd = step(t, m, "enter")
	if m.phase != phaseEmbFetching || cmd == nil {
		t.Fatal("embedding API key leads to fetching")
	}
	m.phase = phaseLLMAPIKey
	m, cmd = step(t, m, "enter")
	if m.phase != phaseLLMFetching || cmd == nil {
		t.Fatal("LLM API key leads to fetching")
	}
}

func TestOnboardTypingGoesToTheActiveInput(t *testing.T) {
	tests := []struct {
		phase wizardPhase
		get   func(OnboardModel) string
	}{
		{phaseEmbHost, func(m OnboardModel) string { return m.embHostInput.Value() }},
		{phaseEmbAPIKey, func(m OnboardModel) string { return m.embKeyInput.Value() }},
		{phaseLLMHost, func(m OnboardModel) string { return m.llmHostInput.Value() }},
		{phaseLLMAPIKey, func(m OnboardModel) string { return m.llmKeyInput.Value() }},
		{phaseEmbOllamaKey, func(m OnboardModel) string { return m.ollamaKeyInput.Value() }},
		{phaseLLMOllamaKey, func(m OnboardModel) string { return m.ollamaKeyInput.Value() }},
		{phaseServerAddr, func(m OnboardModel) string { return m.serverAddrInput.Value() }},
		{phaseCustomHFInput, func(m OnboardModel) string { return m.customHFInput.Value() }},
		{phaseIndexDir, func(m OnboardModel) string { return m.indexDirInput.Value() }},
	}
	for _, tc := range tests {
		m := NewOnboardModel()
		m.phase = tc.phase
		m.focusActiveInput()
		m.customHFInput.Focus() // focused when the step opens
		before := tc.get(m)
		m, _ = step(t, m, "Z")
		if got := tc.get(m); got != before+"Z" {
			t.Errorf("phase %d: value %q -> %q, typed Z was lost", tc.phase, before, got)
		}
	}
	// A phase with no input ignores the key.
	m := NewOnboardModel()
	m.phase = phaseSummary
	if m2, _ := step(t, m, "Z"); m2.phase != phaseSummary {
		t.Fatal("summary ignores stray keys")
	}
}

func TestOnboardSpinnerTickAndInit(t *testing.T) {
	m := NewOnboardModel()
	if m.Init() == nil {
		t.Fatal("Init starts the cursor blink and the spinner")
	}
	r, cmd := m.Update(spinner.TickMsg{})
	if cmd == nil {
		// A tick for an unknown ID is dropped by the spinner; the model stays valid.
		_ = r.(OnboardModel)
	}
	_, cmd = m.Update(m.spinner.Tick())
	_ = cmd
}

func TestOnboardOptionToggleHandlers(t *testing.T) {
	// A2A: both keys sets move, enter stores and moves on.
	m := NewOnboardModel()
	m.phase = phaseA2A
	m, _ = step(t, m, "left")
	if m.a2aOptionIdx != 0 {
		t.Fatal("left selects Disable")
	}
	m, _ = step(t, m, "h", "down", "j", "l", "k", "right")
	m, _ = step(t, m, "enter")
	if !m.a2aEnabled || m.phase != phaseMCP {
		t.Fatalf("a2a=%v phase=%d", m.a2aEnabled, m.phase)
	}
	m.phase, m.menuMode = phaseA2A, true
	m, _ = step(t, m, "up", "enter")
	if m.a2aEnabled || m.phase != phaseMenu {
		t.Fatalf("a2a=%v phase=%d", m.a2aEnabled, m.phase)
	}

	// Backend: FAISS is refused in a build without it; menu mode just returns.
	m = NewOnboardModel()
	m.phase = phaseBackend
	m, _ = step(t, m, "up", "j", "k", "down", "down")
	if m.backendOptionIdx != 2 {
		t.Fatalf("backend idx = %d", m.backendOptionIdx)
	}
	m, _ = step(t, m, "enter")
	if m.phase == phaseBackend && !strings.Contains(m.fetchErr, "FAISS") {
		t.Fatalf("an unavailable FAISS must say so, got %q", m.fetchErr)
	}
	m.phase, m.menuMode = phaseBackend, true
	m, _ = step(t, m, "enter")
	if m.phase != phaseMenu {
		t.Fatal("menu mode returns")
	}
	for i, want := range []string{"hnsw", "diskann", "faiss", "faiss-hybrid", "hnsw"} {
		m.backendOptionIdx = i
		if got := m.selectedBackend(); got != want {
			t.Errorf("backend %d = %q, want %q", i, got, want)
		}
	}

	// MCP / server toggles in both modes.
	m = NewOnboardModel()
	m.phase = phaseMCP
	m, _ = step(t, m, "j", "up", "k", "enter")
	if m.mcpEnabled || m.phase != phaseServer {
		t.Fatalf("mcp=%v phase=%d", m.mcpEnabled, m.phase)
	}
	m.phase, m.menuMode = phaseMCP, true
	m, _ = step(t, m, "j", "enter")
	if !m.mcpEnabled || m.phase != phaseMenu {
		t.Fatalf("mcp=%v phase=%d", m.mcpEnabled, m.phase)
	}
	m = NewOnboardModel()
	m.phase = phaseServer
	m, _ = step(t, m, "enter") // disabled -> straight to the summary
	if m.serverEnabled || m.phase != phaseSummary {
		t.Fatalf("server=%v phase=%d", m.serverEnabled, m.phase)
	}
	m.phase, m.menuMode = phaseServer, true
	m, _ = step(t, m, "enter")
	if m.phase != phaseMenu {
		t.Fatal("menu mode returns")
	}
	m.phase = phaseServer
	m, _ = step(t, m, "j", "k", "j", "enter")
	if !m.serverEnabled || m.phase != phaseServerAddr {
		t.Fatalf("server=%v phase=%d", m.serverEnabled, m.phase)
	}
	// Server address normalisation.
	for in, want := range map[string]string{"": ":8080", "7000": ":7000", "0.0.0.0:80": "0.0.0.0:80", "  :81 ": ":81"} {
		mm := NewOnboardModel()
		mm.phase = phaseServerAddr
		mm.serverAddrInput.SetValue(in)
		mm, _ = step(t, mm, "enter")
		if got := mm.serverAddrInput.Value(); got != want && !(in == "" && got != "") {
			t.Errorf("addr %q -> %q, want %q", in, got, want)
		}
		if mm.phase != phaseSummary {
			t.Errorf("addr %q: phase %d", in, mm.phase)
		}
	}
	mm := NewOnboardModel()
	mm.phase, mm.menuMode = phaseServerAddr, true
	if mm, _ = step(t, mm, "enter"); mm.phase != phaseMenu {
		t.Fatal("menu mode returns")
	}
}

func TestOnboardInstallStepKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := NewOnboardModel()
	m.phase = phaseInstall
	m, _ = step(t, m, "up", "j", "down", "k")
	if m.installOptionIdx != 1 {
		t.Fatalf("idx = %d", m.installOptionIdx)
	}
	for range m.installOptions {
		m, _ = step(t, m, "down")
	}
	if m.installOptionIdx != len(m.installOptions)-1 {
		t.Fatal("down stops at the last option")
	}
	// Menu mode: skip returns to the menu, any install option finishes.
	mm := NewOnboardModel()
	mm.phase, mm.menuMode = phaseInstall, true
	mm, _ = step(t, mm, "enter")
	if mm.phase != phaseMenu || mm.done {
		t.Fatal("skip in menu mode returns to the menu")
	}
	mm.phase = phaseInstall
	mm, cmd := step(t, mm, "down", "enter")
	if !mm.done || cmd == nil {
		t.Fatal("an install choice finishes the menu")
	}
	if p := (OnboardModel{installOptionIdx: 0}).installPath(); p != "" {
		t.Fatalf("skip has no path, got %q", p)
	}
	if p := (OnboardModel{installOptionIdx: 1}).installPath(); !strings.HasSuffix(p, "local/bin") || strings.HasPrefix(p, "/usr") {
		t.Fatalf("user path = %q", p)
	}
	if p := (OnboardModel{installOptionIdx: 2}).installPath(); !strings.HasPrefix(p, "/usr") {
		t.Fatalf("system path = %q", p)
	}
}

func TestOnboardModelSelectionKeys(t *testing.T) {
	list := models("a", "b", "c")
	t.Run("embedding list toggles filter and moves", func(t *testing.T) {
		m := NewOnboardModel()
		m.phase = phaseEmbModel
		m.embAllModels = models("bge-m3", "llama3.2:3b", "nomic-embed-text")
		m.embModels = filterEmbeddingModels(m.embAllModels)
		m, _ = step(t, m, "tab")
		if !m.embShowAll || len(m.embModels) != 3 {
			t.Fatalf("tab shows everything: all=%v n=%d", m.embShowAll, len(m.embModels))
		}
		m, _ = step(t, m, "tab")
		if m.embShowAll || len(m.embModels) >= 3 {
			t.Fatalf("tab again filters: n=%d", len(m.embModels))
		}
		m.embModels = list
		m, _ = step(t, m, "up", "k", "j", "down")
		if m.embModelIdx != 2 {
			t.Fatalf("idx = %d", m.embModelIdx)
		}
	})
	t.Run("custom entries open the HF input", func(t *testing.T) {
		for _, tc := range []struct {
			phase wizardPhase
			what  string
			set   func(*OnboardModel)
		}{
			{phaseEmbModel, "emb", func(m *OnboardModel) { m.embModels = []ModelInfo{{Name: "(Custom)", Tag: "custom"}} }},
			{phaseLLMModel, "llm", func(m *OnboardModel) { m.llmModels = []ModelInfo{{Name: "(Custom)", Tag: "custom"}} }},
			{phaseRerankModel, "reranker", func(m *OnboardModel) { m.rerankModels = []ModelInfo{{Name: "(Custom)", Tag: "custom"}} }},
		} {
			m := NewOnboardModel()
			m.phase = tc.phase
			tc.set(&m)
			m, cmd := step(t, m, "enter")
			if m.phase != phaseCustomHFInput || m.customHFForWhat != tc.what || cmd == nil {
				t.Fatalf("%s: phase=%d what=%q", tc.what, m.phase, m.customHFForWhat)
			}
			m.customHFInput.SetValue("org/model-GGUF")
			m, _ = step(t, m, "enter")
			if m.phase == phaseCustomHFInput {
				t.Fatalf("%s: enter must leave the custom input", tc.what)
			}
			var got ModelInfo
			switch tc.what {
			case "emb":
				got = m.embModels[0]
			case "llm":
				got = m.llmModels[0]
			default:
				got = m.rerankModels[0]
			}
			if got.Name != "org/model-GGUF" || got.Tag != "org/model-GGUF" {
				t.Fatalf("%s: the typed repo must replace the custom row, got %+v", tc.what, got)
			}
		}
		// An empty repo is replaced by a visible placeholder instead of panicking.
		m := NewOnboardModel()
		m.phase, m.customHFForWhat = phaseCustomHFInput, "emb"
		m.embModels = models("x")
		m, _ = step(t, m, "enter")
		if m.embModels[0].Name != "none/none" {
			t.Fatalf("got %+v", m.embModels[0])
		}
		// ... and a menu-mode run returns to the menu.
		m.phase, m.menuMode, m.customHFForWhat = phaseCustomHFInput, true, "llm"
		m.llmModels = models("y")
		if m, _ = step(t, m, "enter"); m.phase != phaseMenu {
			t.Fatal("menu mode returns")
		}
	})
	t.Run("llm list", func(t *testing.T) {
		m := NewOnboardModel()
		m.phase = phaseLLMModel
		m.llmAllModels = models("llama3.2:3b", "bge-m3")
		m.llmModels = filterLLMModels(m.llmAllModels)
		m, _ = step(t, m, "tab")
		if !m.llmShowAll || len(m.llmModels) != 2 {
			t.Fatalf("show all: %v %d", m.llmShowAll, len(m.llmModels))
		}
		m, _ = step(t, m, "tab")
		m.llmModels = list
		m, _ = step(t, m, "j", "j", "k")
		if m.llmModelIdx != 1 {
			t.Fatalf("idx = %d", m.llmModelIdx)
		}
		m.menuMode = true
		if m, _ = step(t, m, "enter"); m.phase != phaseMenu {
			t.Fatal("menu mode returns")
		}
	})
	t.Run("reranker list", func(t *testing.T) {
		m := NewOnboardModel()
		m.phase = phaseRerankModel
		m.rerankAllModels = models("bge-reranker-v2-m3", "llama3.2:3b")
		m.rerankModels = filterRerankerModels(m.rerankAllModels)
		m, _ = step(t, m, "tab")
		if !m.rerankShowAll || len(m.rerankModels) != 2 {
			t.Fatalf("show all: %v %d", m.rerankShowAll, len(m.rerankModels))
		}
		m, _ = step(t, m, "tab")
		m.rerankModels = list
		m, _ = step(t, m, "down", "j")
		if m.rerankModelIdx != 2 {
			t.Fatalf("idx = %d", m.rerankModelIdx)
		}
		m, _ = step(t, m, "enter")
		if m.phase != phaseIndexDir {
			t.Fatalf("phase=%d", m.phase)
		}
		m.phase, m.menuMode = phaseRerankModel, true
		if m, _ = step(t, m, "enter"); m.phase != phaseMenu {
			t.Fatal("menu mode returns")
		}
	})
	t.Run("reranker toggle", func(t *testing.T) {
		m := NewOnboardModel()
		m.phase = phaseReranker
		m, _ = step(t, m, "j", "j", "k", "k", "j")
		m, cmd := step(t, m, "enter")
		if !m.rerankEnabled || m.phase != phaseRerankFetching || cmd == nil {
			t.Fatalf("enable: phase=%d", m.phase)
		}
		m.phase, m.menuMode, m.rerankOptionIdx = phaseReranker, true, 0
		m, _ = step(t, m, "enter")
		if m.rerankEnabled || m.phase != phaseMenu {
			t.Fatalf("skip in menu mode: enabled=%v phase=%d", m.rerankEnabled, m.phase)
		}
	})
}

func TestOnboardCodeAndDocEmbeddingModelChoice(t *testing.T) {
	for _, docs := range []bool{false, true} {
		m := NewOnboardModel()
		m.embModels = models("bge-m3", "nomic-embed-text", "mxbai-embed-large")
		m.embAllModels = m.embModels
		m.phase = phaseCodeEmbModel
		if docs {
			m.phase = phaseDocEmbModel
		}
		pick := func() string {
			if docs {
				return m.docEmbeddingModel
			}
			return m.codeEmbeddingModel
		}
		idx := func() int {
			if docs {
				return m.docEmbModelIdx
			}
			return m.codeEmbModelIdx
		}
		m, _ = step(t, m, "up", "j", "j", "j", "j", "k")
		if idx() < 1 {
			t.Fatalf("docs=%v: cursor must have moved, idx=%d", docs, idx())
		}
		m, _ = step(t, m, "enter")
		if pick() == "" {
			t.Fatalf("docs=%v: a non-default option must be stored", docs)
		}
		next := phaseDocEmbModel
		if docs {
			next = phaseLLMProvider
		}
		if m.phase != next {
			t.Fatalf("docs=%v: phase=%d want %d", docs, m.phase, next)
		}
		// The first option is "use the main embedding model": clears the choice.
		if docs {
			m.phase, m.docEmbModelIdx = phaseDocEmbModel, 0
		} else {
			m.phase, m.codeEmbModelIdx = phaseCodeEmbModel, 0
		}
		m.menuMode = true
		m, _ = step(t, m, "enter")
		if pick() != "" || m.phase != phaseMenu {
			t.Fatalf("docs=%v: default clears the choice (got %q) and returns to the menu", docs, pick())
		}
	}
	// With a local provider the tag (file name) is saved, not the display name.
	m := NewOnboardModel()
	m.embProviderIdx = provIdx(m.embProviders, "llamacpp")
	m.embModels = []ModelInfo{{Name: "A", Tag: "a.gguf"}, {Name: "B (1 GB)", Tag: "b.gguf"}}
	m.phase, m.codeEmbModelIdx = phaseCodeEmbModel, 2 // row 0 is "same as the main model"
	m, _ = step(t, m, "enter")
	if m.codeEmbeddingModel != "b.gguf" {
		t.Fatalf("local providers store the tag, got %q", m.codeEmbeddingModel)
	}
}

func TestOnboardMenuEnterTargets(t *testing.T) {
	cfg := &OnboardResult{EmbeddingProvider: "ollama", LLMProvider: "ollama"}
	open := func(ph wizardPhase) OnboardModel {
		m := NewOnboardModelWithConfig(cfg)
		for i, it := range m.menuItems {
			if it.phase == ph {
				m.menuCursor = i
				return m
			}
		}
		t.Fatalf("menu has no item for phase %d", ph)
		return m
	}
	t.Run("summary saves and quits", func(t *testing.T) {
		m, cmd := step(t, open(phaseSummary), "enter")
		if !m.done || cmd == nil {
			t.Fatal("must finish")
		}
	})
	t.Run("plugins and settings screens hand over", func(t *testing.T) {
		m, _ := step(t, open(phasePlugins), "enter")
		if !m.OpenPlugins() || !m.done {
			t.Fatal("plugins")
		}
		m, _ = step(t, open(phaseSettingsScreen), "enter")
		if !m.OpenSettings() || !m.done {
			t.Fatal("settings")
		}
	})
	t.Run("model entries fetch", func(t *testing.T) {
		for ph, want := range map[wizardPhase]wizardPhase{
			phaseEmbModel:     phaseEmbFetching,
			phaseLLMModel:     phaseLLMFetching,
			phaseCodeEmbModel: phaseEmbFetching,
		} {
			m, cmd := step(t, open(ph), "enter")
			if m.phase != want || cmd == nil {
				t.Errorf("phase %d -> %d (cmd %v), want %d", ph, m.phase, cmd != nil, want)
			}
		}
		// With models already loaded the code/docs lists open at once.
		m := open(phaseDocEmbModel)
		m.embModels = models("bge-m3")
		m.docEmbeddingModel = "bge-m3"
		m, cmd := step(t, m, "enter")
		if m.phase != phaseDocEmbModel || cmd != nil {
			t.Fatalf("phase=%d", m.phase)
		}
	})
	t.Run("reranker model without reranking opens the toggle", func(t *testing.T) {
		m, _ := step(t, open(phaseRerankModel), "enter")
		if m.phase != phaseReranker {
			t.Fatalf("phase=%d", m.phase)
		}
		m = open(phaseRerankModel)
		m.rerankEnabled = true
		m, cmd := step(t, m, "enter")
		if m.phase != phaseRerankFetching || cmd == nil {
			t.Fatalf("phase=%d", m.phase)
		}
	})
	t.Run("host rows depend on the provider", func(t *testing.T) {
		m := open(phaseEmbHost)
		if i := provIdx(m.embProviders, "native"); m.embProviders[i] == "native" {
			m.embProviderIdx = i
			if m2, _ := step(t, m, "enter"); m2.phase != phaseMenu {
				t.Fatal("a native embedder has no host to edit")
			}
		}
		m = open(phaseEmbHost)
		m.embProviderIdx = provIdx(m.embProviders, "openai")
		if m2, _ := step(t, m, "enter"); m2.phase != phaseEmbAPIKey {
			t.Fatalf("openai edits the key, got %d", m2.phase)
		}
		m = open(phaseLLMHost)
		m.llmProviderIdx = provIdx(m.llmProviders, "llamacpp")
		if m2, _ := step(t, m, "enter"); m2.phase != phaseMenu {
			t.Fatal("a local LLM has no host to edit")
		}
		m = open(phaseLLMHost)
		m.llmProviderIdx = provIdx(m.llmProviders, "anthropic")
		if m2, _ := step(t, m, "enter"); m2.phase != phaseLLMAPIKey {
			t.Fatalf("anthropic edits the key, got %d", m2.phase)
		}
	})
	t.Run("cursor stays inside the menu", func(t *testing.T) {
		m := NewOnboardModelWithConfig(cfg)
		m, _ = step(t, m, "up", "k")
		if m.menuCursor != 0 {
			t.Fatal("stops at the top")
		}
		for range m.menuItems {
			m, _ = step(t, m, "down")
		}
		if m.menuCursor != len(m.menuItems)-1 {
			t.Fatal("stops at the bottom")
		}
		m, _ = step(t, m, "k")
		if m.menuCursor != len(m.menuItems)-2 {
			t.Fatal("k moves up")
		}
		if m, cmd := step(t, m, "esc"); !m.Cancelled() || cmd == nil {
			t.Fatal("esc at the menu cancels")
		}
	})
}

func TestOnboardModelsFetchedErrors(t *testing.T) {
	boom := errors.New("connection refused")
	t.Run("embedding by provider", func(t *testing.T) {
		for _, prov := range []string{"ollama", "llamacpp", "eif"} {
			m := NewOnboardModel()
			i := provIdx(m.embProviders, prov)
			if m.embProviders[i] != prov {
				continue // not in this build
			}
			m.embProviderIdx = i
			m = feed(t, m, modelsFetchedMsg{err: boom})
			if m.phase != phaseEmbModel || m.fetchErr == "" || len(m.embModels) == 0 {
				t.Errorf("%s: phase=%d err=%q n=%d", prov, m.phase, m.fetchErr, len(m.embModels))
			}
		}
	})
	t.Run("llm by provider", func(t *testing.T) {
		for _, prov := range []string{"ollama", "llamacpp"} {
			m := NewOnboardModel()
			m.llmProviderIdx = provIdx(m.llmProviders, prov)
			m = feed(t, m, modelsFetchedMsg{err: boom, forLLM: true})
			if m.phase != phaseLLMModel || m.fetchErr == "" || len(m.llmModels) == 0 {
				t.Errorf("%s: phase=%d err=%q", prov, m.phase, m.fetchErr)
			}
		}
	})
	t.Run("reranker falls back to something the user can pick or type", func(t *testing.T) {
		m := NewOnboardModel()
		m = feed(t, m, modelsFetchedMsg{err: boom, forReranker: true})
		if m.phase != phaseRerankModel || !m.rerankEnabled || len(m.rerankModels) == 0 || m.fetchErr == "" {
			t.Fatalf("phase=%d enabled=%v n=%d", m.phase, m.rerankEnabled, len(m.rerankModels))
		}
		m = NewOnboardModel()
		m.embProviderIdx = provIdx(m.embProviders, "llamacpp")
		m = feed(t, m, modelsFetchedMsg{err: boom, forReranker: true})
		if m.phase != phaseRerankModel || !strings.Contains(m.fetchErr, "gguf") && !strings.Contains(m.fetchErr, "built-in") {
			t.Fatalf("phase=%d err=%q", m.phase, m.fetchErr)
		}
	})
}

func TestOnboardModelsFetchedSuccess(t *testing.T) {
	t.Run("embedding keeps the saved model selected", func(t *testing.T) {
		m := NewOnboardModelWithConfig(&OnboardResult{EmbeddingProvider: "ollama", EmbeddingModel: "nomic-embed-text"})
		m.phase = phaseEmbFetching
		m = feed(t, m, modelsFetchedMsg{models: models("bge-m3", "nomic-embed-text")})
		if m.phase != phaseEmbModel || m.embModels[m.embModelIdx].Name != "nomic-embed-text" {
			t.Fatalf("phase=%d sel=%+v", m.phase, m.embModels[m.embModelIdx])
		}
	})
	t.Run("a saved model the server no longer lists is still offered", func(t *testing.T) {
		m := NewOnboardModelWithConfig(&OnboardResult{EmbeddingProvider: "ollama", EmbeddingModel: "gone-model"})
		m = feed(t, m, modelsFetchedMsg{models: models("bge-m3")})
		if m.embModels[0].Name != "gone-model" || m.embModelIdx != 0 {
			t.Fatalf("got %+v", m.embModels)
		}
	})
	t.Run("code and docs lists reopen from the menu", func(t *testing.T) {
		for _, ph := range []wizardPhase{phaseCodeEmbModel, phaseDocEmbModel} {
			m := NewOnboardModelWithConfig(&OnboardResult{EmbeddingProvider: "ollama"})
			m.returnPhase = ph
			m = feed(t, m, modelsFetchedMsg{models: models("bge-m3")})
			if m.phase != ph || m.returnPhase != phaseMenu {
				t.Errorf("phase=%d return=%d", m.phase, m.returnPhase)
			}
		}
	})
	t.Run("local embedding provider adds the catalog", func(t *testing.T) {
		m := NewOnboardModel()
		m.embProviderIdx = provIdx(m.embProviders, "llamacpp")
		m = feed(t, m, modelsFetchedMsg{models: []ModelInfo{{Name: "mine.gguf", Tag: "/x/mine.gguf"}}})
		if !m.embShowAll || m.phase != phaseEmbModel || !hasModel(m.embModels, "mine.gguf") {
			t.Fatalf("phase=%d showAll=%v", m.phase, m.embShowAll)
		}
	})
	t.Run("local llm provider adds the catalog", func(t *testing.T) {
		m := NewOnboardModel()
		m.llmProviderIdx = provIdx(m.llmProviders, "llamacpp")
		m = feed(t, m, modelsFetchedMsg{models: []ModelInfo{{Name: "mine.gguf", Tag: "/x/mine.gguf"}}, forLLM: true})
		if !m.llmShowAll || m.phase != phaseLLMModel || !hasModel(m.llmModels, "mine.gguf") {
			t.Fatalf("phase=%d", m.phase)
		}
	})
	t.Run("rerankers: listed, or none look like one", func(t *testing.T) {
		m := NewOnboardModel()
		m = feed(t, m, modelsFetchedMsg{models: models("bge-reranker-v2-m3", "llama3.2:3b"), forReranker: true})
		if m.phase != phaseRerankModel || m.rerankShowAll {
			t.Fatalf("phase=%d showAll=%v", m.phase, m.rerankShowAll)
		}
		m = NewOnboardModel()
		m = feed(t, m, modelsFetchedMsg{models: models("llama3.2:3b"), forReranker: true})
		if m.phase != phaseRerankModel || !m.rerankShowAll || m.rerankOptionIdx != 0 {
			t.Fatalf("phase=%d showAll=%v", m.phase, m.rerankShowAll)
		}
		m = NewOnboardModel()
		m.embProviderIdx = provIdx(m.embProviders, "llamacpp")
		m = feed(t, m, modelsFetchedMsg{forReranker: true})
		if m.phase != phaseRerankModel || !strings.Contains(m.fetchErr, "gguf") {
			t.Fatalf("phase=%d err=%q", m.phase, m.fetchErr)
		}
	})
}

func TestOnboardCustomHFStepNum(t *testing.T) {
	for what, want := range map[string]int{"emb": 3, "llm": 6, "reranker": 8, "": 8} {
		if got := (OnboardModel{customHFForWhat: what}).customHFStepNum(); got != want {
			t.Errorf("%q: %d, want %d", what, got, want)
		}
	}
}

func TestOnboardModelInputRow(t *testing.T) {
	// Moving down past the last model reaches the free-text row; typing fills
	// it, enter selects the typed name in the list.
	m := NewOnboardModel()
	m.phase = phaseLLMModel
	m.llmModels = models("a", "b")
	m.llmAllModels = m.llmModels
	m, _ = step(t, m, "down") // to b
	m, cmd := step(t, m, "down")
	if !m.onCustom || cmd == nil {
		t.Fatal("down past the last model focuses the free-text row")
	}
	m, _ = step(t, m, "enter") // empty: nothing to use yet
	if !m.onCustom {
		t.Fatal("an empty name stays on the row")
	}
	m, _ = step(t, m, "x", "y")
	if m.modelInput.Value() != "xy" {
		t.Fatalf("typed = %q", m.modelInput.Value())
	}
	m, _ = step(t, m, "up")
	if m.onCustom {
		t.Fatal("up returns to the list")
	}
	m, _ = step(t, m, "down", "enter")
	if m.llmModels[m.llmModelIdx].Name != "xy" {
		t.Fatalf("enter must select the typed model, got %+v", m.llmModels[m.llmModelIdx])
	}
	if m.phase != phaseReranker {
		t.Fatalf("and confirm it: phase=%d", m.phase)
	}
}

func TestOnboardUseTypedModelPerPhase(t *testing.T) {
	m := NewOnboardModel()
	m.embModels = models("a")
	m.embAllModels = m.embModels
	for _, ph := range []wizardPhase{phaseEmbModel, phaseLLMModel, phaseRerankModel, phaseCodeEmbModel, phaseDocEmbModel} {
		mm := m
		mm.llmModels, mm.llmAllModels = models("l"), models("l")
		mm.rerankModels, mm.rerankAllModels = models("r"), models("r")
		mm.phase = ph
		mm.useTypedModel("custom-name")
		switch ph {
		case phaseEmbModel:
			if mm.embModels[mm.embModelIdx].Name != "custom-name" {
				t.Error("emb")
			}
		case phaseLLMModel:
			if mm.llmModels[mm.llmModelIdx].Name != "custom-name" {
				t.Error("llm")
			}
		case phaseRerankModel:
			if mm.rerankModels[mm.rerankModelIdx].Name != "custom-name" {
				t.Error("rerank")
			}
		case phaseCodeEmbModel:
			if mm.codeEmbeddingModel != "custom-name" {
				t.Error("code")
			}
		case phaseDocEmbModel:
			if mm.docEmbeddingModel != "custom-name" {
				t.Error("doc")
			}
		}
	}
}
