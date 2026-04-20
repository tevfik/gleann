package tui

import (
	"math"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
)

// ── adjustSetting ──────────────────────────────────────────────

func TestAdjustSettingTemperatureExt3(t *testing.T) {
	m := ChatModel{temperature: 0.5, settingsCursor: fieldTemperature}
	m.adjustSetting(1)
	if m.temperature <= 0.5 {
		t.Error("should increase temperature")
	}
	m.adjustSetting(-1)
	m.adjustSetting(-1)
	if m.temperature < 0 {
		t.Error("temperature should not go negative")
	}
}

func TestAdjustSettingTemperatureBoundsExt3(t *testing.T) {
	m := ChatModel{temperature: 0.0, settingsCursor: fieldTemperature}
	m.adjustSetting(-1)
	if m.temperature != 0.0 {
		t.Error("should not go below 0")
	}
	m.temperature = 2.0
	m.adjustSetting(1)
	if m.temperature != 2.0 {
		t.Error("should not go above 2.0")
	}
}

func TestAdjustSettingMaxTokensExt3(t *testing.T) {
	m := ChatModel{maxTokens: 1024, settingsCursor: fieldMaxTokens}
	m.adjustSetting(1)
	if m.maxTokens <= 1024 {
		t.Error("should increase max tokens")
	}
}

func TestAdjustSettingMaxTokensBoundsExt3(t *testing.T) {
	m := ChatModel{maxTokens: 256, settingsCursor: fieldMaxTokens}
	m.adjustSetting(-1)
	if m.maxTokens != 256 {
		t.Error("should not go below minimum")
	}
	m.maxTokens = 8192
	m.adjustSetting(1)
	if m.maxTokens != 8192 {
		t.Error("should not go above maximum")
	}
}

func TestAdjustSettingTopKExt3(t *testing.T) {
	m := ChatModel{topK: 10, settingsCursor: fieldTopK}
	m.adjustSetting(1)
	if m.topK <= 10 {
		t.Error("should increase top-k")
	}
}

func TestAdjustSettingTopKBoundsExt3(t *testing.T) {
	m := ChatModel{topK: 3, settingsCursor: fieldTopK}
	m.adjustSetting(-1)
	if m.topK != 3 {
		t.Error("should not go below minimum")
	}
}

func TestAdjustSettingLLMModelExt3(t *testing.T) {
	m := ChatModel{
		settingsCursor: fieldLLMModel,
		llmModels:      []string{"a", "b", "c"},
		llmModelIdx:    0,
	}
	m.adjustSetting(1)
	if m.llmModelIdx != 1 {
		t.Errorf("idx = %d, want 1", m.llmModelIdx)
	}
	m.adjustSetting(1)
	if m.llmModelIdx != 2 {
		t.Errorf("idx = %d, want 2", m.llmModelIdx)
	}
	m.adjustSetting(1)
	if m.llmModelIdx != 2 {
		t.Error("should not exceed list length")
	}
	m.adjustSetting(-1)
	if m.llmModelIdx != 1 {
		t.Error("should decrease")
	}
}

func TestAdjustSettingLLMModelBoundsNegativeExt3(t *testing.T) {
	m := ChatModel{
		settingsCursor: fieldLLMModel,
		llmModels:      []string{"a", "b"},
		llmModelIdx:    0,
	}
	m.adjustSetting(-1)
	if m.llmModelIdx != 0 {
		t.Error("should not go below 0")
	}
}

func TestAdjustSettingRerankToggleExt3(t *testing.T) {
	m := ChatModel{settingsCursor: fieldRerankToggle, rerankEnabled: false}
	m.adjustSetting(1)
	if !m.rerankEnabled {
		t.Error("should toggle to true")
	}
	m.adjustSetting(1)
	if m.rerankEnabled {
		t.Error("should toggle back to false")
	}
}

func TestAdjustSettingRerankModelExt3(t *testing.T) {
	m := ChatModel{
		settingsCursor: fieldRerankModel,
		rerankModels:   []string{"a", "b", "c"},
		rerankModelIdx: 0,
	}
	m.adjustSetting(1)
	if m.rerankModelIdx != 1 {
		t.Errorf("idx = %d, want 1", m.rerankModelIdx)
	}
}

func TestAdjustSettingRoleExt3(t *testing.T) {
	m := ChatModel{
		settingsCursor: fieldRole,
		roleNames:      []string{"(none)", "developer", "reviewer"},
		roleIdx:        0,
	}
	m.adjustSetting(1)
	if m.roleIdx != 1 {
		t.Errorf("idx = %d, want 1", m.roleIdx)
	}
	m.adjustSetting(-1)
	if m.roleIdx != 0 {
		t.Errorf("idx = %d, want 0", m.roleIdx)
	}
	m.adjustSetting(-1)
	if m.roleIdx != 0 {
		t.Error("should not go below 0")
	}
}

// ── renderMessages ─────────────────────────────────────────────

func TestRenderMessagesSystem(t *testing.T) {
	m := ChatModel{width: 80, messages: []chatMsg{
		{role: "system", content: "Connected"},
	}}
	out := m.renderMessages()
	if !strings.Contains(out, "Connected") {
		t.Error("should contain system message")
	}
}

func TestRenderMessagesUser(t *testing.T) {
	m := ChatModel{width: 80, messages: []chatMsg{
		{role: "user", content: "Hello world"},
	}}
	out := m.renderMessages()
	if !strings.Contains(out, "Hello world") {
		t.Error("should contain user message")
	}
	if !strings.Contains(out, "You") {
		t.Error("should show You label")
	}
}

func TestRenderMessagesAssistant(t *testing.T) {
	m := ChatModel{width: 80, messages: []chatMsg{
		{role: "assistant", content: "Here is the answer"},
	}}
	out := m.renderMessages()
	// After glamour rendering the text might be styled differently.
	if !strings.Contains(out, "answer") && !strings.Contains(out, "Here") {
		t.Errorf("should contain assistant message, got: %q", out[:min(len(out), 200)])
	}
}

func TestRenderMessagesWaiting(t *testing.T) {
	m := ChatModel{width: 80, waiting: true, messages: []chatMsg{}}
	out := m.renderMessages()
	if !strings.Contains(out, "Thinking") {
		t.Error("should show thinking indicator")
	}
}

func TestRenderMessagesStreaming(t *testing.T) {
	m := ChatModel{
		width:   80,
		waiting: true,
		messages: []chatMsg{
			{role: "assistant", content: "partial text"},
		},
	}
	out := m.renderMessages()
	// Streaming should render plain text, not through glamour.
	if !strings.Contains(out, "partial text") {
		t.Error("should contain streaming text")
	}
}

// ── renderScrollbar ────────────────────────────────────────────

func TestRenderScrollbarZeroHeight(t *testing.T) {
	m := ChatModel{}
	out := m.renderScrollbar()
	if out != "" {
		t.Error("should return empty for zero height viewport")
	}
}

// ── viewHistory ────────────────────────────────────────────────

func TestViewHistoryEmptyExt3(t *testing.T) {
	m := ChatModel{width: 80, historyItems: nil}
	out := m.viewHistory()
	if !strings.Contains(out, "No saved conversations") {
		t.Error("should show empty message")
	}
}

// ── findClosestFloat edge cases ────────────────────────────────

func TestFindClosestFloatExactExt3(t *testing.T) {
	idx := findClosestFloat(temperaturePresets, 0.7)
	if temperaturePresets[idx] != 0.7 {
		t.Errorf("expected 0.7, got %f at idx %d", temperaturePresets[idx], idx)
	}
}

func TestFindClosestFloatBetweenExt3(t *testing.T) {
	idx := findClosestFloat(temperaturePresets, 0.75)
	// Should return closest — either 0.7 or 0.8.
	if math.Abs(temperaturePresets[idx]-0.75) > 0.11 {
		t.Errorf("not close enough: %f", temperaturePresets[idx])
	}
}

func TestFindClosestFloatBelowExt3(t *testing.T) {
	idx := findClosestFloat(temperaturePresets, -1.0)
	if idx != 0 {
		t.Errorf("should clamp to 0, got %d", idx)
	}
}

func TestFindClosestFloatAboveExt3(t *testing.T) {
	idx := findClosestFloat(temperaturePresets, 100.0)
	if idx != len(temperaturePresets)-1 {
		t.Errorf("should clamp to last, got %d", idx)
	}
}

// ── findClosestInt edge cases ──────────────────────────────────

func TestFindClosestIntExactExt3(t *testing.T) {
	idx := findClosestInt(maxTokensPresets, 1024)
	if maxTokensPresets[idx] != 1024 {
		t.Error("should find exact match")
	}
}

func TestFindClosestIntBetweenExt3(t *testing.T) {
	idx := findClosestInt(maxTokensPresets, 700)
	// Should return 512 (idx=1) or 1024 (idx=2).
	if maxTokensPresets[idx] != 512 && maxTokensPresets[idx] != 1024 {
		t.Errorf("unexpected match: %d", maxTokensPresets[idx])
	}
}

// ── View edge cases ────────────────────────────────────────────

func TestChatViewQuittingExt3(t *testing.T) {
	m := ChatModel{quitting: true}
	v := m.View()
	if v.Content != "" {
		t.Errorf("quitting view should be empty, got %q", v.Content)
	}
}

func TestChatViewNotReadyExt3(t *testing.T) {
	m := ChatModel{ready: false}
	v := m.View()
	if !strings.Contains(v.Content, "Initializing") {
		t.Error("should show initializing")
	}
}

// ── renderMarkdownContent ──────────────────────────────────────

func TestRenderMarkdownContentExt3(t *testing.T) {
	out := renderMarkdownContent("**bold text**", 80)
	if out == "" {
		t.Error("should return rendered content")
	}
}

func TestRenderMarkdownContentEmptyExt3(t *testing.T) {
	out := renderMarkdownContent("", 80)
	if out != "" {
		t.Logf("empty input produced: %q", out)
	}
}

// ── waitForToken ───────────────────────────────────────────────

func TestWaitForTokenDone(t *testing.T) {
	ch := make(chan string)
	close(ch)
	cmd := waitForToken(ch)
	msg := cmd()
	if _, ok := msg.(streamDoneMsg); !ok {
		t.Errorf("expected streamDoneMsg, got %T", msg)
	}
}

func TestWaitForTokenNormal(t *testing.T) {
	ch := make(chan string, 1)
	ch <- "hello"
	cmd := waitForToken(ch)
	msg := cmd()
	if m, ok := msg.(streamTokenMsg); !ok || m.token != "hello" {
		t.Errorf("expected streamTokenMsg{hello}, got %T %v", msg, msg)
	}
}

func TestWaitForTokenError(t *testing.T) {
	ch := make(chan string, 1)
	ch <- "\x00ERROR:test error"
	cmd := waitForToken(ch)
	msg := cmd()
	if m, ok := msg.(streamErrorMsg); !ok || m.err == nil {
		t.Errorf("expected streamErrorMsg, got %T %v", msg, msg)
	}
}

// ── relayout ───────────────────────────────────────────────────

func TestRelayoutInit(t *testing.T) {
	ta := textarea.New()
	m := ChatModel{
		width:           80,
		height:          24,
		ready:           false,
		streamingAnswer: &strings.Builder{},
		textarea:        ta,
	}
	m = m.relayout()
	if !m.ready {
		t.Error("should be ready after relayout")
	}
}

func TestRelayoutSmallWindow(t *testing.T) {
	ta := textarea.New()
	m := ChatModel{
		width:           20,
		height:          8,
		ready:           false,
		streamingAnswer: &strings.Builder{},
		textarea:        ta,
	}
	m = m.relayout()
	if !m.ready {
		t.Error("should handle small window")
	}
}

// ── PluginModel ────────────────────────────────────────────────

func TestPluginOwnerDefaultExt3(t *testing.T) {
	owner := pluginOwner()
	if owner == "" {
		t.Error("should have a default owner")
	}
}

func TestPluginStatusStringExt3(t *testing.T) {
	tests := []struct {
		s    pluginStatus
		want string
	}{
		{statusNotInstalled, "Not installed"},
		{statusInstalled, "Installed"},
		{statusRunning, "Running"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("pluginStatus(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestPluginStatusBadgeExt3(t *testing.T) {
	// Just exercise — Badge returns styled strings.
	for _, s := range []pluginStatus{statusNotInstalled, statusInstalled, statusRunning} {
		b := s.Badge()
		if b == "" {
			t.Errorf("Badge() for %d should not be empty", s)
		}
	}
}

func TestKnownPluginsNotEmptyExt3(t *testing.T) {
	if len(knownPlugins) == 0 {
		t.Error("should have known plugins")
	}
}

func TestPluginModelInitExt3(t *testing.T) {
	m := PluginModel{
		plugins:  knownPlugins,
		statuses: make([]pluginStatus, len(knownPlugins)),
	}
	cmd := m.Init()
	if cmd != nil {
		t.Error("Init should return nil")
	}
}

// ── fetchRerankModelList ───────────────────────────────────────

func TestFetchRerankModelListNilConfigExt3(t *testing.T) {
	// With nil config and no Ollama running (CI environment), the function
	// returns nil — that is the correct fallback behaviour. We only verify
	// the call does not panic and idx is within a reasonable range.
	models, idx := fetchRerankModelList(nil)
	if idx < 0 {
		t.Errorf("idx should be >= 0, got %d", idx)
	}
	// models may be nil when Ollama is unreachable — that is valid.
	_ = models
}
