package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// h2cHome isolates HOME and the config, conversation and memory locations.
func h2cHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_CONFIG_DIR", "")
	t.Setenv("GLEANN_CONVERSATIONS_DIR", filepath.Join(home, "convs"))
	t.Setenv("GLEANN_CONTEXT_TOKENS", "")
	return home
}

// h2cMockChat is a chat on the mock LLM over a searcher with fixed results.
func h2cMockChat(results ...gleann.SearchResult) *gleann.LeannChat {
	return gleann.NewChat(fakeSearcher{results}, gleann.ChatConfig{Provider: gleann.LLMMock, Model: "mock"})
}

// h2cOllamaTags serves GET /api/tags with the given model names.
func h2cOllamaTags(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		var resp ollamaTagsResponse
		for _, n := range names {
			resp.Models = append(resp.Models, ollamaModel{Name: n})
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func h2cKey(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "delete":
		return tea.KeyPressMsg{Code: tea.KeyDelete}
	}
	if strings.HasPrefix(s, "ctrl+") {
		return tea.KeyPressMsg{Code: rune(s[len(s)-1]), Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func h2cYOff(m tea.Model) int {
	cm := m.(ChatModel)
	return cm.viewport.YOffset()
}

func h2cLast(m ChatModel) string {
	if len(m.messages) == 0 {
		return ""
	}
	return m.messages[len(m.messages)-1].content
}

// h2cSlash types a slash command into the chat and presses Enter.
func h2cSlash(t *testing.T, m ChatModel, input string) ChatModel {
	t.Helper()
	m.textarea.SetValue(input)
	res, _ := m.Update(h2cKey("enter"))
	return res.(ChatModel)
}

func TestH2cNewChatModelUsesSavedProviderAndSettings(t *testing.T) {
	h2cHome(t)
	t.Setenv("GLEANN_TEST_MODE", "") // take the real model-fetch and memory paths
	srv := h2cOllamaTags(t, "llama3:8b", "nomic-embed-text", "mock")
	if err := SaveConfig(OnboardResult{
		LLMProvider:    "ollama",
		OllamaHost:     srv.URL,
		SystemPrompt:   "be terse",
		Temperature:    0.3,
		RepeatPenalty:  1.3,
		MaxTokens:      256,
		TopK:           7,
		RerankEnabled:  true,
		EmbeddingModel: "embedder",
		Roles:          map[string][]string{"pirate": {"talk like a pirate"}, "code": {"dup of a builtin?"}},
	}); err != nil {
		t.Fatal(err)
	}
	m := NewChatModel(h2cMockChat(), "idx", "mock")
	if m.memoryMgr == nil {
		t.Fatal("outside test mode the memory manager must be opened")
	}
	t.Cleanup(func() { m.memoryMgr.Close() })

	// The current model stays first, embedders are filtered out, the current
	// model is not listed twice.
	if got := strings.Join(m.llmModels, ","); got != "mock,llama3:8b" {
		t.Errorf("llmModels = %q", got)
	}
	if m.systemPrompt != "be terse" || m.temperature != 0.3 || m.repeatPenalty != 1.3 || m.maxTokens != 256 || m.topK != 7 {
		t.Errorf("saved settings not restored: %+v", m)
	}
	if !m.rerankEnabled || m.embeddingModel != "embedder" {
		t.Errorf("rerank/embedding not restored: %v %q", m.rerankEnabled, m.embeddingModel)
	}
	cfg := m.chat.Config()
	if cfg.SystemPrompt != "be terse" || cfg.Temperature != 0.3 || cfg.MaxTokens != 256 {
		t.Errorf("settings must reach the chat engine: %+v", cfg)
	}
	count := map[string]int{}
	for _, r := range m.roleNames {
		count[r]++
	}
	if count["pirate"] != 1 {
		t.Errorf("custom role missing: %v", m.roleNames)
	}
	for r, n := range count {
		if n > 1 {
			t.Errorf("role %q listed %d times", r, n)
		}
	}
}

func TestH2cNewChatModelProviderKeysAndFallback(t *testing.T) {
	// openai lists the models of OpenAIBaseURL with the OpenAI key; an
	// unsupported provider (anthropic has no model listing) falls back to the
	// built-in defaults without touching the network.
	var gotAuth string
	openai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-x"}]}`))
	}))
	t.Cleanup(openai.Close)
	for _, tc := range []struct {
		name string
		cfg  OnboardResult
		want string
	}{
		{"openai", OnboardResult{LLMProvider: "openai", OpenAIBaseURL: openai.URL, OpenAIKey: "sk-test"}, "mock,gpt-x"},
		{"anthropic", OnboardResult{LLMProvider: "anthropic", AnthropicKey: "ak"}, "mock,nemotron-3-nano:4b,phi-4:14b,qwen2.5:32b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h2cHome(t)
			t.Setenv("GLEANN_TEST_MODE", "")
			if err := SaveConfig(tc.cfg); err != nil {
				t.Fatal(err)
			}
			m := NewChatModel(h2cMockChat(), "idx", "mock")
			if m.memoryMgr != nil {
				t.Cleanup(func() { m.memoryMgr.Close() })
			}
			if got := strings.Join(m.llmModels, ","); got != tc.want {
				t.Errorf("llmModels = %q, want %q", got, tc.want)
			}
		})
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("openai listing must send the OpenAI key, got %q", gotAuth)
	}
}

func TestH2cUpdateMouseScrollAndToggle(t *testing.T) {
	m := newTestChatModel(t)
	m.viewport.SetWidth(40)
	m.viewport.SetHeight(3)
	m.viewport.SetContent(strings.Repeat("line\n", 50))
	m.viewport.GotoBottom()
	bottom := m.viewport.YOffset()

	// Mouse capture off: the wheel does not scroll (native selection mode).
	m.mouseEnabled = false
	res, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if h2cYOff(res) != bottom {
		t.Error("wheel must be ignored with mouse capture off")
	}
	res, _ = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft})
	if h2cYOff(res) != bottom {
		t.Error("click must not move the viewport")
	}

	// ctrl+m turns capture on and says so.
	res, _ = m.Update(h2cKey("ctrl+m"))
	m = res.(ChatModel)
	if !m.mouseEnabled || !strings.Contains(h2cLast(m), "Mouse ON") {
		t.Fatalf("ctrl+m must enable the mouse: %v %q", m.mouseEnabled, h2cLast(m))
	}
	m.viewport.SetContent(strings.Repeat("line\n", 50))
	m.viewport.GotoBottom()
	bottom = m.viewport.YOffset()
	res, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	m = res.(ChatModel)
	if m.viewport.YOffset() != bottom-3 {
		t.Errorf("wheel up must scroll 3 lines: %d -> %d", bottom, m.viewport.YOffset())
	}
	res, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m = res.(ChatModel)
	if m.viewport.YOffset() != bottom {
		t.Errorf("wheel down must scroll back: %d", m.viewport.YOffset())
	}
	res, _ = m.Update(tea.MouseReleaseMsg{Button: tea.MouseLeft})
	if h2cYOff(res) != bottom {
		t.Error("release must not move the viewport")
	}

	res, _ = m.Update(h2cKey("ctrl+m"))
	m = res.(ChatModel)
	if m.mouseEnabled || !strings.Contains(h2cLast(m), "Mouse OFF") {
		t.Errorf("second ctrl+m must disable the mouse: %q", h2cLast(m))
	}
}

func TestH2cEnterSendsAndStreamsAnswer(t *testing.T) {
	h2cHome(t)
	m := newTestChatModel(t)
	m.chat = h2cMockChat(gleann.SearchResult{Text: "alpha passage", Metadata: map[string]any{"source": "a.md"}})
	m.pendingImages = []string{"aW1n"}

	// Enter while a reply is streaming does nothing.
	m.waiting = true
	m.textarea.SetValue("ignored")
	res, cmd := m.Update(h2cKey("enter"))
	if cmd != nil || res.(ChatModel).textarea.Value() != "ignored" {
		t.Fatal("enter while waiting must be ignored")
	}
	m.waiting = false

	m.textarea.SetValue("  what is alpha?  ")
	res, cmd = m.Update(h2cKey("enter"))
	m = res.(ChatModel)
	if !m.waiting || m.textarea.Value() != "" || cmd == nil {
		t.Fatalf("enter must start a request: waiting=%v input=%q", m.waiting, m.textarea.Value())
	}
	n := len(m.messages)
	if m.messages[n-2].role != "user" || m.messages[n-2].content != "what is alpha?" || m.messages[n-1].role != "assistant" {
		t.Errorf("expected the user turn and an empty assistant placeholder: %+v", m.messages[n-2:])
	}
	if m.pendingImages != nil {
		t.Error("queued images must be consumed by the request")
	}
	if m.tokensSent != len("what is alpha?")/4 {
		t.Errorf("tokensSent = %d", m.tokensSent)
	}

	// Run the request the way the program would and feed the stream back.
	var start tea.Msg
	for _, c := range cmd().(tea.BatchMsg) {
		if msg, ok := c().(streamStartMsg); ok {
			start = msg
		}
	}
	if start == nil {
		t.Fatal("the batch must start a stream")
	}
	msg := start
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("stream did not finish")
		default:
		}
		var next tea.Cmd
		res, next = m.Update(msg)
		m = res.(ChatModel)
		if _, done := msg.(streamDoneMsg); done {
			break
		}
		if _, failed := msg.(streamErrorMsg); failed {
			t.Fatalf("stream failed: %q", h2cLast(m))
		}
		if next == nil {
			t.Fatalf("no follow-up command after %T", msg)
		}
		msg = next()
	}
	if m.waiting || m.streamChan != nil {
		t.Error("done must end the request")
	}
	last := m.messages[len(m.messages)-1]
	if last.role != "assistant" || last.content == "" {
		t.Errorf("the streamed answer must fill the assistant message: %+v", last)
	}
}

func TestH2cHistoryEnterLoadsAndDeleteRefreshes(t *testing.T) {
	home := h2cHome(t)
	store := conversations.NewStore(filepath.Join(home, "convs"))
	conv := &conversations.Conversation{
		Title: "a very long conversation title that is cut in the overlay",
		Messages: []conversations.Message{
			{Role: "system", Content: "restored prompt"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		},
	}
	if err := store.Save(conv); err != nil {
		t.Fatal(err)
	}

	m := newTestChatModel(t)
	m.chat = h2cMockChat()
	m = h2cSlash(t, m, "/history")
	if !m.showHistory || len(m.historyItems) != 1 {
		t.Fatalf("/history must open the picker: show=%v items=%d", m.showHistory, len(m.historyItems))
	}
	view := m.viewHistory()
	if !strings.Contains(view, conv.ID[:8]) || strings.Contains(view, conv.ID) || !strings.Contains(view, "...") {
		t.Errorf("overlay must show a short ID and a truncated title:\n%s", view)
	}

	res, _ := m.Update(h2cKey("enter"))
	loaded := res.(ChatModel)
	if loaded.showHistory {
		t.Error("enter must close the picker")
	}
	if loaded.systemPrompt != "restored prompt" {
		t.Errorf("system message must restore the prompt: %q", loaded.systemPrompt)
	}
	if !strings.Contains(h2cLast(loaded), "Loaded conversation") || !strings.Contains(h2cLast(loaded), "(3 messages)") {
		t.Errorf("confirmation: %q", h2cLast(loaded))
	}
	if h := loaded.chat.History(); len(h) != 3 || h[1].Content != "hello" {
		t.Errorf("LLM history must be replayed: %+v", h)
	}

	// A conversation removed behind the picker's back reports the load error.
	m.historyItems = []conversations.Conversation{{ID: "deadbeefcafe", Title: "gone"}}
	res, _ = m.Update(h2cKey("enter"))
	if got := h2cLast(res.(ChatModel)); !strings.Contains(got, "Error loading conversation") {
		t.Errorf("missing conversation: %q", got)
	}

	// Deleting the last entry of a two-item list moves the cursor up; the
	// list is re-read from disk.
	other := &conversations.Conversation{Title: "second", Messages: []conversations.Message{{Role: "user", Content: "x"}}}
	if err := store.Save(other); err != nil {
		t.Fatal(err)
	}
	items, _ := store.List()
	m.historyItems = items
	m.historyCursor = len(items) - 1
	m.showHistory = true
	res, _ = m.Update(h2cKey("delete"))
	m = res.(ChatModel)
	if len(m.historyItems) != 1 || m.historyCursor != 0 || !m.showHistory {
		t.Errorf("after delete: items=%d cursor=%d show=%v", len(m.historyItems), m.historyCursor, m.showHistory)
	}
}

func TestH2cAdjustSettingClampsAtBothEnds(t *testing.T) {
	m := newTestChatModel(t)
	m.rerankModels = []string{"r1", "r2"}

	m.settingsCursor = fieldTopK
	m.topK = topKPresets[len(topKPresets)-1]
	m.adjustSetting(+1)
	if m.topK != topKPresets[len(topKPresets)-1] {
		t.Errorf("top-k must stop at the last preset: %d", m.topK)
	}

	m.settingsCursor = fieldRerankModel
	m.rerankModelIdx = 0
	m.adjustSetting(-1)
	if m.rerankModelIdx != 0 {
		t.Errorf("rerank model must stop at the first: %d", m.rerankModelIdx)
	}

	m.settingsCursor = fieldRepeatPenalty
	m.repeatPenalty = repeatPenaltyPresets[0]
	m.adjustSetting(-1)
	if m.repeatPenalty != repeatPenaltyPresets[0] {
		t.Errorf("repeat penalty below the first preset: %v", m.repeatPenalty)
	}
	m.repeatPenalty = repeatPenaltyPresets[len(repeatPenaltyPresets)-1]
	m.adjustSetting(+1)
	if m.repeatPenalty != repeatPenaltyPresets[len(repeatPenaltyPresets)-1] {
		t.Errorf("repeat penalty above the last preset: %v", m.repeatPenalty)
	}
}

func TestH2cApplySettingsResolvesRoleAndReranker(t *testing.T) {
	h2cHome(t)
	m := newTestChatModel(t)
	m.chat = h2cMockChat()
	m.llmModels = []string{"mock"}
	m.roleNames = []string{"(none)", "reviewer"}
	m.roleIdx = 1
	m.rerankEnabled = true
	m.rerankModels = []string{"bge-reranker"}
	m.applySettings()

	if !strings.Contains(m.systemPrompt, "senior code reviewer") || m.chat.Config().SystemPrompt != m.systemPrompt {
		t.Errorf("the role must resolve to its prompt: %q", m.systemPrompt)
	}
	msg := h2cLast(m)
	if !strings.Contains(msg, "reranker: bge-reranker") || !strings.Contains(msg, "role: reviewer") {
		t.Errorf("notification must name the reranker and role: %q", msg)
	}
}

func TestH2cViewBadges(t *testing.T) {
	m := newTestChatModel(t)
	m.repeatPenalty = 1.15
	m.tokensSent, m.tokensReceived = 7000, 2000
	m.contextLimit = 10000
	m.pendingImages = []string{"x", "y"}
	m.attachedFiles = []string{"a"}
	m.viewport.SetHeight(30)
	m.viewport.SetContent("short")
	out := m.View().Content
	for _, want := range []string{"rep=1.15", "~9k/10k tok (90%)", "📷×2", "📎×1"} {
		if !strings.Contains(out, want) {
			t.Errorf("header lacks %q", want)
		}
	}
	// Without a known window only the total is shown.
	m.contextLimit = 0
	if out := m.View().Content; !strings.Contains(out, "~9k tok") || strings.Contains(out, "/10k") {
		t.Error("unknown window must show only the total")
	}
}

func TestH2cViewSettingsHintsFollowCursor(t *testing.T) {
	base := newTestChatModel(t)
	base.width = 50 // narrower than the panel: the panel shrinks
	base.llmModels = []string{"m1", "m2"}
	base.roleNames = []string{"(none)", "code"}
	for _, tc := range []struct {
		name  string
		field settingsField
		mod   func(*ChatModel)
		want  []string
	}{
		{"llm", fieldLLMModel, nil, []string{"▸ LLM Model", "(1/2)"}},
		{"rerank toggle", fieldRerankToggle, nil, []string{"▸ Reranker", "toggle on/off"}},
		{"rerank model none", fieldRerankModel, nil, []string{"▸ Rerank Model", "no reranker models found"}},
		{"rerank model", fieldRerankModel, func(m *ChatModel) { m.rerankModels = []string{"r1", "r2", "r3"}; m.rerankModelIdx = 2 }, []string{"(3/3)"}},
		{"role", fieldRole, nil, []string{"▸ Role", "(1/2)"}},
		{"no roles", fieldRole, func(m *ChatModel) { m.roleNames = nil }, []string{"no roles configured"}},
		{"prompt", fieldSystemPrompt, nil, []string{"▸ System Prompt", "(enter to edit)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.settingsCursor = tc.field
			if tc.mod != nil {
				tc.mod(&m)
			}
			out := m.viewSettings()
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q", w)
				}
			}
		})
	}
}

func TestH2cRenderSliderClampsRatio(t *testing.T) {
	m := newTestChatModel(t)
	// A tiny panel still gets a 10-cell bar, and an out-of-range ratio fills
	// it without overflowing.
	out := m.renderSlider(fieldTemperature, "Temperature", "9.9", 5, 4)
	if strings.Count(out, "━") != 10 {
		t.Errorf("bar must be full and 10 wide: %q", out)
	}
}

func TestH2cFetchRerankModelListKeepsSavedChoice(t *testing.T) {
	names, idx := fetchRerankModelList(&OnboardResult{RerankProvider: string(gleann.RerankerCohere), RerankModel: "rerank-2"})
	if names[idx] != "rerank-2" {
		t.Errorf("saved model must be selected: %v %d", names, idx)
	}
	names, idx = fetchRerankModelList(&OnboardResult{RerankProvider: string(gleann.RerankerCohere), RerankModel: "rerank-multilingual-v3.0"})
	if names[idx] != "rerank-multilingual-v3.0" || len(names) != 2 {
		t.Errorf("known model must be selected in place: %v %d", names, idx)
	}
}

func TestH2cMediaCommandsRelativePathsAndReadErrors(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, f := range []string{"pic.png", "song.mp3", "clip.mp4"} {
		if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := newTestChatModel(t)
	if out := m.handleImageCommand("pic.png"); !strings.Contains(out, "Queued `pic.png`") {
		t.Errorf("image: %q", out)
	}
	if out := m.handleAudioCommand("song.mp3"); !strings.Contains(out, "Queued `song.mp3`") {
		t.Errorf("audio: %q", out)
	}
	if out := m.handleVideoCommand("clip.mp4"); !strings.Contains(out, "Queued `clip.mp4`") {
		t.Errorf("video: %q", out)
	}
	if len(m.pendingImages) != 3 {
		t.Errorf("three media must be queued: %d", len(m.pendingImages))
	}
	if out := m.handleAttachCommand("pic.png"); !strings.Contains(out, "Attached `pic.png`") || m.attachedFiles[0] != filepath.Join(dir, "pic.png") {
		t.Errorf("attach must resolve against the working directory: %q %v", out, m.attachedFiles)
	}
	if out := m.handlePDFCommand("missing.pdf"); !strings.Contains(out, "PDF not found") {
		t.Errorf("pdf: %q", out)
	}
	if out := m.handleScreenshotCommand("pic.png"); !strings.Contains(out, "Queued `pic.png`") {
		t.Errorf("screenshot with a path acts like /image: %q", out)
	}
}

func TestH2cPDFCommandQueuesFirstPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(path, h2cMinimalPDF(), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestChatModel(t)
	out := m.handlePDFCommand(path)
	if strings.Contains(out, "render error") {
		t.Skipf("no PDF renderer in this build: %s", out)
	}
	if !strings.Contains(out, "Queued `doc.pdf` (Page 1)") || len(m.pendingImages) != 1 {
		t.Errorf("pdf: %q, queued=%d", out, len(m.pendingImages))
	}
}

// h2cMinimalPDF is a valid one-page PDF with an empty page.
func h2cMinimalPDF() []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 72 72] >>",
	}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		b.WriteString(strings.Join([]string{strconv.Itoa(i + 1), " 0 obj\n", o, "\nendobj\n"}, ""))
	}
	xref := b.Len()
	b.WriteString("xref\n0 " + strconv.Itoa(len(objs)+1) + "\n0000000000 65535 f \n")
	for _, off := range offs {
		s := strconv.Itoa(off)
		b.WriteString(strings.Repeat("0", 10-len(s)) + s + " 00000 n \n")
	}
	b.WriteString("trailer\n<< /Size " + strconv.Itoa(len(objs)+1) + " /Root 1 0 R >>\nstartxref\n" + strconv.Itoa(xref) + "\n%%EOF\n")
	return []byte(b.String())
}

func TestH2cBudgetShowsWindowAndSavings(t *testing.T) {
	m := newTestChatModel(t)
	m.chat = h2cMockChat()
	m.chat.AppendHistory(gleann.ChatMessage{Role: "user", Content: "q"})
	m.chat.AppendHistory(gleann.ChatMessage{Role: "assistant", Content: "a"})
	m.tokensSent, m.tokensReceived = 100, 1900
	m.contextLimit = 8000
	m.attachedFiles = []string{"a", "b"}
	out := m.handleBudgetCommand()
	// 2 files * 200 tokens * 2 history messages = 800 raw vs 100 sent.
	for _, want := range []string{"Window: 25% (2k / 8k)", "RAG savings: ~87%"} {
		if !strings.Contains(out, want) {
			t.Errorf("budget lacks %q:\n%s", want, out)
		}
	}
}

func TestH2cGraphCommandListsCallersAndCallees(t *testing.T) {
	s := h2cGraphSearcher(t)
	m := newTestChatModel(t)
	m.chat = gleann.NewChat(s, gleann.ChatConfig{Provider: gleann.LLMMock, Model: "mock"})

	out := m.handleGraphCommand("example.com/calc.Add")
	for _, want := range []string{"**Graph: example.com/calc.Add**", "**Callers** (1)", "← `example.com/calc.Twice` (function)", "**Callees** (1)", "→ `example.com/calc.helper` (function)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	out = m.handleGraphCommand("example.com/calc.helper")
	if !strings.Contains(out, "**Callees:** none") {
		t.Errorf("leaf function has no callees:\n%s", out)
	}
	out = m.handleGraphCommand("example.com/calc.Twice")
	if !strings.Contains(out, "**Callers:** none") {
		t.Errorf("root function has no callers:\n%s", out)
	}
}

func TestH2cSlashMemoryErrorsAndLongEntries(t *testing.T) {
	store, err := memory.OpenStore(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := memory.NewManager(store)
	long := strings.Repeat("x", 80)
	if _, err := mgr.RememberBlock(&memory.Block{Tier: memory.TierLong, Label: "a-very-long-label", Content: long}); err != nil {
		t.Fatal(err)
	}
	m := newTestChatModel(t)
	m.chat = h2cMockChat()
	m.memoryMgr = mgr
	m = h2cSlash(t, m, "/memories")
	out := h2cLast(m)
	if !strings.Contains(out, "a-very-long-") || strings.Contains(out, "a-very-long-label") || !strings.Contains(out, strings.Repeat("x", 57)+"...") {
		t.Errorf("label and content must be shortened:\n%s", out)
	}

	// A closed store makes remember and forget report the failure.
	mgr.Close()
	m = h2cSlash(t, m, "/remember fact")
	if !strings.HasPrefix(h2cLast(m), "⚠ Error:") {
		t.Errorf("remember on a closed store: %q", h2cLast(m))
	}
	m = h2cSlash(t, m, "/forget fact")
	if !strings.HasPrefix(h2cLast(m), "⚠ ") {
		t.Errorf("forget on a closed store: %q", h2cLast(m))
	}
}

func TestH2cSlashScriptRejectsAnswerWithoutScript(t *testing.T) {
	m := newTestChatModel(t)
	// The mock LLM echoes the prompt, which is not a script: nothing is saved.
	m.chat = h2cMockChat()
	m = h2cSlash(t, m, "/script count lines of code")
	if got := h2cLast(m); !strings.Contains(got, "Could not extract a valid script") {
		t.Errorf("/script: %q", got)
	}
}
