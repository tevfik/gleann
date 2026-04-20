package tui

import (
	"fmt"
	"strings"
	"testing"

	"path/filepath"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// newTestChatModel creates a minimal ChatModel for testing.
func newTestChatModel(t *testing.T) ChatModel {
	t.Helper()
	ta := textarea.New()
	vp := viewport.New()
	sp := spinner.New()
	return ChatModel{
		textarea:        ta,
		viewport:        vp,
		spinner:         sp,
		width:           80,
		height:          40,
		ready:           true,
		messages:        []chatMsg{{role: "system", content: "Welcome"}},
		indexName:       "test-index",
		modelName:       "test-model",
		streamingAnswer: &strings.Builder{},
	}
}

// ── streamTokenMsg ─────────────────────────────────────────────

func TestChatUpdateStreamToken(t *testing.T) {
	m := newTestChatModel(t)
	m.waiting = true
	m.messages = append(m.messages, chatMsg{role: "assistant", content: ""})
	ch := make(chan string, 10)
	m.streamChan = ch

	result, _ := m.Update(streamTokenMsg{token: "hello"})
	cm := result.(ChatModel)

	if !cm.waiting {
		t.Error("should still be waiting")
	}
	lastMsg := cm.messages[len(cm.messages)-1]
	if lastMsg.content != "hello" {
		t.Errorf("expected 'hello', got %q", lastMsg.content)
	}
}

func TestChatUpdateStreamTokenNotWaiting(t *testing.T) {
	m := newTestChatModel(t)
	m.waiting = false

	result, _ := m.Update(streamTokenMsg{token: "orphan"})
	cm := result.(ChatModel)
	// Should not crash or append; just ignore
	if cm.waiting {
		t.Error("should not be waiting")
	}
}

// ── streamDoneMsg ──────────────────────────────────────────────

func TestChatUpdateStreamDone(t *testing.T) {
	m := newTestChatModel(t)
	m.waiting = true
	ch := make(chan string)
	m.streamChan = ch

	result, _ := m.Update(streamDoneMsg{})
	cm := result.(ChatModel)

	if cm.waiting {
		t.Error("should not be waiting after done")
	}
	if cm.streamChan != nil {
		t.Error("streamChan should be nil")
	}
}

// ── streamErrorMsg ─────────────────────────────────────────────

func TestChatUpdateStreamError(t *testing.T) {
	m := newTestChatModel(t)
	m.waiting = true
	m.messages = append(m.messages, chatMsg{role: "assistant", content: "partial"})

	result, _ := m.Update(streamErrorMsg{err: fmt.Errorf("connection lost")})
	cm := result.(ChatModel)

	if cm.waiting {
		t.Error("should not be waiting after error")
	}
	// Should have removed the partial assistant message and added error
	found := false
	for _, msg := range cm.messages {
		if msg.role == "system" && strings.Contains(msg.content, "connection lost") {
			found = true
		}
	}
	if !found {
		t.Error("expected error message in messages")
	}
}

// ── answerMsg (fallback non-streaming) ─────────────────────────

func TestChatUpdateAnswerMsg(t *testing.T) {
	m := newTestChatModel(t)
	m.waiting = true

	result, _ := m.Update(answerMsg{content: "LLM response"})
	cm := result.(ChatModel)

	if cm.waiting {
		t.Error("should not be waiting")
	}
	last := cm.messages[len(cm.messages)-1]
	if last.role != "assistant" || last.content != "LLM response" {
		t.Errorf("unexpected last message: %+v", last)
	}
}

func TestChatUpdateAnswerMsgError(t *testing.T) {
	m := newTestChatModel(t)
	m.waiting = true

	result, _ := m.Update(answerMsg{err: fmt.Errorf("model not found")})
	cm := result.(ChatModel)

	if cm.waiting {
		t.Error("should not be waiting")
	}
	found := false
	for _, msg := range cm.messages {
		if msg.role == "system" && strings.Contains(msg.content, "model not found") {
			found = true
		}
	}
	if !found {
		t.Error("expected error message")
	}
}

// ── spinner.TickMsg ────────────────────────────────────────────

func TestChatUpdateSpinnerTickWhileWaiting(t *testing.T) {
	m := newTestChatModel(t)
	m.waiting = true

	// Spinner tick while waiting should update spinner
	result, _ := m.Update(m.spinner.Tick())
	_ = result.(ChatModel) // should not panic
}

func TestChatUpdateSpinnerTickNotWaiting(t *testing.T) {
	m := newTestChatModel(t)
	m.waiting = false

	// Spinner tick while not waiting should be ignored
	result, _ := m.Update(spinner.TickMsg{})
	_ = result.(ChatModel) // should not panic
}

// ── Slash commands: /new ────────────────────────────────────────

func TestChatSlashNew(t *testing.T) {
	m := newTestChatModel(t)
	m.chat = gleann.NewChat(gleann.NullSearcher{}, gleann.DefaultChatConfig())
	m.messages = []chatMsg{
		{role: "system", content: "Welcome"},
		{role: "user", content: "old question"},
		{role: "assistant", content: "old answer"},
	}
	m.textarea.SetValue("/new")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	if len(cm.messages) != 1 {
		t.Errorf("expected 1 message after /new, got %d", len(cm.messages))
	}
	if !strings.Contains(cm.messages[0].content, "New conversation started") {
		t.Error("expected new conversation message")
	}
}

// ── Slash commands: /clear ──────────────────────────────────────

func TestChatSlashClearMultiMsg(t *testing.T) {
	m := newTestChatModel(t)
	m.chat = gleann.NewChat(gleann.NullSearcher{}, gleann.DefaultChatConfig())
	m.messages = []chatMsg{
		{role: "system", content: "sys"},
		{role: "user", content: "q1"},
		{role: "assistant", content: "a1"},
		{role: "user", content: "q2"},
	}
	m.textarea.SetValue("/clear")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	if len(cm.messages) != 1 {
		t.Errorf("expected 1 message after /clear, got %d", len(cm.messages))
	}
}

// ── Slash commands: /help ───────────────────────────────────────

func TestChatSlashHelpContent(t *testing.T) {
	m := newTestChatModel(t)
	m.textarea.SetValue("/help")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	last := cm.messages[len(cm.messages)-1]
	if !strings.Contains(last.content, "/clear") || !strings.Contains(last.content, "/settings") {
		t.Error("help should mention /clear and /settings")
	}
}

// ── Slash commands: /history (no conversations) ─────────────────

// /history when no conversations exist - uses DefaultStore which may have convos
// Just verify it doesn't crash and returns a valid model
func TestChatSlashHistoryEmpty(t *testing.T) {
	m := newTestChatModel(t)
	m.textarea.SetValue("/history")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	// It either opens history view (if convos exist) or shows "no conversations"
	_ = cm // no crash is success
}

// ── Slash commands: /remember with memoryMgr ────────────────────

func TestChatSlashRememberWithMgr(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	store, err := memory.OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mgr := memory.NewManager(store)

	m := newTestChatModel(t)
	m.memoryMgr = mgr
	m.textarea.SetValue("/remember Go uses goroutines for concurrency")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	last := cm.messages[len(cm.messages)-1]
	if !strings.Contains(last.content, "Remembered") {
		t.Errorf("expected remember confirmation, got %q", last.content)
	}
}

// /remember with no manager shows unavailable message
func TestChatSlashRememberNoMgrCov(t *testing.T) {
	m := newTestChatModel(t)
	m.memoryMgr = nil
	m.textarea.SetValue("/remember some fact")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	last := cm.messages[len(cm.messages)-1]
	if !strings.Contains(last.content, "unavailable") {
		t.Errorf("expected unavailable message, got %q", last.content)
	}
}

// ── Slash commands: /forget with memoryMgr ──────────────────────

func TestChatSlashForgetWithMgr(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	store, err := memory.OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mgr := memory.NewManager(store)

	// Remember something first.
	mgr.Remember("test fact")

	m := newTestChatModel(t)
	m.memoryMgr = mgr
	m.textarea.SetValue("/forget test fact")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	last := cm.messages[len(cm.messages)-1]
	if !strings.Contains(last.content, "Forgot") {
		t.Errorf("expected forget confirmation, got %q", last.content)
	}
}

// /forget with no manager shows unavailable message
func TestChatSlashForgetNoMgrCov(t *testing.T) {
	m := newTestChatModel(t)
	m.memoryMgr = nil
	m.textarea.SetValue("/forget some query")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	last := cm.messages[len(cm.messages)-1]
	if !strings.Contains(last.content, "unavailable") {
		t.Errorf("expected unavailable message, got %q", last.content)
	}
}

// ── Slash commands: /memories with memoryMgr ────────────────────

func TestChatSlashMemoriesWithMgr(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	store, err := memory.OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mgr := memory.NewManager(store)

	mgr.Remember("architecture uses hexagonal pattern")

	m := newTestChatModel(t)
	m.memoryMgr = mgr
	m.textarea.SetValue("/memories")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	last := cm.messages[len(cm.messages)-1]
	if !strings.Contains(last.content, "Memories") {
		t.Errorf("expected memories list, got %q", last.content)
	}
}

func TestChatSlashMemoriesEmpty(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	store, err := memory.OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mgr := memory.NewManager(store)

	m := newTestChatModel(t)
	m.memoryMgr = mgr
	m.textarea.SetValue("/memories")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	last := cm.messages[len(cm.messages)-1]
	if !strings.Contains(last.content, "No memories stored") {
		t.Errorf("expected 'no memories', got %q", last.content)
	}
}

// ── Settings: left/right to adjust ──────────────────────────────

func TestUpdateSettingsLeftRightCov(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true
	m.settingsCursor = 0 // temperature

	result, _ := m.updateSettings(tea.KeyPressMsg{Code: 'l'})
	cm := result.(ChatModel)
	// Should adjust setting right (increase)
	_ = cm // just verify no panic

	result, _ = cm.updateSettings(tea.KeyPressMsg{Code: 'h'})
	cm = result.(ChatModel)
	// Should adjust setting left (decrease)
	_ = cm
}

func TestUpdateSettingsEnterOnPromptField(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true
	m.settingsCursor = fieldSystemPrompt
	m.promptInput = textarea.New()

	result, _ := m.updateSettings(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	if !cm.editingPrompt {
		t.Error("enter on system prompt field should enter editing mode")
	}
}

func TestUpdateSettingsWindowSizeCov(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true

	result, _ := m.updateSettings(tea.WindowSizeMsg{Width: 200, Height: 60})
	cm := result.(ChatModel)

	if cm.width != 200 || cm.height != 60 {
		t.Error("should update dimensions")
	}
}

// ── Routing through showSettings/showHistory ────────────────────

func TestChatUpdateRoutesToSettings(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true

	result, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	if !cm.quitting {
		t.Error("should route to updateSettings → quit")
	}
}

func TestChatUpdateRoutesToHistory(t *testing.T) {
	m := newTestChatModel(t)
	m.showHistory = true

	result, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	cm := result.(ChatModel)
	if !cm.quitting {
		t.Error("should route to updateHistory → quit")
	}
}

// ── Esc with non-empty textarea (clear) vs empty (quit) ────────

func TestChatUpdateEscClearsTextarea(t *testing.T) {
	m := newTestChatModel(t)
	m.textarea.SetValue("some text")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	cm := result.(ChatModel)

	if cm.quitting {
		t.Error("should not quit when textarea has content")
	}
	if cm.textarea.Value() != "" {
		t.Error("textarea should be cleared")
	}
}

func TestChatUpdateEscQuitsWhenEmpty(t *testing.T) {
	m := newTestChatModel(t)
	m.textarea.SetValue("")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	cm := result.(ChatModel)

	if !cm.quitting {
		t.Error("should quit when textarea is empty")
	}
}

// ── Enter while waiting does nothing ────────────────────────────

func TestChatUpdateEnterWhileWaitingIsNoop(t *testing.T) {
	m := newTestChatModel(t)
	m.waiting = true
	m.textarea.SetValue("hello")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	if !cm.waiting {
		t.Error("should still be waiting")
	}
}

// ── Ctrl+U clears input ─────────────────────────────────────────

func TestChatUpdateCtrlUClearsInput(t *testing.T) {
	m := newTestChatModel(t)
	m.textarea.SetValue("some query")

	result, _ := m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	cm := result.(ChatModel)

	if cm.textarea.Value() != "" {
		t.Error("ctrl+u should clear input")
	}
}

// ── PageUp/PageDown ─────────────────────────────────────────────

func TestChatUpdatePageUpDown(t *testing.T) {
	m := newTestChatModel(t)

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	_ = result.(ChatModel) // no panic

	result, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	_ = result.(ChatModel) // no panic
}

// ── updateHistory: delete conversation ──────────────────────────

func TestUpdateHistoryDelete(t *testing.T) {
	// Create conversations store in temp dir
	dir := t.TempDir()
	store := conversations.NewStore(dir)

	conv := &conversations.Conversation{
		ID:    "test-conv-1",
		Title: "Test",
		Messages: []conversations.Message{
			{Role: "user", Content: "hello"},
		},
	}
	store.Save(conv)

	items, _ := store.List()

	m := ChatModel{
		showHistory:     true,
		historyCursor:   0,
		historyItems:    items,
		textarea:        textarea.New(),
		viewport:        viewport.New(),
		messages:        []chatMsg{{role: "system", content: "Welcome"}},
		streamingAnswer: &strings.Builder{},
	}

	// Delete the conversation — note: uses DefaultStore not our temp store
	// so the delete may fail. Just verify no crash.
	result, _ := m.updateHistory(tea.KeyPressMsg{Code: 'd'})
	_ = result.(ChatModel)
}

// ── renderScrollbar edge cases ──────────────────────────────────

func TestRenderScrollbarTallContent(t *testing.T) {
	m := newTestChatModel(t)
	m.height = 20
	m.viewport.SetContent(strings.Repeat("line\n", 100))
	// Force viewport to have smaller height than content
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(10)

	s := m.renderScrollbar()
	// May or may not produce output depending on viewport state
	_ = s // no crash is success
}

func TestRenderScrollbarShortContent(t *testing.T) {
	m := newTestChatModel(t)
	m.height = 40
	m.viewport.SetContent("short")

	s := m.renderScrollbar()
	// Short content may still produce scrollbar with bars
	_ = s // no panic
}

// ── viewSettings ────────────────────────────────────────────────

func TestViewSettingsContent(t *testing.T) {
	m := newTestChatModel(t)
	m.showSettings = true
	m.modelName = "llama3.2"
	m.settingsCursor = 0

	s := m.viewSettings()
	if !strings.Contains(s, "Temperature") {
		t.Error("should show Temperature field")
	}
}

// ── Chat Update with /settings ──────────────────────────────────

func TestChatSlashSettingsOpensPanel(t *testing.T) {
	m := newTestChatModel(t)
	m.textarea.SetValue("/settings")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	if !cm.showSettings {
		t.Error("should open settings")
	}
}

// ── Chat Update with /quit ──────────────────────────────────────

func TestChatSlashQuitExits(t *testing.T) {
	m := newTestChatModel(t)
	m.textarea.SetValue("/quit")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	if !cm.quitting {
		t.Error("should quit")
	}
}

func TestChatSlashExitExits(t *testing.T) {
	m := newTestChatModel(t)
	m.textarea.SetValue("/exit")

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := result.(ChatModel)

	if !cm.quitting {
		t.Error("should quit")
	}
}

// ── Chat Update with Ctrl+S toggles settings ────────────────────

func TestChatCtrlSOpensSettings(t *testing.T) {
	m := newTestChatModel(t)

	result, _ := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	cm := result.(ChatModel)

	if !cm.showSettings {
		t.Error("ctrl+s should open settings")
	}
}
