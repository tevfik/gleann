package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/pkg/gleann"
)

// h2aIdx returns the index of name in list, failing the test when it is absent
// (the provider lists depend on the build tags).
func h2aIdx(t *testing.T, list []string, name string) int {
	t.Helper()
	for i, s := range list {
		if s == name {
			return i
		}
	}
	t.Fatalf("%q not in %v", name, list)
	return -1
}

// h2aWizard returns a wizard model with the given providers selected.
func h2aWizard(t *testing.T, emb, llm string) OnboardModel {
	t.Helper()
	m := NewOnboardModel()
	m.embProviderIdx = h2aIdx(t, m.embProviders, emb)
	m.llmProviderIdx = h2aIdx(t, m.llmProviders, llm)
	return m
}

// h2aMenu returns a settings-menu model for cfg.
func h2aMenu(t *testing.T, cfg OnboardResult) OnboardModel {
	t.Helper()
	return NewOnboardModelWithConfig(&cfg)
}

// h2aFetch runs a fetch command and returns its message.
func h2aFetch(t *testing.T, cmd tea.Cmd) modelsFetchedMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a fetch command")
	}
	msg, ok := cmd().(modelsFetchedMsg)
	if !ok {
		t.Fatalf("fetch command returned %T", msg)
	}
	return msg
}

// h2aModelDir is a directory with one .gguf model file.
func h2aModelDir(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// An Ollama host typed as host:port gets a scheme, and the other Ollama step
// follows it.
func TestOnboardH2AHostWithPortGetsScheme(t *testing.T) {
	m := h2aWizard(t, "ollama", "ollama")
	m.phase = phaseEmbHost
	m.embHostInput.SetValue("gpu-box:11434")
	m, _ = step(t, m, "enter")
	if got := m.embHostInput.Value(); got != "http://gpu-box:11434" {
		t.Errorf("emb host = %q", got)
	}
	if got := m.llmHostInput.Value(); got != "http://gpu-box:11434" {
		t.Errorf("llm host not synced: %q", got)
	}
	if m.phase != phaseEmbOllamaKey {
		t.Errorf("phase = %d, want the Ollama key step", m.phase)
	}

	m = h2aWizard(t, "ollama", "ollama")
	m.phase = phaseLLMHost
	m.llmHostInput.SetValue("other:11500")
	m, _ = step(t, m, "enter")
	if got := m.llmHostInput.Value(); got != "http://other:11500" {
		t.Errorf("llm host = %q", got)
	}
	if got := m.embHostInput.Value(); got != "http://other:11500" {
		t.Errorf("emb host not synced: %q", got)
	}
	if m.phase != phaseLLMOllamaKey {
		t.Errorf("phase = %d, want the LLM Ollama key step", m.phase)
	}
}

// For llama.cpp the host field is a model directory: enter scans it.
func TestOnboardH2ALocalSearchPathScansDirectory(t *testing.T) {
	dir := h2aModelDir(t, "tiny-embed.gguf")

	m := h2aWizard(t, "llamacpp", "ollama")
	m.phase = phaseEmbHost
	m.embHostInput.SetValue(dir)
	m, cmd := step(t, m, "enter")
	if m.phase != phaseEmbFetching {
		t.Fatalf("phase = %d, want fetching", m.phase)
	}
	msg := h2aFetch(t, cmd)
	if msg.err != nil || len(msg.models) != 1 || msg.models[0].Name != "tiny-embed.gguf" {
		t.Fatalf("scan of %s: %+v %v", dir, msg.models, msg.err)
	}
	m = feed(t, m, msg)
	if m.phase != phaseEmbModel || !m.embShowAll {
		t.Errorf("phase = %d showAll = %v", m.phase, m.embShowAll)
	}
	if m.embModels[0].Tag != filepath.Join(dir, "tiny-embed.gguf") {
		t.Errorf("first model = %+v", m.embModels[0])
	}

	llmDir := h2aModelDir(t, "tiny-chat.gguf")
	m = h2aWizard(t, "ollama", "llamacpp")
	m.phase = phaseLLMHost
	m.llmHostInput.SetValue(llmDir)
	m, cmd = step(t, m, "enter")
	if m.phase != phaseLLMFetching {
		t.Fatalf("phase = %d, want LLM fetching", m.phase)
	}
	msg = h2aFetch(t, cmd)
	if msg.err != nil || !msg.forLLM || len(msg.models) != 1 {
		t.Fatalf("LLM scan: %+v %v", msg, msg.err)
	}

	// In the settings menu, enter goes back to the menu without fetching.
	m = h2aMenu(t, OnboardResult{EmbeddingProvider: "llamacpp", LLMProvider: "llamacpp"})
	m.phase = phaseEmbHost
	m, cmd = step(t, m, "enter")
	if m.phase != phaseMenu || cmd != nil {
		t.Errorf("menu emb host: phase %d cmd %v", m.phase, cmd != nil)
	}
	m.phase = phaseLLMHost
	m, cmd = step(t, m, "enter")
	if m.phase != phaseMenu || cmd != nil {
		t.Errorf("menu llm host: phase %d cmd %v", m.phase, cmd != nil)
	}
}

// The reranker fetch uses the embedding provider; for llama.cpp it scans the
// embedding search path, and a list without a reranker offers the catalog.
func TestOnboardH2ARerankFetchLocal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GLEANN_CONFIG_DIR", "")
	dir := h2aModelDir(t, "bge-reranker-v2-m3-Q4.gguf")
	m := h2aWizard(t, "llamacpp", "ollama")
	m.phase = phaseReranker
	m.embHostInput.SetValue(dir)
	m, cmd := step(t, m, "down", "enter")
	if m.phase != phaseRerankFetching || !m.rerankEnabled {
		t.Fatalf("phase = %d enabled = %v", m.phase, m.rerankEnabled)
	}
	msg := h2aFetch(t, cmd)
	if !msg.forReranker || msg.err != nil {
		t.Fatalf("rerank fetch: %+v", msg)
	}
	m = feed(t, m, msg)
	if m.phase != phaseRerankModel || len(m.rerankModels) == 0 || m.rerankModels[0].Name != "bge-reranker-v2-m3-Q4.gguf" {
		t.Errorf("rerank models: %+v", m.rerankModels)
	}

	// No reranker among the local files: the catalog's rerankers are offered.
	m = h2aWizard(t, "llamacpp", "ollama")
	m.phase = phaseRerankFetching
	m.rerankEnabled = true
	m = feed(t, m, modelsFetchedMsg{models: models("plain-embed.gguf"), forReranker: true})
	if m.phase != phaseRerankModel || !strings.Contains(m.fetchErr, ".gguf") {
		t.Fatalf("phase %d fetchErr %q", m.phase, m.fetchErr)
	}
	if want := len(catalogOptions(modelcatalog.Reranker, false, nil)); len(m.rerankModels) != want || want == 0 {
		t.Errorf("%d reranker options, want %d", len(m.rerankModels), want)
	}
}

// Custom HF repository input: an empty value is a placeholder, and the next
// step depends on what was being chosen and on the menu mode.
func TestOnboardH2ACustomHFInput(t *testing.T) {
	cases := []struct {
		what      string
		menu      bool
		wantPhase wizardPhase
	}{
		{"emb", false, phaseLLMProvider},
		{"emb", true, phaseMenu},
		{"llm", false, phaseReranker},
		{"llm", true, phaseMenu},
		{"reranker", false, phaseIndexDir},
		{"reranker", true, phaseMenu},
	}
	for _, c := range cases {
		m := h2aWizard(t, "llamacpp", "llamacpp")
		m.menuMode = c.menu
		m.embModels = models("a", "b")
		m.llmModels = models("c")
		m.rerankModels = models("d")
		m.embModelIdx = 1
		m.customHFForWhat = c.what
		m.phase = phaseCustomHFInput
		m.customHFInput.Focus() // as the model step does when it opens this one
		if c.what == "emb" {
			m = typeText(t, m, "org/repo-GGUF")
		}
		m, _ = step(t, m, "enter")
		if m.phase != c.wantPhase {
			t.Errorf("%s menu=%v: phase %d, want %d", c.what, c.menu, m.phase, c.wantPhase)
		}
		var got ModelInfo
		switch c.what {
		case "emb":
			got = m.embModels[1]
			if got.Name != "org/repo-GGUF" || got.Tag != "org/repo-GGUF" {
				t.Errorf("typed repo not stored: %+v", got)
			}
		case "llm":
			got = m.llmModels[0]
		case "reranker":
			got = m.rerankModels[0]
		}
		if c.what != "emb" && got.Name != "none/none" {
			t.Errorf("%s: empty repo stored as %+v", c.what, got)
		}
	}
}

// Summary in the settings menu saves and quits; in the wizard it moves on.
func TestOnboardH2ASummaryEnter(t *testing.T) {
	m := h2aMenu(t, OnboardResult{EmbeddingProvider: "ollama", LLMProvider: "ollama", LLMModel: "phi"})
	m.phase = phaseSummary
	m, cmd := step(t, m, "enter")
	if !m.done || cmd == nil || m.result.LLMModel != "phi" {
		t.Errorf("menu summary: done=%v quit=%v result=%+v", m.done, cmd != nil, m.result.LLMModel)
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("summary enter must quit")
	}
}

// Menu entries pick the step that fits the provider.
func TestOnboardH2AMenuRoutes(t *testing.T) {
	menuAt := func(m OnboardModel, p wizardPhase) OnboardModel {
		for i, it := range m.menuItems {
			if it.phase == p {
				m.menuCursor = i
				return m
			}
		}
		t.Fatalf("no menu item for phase %d", p)
		return m
	}

	m := h2aMenu(t, OnboardResult{EmbeddingProvider: "openai", LLMProvider: "anthropic"})
	m, _ = step(t, menuAt(m, phaseEmbHost), "enter")
	if m.phase != phaseEmbAPIKey {
		t.Errorf("openai host/key → phase %d, want API key", m.phase)
	}
	m.phase = phaseMenu
	m, _ = step(t, menuAt(m, phaseLLMHost), "enter")
	if m.phase != phaseLLMAPIKey {
		t.Errorf("anthropic host/key → phase %d, want API key", m.phase)
	}

	// The LLM host of a local provider is not editable from the menu.
	m = h2aMenu(t, OnboardResult{EmbeddingProvider: "ollama", LLMProvider: "llamacpp"})
	m, cmd := step(t, menuAt(m, phaseLLMHost), "enter")
	if m.phase != phaseMenu || cmd != nil {
		t.Errorf("llamacpp LLM host: phase %d", m.phase)
	}

	// Code/doc models with a known list go straight to the list, with the
	// saved model selected.
	m = h2aMenu(t, OnboardResult{EmbeddingProvider: "ollama", DocEmbeddingModel: "doc-embed"})
	m.embModels = models("bge-m3", "doc-embed")
	m, _ = step(t, menuAt(m, phaseDocEmbModel), "enter")
	if m.phase != phaseDocEmbModel || m.docModelOptions()[m.docEmbModelIdx].Name != "doc-embed" {
		t.Errorf("doc model: phase %d idx %d", m.phase, m.docEmbModelIdx)
	}

	// Without a list, the doc model step fetches first and returns to it.
	m = h2aMenu(t, OnboardResult{EmbeddingProvider: "llamacpp", DocEmbeddingModel: "d.gguf"})
	m.embHostInput.SetValue(h2aModelDir(t, "d.gguf"))
	m, cmd = step(t, menuAt(m, phaseDocEmbModel), "enter")
	if m.phase != phaseEmbFetching || m.returnPhase != phaseDocEmbModel {
		t.Fatalf("phase %d return %d", m.phase, m.returnPhase)
	}
	m = feed(t, m, h2aFetch(t, cmd))
	if m.phase != phaseDocEmbModel || m.returnPhase != phaseMenu {
		t.Errorf("after fetch: phase %d return %d", m.phase, m.returnPhase)
	}
	if opt := m.docModelOptions()[m.docEmbModelIdx]; opt.Name != "d.gguf" {
		t.Errorf("saved doc model not selected: %+v", opt)
	}
}

// With an embedding provider that is not Ollama, an Ollama LLM starts from
// the default host.
func TestOnboardH2ALLMProviderOllamaDefaultsHost(t *testing.T) {
	m := h2aWizard(t, "openai", "ollama")
	m.phase = phaseLLMProvider
	m.embHostInput.SetValue("https://api.openai.com")
	m.llmHostInput.SetValue("")
	m, _ = step(t, m, "enter")
	if m.phase != phaseLLMHost || m.llmHostInput.Value() != gleann.DefaultOllamaHost {
		t.Errorf("phase %d host %q", m.phase, m.llmHostInput.Value())
	}

	m = h2aWizard(t, "openai", "ollama")
	m.phase = phaseEmbProvider
	m.embProviderIdx = h2aIdx(t, m.embProviders, "ollama")
	m.embHostInput.SetValue("")
	m, _ = step(t, m, "enter")
	if m.phase != phaseEmbHost || m.embHostInput.Value() != gleann.DefaultOllamaHost {
		t.Errorf("emb provider: phase %d host %q", m.phase, m.embHostInput.Value())
	}
}

// Regression: with an OpenAI embedder (whose host field holds the OpenAI URL)
// and an Ollama LLM on the default host, the saved Ollama host must not be
// the OpenAI URL.
func TestOnboardH2AOpenAIEmbedderDoesNotBecomeOllamaHost(t *testing.T) {
	m := h2aWizard(t, "openai", "ollama")
	m.phase = phaseEmbAPIKey
	m.embKeyInput.SetValue("sk-1234567890")
	m, _ = step(t, m, "enter") // sets the OpenAI base URL as the emb host
	if m.embHostInput.Value() != "https://api.openai.com" {
		t.Fatalf("emb host = %q", m.embHostInput.Value())
	}
	m.llmHostInput.SetValue(gleann.DefaultOllamaHost)
	m.buildResult()
	if m.result.OllamaHost != gleann.DefaultOllamaHost {
		t.Errorf("OllamaHost = %q, want %q", m.result.OllamaHost, gleann.DefaultOllamaHost)
	}
	if m.result.OpenAIKey != "sk-1234567890" || m.result.OpenAIBaseURL == "" {
		t.Errorf("OpenAI settings lost: %+v", m.result)
	}
}

// buildResult: which Ollama host is saved for each provider combination.
func TestOnboardH2ABuildResultOllamaHost(t *testing.T) {
	def := gleann.DefaultOllamaHost
	cases := []struct {
		name             string
		emb, llm         string
		embHost, llmHost string
		existing         *OnboardResult
		want             string
	}{
		{"both ollama, nothing typed", "ollama", "ollama", "", "", nil, ""},
		{"both ollama, default llm host, emb host typed", "ollama", "ollama", "box:1", def, nil, "http://box:1"},
		{"both ollama, bare port", "ollama", "ollama", "", "11500", nil, "http://localhost:11500"},
		{"llm ollama, saved host", "openai", "ollama", "", def, &OnboardResult{OllamaHost: "http://saved:1"}, "http://saved:1"},
		{"llm ollama, llamacpp embedder", "llamacpp", "ollama", "", def, nil, ""},
		{"llm ollama, nothing", "openai", "ollama", "", "", nil, def},
		{"emb ollama, empty host, saved", "ollama", "openai", "", "", &OnboardResult{OllamaHost: "http://saved:2"}, "http://saved:2"},
		{"emb ollama, empty host", "ollama", "anthropic", "", "", nil, def},
		{"neither, custom llm host dropped", "openai", "anthropic", "", "http://x:1", nil, ""},
		{"neither, custom emb host dropped", "openai", "anthropic", "http://y:1", "", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := h2aWizard(t, c.emb, c.llm)
			m.existingCfg = c.existing
			m.embHostInput.SetValue(c.embHost)
			m.llmHostInput.SetValue(c.llmHost)
			m.buildResult()
			if m.result.OllamaHost != c.want {
				t.Errorf("OllamaHost = %q, want %q", m.result.OllamaHost, c.want)
			}
		})
	}
}

// buildResult: local providers save the file (Tag), or the name when there is
// no file; a saved reranker is kept while reranking stays on.
func TestOnboardH2ABuildResultLocalModels(t *testing.T) {
	m := h2aWizard(t, "llamacpp", "llamacpp")
	m.embModels = []ModelInfo{{Name: "named-embed"}}
	m.llmModels = []ModelInfo{{Name: "named-llm"}}
	m.rerankEnabled = true
	m.existingCfg = &OnboardResult{RerankModel: "saved-reranker.gguf"}
	m.buildResult()
	r := m.result
	if r.EmbeddingModel != "named-embed" || r.LLMModel != "named-llm" {
		t.Errorf("models: %q %q", r.EmbeddingModel, r.LLMModel)
	}
	if r.RerankModel != "saved-reranker.gguf" {
		t.Errorf("RerankModel = %q", r.RerankModel)
	}
	if r.OllamaHost != "" || r.OpenAIKey != "" || r.AnthropicKey != "" {
		t.Errorf("unused provider settings kept: %+v", r)
	}
}

// A non-key message such as a paste goes to the focused input.
func TestOnboardH2APasteGoesToActiveInput(t *testing.T) {
	m := h2aWizard(t, "ollama", "ollama")
	m.phase = phaseIndexDir
	m.indexDirInput.SetValue("")
	m.focusActiveInput()
	m = feed(t, m, tea.PasteMsg{Content: "/data/idx"})
	if got := m.indexDirInput.Value(); got != "/data/idx" {
		t.Errorf("index dir = %q", got)
	}
}

// Each wizard step renders its own screen.
func TestOnboardH2AViewPerPhase(t *testing.T) {
	cases := []struct {
		phase wizardPhase
		setup func(*OnboardModel)
		want  string
	}{
		{phaseEmbHost, func(m *OnboardModel) { m.embProviderIdx = 2 }, "Embed Model Search Path"},
		{phaseCodeEmbModel, func(m *OnboardModel) { m.embModels = models("bge-m3") }, "Code Embedding Model"},
		{phaseDocEmbModel, func(m *OnboardModel) { m.embModels = models("bge-m3") }, "Docs Embedding Model"},
		{phaseLLMHost, func(m *OnboardModel) { m.llmProviderIdx = 3 }, "LLM Model Search Path"},
		{phaseBackend, nil, "Vector Backend"},
		{phaseA2A, nil, "Agent-to-Agent"},
		{phaseServerAddr, nil, "REST API Server Address"},
		{phaseCustomHFInput, func(m *OnboardModel) { m.customHFForWhat = "llm" }, "Custom HF Model"},
	}
	for _, c := range cases {
		m := NewOnboardModel()
		m.width, m.height = 100, 40
		m.phase = c.phase
		if c.setup != nil {
			c.setup(&m)
		}
		if v := m.View().Content; !strings.Contains(v, c.want) {
			t.Errorf("phase %d view lacks %q", c.phase, c.want)
		}
	}
	if m := NewOnboardModel(); m.embProviders[2] != "llamacpp" || m.llmProviders[3] != "llamacpp" {
		t.Fatalf("provider order changed: %v %v", m.embProviders, m.llmProviders)
	}
}

func TestOnboardH2AVisibleStep(t *testing.T) {
	m := NewOnboardModel()
	for phase, want := range map[wizardPhase]int{phaseA2A: 13, phasePlugins: 1, phaseMenu: 1} {
		m.phase = phase
		if got := m.visibleStep(); got != want {
			t.Errorf("phase %d: step %d, want %d", phase, got, want)
		}
	}
	m.phase = phaseCustomHFInput
	m.customHFForWhat = "reranker"
	if got := m.visibleStep(); got != 8 {
		t.Errorf("custom HF reranker step = %d", got)
	}
}

// settingsMenuValues masks keys and names the local runtimes.
func TestOnboardH2ASettingsMenuValues(t *testing.T) {
	values := func(m OnboardModel) string { return strings.Join(m.settingsMenuValues(), "\n") }

	m := h2aMenu(t, OnboardResult{EmbeddingProvider: "openai", LLMProvider: "anthropic", RerankEnabled: true})
	m.embKeyInput.SetValue("short")
	m.llmKeyInput.SetValue("sk-ant-1234567890")
	v := values(m)
	for _, want := range []string{"****", "sk-a...7890", "(select model)"} {
		if !strings.Contains(v, want) {
			t.Errorf("values lack %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "sk-ant-1234567890") {
		t.Error("the Anthropic key is shown in clear")
	}

	m = h2aMenu(t, OnboardResult{EmbeddingProvider: "ollama", LLMProvider: "llamacpp", OllamaHost: "http://h:1"})
	m.llmKeyInput.SetValue("short")
	if v := values(m); !strings.Contains(v, "(auto-scan default dirs)") {
		t.Errorf("llamacpp with an http host must auto-scan:\n%s", v)
	}

	m = h2aMenu(t, OnboardResult{EmbeddingProvider: "ollama", LLMProvider: "openai"})
	m.llmKeyInput.SetValue("abc")
	if v := values(m); !strings.Contains(v, "****") {
		t.Errorf("short OpenAI key not masked:\n%s", v)
	}
}

// renderModelSelect: fetch errors, the filter hint and scrolling markers.
func TestOnboardH2ARenderModelSelect(t *testing.T) {
	m := NewOnboardModel()
	m.phase = phaseEmbModel
	m.height = 10 // fewer than 5 visible rows is raised to 5

	var many []ModelInfo
	for i := 0; i < 12; i++ {
		many = append(many, ModelInfo{Name: "m" + string(rune('a'+i)), Tag: "tag" + string(rune('a'+i))})
	}
	out := m.renderModelSelect("3", "Models", many, 8, true, true)
	for _, want := range []string{"(all, tab to filter)", "↑ more", "↓ more", "tag" + "h", "tab toggle filter"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if strings.Contains(out, "ma") {
		t.Error("rows above the window are rendered")
	}

	m.fetchErr = "No local .gguf models found"
	if out := m.renderModelSelect("3", "M", nil, 0, false, false); !strings.Contains(out, "download one to ~/.gleann/models") {
		t.Errorf("gguf error not shown:\n%s", out)
	}
	m.fetchErr = "connection refused"
	if out := m.renderModelSelect("3", "M", nil, 0, false, false); !strings.Contains(out, "Could not list the service's models") {
		t.Errorf("fetch error not shown:\n%s", out)
	}
}

// renderSummary lists every chosen option.
func TestOnboardH2ARenderSummary(t *testing.T) {
	m := h2aWizard(t, "llamacpp", "ollama")
	m.codeEmbeddingModel = "code.gguf"
	m.docEmbeddingModel = "doc.gguf"
	m.rerankEnabled = true
	m.rerankModels = models("rr")
	m.mcpEnabled = true
	m.serverEnabled = true
	m.serverAddrInput.SetValue(":9999")
	out := m.renderSummary()
	for _, want := range []string{"code.gguf", "doc.gguf", "Embedded llama-server", "rr", ":9999", "enabled"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q", want)
		}
	}

	m = h2aWizard(t, "openai", "openai")
	m.embKeyInput.SetValue("sk-abcdefghijkl")
	m.rerankEnabled = true
	out = m.renderSummary()
	if !strings.Contains(out, "sk-a...ijkl") || strings.Contains(out, "sk-abcdefghijkl") {
		t.Error("OpenAI key not masked in the summary")
	}
	if !strings.Contains(out, "(no model selected)") {
		t.Error("reranker without a model not shown")
	}
}

// List navigation and confirmation in the model steps.
func TestOnboardH2AModelListKeys(t *testing.T) {
	m := h2aMenu(t, OnboardResult{EmbeddingProvider: "ollama"})
	m.phase = phaseEmbModel
	m.embModels = models("a", "b")
	m.embAllModels = m.embModels
	m.embModelIdx = 1
	m, _ = step(t, m, "up")
	if m.embModelIdx != 0 {
		t.Errorf("up: idx %d", m.embModelIdx)
	}
	m, _ = step(t, m, "enter")
	if m.phase != phaseMenu {
		t.Errorf("menu enter: phase %d", m.phase)
	}

	m = h2aWizard(t, "llamacpp", "ollama")
	m.embModels = []ModelInfo{{Name: "E", Tag: "/m/e.gguf"}, {Name: "D", Tag: "/m/d.gguf"}}
	m.phase = phaseCodeEmbModel
	m.codeEmbModelIdx = 2
	m, _ = step(t, m, "up")
	if m.codeEmbModelIdx != 1 {
		t.Errorf("code up: %d", m.codeEmbModelIdx)
	}
	m.phase = phaseDocEmbModel
	m.docEmbModelIdx = 2
	m, _ = step(t, m, "up", "down", "enter")
	if m.docEmbeddingModel != "/m/d.gguf" || m.phase != phaseLLMProvider {
		t.Errorf("doc model %q phase %d", m.docEmbeddingModel, m.phase)
	}

	m.phase = phaseRerankModel
	m.rerankModels = models("r1", "r2")
	m.rerankModelIdx = 1
	m, _ = step(t, m, "up")
	if m.rerankModelIdx != 0 {
		t.Errorf("rerank up: %d", m.rerankModelIdx)
	}
}

// On the Custom… row, down stays and tab leaves the row for the list filter.
func TestOnboardH2ACustomRowDownAndTab(t *testing.T) {
	m := h2aWizard(t, "ollama", "ollama")
	m.phase = phaseEmbModel
	m.embAllModels = models("bge-m3", "llama3")
	m.embModels = filterEmbeddingModels(m.embAllModels)
	m = toCustom(t, m)
	m = pressKey(m, kDown)
	if !m.onCustom {
		t.Error("down on the Custom… row left it")
	}
	m = pressKey(m, kTab)
	if m.onCustom || !m.embShowAll || len(m.embModels) != 2 {
		t.Errorf("tab: onCustom=%v showAll=%v models=%d", m.onCustom, m.embShowAll, len(m.embModels))
	}
}

// Code/doc options fall back to the unfiltered list or the saved model, and
// never offer the Custom placeholder.
func TestOnboardH2ACodeDocOptionsFallbacks(t *testing.T) {
	m := h2aWizard(t, "ollama", "ollama")
	m.embAllModels = []ModelInfo{{Name: "x"}, {Name: "(Custom) Enter HF Repo...", Tag: "custom"}}
	for _, opts := range [][]ModelInfo{m.codeModelOptions(), m.docModelOptions()} {
		if len(opts) != 2 || opts[1].Name != "x" {
			t.Errorf("options from the full list: %+v", opts)
		}
	}
	m.embAllModels = nil
	m.existingCfg = &OnboardResult{EmbeddingModel: "saved"}
	for _, opts := range [][]ModelInfo{m.codeModelOptions(), m.docModelOptions()} {
		if len(opts) != 2 || opts[1].Name != "saved" || !strings.Contains(opts[0].Name, "saved") {
			t.Errorf("options from the saved model: %+v", opts)
		}
	}
	if got := m.primaryEmbModel(); got != "saved" {
		t.Errorf("primary = %q", got)
	}
	m.existingCfg = nil
	if got := m.primaryEmbModel(); got != "bge-m3" {
		t.Errorf("default primary = %q", got)
	}
}

func TestOnboardH2AWithConfigA2ADisabledAndRerankModel(t *testing.T) {
	off := false
	m := h2aMenu(t, OnboardResult{A2AEnabled: &off, RerankModel: "bge-reranker"})
	if m.a2aEnabled || m.a2aOptionIdx != 0 {
		t.Errorf("A2A: enabled=%v idx=%d", m.a2aEnabled, m.a2aOptionIdx)
	}
	if got := m.currentRerankModel(); got != "bge-reranker" {
		t.Errorf("current reranker = %q", got)
	}
}

// A bare port typed as the LLM Ollama host means localhost; an Ollama
// embedder with a typed host is saved when the LLM is elsewhere.
func TestOnboardH2ABarePortAndEmbedderHost(t *testing.T) {
	m := h2aWizard(t, "openai", "ollama")
	m.phase = phaseLLMHost
	m.embHostInput.SetValue("https://api.openai.com")
	m.llmHostInput.SetValue("11500")
	m, _ = step(t, m, "enter")
	if got := m.llmHostInput.Value(); got != "http://localhost:11500" {
		t.Errorf("llm host = %q", got)
	}
	if got := m.embHostInput.Value(); got != "https://api.openai.com" {
		t.Errorf("the OpenAI embedder's host was overwritten: %q", got)
	}

	m = h2aWizard(t, "ollama", "anthropic")
	m.embHostInput.SetValue("http://gpu:11434")
	m.buildResult()
	if m.result.OllamaHost != "http://gpu:11434" {
		t.Errorf("OllamaHost = %q", m.result.OllamaHost)
	}
}

// The code model menu entry with a known list selects the saved model.
func TestOnboardH2AMenuCodeModelSelectsSaved(t *testing.T) {
	m := h2aMenu(t, OnboardResult{EmbeddingProvider: "ollama", CodeEmbeddingModel: "code-embed"})
	m.embModels = models("bge-m3", "code-embed")
	for i, it := range m.menuItems {
		if it.phase == phaseCodeEmbModel {
			m.menuCursor = i
		}
	}
	m, _ = step(t, m, "enter")
	if m.phase != phaseCodeEmbModel || m.codeModelOptions()[m.codeEmbModelIdx].Name != "code-embed" {
		t.Errorf("phase %d idx %d", m.phase, m.codeEmbModelIdx)
	}
}
