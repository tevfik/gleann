package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tevfik/agenttui"

	"github.com/tevfik/gleann/pkg/gleann"
)

// isolateAgentChatEnv keeps a test away from the real config and memory.
func isolateAgentChatEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("GLEANN_CONTEXT_TOKENS", "")
	t.Setenv("GLEANN_OLLAMA_NUM_CTX", "")
	t.Setenv("GLEANN_CONFIG_DIR", t.TempDir())
}

// TestAgentChatContextLimitIsNotRaced: stream and the /new handler read
// contextLimit while refreshContextLimit writes it; every access must hold
// ac.mu. Only meaningful under -race.
func TestAgentChatContextLimitIsNotRaced(t *testing.T) {
	isolateAgentChatEnv(t)
	ac := newTestAgentChat(nil)
	t.Setenv("GLEANN_CONTEXT_TOKENS", "4096") // the probe now answers without a network
	t.Cleanup(ac.close)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			ac.refreshContextLimit(context.Background())
		}
	}()
	newCmd := findCommand(t, ac, "new")
	for i := 0; i < 50; i++ {
		if _, err := ac.stream(context.Background(), "q", func(agenttui.Event) {}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := newCmd.Handler(context.Background(), nil, ac.app); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}

func findCommand(t *testing.T, ac *agentChat, name string) agenttui.SlashCommand {
	t.Helper()
	for _, c := range ac.commands() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("command %q not found", name)
	return agenttui.SlashCommand{}
}

// TestAgentChatCloseStopsContextProbe: a probe blocked on a slow provider is
// cancelled by close, close waits for it, and it touches no state afterwards.
func TestAgentChatCloseStopsContextProbe(t *testing.T) {
	isolateAgentChatEnv(t)
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/show" {
			entered <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	ac := newTestAgentChat(nil)
	ac.cm.chat = gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{Provider: gleann.LLMOllama, Model: "m", BaseURL: srv.URL})

	ac.goBackground(ac.refreshContextLimit)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("probe never reached the server")
	}

	done := make(chan struct{})
	go func() { ac.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("close did not stop the blocked probe (the provider timeout is 4s)")
	}
	ac.mu.Lock()
	limit := ac.cm.contextLimit
	ac.mu.Unlock()
	if limit != 0 {
		t.Errorf("cancelled probe still stored a limit: %d", limit)
	}

	ran := false
	ac.goBackground(func(context.Context) { ran = true })
	ac.close() // idempotent
	if ran {
		t.Error("goBackground must not start work after close")
	}
}

// TestAgentChatStreamSurfacesMemoryError: a failing memory build does not fail
// the answer, but the user sees a failed "memory" card with the reason.
func TestAgentChatStreamSurfacesMemoryError(t *testing.T) {
	isolateAgentChatEnv(t)
	t.Chdir(initMemoryRepo(t, "https://github.com/org/repo-a.git"))
	mgr := memoryScopeFixture(t)
	if err := mgr.Close(); err != nil { // a closed store makes BuildRepoContext fail
		t.Fatal(err)
	}
	if _, err := mgr.BuildRepoContext(); err == nil {
		t.Fatal("test premise: BuildRepoContext must fail on a closed store")
	}

	ac := newTestAgentChat(nil)
	t.Cleanup(ac.close)
	ac.cm.memoryMgr = mgr
	var events []agenttui.Event
	if _, err := ac.stream(context.Background(), "hello", func(e agenttui.Event) { events = append(events, e) }); err != nil {
		t.Fatalf("a memory error must not fail the answer: %v", err)
	}
	var started, ended, text bool
	for _, e := range events {
		switch {
		case e.Kind == agenttui.EventToolStart && e.ToolName == "memory":
			started = true
		case e.Kind == agenttui.EventToolEnd && !e.ToolOK && strings.Contains(e.ToolOutput, "memory not injected"):
			ended = true
		case e.Kind == agenttui.EventText:
			text = true
		}
	}
	if !started || !ended {
		t.Errorf("no failed memory card among events: %+v", events)
	}
	if !text {
		t.Error("the answer must still be produced")
	}
}
