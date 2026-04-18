package tui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── adjustSetting ──────────────────────────────────────────────

func TestAdjustSettingTemperature(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "test", "model")
	m.temperature = 0.7

	// Move up.
	m.settingsCursor = fieldTemperature
	m.adjustSetting(1)
	if m.temperature != 0.8 {
		t.Errorf("temperature = %f, want 0.8", m.temperature)
	}

	// Move down.
	m.adjustSetting(-1)
	if m.temperature != 0.7 {
		t.Errorf("temperature = %f, want 0.7", m.temperature)
	}

	// Clamp at min.
	m.temperature = 0.0
	m.adjustSetting(-1)
	if m.temperature != 0.0 {
		t.Errorf("temperature = %f, want 0.0", m.temperature)
	}

	// Clamp at max.
	m.temperature = 2.0
	m.adjustSetting(1)
	if m.temperature != 2.0 {
		t.Errorf("temperature = %f, want 2.0", m.temperature)
	}
}

func TestAdjustSettingMaxTokens(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "test", "model")
	m.maxTokens = 2048

	m.settingsCursor = fieldMaxTokens
	m.adjustSetting(1)
	if m.maxTokens != 4096 {
		t.Errorf("maxTokens = %d, want 4096", m.maxTokens)
	}

	m.adjustSetting(-1)
	if m.maxTokens != 2048 {
		t.Errorf("maxTokens = %d, want 2048", m.maxTokens)
	}
}

func TestAdjustSettingTopK(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "test", "model")
	m.topK = 10

	m.settingsCursor = fieldTopK
	m.adjustSetting(1)
	if m.topK != 15 {
		t.Errorf("topK = %d, want 15", m.topK)
	}

	m.adjustSetting(-1)
	if m.topK != 10 {
		t.Errorf("topK = %d, want 10", m.topK)
	}
}

func TestAdjustSettingLLMModel(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "test", "model")
	m.llmModels = []string{"model-a", "model-b", "model-c"}
	m.llmModelIdx = 0

	m.settingsCursor = fieldLLMModel
	m.adjustSetting(1)
	if m.llmModelIdx != 1 {
		t.Errorf("llmModelIdx = %d, want 1", m.llmModelIdx)
	}

	m.adjustSetting(1)
	if m.llmModelIdx != 2 {
		t.Errorf("llmModelIdx = %d, want 2", m.llmModelIdx)
	}

	// Clamp at end.
	m.adjustSetting(1)
	if m.llmModelIdx != 2 {
		t.Errorf("llmModelIdx = %d, want 2 (clamped)", m.llmModelIdx)
	}
}

func TestAdjustSettingRerankToggle(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "test", "model")
	m.rerankEnabled = false

	m.settingsCursor = fieldRerankToggle
	m.adjustSetting(1)
	if !m.rerankEnabled {
		t.Error("should be enabled after toggle")
	}

	m.adjustSetting(1) // toggle again
	if m.rerankEnabled {
		t.Error("should be disabled after second toggle")
	}
}

func TestAdjustSettingRerankModel(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "test", "model")
	m.rerankModels = []string{"reranker-a", "reranker-b"}
	m.rerankModelIdx = 0

	m.settingsCursor = fieldRerankModel
	m.adjustSetting(1)
	if m.rerankModelIdx != 1 {
		t.Errorf("rerankModelIdx = %d, want 1", m.rerankModelIdx)
	}

	// Clamp.
	m.adjustSetting(1)
	if m.rerankModelIdx != 1 {
		t.Errorf("rerankModelIdx = %d, want 1 (clamped)", m.rerankModelIdx)
	}
}

func TestAdjustSettingRole(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "test", "model")
	m.roleNames = []string{"(none)", "code", "debug"}
	m.roleIdx = 0

	m.settingsCursor = fieldRole
	m.adjustSetting(1)
	if m.roleIdx != 1 {
		t.Errorf("roleIdx = %d, want 1", m.roleIdx)
	}

	m.adjustSetting(1)
	if m.roleIdx != 2 {
		t.Errorf("roleIdx = %d, want 2", m.roleIdx)
	}

	// Clamp.
	m.adjustSetting(1)
	if m.roleIdx != 2 {
		t.Errorf("roleIdx = %d, want 2 (clamped)", m.roleIdx)
	}
}

// ── ChatModel basic creation ───────────────────────────────────

func TestNewChatModel(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "myindex", "mymodel")

	if m.indexName != "myindex" {
		t.Errorf("indexName = %q", m.indexName)
	}
	if m.modelName != "mymodel" {
		t.Errorf("modelName = %q", m.modelName)
	}
	if m.chat != chat {
		t.Error("chat reference mismatch")
	}
}

// ── ChatModel Init ─────────────────────────────────────────────

func TestChatModelInit(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	cmd := m.Init()
	// Init should return a batch command.
	if cmd == nil {
		t.Error("Init should return a command")
	}
}

// ── ChatModel Update ───────────────────────────────────────────

func TestChatModelUpdateWindowSize(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 50})
	cm := updated.(ChatModel)
	if cm.width != 120 || cm.height != 50 {
		t.Errorf("size = %dx%d, want 120x50", cm.width, cm.height)
	}
}

func TestChatModelUpdateCtrlC(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	cm := updated.(ChatModel)
	if !cm.quitting {
		t.Error("ctrl+c should quit")
	}
}

func TestChatModelUpdateEscEmpty(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	cm := updated.(ChatModel)
	if !cm.quitting {
		t.Error("esc with empty input should quit")
	}
}

func TestChatModelUpdateCtrlS(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40

	updated, _ := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	cm := updated.(ChatModel)
	if !cm.showSettings {
		t.Error("ctrl+s should show settings")
	}
}

func TestChatModelUpdatePageUpDown(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40

	// Page up.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	_ = updated.(ChatModel) // should not crash

	// Page down.
	updated2, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	_ = updated2.(ChatModel) // should not crash
}

func TestChatModelUpdateCtrlU(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	_ = updated.(ChatModel)
}

// ── ChatModel View ─────────────────────────────────────────────

func TestChatModelView(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.messages = []chatMsg{
		{role: "system", content: "Welcome"},
		{role: "user", content: "Hello"},
		{role: "assistant", content: "Hi back"},
	}

	v := m.View()
	if v.Content == "" {
		t.Error("View should produce output")
	}
}

func TestChatModelViewNotReady(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = false

	v := m.View()
	// Should show loading or empty state.
	_ = v
}

func TestChatModelViewSettings(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.showSettings = true
	m.llmModels = []string{"model-a"}
	m.roleNames = []string{"(none)"}

	v := m.View()
	if v.Content == "" {
		t.Error("Settings view should produce output")
	}
}

// ── viewHistory (empty) ────────────────────────────────────────

func TestViewHistoryEmpty(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.historyItems = nil

	out := m.viewHistory()
	if !strings.Contains(out, "No saved conversations") {
		t.Error("should indicate no conversations")
	}
}

// ── renderScrollbar ────────────────────────────────────────────

func TestRenderScrollbar(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.messages = []chatMsg{
		{role: "user", content: "hello"},
		{role: "assistant", content: "hi there"},
	}

	out := m.renderScrollbar()
	// May be empty if viewport not initialized.
	_ = out
}

// ── renderSlider ───────────────────────────────────────────────

func TestRenderSlider(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")

	out := m.renderSlider(fieldTemperature, "Temperature", "0.7", 0.35, 60)
	if out == "" {
		t.Error("should produce output")
	}
	if !strings.Contains(out, "Temperature") {
		t.Error("should contain label")
	}
}

// ── viewSettings ───────────────────────────────────────────────

func TestViewSettings(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.showSettings = true
	m.llmModels = []string{"model-a"}
	m.roleNames = []string{"(none)"}

	out := m.viewSettings()
	if out == "" {
		t.Error("should produce output")
	}
}

// ── relayout ───────────────────────────────────────────────────

func TestRelayout(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 120
	m.height = 50

	m2 := m.relayout()
	// relayout should not crash and should preserve dimensions.
	if m2.width != 120 {
		t.Errorf("width = %d, want 120", m2.width)
	}
}

// ── renderMessages ─────────────────────────────────────────────

func TestRenderMessages(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.messages = []chatMsg{
		{role: "user", content: "What is Go?"},
		{role: "assistant", content: "Go is a programming language."},
	}

	out := m.renderMessages()
	if out == "" {
		t.Error("should produce output")
	}
	if !strings.Contains(out, "What is Go?") {
		t.Error("should contain user message")
	}
}

// ── waitForToken ───────────────────────────────────────────────

func TestWaitForToken(t *testing.T) {
	ch := make(chan string, 1)
	ch <- "hello"
	close(ch)

	cmd := waitForToken(ch)
	if cmd == nil {
		t.Fatal("should return a command")
	}
	// Execute the command.
	msg := cmd()
	switch msg.(type) {
	case streamTokenMsg:
		// OK.
	case streamDoneMsg:
		// Also OK (channel may be drained).
	default:
		t.Errorf("unexpected msg type: %T", msg)
	}
}

func TestWaitForTokenClosed(t *testing.T) {
	ch := make(chan string)
	close(ch)

	cmd := waitForToken(ch)
	msg := cmd()
	if _, ok := msg.(streamDoneMsg); !ok {
		t.Errorf("expected streamDoneMsg, got %T", msg)
	}
}

// ── fetchLlamaCPPModels (with empty dir) ───────────────────────

func TestFetchLlamaCPPModels(t *testing.T) {
	// With an empty temp dir, should return error (no .gguf found).
	tmp := t.TempDir()
	_, err := fetchLlamaCPPModels(tmp)
	if err == nil {
		t.Error("expected error when no .gguf files found")
	}
}

func TestFetchLlamaCPPModelsWithGGUF(t *testing.T) {
	tmp := t.TempDir()
	// Create a fake GGUF file.
	fakeModel := "test-model.gguf"
	os.WriteFile(tmp+"/"+fakeModel, []byte("fake gguf data"), 0o644)

	models, err := fetchLlamaCPPModels(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Name != "test-model.gguf" {
		t.Errorf("name = %q", models[0].Name)
	}
}

// ── Slash commands via Update ──────────────────────────────────

func TestChatModelSlashQuit(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.messages = []chatMsg{{role: "system", content: "Welcome"}}
	m.textarea.SetValue("/quit")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(ChatModel)
	if !cm.quitting {
		t.Error("/quit should set quitting")
	}
}

func TestChatModelSlashClear(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.messages = []chatMsg{
		{role: "system", content: "Welcome"},
		{role: "user", content: "Hello"},
		{role: "assistant", content: "Hi"},
	}
	m.textarea.SetValue("/clear")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(ChatModel)
	// Should clear to just the first message.
	if len(cm.messages) != 1 {
		t.Errorf("messages = %d, want 1", len(cm.messages))
	}
}

func TestChatModelSlashSettings(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.textarea.SetValue("/settings")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(ChatModel)
	if !cm.showSettings {
		t.Error("/settings should show settings")
	}
}

func TestChatModelSlashHelp(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.messages = []chatMsg{{role: "system", content: "Welcome"}}
	m.textarea.SetValue("/help")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(ChatModel)
	// Help text should be appended as a system message.
	found := false
	for _, msg := range cm.messages {
		if strings.Contains(msg.content, "Commands:") {
			found = true
		}
	}
	if !found {
		t.Error("/help should add help message")
	}
}

func TestChatModelSlashNew(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.messages = []chatMsg{
		{role: "system", content: "Welcome"},
		{role: "user", content: "Hello"},
	}
	m.textarea.SetValue("/new")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(ChatModel)
	// Should have only the "New conversation" message.
	if len(cm.messages) != 1 {
		t.Errorf("messages = %d, want 1", len(cm.messages))
	}
	if !strings.Contains(cm.messages[0].content, "New conversation") {
		t.Errorf("content = %q", cm.messages[0].content)
	}
}

func TestChatModelSlashMemoriesNoMgr(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.memoryMgr = nil
	m.messages = []chatMsg{{role: "system", content: "Welcome"}}
	m.textarea.SetValue("/memories")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(ChatModel)
	found := false
	for _, msg := range cm.messages {
		if strings.Contains(msg.content, "unavailable") {
			found = true
		}
	}
	if !found {
		t.Error("/memories without manager should show unavailable")
	}
}

func TestChatModelSlashRememberNoMgr(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.memoryMgr = nil
	m.messages = []chatMsg{{role: "system", content: "Welcome"}}
	m.textarea.SetValue("/remember something important")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(ChatModel)
	found := false
	for _, msg := range cm.messages {
		if strings.Contains(msg.content, "unavailable") {
			found = true
		}
	}
	if !found {
		t.Error("/remember without manager should show unavailable")
	}
}

func TestChatModelSlashForgetNoMgr(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.memoryMgr = nil
	m.messages = []chatMsg{{role: "system", content: "Welcome"}}
	m.textarea.SetValue("/forget something")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(ChatModel)
	found := false
	for _, msg := range cm.messages {
		if strings.Contains(msg.content, "unavailable") {
			found = true
		}
	}
	if !found {
		t.Error("/forget without manager should show unavailable")
	}
}

func TestChatModelEnterEmpty(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	// Leave textarea empty.

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(ChatModel)
	// Should be a no-op: not quitting, not waiting.
	if cm.quitting {
		t.Error("empty enter should not quit")
	}
	if cm.waiting {
		t.Error("empty enter should not start waiting")
	}
}

func TestChatModelEnterWhileWaiting(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.waiting = true
	m.textarea.SetValue("test")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(ChatModel)
	// Should be a no-op while waiting.
	if !cm.waiting {
		t.Error("should still be waiting")
	}
}

// ── Stream messages ────────────────────────────────────────────

func TestChatModelStreamDone(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.waiting = true
	m.messages = []chatMsg{
		{role: "user", content: "hi"},
		{role: "assistant", content: "hello"},
	}

	updated, _ := m.Update(streamDoneMsg{})
	cm := updated.(ChatModel)
	if cm.waiting {
		t.Error("streamDone should clear waiting")
	}
}

func TestChatModelStreamError(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.waiting = true
	m.messages = []chatMsg{
		{role: "user", content: "hi"},
		{role: "assistant", content: "partial"},
	}

	updated, _ := m.Update(streamErrorMsg{err: os.ErrClosed})
	cm := updated.(ChatModel)
	if cm.waiting {
		t.Error("streamError should clear waiting")
	}
	// Should have removed incomplete assistant msg and added error msg.
	found := false
	for _, msg := range cm.messages {
		if strings.Contains(msg.content, "Error") {
			found = true
		}
	}
	if !found {
		t.Error("should have error message")
	}
}

func TestChatModelStreamToken(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.waiting = true
	m.messages = []chatMsg{
		{role: "user", content: "hi"},
		{role: "assistant", content: ""},
	}

	updated, _ := m.Update(streamTokenMsg{token: "hello "})
	cm := updated.(ChatModel)
	if cm.messages[1].content != "hello " {
		t.Errorf("content = %q", cm.messages[1].content)
	}
}

// ── Settings overlay Update ────────────────────────────────────

func TestChatModelUpdateSettingsNav(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.showSettings = true
	m.settingsCursor = 0
	m.llmModels = []string{"a", "b"}
	m.roleNames = []string{"(none)"}

	// Down.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	cm := updated.(ChatModel)
	if cm.settingsCursor != 1 {
		t.Errorf("cursor = %d, want 1", cm.settingsCursor)
	}

	// Up.
	updated, _ = cm.Update(tea.KeyPressMsg{Code: 'k'})
	cm = updated.(ChatModel)
	if cm.settingsCursor != 0 {
		t.Errorf("cursor = %d, want 0", cm.settingsCursor)
	}
}

func TestChatModelUpdateSettingsClose(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.showSettings = true
	m.llmModels = []string{"a"}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	cm := updated.(ChatModel)
	if cm.showSettings {
		t.Error("esc should close settings")
	}
}

func TestChatModelUpdateSettingsAdjust(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.showSettings = true
	m.settingsCursor = fieldTemperature
	m.temperature = 0.5
	m.llmModels = []string{"a"}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'l'}) // right = increase
	cm := updated.(ChatModel)
	if cm.temperature <= 0.5 {
		t.Errorf("temperature = %f, expected increase", cm.temperature)
	}
}

// ── History overlay Update ─────────────────────────────────────

func TestChatModelUpdateHistoryClose(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.showHistory = true

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	cm := updated.(ChatModel)
	if cm.showHistory {
		t.Error("esc should close history")
	}
}

func TestChatModelUpdateHistoryNav(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.showHistory = true
	m.historyItems = []conversations.Conversation{
		{ID: "1", Title: "First"},
		{ID: "2", Title: "Second"},
	}
	m.historyCursor = 0

	// Down.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	cm := updated.(ChatModel)
	if cm.historyCursor != 1 {
		t.Errorf("cursor = %d, want 1", cm.historyCursor)
	}

	// Up.
	updated, _ = cm.Update(tea.KeyPressMsg{Code: 'k'})
	cm = updated.(ChatModel)
	if cm.historyCursor != 0 {
		t.Errorf("cursor = %d, want 0", cm.historyCursor)
	}
}

// ── EscWithContent (clear input, not quit) ─────────────────────

func TestChatModelEscWithContent(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.textarea.SetValue("some text")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	cm := updated.(ChatModel)
	if cm.quitting {
		t.Error("esc with content should clear, not quit")
	}
	if cm.textarea.Value() != "" {
		t.Errorf("textarea = %q, should be cleared", cm.textarea.Value())
	}
}

// ── AnswerMsg fallback ─────────────────────────────────────────

func TestChatModelAnswerMsg(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.waiting = true

	updated, _ := m.Update(answerMsg{content: "LLM response"})
	cm := updated.(ChatModel)
	if cm.waiting {
		t.Error("answerMsg should clear waiting")
	}
	found := false
	for _, msg := range cm.messages {
		if msg.content == "LLM response" {
			found = true
		}
	}
	if !found {
		t.Error("should have LLM response message")
	}
}

func TestChatModelAnswerMsgError(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.ready = true
	m.waiting = true

	updated, _ := m.Update(answerMsg{err: os.ErrNotExist})
	cm := updated.(ChatModel)
	found := false
	for _, msg := range cm.messages {
		if strings.Contains(msg.content, "Error") {
			found = true
		}
	}
	if !found {
		t.Error("should have error message")
	}
}

// ── viewHistory with items ─────────────────────────────────────

func TestViewHistoryWithItems(t *testing.T) {
	chat := gleann.NewChat(nil, gleann.DefaultChatConfig())
	m := NewChatModel(chat, "idx", "mdl")
	m.width = 100
	m.height = 40
	m.historyItems = []conversations.Conversation{
		{ID: "abc123", Title: "Test conversation"},
	}
	m.historyCursor = 0

	out := m.viewHistory()
	if !strings.Contains(out, "Test conversation") {
		t.Error("should show conversation title")
	}
}
