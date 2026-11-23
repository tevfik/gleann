package main

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

	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

func initAskMemoryRepo(t *testing.T, remote string) string {
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

func captureAskOllama(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var mu sync.Mutex
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"role": "assistant", "content": "ok"},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, func() string {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

// TestAskMemoryInjectionUsesRepoScope covers the shared helper that
// cmdAsk now calls instead of BuildContext: with the cwd inside repo A, the
// chat receives repo A's block plus global memory and never repo B's.
func TestAskMemoryInjectionUsesRepoScope(t *testing.T) {
	t.Chdir(initAskMemoryRepo(t, "git@github.com:org/repo-a.git"))

	store, err := memory.OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mgr := memory.NewManager(store)
	for _, tc := range []struct {
		scope   string
		content string
	}{
		{"github.com/org/repo-a", "ask repo-a secret rule"},
		{"github.com/org/repo-b", "ask repo-b secret rule"},
		{"", "ask global user rule"},
	} {
		if _, err := mgr.AddScopedNote(tc.scope, memory.TierLong, "rule", tc.content); err != nil {
			t.Fatal(err)
		}
	}

	srv, captured := captureAskOllama(t)
	chat := gleann.NewChat(gleann.NullSearcher{}, gleann.ChatConfig{
		Provider: gleann.LLMOllama,
		Model:    "test-model",
		BaseURL:  srv.URL,
	})

	memory.InjectRepoContext(chat, mgr)
	if _, err := chat.Ask(context.Background(), "probe"); err != nil {
		t.Fatal(err)
	}

	injected := captured()
	for _, want := range []string{"ask repo-a secret rule", "ask global user rule"} {
		if !strings.Contains(injected, want) {
			t.Errorf("injected memory is missing %q:\n%s", want, injected)
		}
	}
	if strings.Contains(injected, "ask repo-b secret rule") {
		t.Errorf("injected memory leaked a sibling repository's block:\n%s", injected)
	}
}
