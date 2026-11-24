package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"

	"github.com/tevfik/agenttui"
)

// brokenMemoryManager returns a manager whose store is closed, so
// BuildRepoContext fails; healthyMemoryManager returns one that works.
func brokenMemoryManager(t *testing.T) *memory.Manager {
	t.Helper()
	mgr := memoryScopeFixture(t)
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.BuildRepoContext(); err == nil {
		t.Fatal("test premise: BuildRepoContext must fail on a closed store")
	}
	return mgr
}

// memorySequence is the manager used on each turn: false = broken store.
var memorySequence = []bool{false, false, true, false, false}

// wantMemoryNotices is the number of notices after each turn of memorySequence:
// the first failure, no repeat, nothing on success, a new failure after the
// success, no repeat.
var wantMemoryNotices = []int{1, 1, 1, 2, 2}

func TestMemoryNoticeGate(t *testing.T) {
	g := &memoryNoticeGate{}
	boom := errors.New("boom")
	var shown []bool
	for _, ok := range memorySequence {
		var err error
		if !ok {
			err = boom
		}
		shown = append(shown, g.report(err))
	}
	want := []bool{true, false, false, true, false}
	for i := range want {
		if shown[i] != want[i] {
			t.Fatalf("turn %d: shown=%v want %v (all: %v)", i, shown[i], want[i], shown)
		}
	}
	var nilGate *memoryNoticeGate
	if !nilGate.report(boom) || nilGate.report(nil) {
		t.Error("a nil gate reports every failure and never a success")
	}
}

// TestAgentChatMemoryCardOncePerFailureRun: the failed memory card appears on
// the first failure, not on repeats, and again after a success.
func TestAgentChatMemoryCardOncePerFailureRun(t *testing.T) {
	isolateAgentChatEnv(t)
	t.Chdir(initMemoryRepo(t, "https://github.com/org/repo-a.git"))
	good, bad := memoryScopeFixture(t), brokenMemoryManager(t)

	ac := newTestAgentChat(nil)
	t.Cleanup(ac.close)
	cards := 0
	for i, ok := range memorySequence {
		ac.cm.memoryMgr = bad
		if ok {
			ac.cm.memoryMgr = good
		}
		if _, err := ac.stream(context.Background(), "hello", func(e agenttui.Event) {
			if e.Kind == agenttui.EventToolEnd && !e.ToolOK && strings.Contains(e.ToolOutput, "memory not injected") {
				cards++
			}
		}); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
		if cards != wantMemoryNotices[i] {
			t.Fatalf("after turn %d: %d failed memory cards, want %d", i, cards, wantMemoryNotices[i])
		}
	}
}

// TestChatModelMemoryNoticeOncePerFailureRun: the classic chat shows a system
// line for a failed memory refresh, once per failure run, and still answers.
func TestChatModelMemoryNoticeOncePerFailureRun(t *testing.T) {
	isolateAgentChatEnv(t)
	t.Chdir(initMemoryRepo(t, "https://github.com/org/repo-a.git"))
	good, bad := memoryScopeFixture(t), brokenMemoryManager(t)

	m := newTestChatModel(t)
	m.chat = gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{Provider: gleann.LLMMock, Model: "mock"})
	m.memNotice = &memoryNoticeGate{}
	for i, ok := range memorySequence {
		m.memoryMgr = bad
		if ok {
			m.memoryMgr = good
		}
		m.messages = append(m.messages, chatMsg{role: "assistant"}) // the streaming placeholder
		start, isStart := m.askLLMStreamWithMedia("q", nil)().(streamStartMsg)
		if !isStart {
			t.Fatalf("turn %d: want streamStartMsg", i)
		}
		answered := false
		for tok := range start.tokenChan {
			if strings.HasPrefix(tok, "\x00ERROR:") {
				t.Fatalf("turn %d: a memory error must not fail the answer: %s", i, tok)
			}
			answered = true
		}
		if !answered {
			t.Fatalf("turn %d: no answer tokens", i)
		}
		next, _ := m.Update(start)
		m = next.(ChatModel)
		if last := m.messages[len(m.messages)-1]; last.role != "assistant" {
			t.Fatalf("turn %d: the streaming placeholder must stay last, got %q", i, last.role)
		}
		notices := 0
		for _, msg := range m.messages {
			if msg.role == "system" && strings.Contains(msg.content, "Memory not injected") {
				notices++
			}
		}
		if notices != wantMemoryNotices[i] {
			t.Fatalf("after turn %d: %d memory notices, want %d", i, notices, wantMemoryNotices[i])
		}
	}
}

func TestNewChatModelCreatesMemoryGate(t *testing.T) {
	isolateAgentChatEnv(t)
	chat := gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{Provider: gleann.LLMMock, Model: "mock"})
	if cm := NewChatModel(chat, "idx", "mock"); cm.memNotice == nil {
		t.Fatal("NewChatModel must create the memory notice gate")
	}
}

// agentCommand runs the named agent-chat slash command.
func agentCommand(t *testing.T, ac *agentChat, name string) {
	t.Helper()
	for _, c := range ac.commands() {
		if c.Name == name {
			if _, _, err := c.Handler(context.Background(), nil, ac.app); err != nil {
				t.Fatalf("/%s: %v", name, err)
			}
			return
		}
	}
	t.Fatalf("no /%s command", name)
}

// TestAgentChatMemoryCardAgainAfterClear: a store that stays broken is
// announced again in the conversation started by /new or /clear.
func TestAgentChatMemoryCardAgainAfterClear(t *testing.T) {
	for _, cmd := range []string{"new", "clear"} {
		t.Run(cmd, func(t *testing.T) {
			isolateAgentChatEnv(t)
			t.Chdir(initMemoryRepo(t, "https://github.com/org/repo-a.git"))
			ac := newTestAgentChat(nil)
			t.Cleanup(ac.close)
			ac.cm.memoryMgr = brokenMemoryManager(t)
			cards := 0
			emit := func(e agenttui.Event) {
				if e.Kind == agenttui.EventToolEnd && !e.ToolOK && strings.Contains(e.ToolOutput, "memory not injected") {
					cards++
				}
			}
			for turn, want := range []int{1, 1, 2, 2} {
				if turn == 2 {
					agentCommand(t, ac, cmd)
				}
				if _, err := ac.stream(context.Background(), "hello", emit); err != nil {
					t.Fatal(err)
				}
				if cards != want {
					t.Fatalf("turn %d: %d cards, want %d", turn, cards, want)
				}
			}
		})
	}
}

// TestChatModelMemoryNoticeAgainAfterNewAndClear: same for the classic chat.
func TestChatModelMemoryNoticeAgainAfterNewAndClear(t *testing.T) {
	for _, cmd := range []string{"/new", "/clear"} {
		t.Run(cmd, func(t *testing.T) {
			isolateAgentChatEnv(t)
			t.Chdir(initMemoryRepo(t, "https://github.com/org/repo-a.git"))
			m := newTestChatModel(t)
			m.chat = gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{Provider: gleann.LLMMock, Model: "mock"})
			m.memNotice = &memoryNoticeGate{}
			m.memoryMgr = brokenMemoryManager(t)
			notices := func() int {
				n := 0
				for _, msg := range m.messages {
					if msg.role == "system" && strings.Contains(msg.content, "Memory not injected") {
						n++
					}
				}
				return n
			}
			for turn, want := range []int{1, 1, 1, 1} {
				if turn == 2 {
					next, _, handled := m.handleSlashCommand(cmd)
					if !handled {
						t.Fatal("not handled")
					}
					m = next.(ChatModel)
					if notices() != 0 {
						t.Fatalf("%s must clear the visible notice", cmd)
					}
					want = 1
				}
				m.messages = append(m.messages, chatMsg{role: "assistant"})
				start := m.askLLMStreamWithMedia("q", nil)().(streamStartMsg)
				for range start.tokenChan {
				}
				next, _ := m.Update(start)
				m = next.(ChatModel)
				if n := notices(); n != want {
					t.Fatalf("turn %d: %d notices, want %d", turn, n, want)
				}
			}
		})
	}
}

// TestChatModelTokensFillPlaceholderBehindSystemLine: a system line that lands
// after the placeholder must not make the answer tokens vanish.
func TestChatModelTokensFillPlaceholderBehindSystemLine(t *testing.T) {
	m := newTestChatModel(t)
	m.waiting = true
	m.messages = append(m.messages, chatMsg{role: "assistant"}, chatMsg{role: "system", content: "late"})
	next, _ := m.Update(streamTokenMsg{token: "hi"})
	m = next.(ChatModel)
	if got := m.messages[len(m.messages)-2].content; got != "hi" {
		t.Fatalf("placeholder content %q, want hi", got)
	}
}
