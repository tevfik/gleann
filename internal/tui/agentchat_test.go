package tui

import (
	"context"
	"strings"
	"testing"

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
