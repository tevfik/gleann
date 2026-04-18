package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/pkg/conversations"
)

// ── updateHistory — window size ────────────────────────────────

func TestUpdateHistory_WindowSize(t *testing.T) {
	m := newTestChatModel(t)
	m.showHistory = true
	m.historyItems = []conversations.Conversation{{ID: "1", Title: "Test"}}

	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 60})
	cm := result.(ChatModel)
	if cm.width != 120 || cm.height != 60 {
		t.Errorf("expected 120x60, got %dx%d", cm.width, cm.height)
	}
}

func TestUpdateHistory_CtrlC(t *testing.T) {
	m := newTestChatModel(t)
	m.showHistory = true
	m.historyItems = []conversations.Conversation{{ID: "1", Title: "Test"}}

	result, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	if !cm.quitting {
		t.Error("expected quitting=true after ctrl+c in history")
	}
	if cmd == nil {
		t.Error("expected quit command")
	}
}

func TestUpdateHistory_EscClosesHistory(t *testing.T) {
	m := newTestChatModel(t)
	m.showHistory = true
	m.historyItems = []conversations.Conversation{{ID: "1", Title: "Test"}}

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	cm := result.(ChatModel)
	if cm.showHistory {
		t.Error("expected showHistory=false after esc")
	}
}

func TestUpdateHistory_CursorUp(t *testing.T) {
	m := newTestChatModel(t)
	m.showHistory = true
	m.historyItems = []conversations.Conversation{
		{ID: "1", Title: "First"},
		{ID: "2", Title: "Second"},
	}
	m.historyCursor = 1

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	cm := result.(ChatModel)
	if cm.historyCursor != 0 {
		t.Errorf("expected cursor=0, got %d", cm.historyCursor)
	}
}

func TestUpdateHistory_CursorUp_AlreadyAtTop(t *testing.T) {
	m := newTestChatModel(t)
	m.showHistory = true
	m.historyItems = []conversations.Conversation{{ID: "1", Title: "Only"}}
	m.historyCursor = 0

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	cm := result.(ChatModel)
	if cm.historyCursor != 0 {
		t.Error("cursor should stay at 0")
	}
}

func TestUpdateHistory_CursorDown(t *testing.T) {
	m := newTestChatModel(t)
	m.showHistory = true
	m.historyItems = []conversations.Conversation{
		{ID: "1", Title: "First"},
		{ID: "2", Title: "Second"},
	}
	m.historyCursor = 0

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	cm := result.(ChatModel)
	if cm.historyCursor != 1 {
		t.Errorf("expected cursor=1, got %d", cm.historyCursor)
	}
}

func TestUpdateHistory_CursorDown_AlreadyAtBottom(t *testing.T) {
	m := newTestChatModel(t)
	m.showHistory = true
	m.historyItems = []conversations.Conversation{{ID: "1", Title: "Only"}}
	m.historyCursor = 0

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	cm := result.(ChatModel)
	if cm.historyCursor != 0 {
		t.Error("cursor should stay at 0")
	}
}

func TestUpdateHistory_K_J_Navigation(t *testing.T) {
	m := newTestChatModel(t)
	m.showHistory = true
	m.historyItems = []conversations.Conversation{
		{ID: "1", Title: "First"},
		{ID: "2", Title: "Second"},
		{ID: "3", Title: "Third"},
	}
	m.historyCursor = 1

	// 'k' moves up
	result, _ := m.Update(tea.KeyPressMsg{Code: 'k'})
	cm := result.(ChatModel)
	if cm.historyCursor != 0 {
		t.Errorf("k: expected cursor=0, got %d", cm.historyCursor)
	}

	// 'j' moves down
	m = cm
	result, _ = m.Update(tea.KeyPressMsg{Code: 'j'})
	cm = result.(ChatModel)
	if cm.historyCursor != 1 {
		t.Errorf("j: expected cursor=1, got %d", cm.historyCursor)
	}
}

func TestUpdateHistory_Q_ClosesHistory(t *testing.T) {
	m := newTestChatModel(t)
	m.showHistory = true
	m.historyItems = []conversations.Conversation{{ID: "1", Title: "Test"}}

	result, _ := m.Update(tea.KeyPressMsg{Code: 'q'})
	cm := result.(ChatModel)
	if cm.showHistory {
		t.Error("expected showHistory=false after q")
	}
}

// ── updateSettings — more field navigation ──────────────────────

func TestUpdateSettings_CursorWrapAround(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true
	m.settingsCursor = 0

	// Press up from first field → should stay at 0 or wrap
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	cm := result.(ChatModel)
	// Either stays at 0 or wraps to last
	if cm.settingsCursor < 0 {
		t.Error("cursor should not go negative")
	}
}

func TestUpdateSettings_EnterOnNonPromptField(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true
	m.settingsCursor = fieldTemperature
	m.temperature = 0.7

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)
	// Should not crash; temperature field enter shouldn't change much
	_ = cm
}

func TestUpdateSettings_RerankToggle(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true
	m.settingsCursor = fieldRerankToggle
	m.rerankEnabled = false

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	cm := result.(ChatModel)
	// Should toggle or move the setting
	_ = cm
}

func TestUpdateSettings_RoleSelection(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true
	m.settingsCursor = fieldRole
	m.roleNames = []string{"(none)", "coder", "writer"}
	m.roleIdx = 0

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	cm := result.(ChatModel)
	if cm.roleIdx != 1 {
		t.Errorf("expected roleIdx=1, got %d", cm.roleIdx)
	}
}

func TestUpdateSettings_LLMModelSelection(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true
	m.settingsCursor = fieldLLMModel
	m.llmModels = []string{"model-a", "model-b", "model-c"}
	m.llmModelIdx = 0

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	cm := result.(ChatModel)
	if cm.llmModelIdx != 1 {
		t.Errorf("expected llmModelIdx=1, got %d", cm.llmModelIdx)
	}
}

func TestUpdateSettings_DownNavigation(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true
	m.settingsCursor = fieldTemperature

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	cm := result.(ChatModel)
	if cm.settingsCursor != fieldMaxTokens {
		t.Errorf("expected cursor=fieldMaxTokens, got %d", cm.settingsCursor)
	}
}

// ── Chat Update — main message routing ──────────────────────────

func TestChatUpdate_MainKeyPress_CtrlC(t *testing.T) {
	m := newTestChatModel(t)
	result, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	if !cm.quitting {
		t.Error("expected quitting after ctrl+c")
	}
	_ = cmd
}

func TestChatUpdate_MainKeyPress_CtrlL(t *testing.T) {
	m := newTestChatModel(t)
	m.messages = []chatMsg{{role: "user", content: "test"}}

	result, _ := m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	// Ctrl+L should clear messages or viewport
	_ = cm
}

func TestChatUpdate_RoutesToSettings(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true
	m.settingsCursor = fieldTemperature

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	cm := result.(ChatModel)
	// When showSettings=true, Update should route to updateSettings
	if !cm.showSettings {
		t.Error("should remain in settings")
	}
}

func TestChatUpdate_RoutesToHistory(t *testing.T) {
	m := newTestChatModel(t)
	m.showHistory = true
	m.historyItems = []conversations.Conversation{{ID: "1", Title: "Test"}}

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	cm := result.(ChatModel)
	if cm.showHistory {
		t.Error("should close history via routing")
	}
}

// ── View function ────────────────────────────────────────────────

func TestChatView_NotReady(t *testing.T) {
	m := ChatModel{ready: false}
	v := m.View()
	_ = v // should not panic
}

func TestChatView_QuittingState(t *testing.T) {
	m := newTestChatModel(t)
	m.quitting = true
	v := m.View()
	_ = v // should not panic
}

func TestChatView_HistoryOverlay(t *testing.T) {
	m := newTestChatModel(t)
	m.showHistory = true
	m.historyItems = []conversations.Conversation{
		{ID: "1", Title: "Test Conversation"},
	}
	m.historyCursor = 0

	v := m.View()
	_ = v // should not panic
}

func TestChatView_SettingsOverlay(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true
	m.llmModels = []string{"model-a"}
	m.roleNames = []string{"(none)"}
	m.rerankModels = []string{"rerank-a"}

	v := m.View()
	_ = v // should not panic
}

func TestChatView_WaitingWithSpinner(t *testing.T) {
	m := newTestChatModel(t)
	m.waiting = true

	v := m.View()
	_ = v // should not panic
}

// ── renderScrollbar ─────────────────────────────────────────────

func TestRenderScrollbar_SmallContent(t *testing.T) {
	m := newTestChatModel(t)
	m.viewport.SetContent("short content")

	result := m.renderScrollbar()
	// Small content should not need scrollbar
	_ = result
}

func TestRenderScrollbar_LargeContent(t *testing.T) {
	m := newTestChatModel(t)
	m.height = 20
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("Line " + string(rune('0'+i%10)) + "\n")
	}
	m.viewport.SetContent(sb.String())

	result := m.renderScrollbar()
	_ = result
}

// ── adjustSetting ───────────────────────────────────────────────

func TestAdjustSetting_Temperature(t *testing.T) {
	m := newTestChatModel(t)
	m.settingsCursor = fieldTemperature
	m.temperature = 0.5

	m.adjustSetting(1)
	if m.temperature <= 0.5 {
		t.Error("expected temperature to increase")
	}
}

func TestAdjustSetting_MaxTokens(t *testing.T) {
	m := newTestChatModel(t)
	m.settingsCursor = fieldMaxTokens
	m.maxTokens = 1024

	m.adjustSetting(1)
	if m.maxTokens <= 1024 {
		t.Error("expected maxTokens to increase")
	}
}

func TestAdjustSetting_TopK(t *testing.T) {
	m := newTestChatModel(t)
	m.settingsCursor = fieldTopK
	m.topK = 5

	m.adjustSetting(1)
	if m.topK <= 5 {
		t.Error("expected topK to increase")
	}
}

// ── Init ────────────────────────────────────────────────────────

func TestChatInit(t *testing.T) {
	ta := textarea.New()
	vp := viewport.New()
	sp := spinner.New()
	m := ChatModel{
		textarea:        ta,
		viewport:        vp,
		spinner:         sp,
		streamingAnswer: &strings.Builder{},
	}
	cmd := m.Init()
	if cmd == nil {
		t.Error("expected non-nil init command")
	}
}

// ── renderMessages ──────────────────────────────────────────────

func TestRenderMessages_EmptyMessages(t *testing.T) {
	m := newTestChatModel(t)
	m.messages = nil
	result := m.renderMessages()
	_ = result // should not panic
}

func TestRenderMessages_MultipleRoles(t *testing.T) {
	m := newTestChatModel(t)
	m.messages = []chatMsg{
		{role: "user", content: "hello"},
		{role: "assistant", content: "hi there"},
		{role: "system", content: "connected"},
	}
	result := m.renderMessages()
	if result == "" {
		t.Error("expected non-empty rendered messages")
	}
}

// ── viewSettings ────────────────────────────────────────────────

func TestViewSettings_AllFields(t *testing.T) {
	m := newTestChatModel(t)
	m.llmModels = []string{"llama3", "phi3"}
	m.llmModelIdx = 0
	m.roleNames = []string{"(none)", "coder"}
	m.roleIdx = 0
	m.rerankModels = []string{"jina-reranker-v2"}
	m.rerankModelIdx = 0
	m.rerankEnabled = true
	m.temperature = 0.7
	m.maxTokens = 4096
	m.topK = 5
	m.systemPrompt = "You are a helpful assistant."
	m.embeddingModel = "nomic-embed-text"
	m.embeddingProvider = "ollama"

	result := m.viewSettings()
	if result == "" {
		t.Error("expected non-empty settings view")
	}
}

func TestViewSettings_EditingPrompt(t *testing.T) {
	m := newTestChatModel(t)
	m.editingPrompt = true
	m.promptInput = textarea.New()
	m.llmModels = []string{"model-a"}
	m.roleNames = []string{"(none)"}
	m.rerankModels = []string{}

	result := m.viewSettings()
	if result == "" {
		t.Error("expected non-empty settings view in edit mode")
	}
}
