package tui

import (
	"strings"
	"testing"
)

// ── OnboardModel Creation ──────────────────────────────────────

func TestNewOnboardModelDefaults(t *testing.T) {
	m := NewOnboardModel()
	if m.phase != phaseQuickOrAdv {
		t.Fatalf("phase = %d, want %d", m.phase, phaseQuickOrAdv)
	}
	if len(m.embProviders) < 2 {
		t.Fatal("expected at least 2 embedding providers")
	}
	if len(m.llmProviders) < 3 {
		t.Fatal("expected at least 3 LLM providers")
	}
	if len(m.rerankOptions) != 2 {
		t.Fatalf("expected 2 rerank options, got %d", len(m.rerankOptions))
	}
	if len(m.installOptions) == 0 {
		t.Fatal("expected non-empty install options")
	}
	if m.cancelled {
		t.Fatal("should not be cancelled initially")
	}
	if m.done {
		t.Fatal("should not be done initially")
	}
}

func TestNewOnboardModelWithConfigPrePopulates(t *testing.T) {
	cfg := &OnboardResult{
		EmbeddingProvider: "openai",
		LLMProvider:       "anthropic",
		OllamaHost:        "http://custom:11434",
		OpenAIKey:         "sk-test",
		IndexDir:          "/tmp/test-idx",
		RerankEnabled:     true,
		MCPEnabled:        true,
		ServerEnabled:     true,
		ServerAddr:        "0.0.0.0:9999",
	}
	m := NewOnboardModelWithConfig(cfg)
	if !m.menuMode {
		t.Fatal("should be in menu mode")
	}
	if m.phase != phaseMenu {
		t.Fatalf("phase = %d, want %d", m.phase, phaseMenu)
	}
	if m.embProviders[m.embProviderIdx] != "openai" {
		t.Error("emb provider not pre-selected")
	}
	if m.llmProviders[m.llmProviderIdx] != "anthropic" {
		t.Error("llm provider not pre-selected")
	}
	if m.embHostInput.Value() != "http://custom:11434" {
		t.Error("emb host not pre-populated")
	}
	if m.indexDirInput.Value() != "/tmp/test-idx" {
		t.Error("index dir not pre-populated")
	}
	if !m.rerankEnabled {
		t.Error("rerank should be enabled")
	}
	if !m.mcpEnabled {
		t.Error("MCP should be enabled")
	}
	if !m.serverEnabled {
		t.Error("server should be enabled")
	}
}

// ── View tests for every phase ─────────────────────────────────

func TestViewCancelled(t *testing.T) {
	m := NewOnboardModel()
	m.cancelled = true
	v := m.View()
	if !strings.Contains(v.Content, "cancelled") {
		t.Error("cancelled view should contain 'cancelled'")
	}
}

func TestViewQuickOrAdvPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	v := m.View()
	if !strings.Contains(v.Content, "Quick Setup") {
		t.Error("should show Quick Setup option")
	}
	if !strings.Contains(v.Content, "Advanced Setup") {
		t.Error("should show Advanced Setup option")
	}
}

func TestViewEmbProviderPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseEmbProvider
	v := m.View()
	if !strings.Contains(v.Content, "Embedding Provider") {
		t.Error("should show Embedding Provider title")
	}
	if !strings.Contains(v.Content, "ollama") {
		t.Error("should list ollama")
	}
}

func TestViewEmbHostPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseEmbHost
	v := m.View()
	if !strings.Contains(v.Content, "Ollama URL") && !strings.Contains(v.Content, "Embed Model") {
		t.Error("should show host input")
	}
}

func TestViewEmbAPIKeyPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseEmbAPIKey
	v := m.View()
	if !strings.Contains(v.Content, "API Key") {
		t.Error("should show API Key input")
	}
}

func TestViewEmbFetchingPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseEmbFetching
	v := m.View()
	if !strings.Contains(v.Content, "Embedding") {
		t.Error("should show fetching status")
	}
}

func TestViewEmbModelPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseEmbModel
	m.embModels = []ModelInfo{{Name: "bge-m3"}, {Name: "nomic"}}
	v := m.View()
	if !strings.Contains(v.Content, "Embedding Model") {
		t.Error("should show Embedding Model title")
	}
}

func TestViewLLMProviderPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseLLMProvider
	v := m.View()
	if !strings.Contains(v.Content, "LLM Provider") {
		t.Error("should show LLM Provider title")
	}
}

func TestViewLLMHostPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseLLMHost
	v := m.View()
	if !strings.Contains(v.Content, "URL") || !strings.Contains(v.Content, "LLM") {
		// Also accepts "LLM Model Search Path" for llamacpp
		if !strings.Contains(v.Content, "URL") && !strings.Contains(v.Content, "Search Path") {
			t.Error("should show host/URL input")
		}
	}
}

func TestViewLLMAPIKeyPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseLLMAPIKey
	v := m.View()
	if !strings.Contains(v.Content, "API Key") {
		t.Error("should show API key input")
	}
}

func TestViewLLMFetchingPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseLLMFetching
	v := m.View()
	if !strings.Contains(v.Content, "LLM") {
		t.Error("should show LLM fetching")
	}
}

func TestViewLLMModelPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseLLMModel
	m.llmModels = []ModelInfo{{Name: "llama3.2"}}
	v := m.View()
	if !strings.Contains(v.Content, "LLM Model") {
		t.Error("should show LLM Model title")
	}
}

func TestViewRerankerPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseReranker
	v := m.View()
	if !strings.Contains(v.Content, "Reranker") {
		t.Error("should show Reranker title")
	}
}

func TestViewRerankFetchingPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseRerankFetching
	v := m.View()
	if !strings.Contains(v.Content, "Reranker") {
		t.Error("should show reranker fetching")
	}
}

func TestViewRerankModelPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseRerankModel
	m.rerankModels = []ModelInfo{{Name: "bge-reranker-v2-m3"}}
	v := m.View()
	if !strings.Contains(v.Content, "Reranker Model") {
		t.Error("should show Reranker Model title")
	}
}

func TestViewIndexDirPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseIndexDir
	v := m.View()
	if !strings.Contains(v.Content, "Index Directory") {
		t.Error("should show Index Directory title")
	}
}

func TestViewMCPPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseMCP
	v := m.View()
	if !strings.Contains(v.Content, "MCP") {
		t.Error("should show MCP title")
	}
}

func TestViewServerPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseServer
	v := m.View()
	if !strings.Contains(v.Content, "REST API") {
		t.Error("should show REST API title")
	}
}

func TestViewSummaryPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseSummary
	v := m.View()
	// Summary shows a summary of the configuration.
	if v.Content == "" {
		t.Error("should have content")
	}
}

func TestViewInstallPhase(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseInstall
	v := m.View()
	if !strings.Contains(v.Content, "Install") {
		t.Error("should show Install title")
	}
}

func TestViewMenuPhase(t *testing.T) {
	cfg := &OnboardResult{
		EmbeddingProvider: "ollama",
		LLMProvider:       "ollama",
	}
	m := NewOnboardModelWithConfig(cfg)
	m.width = 80
	m.height = 24
	v := m.View()
	if !strings.Contains(v.Content, "Settings") {
		t.Error("should show Settings title")
	}
}

// ── visibleStep ────────────────────────────────────────────────

func TestVisibleStepExt3(t *testing.T) {
	tests := []struct {
		phase wizardPhase
		want  int
	}{
		{phaseEmbProvider, 1},
		{phaseEmbHost, 2},
		{phaseEmbAPIKey, 2},
		{phaseEmbFetching, 2},
		{phaseEmbModel, 3},
		{phaseLLMProvider, 4},
		{phaseLLMHost, 5},
		{phaseLLMAPIKey, 5},
		{phaseLLMFetching, 5},
		{phaseLLMModel, 6},
		{phaseReranker, 7},
		{phaseRerankFetching, 7},
		{phaseRerankModel, 8},
		{phaseIndexDir, 9},
		{phaseMCP, 10},
		{phaseServer, 11},
		{phaseSummary, 12},
		{phaseInstall, 13},
	}
	for _, tt := range tests {
		m := NewOnboardModel()
		m.phase = tt.phase
		got := m.visibleStep()
		if got != tt.want {
			t.Errorf("visibleStep(%d) = %d, want %d", tt.phase, got, tt.want)
		}
	}
}

// ── settingsMenuValues covers lots of rendering logic ──────────

func TestSettingsMenuValuesExt3(t *testing.T) {
	m := NewOnboardModel()
	// Set up some state for coverage.
	m.embModels = []ModelInfo{{Name: "bge-m3"}}
	m.llmModels = []ModelInfo{{Name: "llama3.2"}}
	m.rerankEnabled = true
	m.rerankModels = []ModelInfo{{Name: "bge-reranker"}}
	m.mcpEnabled = true
	m.serverEnabled = true

	vals := m.settingsMenuValues()
	if len(vals) < 10 {
		t.Fatalf("expected at least 10 values, got %d", len(vals))
	}
	// Embedding provider should be "ollama" (default index 0).
	if vals[0] != "ollama" {
		t.Errorf("first value = %q, want 'ollama'", vals[0])
	}
}

func TestSettingsMenuValuesOpenAI(t *testing.T) {
	m := NewOnboardModel()
	m.embProviderIdx = 1 // openai
	m.embKeyInput.SetValue("sk-1234567890abcdef")
	vals := m.settingsMenuValues()
	// Should mask the key.
	if !strings.Contains(vals[1], "...") && !strings.Contains(vals[1], "****") {
		t.Errorf("expected masked key, got %q", vals[1])
	}
}

func TestSettingsMenuValuesLlamaCPP(t *testing.T) {
	m := NewOnboardModel()
	m.embProviderIdx = 2 // llamacpp
	vals := m.settingsMenuValues()
	if !strings.Contains(vals[1], "auto-scan") {
		t.Errorf("expected auto-scan for llamacpp, got %q", vals[1])
	}
}

func TestSettingsMenuValuesWithExistingCfg(t *testing.T) {
	cfg := &OnboardResult{
		EmbeddingModel: "custom-model",
		LLMModel:       "custom-llm",
		RerankModel:    "custom-reranker",
	}
	m := NewOnboardModelWithConfig(cfg)
	m.rerankEnabled = true
	vals := m.settingsMenuValues()
	// Check model names propagated from existing config.
	// Embedding model appears in position 2.
	if vals[2] != "custom-model" {
		t.Errorf("expected 'custom-model', got %q", vals[2])
	}
}

// ── buildResult ────────────────────────────────────────────────

func TestBuildResultDefaults(t *testing.T) {
	m := NewOnboardModel()
	m.buildResult()
	res := m.Result()
	if res.EmbeddingProvider != "ollama" {
		t.Errorf("provider = %q, want 'ollama'", res.EmbeddingProvider)
	}
	if !res.Completed {
		t.Error("should be completed")
	}
}

func TestBuildResultWithModels(t *testing.T) {
	m := NewOnboardModel()
	m.embModels = []ModelInfo{{Name: "bge-m3"}}
	m.llmModels = []ModelInfo{{Name: "llama3.2"}}
	m.buildResult()
	res := m.Result()
	if res.EmbeddingModel != "bge-m3" {
		t.Errorf("emb model = %q", res.EmbeddingModel)
	}
	if res.LLMModel != "llama3.2" {
		t.Errorf("llm model = %q", res.LLMModel)
	}
}

func TestBuildResultLlamaCPPExt3(t *testing.T) {
	m := NewOnboardModel()
	m.embProviderIdx = 2 // llamacpp
	m.llmProviderIdx = 3 // llamacpp
	m.embModels = []ModelInfo{{Name: "model.gguf", Tag: "/path/to/model.gguf"}}
	m.llmModels = []ModelInfo{{Name: "llm.gguf", Tag: "/path/to/llm.gguf"}}
	m.buildResult()
	res := m.Result()
	if res.EmbeddingModel != "/path/to/model.gguf" {
		t.Errorf("emb model = %q, want tag path", res.EmbeddingModel)
	}
	if res.OllamaHost != "" {
		t.Errorf("OllamaHost should be empty for llamacpp, got %q", res.OllamaHost)
	}
}

func TestBuildResultWithExistingConfigExt3(t *testing.T) {
	cfg := &OnboardResult{
		EmbeddingModel: "existing-emb",
		LLMModel:       "existing-llm",
		SystemPrompt:   "You are helpful.",
		Temperature:    0.7,
		MaxTokens:      2048,
		TopK:           10,
		AnthropicKey:   "ant-key",
	}
	m := NewOnboardModelWithConfig(cfg)
	m.buildResult()
	res := m.Result()
	// Should preserve chat settings from existing config.
	if res.SystemPrompt != "You are helpful." {
		t.Errorf("system prompt = %q", res.SystemPrompt)
	}
	if res.Temperature != 0.7 {
		t.Errorf("temperature = %f", res.Temperature)
	}
}

func TestBuildResultReranker(t *testing.T) {
	m := NewOnboardModel()
	m.rerankEnabled = true
	m.rerankModels = []ModelInfo{{Name: "bge-reranker"}}
	m.buildResult()
	res := m.Result()
	if !res.RerankEnabled {
		t.Error("reranker should be enabled")
	}
	if res.RerankModel != "bge-reranker" {
		t.Errorf("rerank model = %q", res.RerankModel)
	}
}

func TestBuildResultInstallOptions(t *testing.T) {
	m := NewOnboardModel()
	// Test uninstall option (index 3 on unix = remove binary).
	m.installOptionIdx = 3
	m.buildResult()
	res := m.Result()
	if !res.Uninstall {
		t.Error("should be marked for uninstall")
	}
}

func TestBuildResultUninstallData(t *testing.T) {
	m := NewOnboardModel()
	m.installOptionIdx = 4
	m.buildResult()
	res := m.Result()
	if !res.UninstallData {
		t.Error("should be marked for uninstall + data")
	}
}

// ── Cancelled / OpenPlugins ────────────────────────────────────

func TestCancelledAccessor(t *testing.T) {
	m := NewOnboardModel()
	if m.Cancelled() {
		t.Error("should not be cancelled")
	}
	m.cancelled = true
	if !m.Cancelled() {
		t.Error("should be cancelled")
	}
}

func TestOpenPluginsAccessor(t *testing.T) {
	m := NewOnboardModel()
	if m.OpenPlugins() {
		t.Error("should not open plugins")
	}
	m.openPlugins = true
	if !m.OpenPlugins() {
		t.Error("should open plugins")
	}
}

// ── renderSettingsMenu ─────────────────────────────────────────

func TestRenderSettingsMenu(t *testing.T) {
	cfg := &OnboardResult{
		EmbeddingProvider: "ollama",
		LLMProvider:       "ollama",
	}
	m := NewOnboardModelWithConfig(cfg)
	m.width = 80
	out := m.renderSettingsMenu()
	if !strings.Contains(out, "Embedding Provider") {
		t.Error("should show Embedding Provider")
	}
	if !strings.Contains(out, "Save & Exit") {
		t.Error("should show Save & Exit")
	}
}

// ── focusActiveInput ───────────────────────────────────────────

func TestFocusActiveInputExt3(t *testing.T) {
	phases := []wizardPhase{
		phaseEmbHost,
		phaseEmbAPIKey,
		phaseLLMHost,
		phaseLLMAPIKey,
		phaseIndexDir,
	}
	for _, p := range phases {
		m := NewOnboardModel()
		m.phase = p
		m.focusActiveInput()
		// Just verify no panic.
	}
}

// ── renderSelect and renderInput helpers ───────────────────────

func TestRenderSelectBasicExt3(t *testing.T) {
	m := NewOnboardModel()
	out := m.renderSelect("1", "Test Title", "Test Desc",
		[]string{"opt1", "opt2"}, 0, []string{"desc1", "desc2"})
	if !strings.Contains(out, "Test Title") {
		t.Error("should contain title")
	}
	if !strings.Contains(out, "opt1") {
		t.Error("should contain option")
	}
}

func TestRenderInputBasicExt3(t *testing.T) {
	m := NewOnboardModel()
	out := m.renderInput("2", "Test Input", "Enter value", &m.embHostInput)
	if !strings.Contains(out, "Test Input") {
		t.Error("should contain title")
	}
}

// ── Progress bar in View ───────────────────────────────────────

func TestViewShowsProgressBar(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseEmbProvider
	v := m.View()
	// Progress bar shows "1/13".
	if !strings.Contains(v.Content, "1/13") {
		t.Error("should show step 1/13 in progress bar")
	}
}

func TestViewProgressBarSummaryFull(t *testing.T) {
	m := NewOnboardModel()
	m.width = 80
	m.height = 24
	m.phase = phaseSummary
	v := m.View()
	if !strings.Contains(v.Content, "12/13") {
		t.Error("should show step 12/13")
	}
}

// ── renderModelSelect ──────────────────────────────────────────

func TestRenderModelSelectWithModelsExt3(t *testing.T) {
	m := NewOnboardModel()
	m.embModels = []ModelInfo{
		{Name: "bge-m3", Size: "1.0 GB"},
		{Name: "nomic-embed-text", Size: "2.0 GB"},
	}
	out := m.renderModelSelect("3", "Embedding Model", m.embModels, 0, false, true)
	if !strings.Contains(out, "Embedding Model") {
		t.Error("should contain title")
	}
	if !strings.Contains(out, "bge-m3") {
		t.Error("should list bge-m3")
	}
}

// ── renderFetching ─────────────────────────────────────────────

func TestRenderFetchingExt3(t *testing.T) {
	m := NewOnboardModel()
	out := m.renderFetching("Embedding")
	if !strings.Contains(out, "Embedding") {
		t.Error("should mention Embedding")
	}
}

func TestRenderFetchingWithErrorExt3(t *testing.T) {
	m := NewOnboardModel()
	m.fetchErr = "connection refused"
	out := m.renderFetching("LLM")
	if !strings.Contains(out, "connection refused") {
		t.Error("should show error message")
	}
}
