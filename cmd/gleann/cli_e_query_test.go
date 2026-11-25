package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tevfik/gleann/pkg/conversations"
)

// More CLI tests of `gleann ask`, `gleann chat` and `gleann index list|info`:
// the terminal (markdown) rendering, the saved configuration, conversation
// handling and the error paths of each mode.

// eLLMStep is one scripted /api/chat answer of eScriptedLLM.
type eLLMStep struct {
	Status int    // 0 means 200
	Reply  string // assistant message of a 200 answer
}

// eScriptedLLM is a loopback Ollama that answers /api/chat from a script: the
// n-th chat request gets the n-th step, the last step repeats. Other paths
// are 404 (point only the chat model at it, with --base-url).
func eScriptedLLM(t *testing.T, steps ...eLLMStep) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		step := steps[min(n, len(steps)-1)]
		n++
		mu.Unlock()
		if step.Status != 0 && step.Status != http.StatusOK {
			http.Error(w, "scripted failure", step.Status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"role": "assistant", "content": step.Reply},
			"done":    true,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// eWriteSavedConfig writes ~/.gleann/config.json of the test home.
func eWriteSavedConfig(t *testing.T, cfg map[string]any) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".gleann")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// eSetTTY makes `gleann ask` believe it talks to a terminal.
func eSetTTY(t *testing.T, out, in bool) {
	t.Helper()
	origOut, origIn := askOutputIsTTY, askInputIsTTY
	askOutputIsTTY = func() bool { return out }
	askInputIsTTY = func() bool { return in }
	t.Cleanup(func() { askOutputIsTTY, askInputIsTTY = origOut, origIn })
}

func TestCLIEIndexListAndInfoErrors(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	// The index directory is a regular file.
	file := filepath.Join(e.Home, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_INDEX_DIR", file)
	r := e.run("index", "list")
	wantExit(t, r, 1)
	mustContain(t, "list over a file", r.Err, "error:")
	t.Setenv("GLEANN_INDEX_DIR", e.IndexDir)

	// A private index is labelled as such in info.
	r = e.run("index", "set", "demo", "--mcp", "false")
	wantExit(t, r, 0)
	r = e.run("index", "info", "demo")
	wantExit(t, r, 0)
	mustContain(t, "private info", r.Out, "Private (Hidden from MCP)")

	// A corrupt meta file is reported, not printed.
	meta := filepath.Join(e.IndexDir, "demo", "demo.meta.json")
	if err := os.WriteFile(meta, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run("index", "info", "demo")
	wantExit(t, r, 1)
	mustContain(t, "corrupt meta", r.Err, "error:", "invalid character")
}

func TestCLIEAskRendersMarkdownOnATerminal(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")
	e.Ollama.setReply("Tokenvalidation")
	eSetTTY(t, true, false)

	// A terminal gets the glamour-rendered answer.
	r := e.run("ask", "demo", "how are tokens validated?", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "rendered answer", r.Out, "Tokenvalidation")

	// The only index is picked and announced when stdout is a terminal.
	r = e.run("ask", "how are tokens validated?", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "single index", r.Err, "Using index: demo")

	// Interactive mode renders every answer; end of input without "quit"
	// simply stops.
	r = e.runIn("first\nsecond\n", "ask", "demo", "--interactive", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "interactive status", r.Err, "Interactive mode (index: demo", "Type 'quit' or 'exit' to stop.")
	mustContain(t, "interactive answer", r.Out, "Tokenvalidation")
	mustNotContain(t, "interactive without quit", r.Out, "Goodbye!")

	// A failing LLM does not end an interactive session.
	e.Ollama.mu.Lock()
	e.Ollama.failChat = true
	e.Ollama.mu.Unlock()
	r = e.runIn("first\nexit\n", "ask", "demo", "--interactive", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "interactive survives an LLM error", r.Err, "error:", "500")
	mustContain(t, "interactive goodbye", r.Out, "Goodbye!")
	e.Ollama.mu.Lock()
	e.Ollama.failChat = false
	e.Ollama.mu.Unlock()

	// Swarm renders the coding agent's answer.
	r = e.run("ask", "demo", "explain auth", "--swarm", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "swarm rendered", r.Out, "Tokenvalidation")
}

func TestCLIEAskIndexPicker(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("alpha")
	e.build("beta")
	eSetTTY(t, false, true)

	// Interactive mode reads its questions from stdin, so the choice is the
	// first number on it.
	r := e.runIn("2\nquit\n", "ask", "--interactive", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "choices listed", r.Err, "Available indexes:", "1. alpha", "2. beta", "Select index (1-2)")
	mustContain(t, "chosen index", r.Err, "Interactive mode (index: beta")

	r = e.runIn("7\n", "ask", "--interactive", "--no-cache")
	wantExit(t, r, 1)
	mustContain(t, "out of range", r.Err, "error: invalid selection")

	r = e.runIn("not a number\n", "ask", "--interactive", "--no-cache")
	wantExit(t, r, 1)
	mustContain(t, "not a number", r.Err, "error: invalid selection")
}

func TestCLIEAskSavedConversationTitleAndFailures(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	// Without --title the LLM names the conversation.
	e.Ollama.setReply("Token checks")
	r := e.run("ask", "demo", "how are tokens validated?")
	wantExit(t, r, 0)
	convs := askConversations(t)
	if len(convs) != 1 || convs[0].Title != "Token checks" {
		t.Fatalf("conversation title = %+v, want the LLM's summary", convs)
	}

	// An unusable conversation store is a warning, not a failure.
	file := filepath.Join(e.Home, "store-is-a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_CONVERSATIONS_DIR", filepath.Join(file, "sub"))
	r = e.run("ask", "demo", "again?", "--title", "T")
	wantExit(t, r, 0)
	mustContain(t, "answer still printed", r.Out, "Token checks")
	mustContain(t, "save warning", r.Err, "warning: failed to save conversation")
}

func TestCLIEAskContinueHandling(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	// A conversation without indexes: --continue falls back to the single index.
	conv := &conversations.Conversation{Title: "old", Messages: []conversations.Message{{Role: "user", Content: "earlier question"}, {Role: "assistant", Content: "earlier answer"}}}
	if err := conversations.DefaultStore().Save(conv); err != nil {
		t.Fatal(err)
	}
	r := e.run("ask", "follow up", "--continue", conv.ID, "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "history sent", e.Ollama.lastBody("/api/chat"), "earlier question", "earlier answer", "follow up")

	// A conversation of an index that no longer exists cannot be searched.
	ghost := &conversations.Conversation{Title: "ghost", Indexes: []string{"ghost"}, Messages: []conversations.Message{{Role: "user", Content: "hello"}}}
	if err := conversations.DefaultStore().Save(ghost); err != nil {
		t.Fatal(err)
	}
	r = e.run("ask", "q", "--continue", ghost.ID, "--no-cache")
	wantExit(t, r, 1)
	mustContain(t, "missing index", r.Err, "error loading index")

	// --continue-last takes the index from the latest conversation.
	r = e.run("ask", "q", "--continue-last", "--no-cache")
	wantExit(t, r, 1)
	mustContain(t, "latest names the ghost index", r.Err, "error loading index")

	r = e.run("ask", "q", "--continue", "gone", "--no-cache")
	wantExit(t, r, 1)
	mustContain(t, "unknown id", r.Err, "error loading conversation")
}

func TestCLIEAskMemoryContextAndAttachments(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	r := e.run("memory", "remember", "The team prefers hexagonal architecture")
	wantExit(t, r, 0)
	r = e.run("ask", "demo", "what is the architecture?", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "memory in the prompt", e.Ollama.lastBody("/api/chat"), "hexagonal architecture")

	// --attach and --image are consumed with their file, not read as the question.
	img := filepath.Join(e.Home, "pic.png")
	if err := os.WriteFile(img, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run("ask", "demo", "describe", "--attach", img, "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "image sent", e.Ollama.lastBody("/api/chat"), "describe", `"images"`)
	r = e.run("ask", "demo", "describe", "--image", img, "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "image alias", e.Ollama.lastBody("/api/chat"), `"images"`)
}

func TestCLIEAskSearchSettingsAndErrors(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("pair-code")
	e.build("pair-docs")

	// --graph and --expand reach the searcher; a <name>-code/<name>-docs pair
	// is addressed by its base name even when the base index is absent.
	r := e.run("ask", "pair", "deploy script?", "--graph", "--expand", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "answer", r.Out, "forty-two")

	r = e.run("ask", "pair", "q", "--provider", "llamacpp", "--no-cache")
	wantExit(t, r, 1)
	mustContain(t, "llamacpp needs a downloaded model", r.Err, "error initializing llamacpp", "gleann models download bge-m3")
}

func TestCLIEAskAgentSteps(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")

	llm := eScriptedLLM(t,
		eLLMStep{Reply: "Thought: I should consult a tool\nAction: no_such_tool\nAction Input: x"},
		eLLMStep{Reply: "Thought: done\nFinal Answer: the answer is 7"},
	)
	r := e.run("ask", "demo", "compute", "--agent", "--no-cache", "--base-url", llm.URL)
	wantExit(t, r, 0)
	mustContain(t, "step printed", r.Err, "Step 1:", `Action="no_such_tool"`)
	mustContain(t, "final answer", r.Out, "the answer is 7")

	// Swarm: the coding agent failing after the search agent worked.
	llm = eScriptedLLM(t, eLLMStep{Reply: "context summary"}, eLLMStep{Status: http.StatusInternalServerError})
	r = e.run("ask", "demo", "q", "--swarm", "--no-cache", "--base-url", llm.URL)
	wantExit(t, r, 1)
	mustContain(t, "coding agent failed", r.Err, "[Search Agent] Context retrieved", "coding agent error")
}

func TestCLIEAskSavedConfig(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")
	eWriteSavedConfig(t, map[string]any{
		"word_wrap": 30,
		"llm_model": "saved-model",
		"roles":     map[string][]string{"pirate": {"Answer like a pirate.", "Be brief."}},
		"format_text": map[string]string{
			"json": "Custom JSON rules.",
		},
	})
	e.Ollama.setReply("alpha beta gamma delta epsilon zeta eta theta iota kappa lambda")

	r := e.run("ask", "demo", "q", "--no-cache", "--role", "pirate", "--format", "JSON", "--smart-context")
	wantExit(t, r, 0)
	body := e.Ollama.lastBody("/api/chat")
	mustContain(t, "saved role and format", body, "Answer like a pirate.\\nBe brief.", "Custom JSON rules.")
	mustContain(t, "saved model", body, `"model":"saved-model"`)
	// The saved word-wrap applies: the one-line reply is wrapped, and an
	// explicit --word-wrap 0 turns that off.
	if lines := strings.Count(strings.TrimSpace(r.Out), "\n") + 1; lines < 2 {
		t.Errorf("saved word wrap not applied, got one line: %q", r.Out)
	}
	r = e.run("ask", "demo", "q", "--no-cache", "--word-wrap", "0")
	wantExit(t, r, 0)
	if got := strings.TrimSpace(r.Out); strings.Contains(got, "\n") {
		t.Errorf("--word-wrap 0 must not wrap: %q", got)
	}

	r = e.run("ask", "demo", "q", "--no-cache", "--llm-model", "flag-model", "--word-wrap", "0")
	wantExit(t, r, 0)
	mustContain(t, "flag model wins", e.Ollama.lastBody("/api/chat"), `"model":"flag-model"`)
}

func TestCLIEChatWithSavedConfig(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("a")
	e.build("b")
	eWriteSavedConfig(t, map[string]any{
		"embedding_provider": "ollama",
		"embedding_model":    "bge-m3",
		"ollama_host":        e.Ollama.URL,
		"ollama_api_key":     "saved-key",
		"llm_provider":       "ollama",
		"llm_model":          "saved-llm",
		"repeat_penalty":     1.3,
	})
	missing := filepath.Join(e.Home, "no-such-session.json")

	// Saved settings apply, flags override them; the TUI is never reached
	// because the session file is unreadable.
	r := e.run("chat", "a,b", "--classic", "--session", missing, "--repetition-penalty", "1.1")
	wantExit(t, r, 1)
	mustContain(t, "multi index loaded", r.Err, "Loading 2 indexes: a,b", "Loading session from", "error loading session")

	r = e.run("chat", "a", "--session", missing, "--repeat-penalty", "x")
	wantExit(t, r, 1)
	mustContain(t, "single index loaded", r.Err, `Loading index "a"`, "error loading session")

	// llamacpp as the embedding provider needs a model first.
	r = e.run("chat", "a", "--provider", "llamacpp")
	wantExit(t, r, 1)
	mustContain(t, "llamacpp", r.Err, "error initializing llamacpp")
}

func TestCLIEAskCiteWithoutAnIndex(t *testing.T) {
	e := newCLIEnv(t)
	e.Ollama.setReply("Go is a language.")
	// No index: there is no searcher embedder, --cite builds one and finds
	// no passages to link.
	r := e.run("ask", "what is go?", "--cite", "--no-cache", "--quiet")
	wantExit(t, r, 0)
	mustContain(t, "answer printed", r.Out, "Go is a language.")
	mustNotContain(t, "no sources", r.Out, "of sentences matched a source")
}
