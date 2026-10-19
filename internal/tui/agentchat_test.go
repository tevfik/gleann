package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/agenttui"

	"github.com/tevfik/gleann/pkg/gleann"
)

type fakeSearcher struct{ results []gleann.SearchResult }

func (f fakeSearcher) Search(ctx context.Context, q string, opts ...gleann.SearchOption) ([]gleann.SearchResult, error) {
	return f.results, nil
}
func (f fakeSearcher) Close() error { return nil }

func newTestAgentChat(results []gleann.SearchResult) *agentChat {
	chat := gleann.NewChat(fakeSearcher{results}, gleann.ChatConfig{Provider: gleann.LLMMock, Model: "mock"})
	cm := NewChatModel(chat, "idx", "mock")
	return &agentChat{cm: &cm, app: agenttui.New(agenttui.Config{})}
}

func TestAgentChat_StreamEmitsTextAndSources(t *testing.T) {
	ac := newTestAgentChat([]gleann.SearchResult{
		{Text: "alpha doc", Score: 0.9, Metadata: map[string]any{"source": "docs/a.md"}},
		{Text: "beta passage with no source", Score: 0.5},
	})
	var events []agenttui.Event
	tok, err := ac.stream(context.Background(), "what is alpha?", func(e agenttui.Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if tok <= 0 {
		t.Errorf("expected token estimate > 0, got %d", tok)
	}
	if len(events) == 0 || events[0].Kind != agenttui.EventToolStart {
		t.Fatalf("sources card must come before the answer, first event: %+v", events)
	}
	var text string
	var start, end *agenttui.Event
	for i := range events {
		switch events[i].Kind {
		case agenttui.EventText:
			text += events[i].Text
		case agenttui.EventToolStart:
			start = &events[i]
		case agenttui.EventToolEnd:
			end = &events[i]
		}
	}
	if !strings.Contains(text, "alpha doc") {
		t.Errorf("answer should echo retrieved context, got %q", text)
	}
	if start == nil || end == nil || start.ToolName != "sources" || start.ToolArgs != "2 passages" {
		t.Fatalf("missing sources card: %+v %+v", start, end)
	}
	if !strings.Contains(end.ToolOutput, "docs/a.md (0.90)") || !strings.Contains(end.ToolOutput, "beta passage") {
		t.Errorf("unexpected sources output: %q", end.ToolOutput)
	}
}

func TestAgentChat_StreamHonoursCancelledContext(t *testing.T) {
	ac := newTestAgentChat(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Must return promptly without panicking; mock provider ignores ctx but search path must not hang.
	if _, err := ac.stream(ctx, "x", func(agenttui.Event) {}); err != nil {
		t.Logf("cancelled stream returned: %v", err)
	}
}

func TestAgentChat_Commands(t *testing.T) {
	ac := newTestAgentChat(nil)
	byName := map[string]agenttui.SlashCommand{}
	for _, c := range ac.commands() {
		byName[c.Name] = c
	}
	for _, n := range []string{"new", "history", "remember", "forget", "memories", "image", "attach", "detach", "index", "budget", "graph", "script", "pdf", "video", "audio", "screenshot", "repeat"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("missing /%s", n)
		}
	}

	out, local, err := byName["budget"].Handler(context.Background(), nil, ac.app)
	if err != nil || !local || !strings.Contains(out, "Token Budget") {
		t.Errorf("budget: %q local=%v err=%v", out, local, err)
	}
	out, _, _ = byName["repeat"].Handler(context.Background(), []string{"9"}, ac.app)
	if !strings.Contains(out, "Usage") {
		t.Errorf("repeat should reject out-of-range values, got %q", out)
	}

	ac.cm.tokensSent = 100
	ac.cm.attachedFiles = []string{"x"}
	out, _, _ = byName["new"].Handler(context.Background(), nil, ac.app)
	if ac.cm.tokensSent != 0 || ac.cm.attachedFiles != nil || !strings.Contains(out, "New conversation") {
		t.Errorf("/new did not reset state: %+v", ac.cm.tokensSent)
	}
}

func TestFormatSources_FallsBackToTextSnippet(t *testing.T) {
	got := formatSources([]gleann.SearchResult{{Text: "line one\nline   two", Score: 0.25}})
	if got != "1. line one line two (0.25)" {
		t.Errorf("got %q", got)
	}
}

func TestIndexOf(t *testing.T) {
	if indexOf([]string{"a", "b"}, "b", 0) != 1 || indexOf([]string{"a"}, "z", 7) != 7 {
		t.Error("indexOf lookup/fallback wrong")
	}
}

func TestAgentChat_InfoMatchesSettingsAndIsGleannSpecific(t *testing.T) {
	chat := gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{Provider: gleann.LLMMock, Model: "qwen3:8b", Temperature: 0.7})
	ac := newAgentChat(chat, "docs", "qwen3:8b")
	ac.app.Update(tea.WindowSizeMsg{Width: 140, Height: 36})
	ac.cm.temperature, ac.cm.topK, ac.cm.rerankEnabled = 0.3, 15, true
	ac.cm.pendingImages = []string{"x"}

	view := ansi.Strip(ac.app.View().Content)
	for _, want := range []string{
		"gleann", "docs · t=0.3 · k=15 · rerank · 📷×1", // header
		"RAG · docs", "qwen3:8b", "temperature", "0.3", "top-k", "15", "repeat penalty", "max tokens", "reranker", "role", // info panel
		"Ctrl+S settings",                    // footer
		"/settings", "/screenshot", "/image", // welcome quick actions
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q\n%s", want, view)
		}
	}
	for _, bad := range []string{"[BUILD]", "DEVRAN", "Devran", "/plan", "/review", "Ctrl+B"} {
		if strings.Contains(view, bad) {
			t.Errorf("view contains foreign text %q", bad)
		}
	}

	ac.cm.indexName = "(no index)"
	rows := ac.infoRows()
	if rows[0] != [2]string{"mode", "LLM only (no retrieval)"} {
		t.Errorf("mode row = %v", rows[0])
	}
}

func TestAgentChat_InfoDoesNotBlockWhileStateLocked(t *testing.T) {
	ac := newTestAgentChat(nil)
	_ = ac.infoRows()
	_ = ac.headerInfo()
	ac.mu.Lock()
	done := make(chan struct{})
	go func() { _ = ac.infoRows(); _ = ac.headerInfo(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("infoRows/headerInfo blocked on the state lock")
	}
	ac.mu.Unlock()
}
