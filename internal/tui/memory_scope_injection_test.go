package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tevfik/agenttui"

	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// initMemoryRepo creates a temporary git repository whose origin is remote.
// It skips the test when git is unavailable.
func initMemoryRepo(t *testing.T, remote string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL="+os.DevNull,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	if remote != "" {
		runGit("remote", "add", "origin", remote)
	}
	return dir
}

// memoryScopeFixture stores one block in repo A, one in repo B and one global.
func memoryScopeFixture(t *testing.T) *memory.Manager {
	t.Helper()
	store, err := memory.OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := memory.NewManager(store)
	t.Cleanup(func() { _ = mgr.Close() })

	if _, err := mgr.AddScopedNote("github.com/org/repo-a", memory.TierLong, "rule", "repo-a secret rule"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AddScopedNote("github.com/org/repo-b", memory.TierLong, "rule", "repo-b secret rule"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AddNote(memory.TierLong, "rule", "global user rule"); err != nil {
		t.Fatal(err)
	}
	return mgr
}

// capturingOllama returns a fake Ollama server and a function that reports the
// system messages of the last chat request, which is where injected memory goes.
// Only /api/chat requests are captured: the agent-chat path also probes
// /api/show in the background to learn the model's context window, and such a
// probe carries no messages — capturing it would overwrite the chat prompt
// with an empty string and flake the memory assertions under load.
func capturingOllama(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var mu sync.Mutex
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat" {
			var req struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)

			var system strings.Builder
			for _, m := range req.Messages {
				if m.Role == "system" {
					system.WriteString(m.Content)
					system.WriteString("\n")
				}
			}
			mu.Lock()
			captured = system.String()
			mu.Unlock()
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"role": "assistant", "content": "ok"},
			"done":    true,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, func() string {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

func assertCurrentRepoMemory(t *testing.T, injected string) {
	t.Helper()
	for _, want := range []string{"repo-a secret rule", "global user rule"} {
		if !strings.Contains(injected, want) {
			t.Errorf("injected memory is missing %q:\n%s", want, injected)
		}
	}
	if strings.Contains(injected, "repo-b secret rule") {
		t.Errorf("injected memory leaked a sibling repository's block:\n%s", injected)
	}
}

// TestChatModelInjectsOnlyCurrentRepoMemory exercises the exact TUI chat path:
// askLLMStreamWithMedia refreshes the memory context before every prompt.
func TestChatModelInjectsOnlyCurrentRepoMemory(t *testing.T) {
	t.Chdir(initMemoryRepo(t, "git@github.com:org/repo-a.git"))
	mgr := memoryScopeFixture(t)
	srv, captured := capturingOllama(t)

	m := newTestChatModel(t)
	m.chat = gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{
		Provider: gleann.LLMOllama,
		Model:    "test-model",
		BaseURL:  srv.URL,
	})
	m.memoryMgr = mgr

	msg := m.askLLMStreamWithMedia("what is the rule?", nil)()
	start, ok := msg.(streamStartMsg)
	if !ok {
		t.Fatalf("expected streamStartMsg, got %T", msg)
	}
	for tok := range start.tokenChan {
		if strings.HasPrefix(tok, "\x00ERROR:") {
			t.Fatalf("stream failed: %s", tok)
		}
	}

	assertCurrentRepoMemory(t, captured())
}

// TestAgentChatStreamInjectsOnlyCurrentRepoMemory exercises the exact TUI
// agent-chat path: stream refreshes the memory context before every answer.
func TestAgentChatStreamInjectsOnlyCurrentRepoMemory(t *testing.T) {
	t.Chdir(initMemoryRepo(t, "https://github.com/org/repo-a.git"))
	mgr := memoryScopeFixture(t)
	srv, captured := capturingOllama(t)

	ac := newTestAgentChat(nil)
	t.Cleanup(ac.close)
	ac.cm.chat = gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{
		Provider: gleann.LLMOllama,
		Model:    "test-model",
		BaseURL:  srv.URL,
	})
	ac.cm.memoryMgr = mgr

	if _, err := ac.stream(context.Background(), "what is the rule?", func(agenttui.Event) {}); err != nil {
		t.Fatal(err)
	}

	assertCurrentRepoMemory(t, captured())
}

// TestAgentChatContextProbeKeepsCapturedMemory is a deterministic reproduction
// of the CI flake in TestAgentChatStreamInjectsOnlyCurrentRepoMemory: when the
// context limit is unknown, stream spawns refreshContextLimit in the background
// and that probe POSTs /api/show to the same server. The capture must keep
// reporting the chat request's system prompt, not the probe's empty one.
func TestAgentChatContextProbeKeepsCapturedMemory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GLEANN_CONTEXT_TOKENS", "")
	t.Setenv("GLEANN_OLLAMA_NUM_CTX", "")
	t.Chdir(initMemoryRepo(t, "https://github.com/org/repo-a.git"))
	mgr := memoryScopeFixture(t)
	srv, captured := capturingOllama(t)

	ac := newTestAgentChat(nil)
	t.Cleanup(ac.close)
	ac.cm.chat = gleann.NewChat(fakeSearcher{}, gleann.ChatConfig{
		Provider: gleann.LLMOllama,
		Model:    "test-model",
		BaseURL:  srv.URL,
	})
	ac.cm.memoryMgr = mgr

	if _, err := ac.stream(context.Background(), "what is the rule?", func(agenttui.Event) {}); err != nil {
		t.Fatal(err)
	}
	// stream spawned `go ac.refreshContextLimit(context.Background())` because contextLimit is 0.
	// Run the same probe synchronously so the ordering is deterministic.
	ac.refreshContextLimit(context.Background())
	ac.mu.Lock()
	limit := ac.cm.contextLimit
	ac.mu.Unlock()
	if limit == 0 {
		t.Fatal("context probe did not run; the test would pass vacuously")
	}

	assertCurrentRepoMemory(t, captured())
}
