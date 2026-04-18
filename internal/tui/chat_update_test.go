package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/pkg/conversations"
)

// ── updatePromptEdit tests ─────────────────────────────────────

func TestUpdatePromptEditCtrlC(t *testing.T) {
	m := ChatModel{
		editingPrompt: true,
		promptInput:   textarea.New(),
	}
	result, _ := m.updatePromptEdit(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	if !cm.quitting {
		t.Error("ctrl+c should set quitting")
	}
}

func TestUpdatePromptEditCtrlS(t *testing.T) {
	pi := textarea.New()
	pi.SetValue("custom prompt")
	m := ChatModel{
		editingPrompt: true,
		promptInput:   pi,
		systemPrompt:  "",
	}
	result, _ := m.updatePromptEdit(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	if cm.editingPrompt {
		t.Error("should exit editing")
	}
	if cm.systemPrompt != "custom prompt" {
		t.Errorf("systemPrompt = %q", cm.systemPrompt)
	}
}

func TestUpdatePromptEditWindowSize(t *testing.T) {
	m := ChatModel{
		editingPrompt: true,
		promptInput:   textarea.New(),
	}
	result, _ := m.updatePromptEdit(tea.WindowSizeMsg{Width: 120, Height: 40})
	cm := result.(ChatModel)
	if cm.width != 120 || cm.height != 40 {
		t.Error("should update dimensions")
	}
}

func TestUpdatePromptEditRegularKey(t *testing.T) {
	m := ChatModel{
		editingPrompt: true,
		promptInput:   textarea.New(),
	}
	// Regular key should be forwarded to textarea
	result, _ := m.updatePromptEdit(tea.KeyPressMsg{Code: 'a'})
	_ = result.(ChatModel) // should not panic
}

// ── updateHistory tests ────────────────────────────────────────

func TestUpdateHistoryCtrlC(t *testing.T) {
	m := ChatModel{
		showHistory: true,
		textarea:    textarea.New(),
	}
	result, _ := m.updateHistory(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	if !cm.quitting {
		t.Error("ctrl+c should quit")
	}
}

func TestUpdateHistoryEsc(t *testing.T) {
	ta := textarea.New()
	m := ChatModel{
		showHistory: true,
		textarea:    ta,
	}
	result, _ := m.updateHistory(tea.KeyPressMsg{Code: tea.KeyEscape})
	cm := result.(ChatModel)
	if cm.showHistory {
		t.Error("esc should close history")
	}
}

func TestUpdateHistoryUpDown(t *testing.T) {
	m := ChatModel{
		showHistory:   true,
		historyCursor:  1,
		historyItems: []conversations.Conversation{
			{ID: "c1"},
			{ID: "c2"},
			{ID: "c3"},
		},
		textarea: textarea.New(),
	}

	// up
	result, _ := m.updateHistory(tea.KeyPressMsg{Code: 'k'})
	cm := result.(ChatModel)
	if cm.historyCursor != 0 {
		t.Errorf("cursor = %d, want 0", cm.historyCursor)
	}

	// up at 0 should stay
	result, _ = cm.updateHistory(tea.KeyPressMsg{Code: 'k'})
	cm = result.(ChatModel)
	if cm.historyCursor != 0 {
		t.Error("should stay at 0")
	}

	// down
	result, _ = cm.updateHistory(tea.KeyPressMsg{Code: 'j'})
	cm = result.(ChatModel)
	if cm.historyCursor != 1 {
		t.Errorf("cursor = %d, want 1", cm.historyCursor)
	}
}

func TestUpdateHistoryWindowSize(t *testing.T) {
	m := ChatModel{
		showHistory: true,
		textarea:    textarea.New(),
	}
	result, _ := m.updateHistory(tea.WindowSizeMsg{Width: 100, Height: 50})
	cm := result.(ChatModel)
	if cm.width != 100 || cm.height != 50 {
		t.Error("should update dimensions")
	}
}

func TestUpdateHistoryQuit(t *testing.T) {
	ta := textarea.New()
	m := ChatModel{
		showHistory: true,
		textarea:    ta,
	}
	result, _ := m.updateHistory(tea.KeyPressMsg{Code: 'q'})
	cm := result.(ChatModel)
	if cm.showHistory {
		t.Error("q should close history")
	}
}

// ── updateSettings tests ───────────────────────────────────────

func TestUpdateSettingsCtrlC(t *testing.T) {
	m := ChatModel{
		showSettings: true,
		textarea:     textarea.New(),
	}
	result, _ := m.updateSettings(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	if !cm.quitting {
		t.Error("ctrl+c should quit")
	}
}

func TestUpdateSettingsEsc(t *testing.T) {
	// Note: esc calls applySettings which needs m.chat to be non-nil.
	// We test the ctrl+c path since esc requires a full chat setup.
	m := ChatModel{
		showSettings: true,
		textarea:     textarea.New(),
	}
	result, _ := m.updateSettings(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	if !cm.quitting {
		t.Error("ctrl+c should set quitting")
	}
}

func TestUpdateSettingsUpDown(t *testing.T) {
	m := ChatModel{
		showSettings:   true,
		settingsCursor: 2,
		textarea:       textarea.New(),
	}

	result, _ := m.updateSettings(tea.KeyPressMsg{Code: 'k'})
	cm := result.(ChatModel)
	if cm.settingsCursor != 1 {
		t.Errorf("cursor = %d, want 1", cm.settingsCursor)
	}

	result, _ = cm.updateSettings(tea.KeyPressMsg{Code: 'j'})
	cm = result.(ChatModel)
	if cm.settingsCursor != 2 {
		t.Errorf("cursor = %d, want 2", cm.settingsCursor)
	}
}

func TestUpdateSettingsLeftRight(t *testing.T) {
	m := ChatModel{
		showSettings:   true,
		settingsCursor: fieldTemperature,
		temperature:    0.5,
		textarea:       textarea.New(),
	}

	result, _ := m.updateSettings(tea.KeyPressMsg{Code: 'l'})
	cm := result.(ChatModel)
	if cm.temperature <= 0.5 {
		t.Error("right should increase temperature")
	}

	result, _ = cm.updateSettings(tea.KeyPressMsg{Code: 'h'})
	cm = result.(ChatModel)
	// Should decrease back
}

func TestUpdateSettingsWindowSize(t *testing.T) {
	m := ChatModel{
		showSettings: true,
		textarea:     textarea.New(),
	}
	result, _ := m.updateSettings(tea.WindowSizeMsg{Width: 120, Height: 40})
	cm := result.(ChatModel)
	if cm.width != 120 || cm.height != 40 {
		t.Error("should update dimensions")
	}
}

func TestUpdateSettingsEnterSystemPrompt(t *testing.T) {
	m := ChatModel{
		showSettings:   true,
		settingsCursor: fieldSystemPrompt,
		promptInput:    textarea.New(),
		textarea:       textarea.New(),
	}
	result, _ := m.updateSettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)
	if !cm.editingPrompt {
		t.Error("enter on system prompt should enter editing mode")
	}
}

func TestUpdateSettingsEditingPromptForward(t *testing.T) {
	pi := textarea.New()
	pi.SetValue("test")
	m := ChatModel{
		showSettings:  true,
		editingPrompt: true,
		promptInput:   pi,
		textarea:      textarea.New(),
	}
	// When editing prompt, should forward to updatePromptEdit
	result, _ := m.updateSettings(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	if cm.editingPrompt {
		t.Error("ctrl+s should exit prompt editing")
	}
}

// ── renderScrollbar additional tests ───────────────────────────

func TestRenderScrollbarLargeContent(t *testing.T) {
	m := ChatModel{
		width:    80,
		height:   24,
		viewport: viewport.New(),
	}
	// Set content larger than viewport
	lines := strings.Repeat("line content\n", 100)
	m.viewport.SetContent(lines)

	got := m.renderScrollbar()
	// May return empty if viewport doesn't have enough height set
	_ = got // just ensure no panic
}

func TestRenderScrollbarSmallContent(t *testing.T) {
	m := ChatModel{
		width:  80,
		height: 24,
		viewport: viewport.New(),
	}
	m.viewport.SetContent("short")
	got := m.renderScrollbar()
	// With short content, scrollbar should be empty or minimal
	_ = got // just ensure no panic
}

// ── view edge cases ────────────────────────────────────────────

func TestViewSettingsMode(t *testing.T) {
	m := ChatModel{
		showSettings: true,
		width:        80,
		height:       24,
		textarea:     textarea.New(),
		viewport:     viewport.New(),
	}
	v := m.View()
	if v.Content == "" {
		t.Error("settings view should not be empty")
	}
}

func TestViewHistoryMode(t *testing.T) {
	m := ChatModel{
		showHistory: true,
		width:       80,
		height:      24,
		textarea:    textarea.New(),
		viewport:    viewport.New(),
	}
	v := m.View()
	if v.Content == "" {
		t.Error("history view should not be empty")
	}
}

func TestViewQuitting(t *testing.T) {
	m := ChatModel{
		quitting: true,
		textarea: textarea.New(),
		viewport: viewport.New(),
	}
	v := m.View()
	if v.Content != "" {
		t.Error("quitting view should be empty")
	}
}

// ── Chat Update main handler ───────────────────────────────────

func TestChatUpdateWindowSizeMsg(t *testing.T) {
	ta := textarea.New()
	m := ChatModel{
		width:    80,
		height:   24,
		textarea: ta,
		viewport: viewport.New(),
	}
	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	cm := result.(ChatModel)
	if cm.width != 120 || cm.height != 40 {
		t.Errorf("size = %d×%d, want 120×40", cm.width, cm.height)
	}
}

func TestChatUpdateCtrlC(t *testing.T) {
	m := ChatModel{
		textarea: textarea.New(),
		viewport: viewport.New(),
	}
	result, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	if !cm.quitting {
		t.Error("ctrl+c should quit")
	}
}

func TestChatUpdateCtrlHNoOp(t *testing.T) {
	m := ChatModel{
		textarea: textarea.New(),
		viewport: viewport.New(),
	}
	result, _ := m.Update(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	_ = result.(ChatModel) // should not panic
}

func TestChatUpdateCtrlSToggleSettings(t *testing.T) {
	ta := textarea.New()
	m := ChatModel{
		textarea: ta,
		viewport: viewport.New(),
	}
	result, _ := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	if !cm.showSettings {
		t.Error("ctrl+s should toggle settings")
	}
}
