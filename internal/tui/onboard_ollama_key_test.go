package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// authRecorder is a fake Ollama /api/tags server that records the
// Authorization header of every request.
type authRecorder struct {
	mu    sync.Mutex
	auths []string
}

func (a *authRecorder) handler(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.auths = append(a.auths, r.Header.Get("Authorization"))
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"models":[{"name":"bge-m3:latest","size":1000},{"name":"llama3:8b","size":2000}]}`))
}

func (a *authRecorder) last() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.auths) == 0 {
		return "<no request>"
	}
	return a.auths[len(a.auths)-1]
}

func newAuthServer(t *testing.T) (*httptest.Server, *authRecorder) {
	t.Helper()
	rec := &authRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	t.Cleanup(srv.Close)
	return srv, rec
}

func press(t *testing.T, m OnboardModel, k tea.KeyPressMsg) (OnboardModel, tea.Cmd) {
	t.Helper()
	res, cmd := m.Update(k)
	return asOnboard(t, res), cmd
}

// asOnboard unwraps a tea.Model that may be OnboardModel or *OnboardModel
// (text-input updates return the pointer form).
func asOnboard(t *testing.T, res tea.Model) OnboardModel {
	t.Helper()
	switch v := res.(type) {
	case OnboardModel:
		return v
	case *OnboardModel:
		return *v
	}
	t.Fatalf("unexpected model type %T", res)
	return OnboardModel{}
}

func enter() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEnter} }

func typeText(t *testing.T, m OnboardModel, s string) OnboardModel {
	t.Helper()
	for _, r := range s {
		m, _ = press(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

// driveToOllamaKeyStep walks Advanced Setup → ollama provider → host → key step.
func driveToOllamaKeyStep(t *testing.T, host string) OnboardModel {
	t.Helper()
	m := NewOnboardModel()
	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown}) // Advanced Setup
	m, _ = press(t, m, enter())
	if m.phase != phaseEmbProvider {
		t.Fatalf("phase = %d, want phaseEmbProvider", m.phase)
	}
	if m.embProviders[m.embProviderIdx] != "ollama" {
		t.Fatalf("default embedding provider = %q, want ollama", m.embProviders[m.embProviderIdx])
	}
	m, _ = press(t, m, enter())
	if m.phase != phaseEmbHost {
		t.Fatalf("phase = %d, want phaseEmbHost", m.phase)
	}
	m.embHostInput.SetValue(host)
	m, _ = press(t, m, enter())
	if m.phase != phaseEmbOllamaKey {
		t.Fatalf("phase = %d, want phaseEmbOllamaKey", m.phase)
	}
	if !m.ollamaKeyInput.Focused() {
		t.Error("ollama key input should be focused on the key step")
	}
	return m
}

// finishFetch runs the fetch command and feeds the result back to the model.
func finishFetch(t *testing.T, m OnboardModel, cmd tea.Cmd) OnboardModel {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a fetch command")
	}
	msg := cmd()
	fetched, ok := msg.(modelsFetchedMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want modelsFetchedMsg", msg)
	}
	if fetched.err != nil {
		t.Fatalf("fetch error: %v", fetched.err)
	}
	res, _ := m.Update(fetched)
	return asOnboard(t, res)
}

func TestOnboardOllamaKeyStepSendsBearerAndPersists(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "")
	srv, rec := newAuthServer(t)

	m := driveToOllamaKeyStep(t, srv.URL)
	m = typeText(t, m, "secret-token")
	if got := m.ollamaKeyInput.Value(); got != "secret-token" {
		t.Fatalf("typed key = %q", got)
	}
	// The key is entered masked.
	if v := m.View().Content; strings.Contains(v, "secret-token") {
		t.Error("key step view must not echo the key in clear text")
	}

	m, cmd := press(t, m, enter())
	if m.phase != phaseEmbFetching {
		t.Fatalf("phase = %d, want phaseEmbFetching", m.phase)
	}
	m = finishFetch(t, m, cmd)
	if m.phase != phaseEmbModel {
		t.Fatalf("phase = %d, want phaseEmbModel", m.phase)
	}
	if got := rec.last(); got != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want Bearer secret-token", got)
	}

	m.buildResult()
	r := m.Result()
	if r.OllamaAPIKey != "secret-token" {
		t.Errorf("result.OllamaAPIKey = %q, want secret-token", r.OllamaAPIKey)
	}
	if r.OpenAIKey != "" {
		t.Errorf("OpenAIKey should stay empty, got %q", r.OpenAIKey)
	}
	if r.OllamaHost != srv.URL {
		t.Errorf("OllamaHost = %q, want %q", r.OllamaHost, srv.URL)
	}
}

func TestOnboardOllamaKeyStepEmptyIsAccepted(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "")
	srv, rec := newAuthServer(t)

	m := driveToOllamaKeyStep(t, srv.URL)
	m, cmd := press(t, m, enter()) // skip — no key
	if m.phase != phaseEmbFetching {
		t.Fatalf("phase = %d, want phaseEmbFetching (empty key must be accepted)", m.phase)
	}
	m = finishFetch(t, m, cmd)
	if got := rec.last(); got != "" {
		t.Errorf("Authorization = %q, want none", got)
	}
	m.buildResult()
	if got := m.Result().OllamaAPIKey; got != "" {
		t.Errorf("OllamaAPIKey = %q, want empty", got)
	}
}

func TestOnboardOllamaKeyLLMStepPrefilledAndUsed(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "")
	srv, rec := newAuthServer(t)

	m := NewOnboardModel()
	m.ollamaKeyInput.SetValue("shared-key")
	m.llmHostInput.SetValue(srv.URL)
	m.phase = phaseLLMHost
	m, _ = press(t, m, enter())
	if m.phase != phaseLLMOllamaKey {
		t.Fatalf("phase = %d, want phaseLLMOllamaKey", m.phase)
	}
	if got := m.ollamaKeyInput.Value(); got != "shared-key" {
		t.Errorf("LLM key step should be prefilled, got %q", got)
	}
	if !strings.Contains(m.View().Content, "Ollama API Key") {
		t.Error("LLM key step should render the Ollama API Key prompt")
	}
	// Point the LLM fetch at the test server (emb provider == llm provider
	// reuses the embedding host).
	m.embHostInput.SetValue(srv.URL)
	m, cmd := press(t, m, enter())
	if m.phase != phaseLLMFetching {
		t.Fatalf("phase = %d, want phaseLLMFetching", m.phase)
	}
	m = finishFetch(t, m, cmd)
	if m.phase != phaseLLMModel {
		t.Fatalf("phase = %d, want phaseLLMModel", m.phase)
	}
	if got := rec.last(); got != "Bearer shared-key" {
		t.Errorf("Authorization = %q, want Bearer shared-key", got)
	}
}

func TestOnboardOllamaKeyNotSentToOpenAIProvider(t *testing.T) {
	m := NewOnboardModel()
	m.ollamaKeyInput.SetValue("ollama-secret")
	m.embKeyInput.SetValue("sk-openai")
	if got := m.providerKey("openai", m.embKeyInput.Value()); got != "sk-openai" {
		t.Errorf("openai key = %q", got)
	}
	if got := m.providerKey("ollama", m.embKeyInput.Value()); got != "ollama-secret" {
		t.Errorf("ollama key = %q", got)
	}
}

func TestOnboardOllamaKeyMenuModeAndSummary(t *testing.T) {
	cfg := &OnboardResult{
		EmbeddingProvider: "ollama",
		LLMProvider:       "ollama",
		OllamaHost:        "http://gpu-box:11434",
		OllamaAPIKey:      "very-secret-ollama-key",
		Completed:         true,
	}
	m := NewOnboardModelWithConfig(cfg)
	if got := m.ollamaKeyInput.Value(); got != "very-secret-ollama-key" {
		t.Fatalf("key not loaded from saved config: %q", got)
	}

	// Menu shows that a key is set, never the key itself.
	vals := strings.Join(m.settingsMenuValues(), "|")
	if !strings.Contains(vals, "(key)") {
		t.Errorf("menu values should flag the key: %s", vals)
	}
	if strings.Contains(vals, "very-secret") {
		t.Error("menu values must not leak the key")
	}

	// Menu: LLM Host/Key → host → key step → back to menu.
	m.phase = phaseLLMHost
	m, _ = press(t, m, enter())
	if m.phase != phaseLLMOllamaKey {
		t.Fatalf("phase = %d, want phaseLLMOllamaKey", m.phase)
	}
	m, _ = press(t, m, enter())
	if m.phase != phaseMenu {
		t.Fatalf("phase = %d, want phaseMenu", m.phase)
	}

	// Summary masks the key.
	m.phase = phaseSummary
	v := m.View().Content
	if !strings.Contains(v, "Ollama API Key") {
		t.Error("summary should list the Ollama API key row")
	}
	if strings.Contains(v, "very-secret-ollama-key") {
		t.Error("summary must mask the key")
	}

	// Save & Exit preserves it.
	m.buildResult()
	if got := m.Result().OllamaAPIKey; got != "very-secret-ollama-key" {
		t.Errorf("OllamaAPIKey = %q after buildResult", got)
	}
}

func TestOnboardOllamaKeyPrevPhaseAndStep(t *testing.T) {
	m := NewOnboardModel()
	m.phase = phaseEmbOllamaKey
	if got := wizardPhase(m.prevPhase()); got != phaseEmbHost {
		t.Errorf("prev of phaseEmbOllamaKey = %d, want phaseEmbHost", got)
	}
	if m.visibleStep() != 2 {
		t.Errorf("visibleStep = %d, want 2", m.visibleStep())
	}
	m.phase = phaseLLMOllamaKey
	if got := wizardPhase(m.prevPhase()); got != phaseLLMHost {
		t.Errorf("prev of phaseLLMOllamaKey = %d, want phaseLLMHost", got)
	}
	if m.visibleStep() != 7 {
		t.Errorf("visibleStep = %d, want 7", m.visibleStep())
	}
}

func TestFetchOllamaModelsBearerFromEnv(t *testing.T) {
	srv, rec := newAuthServer(t)

	t.Setenv("OLLAMA_API_KEY", "env-key")
	if _, err := fetchModels("ollama", srv.URL, ""); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(); got != "Bearer env-key" {
		t.Errorf("Authorization = %q, want Bearer env-key", got)
	}
	// An explicit key wins over the environment.
	if _, err := fetchModels("ollama", srv.URL, "explicit"); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(); got != "Bearer explicit" {
		t.Errorf("Authorization = %q, want Bearer explicit", got)
	}
	if !ollamaReachable(srv.URL, "explicit") {
		t.Error("ollamaReachable should succeed")
	}
	if got := rec.last(); got != "Bearer explicit" {
		t.Errorf("ollamaReachable Authorization = %q", got)
	}
}
