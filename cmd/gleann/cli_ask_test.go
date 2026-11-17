package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/conversations"
)

// CLI tests of `gleann ask` and the non-interactive parts of `gleann chat`
// against the fake Ollama (embeddings and /api/chat).

// askConversations returns the conversations saved under the test home.
func askConversations(t *testing.T) []conversations.Conversation {
	t.Helper()
	list, err := conversations.DefaultStore().List()
	if err != nil {
		t.Fatal(err)
	}
	var out []conversations.Conversation
	for _, c := range list {
		full, err := conversations.DefaultStore().Load(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, *full)
	}
	return out
}

func TestCLIAskAnswersFromTheIndex(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")
	e.Ollama.setReply("Tokens are checked by ValidateToken.")

	r := e.run("ask", "demo", "how", "are", "tokens", "validated?", "--title", "Auth question")
	wantExit(t, r, 0)
	mustContain(t, "answer", r.Out, "Tokens are checked by ValidateToken.")
	// The question and the retrieved context reach the LLM.
	body := e.Ollama.lastBody("/api/chat")
	mustContain(t, "chat request", body, "how are tokens validated?", "ValidateToken")

	convs := askConversations(t)
	if len(convs) != 1 || convs[0].Title != "Auth question" || len(convs[0].Messages) < 2 || convs[0].Indexes[0] != "demo" {
		t.Fatalf("saved conversation = %+v", convs)
	}
	id := convs[0].ID

	// --continue restores the history: the earlier question is sent again.
	r = e.run("ask", "and", "rate", "limits?", "--continue", id)
	wantExit(t, r, 0)
	mustContain(t, "continue", r.Err, "Continuing conversation")
	mustContain(t, "continued history", e.Ollama.lastBody("/api/chat"), "how are tokens validated?", "and rate limits?")

	r = e.run("ask", "one", "more", "--continue-last", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "continue-last", r.Err, "Continuing conversation")
	if got := askConversations(t); len(got) != 1 || len(got[0].Messages) != 4 {
		t.Errorf("--no-cache must not save; conversation now %+v", got)
	}

	r = e.run("ask", "x", "--continue", "no-such-conversation")
	wantExit(t, r, 1)
	mustContain(t, "unknown conversation", r.Err, "error loading conversation")
}

func TestCLIAskModes(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")
	e.Ollama.setReply("The token bucket rate limiter allows bursts of requests.")

	// --cite links answer sentences to the passages they came from.
	r := e.run("ask", "demo", "what does the rate limiter allow?", "--cite", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "cite", r.Out, "of sentences matched a source")

	// Role and format shape the system prompt.
	r = e.run("ask", "demo", "list the files", "--role", "explain", "--format", "json", "--no-cache", "--no-limit", "--repeat-penalty", "1.2", "--word-wrap", "40")
	wantExit(t, r, 0)
	body := e.Ollama.lastBody("/api/chat")
	mustContain(t, "format json", body, "Respond ONLY with valid JSON")
	var req struct {
		Options map[string]any `json:"options"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil || req.Options["repeat_penalty"] != 1.2 {
		t.Errorf("repeat penalty not sent: %v %v", req.Options, err)
	}
	for format, want := range map[string]string{"markdown": "well-structured Markdown", "raw": "plain text", "yaml": "Respond in yaml format."} {
		r = e.run("ask", "demo", "q", "--format", format, "--no-cache")
		wantExit(t, r, 0)
		mustContain(t, "format "+format, e.Ollama.lastBody("/api/chat"), want)
	}
	r = e.run("ask", "demo", "q", "--role", "no-such-role")
	wantExit(t, r, 1)
	mustContain(t, "bad role", r.Err, "error")

	// Swarm: a search agent and a coding agent, two LLM calls.
	before := e.Ollama.count("/api/chat")
	r = e.run("ask", "demo", "explain auth", "--swarm", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "swarm", r.Err, "Swarm Mode", "[Search Agent]", "[Coding Agent]")
	if n := e.Ollama.count("/api/chat") - before; n < 2 {
		t.Errorf("swarm made %d LLM calls, want 2", n)
	}

	// Agent mode: a reply without an action is the final answer.
	e.Ollama.setReply("Final Answer: tokens are validated by ValidateToken.")
	r = e.run("ask", "demo", "how are tokens validated", "--agent", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "agent", r.Err, "Agent mode (index: demo")
	mustContain(t, "agent answer", r.Out, "ValidateToken")

	// Interactive: questions from stdin until "quit".
	e.Ollama.setReply("interactive reply")
	r = e.runIn("\nfirst question\nquit\n", "ask", "demo", "--interactive", "--title", "Interactive")
	wantExit(t, r, 0)
	mustContain(t, "interactive", r.Out, "You: ", "Assistant: interactive reply", "Goodbye!")
	found := false
	for _, c := range askConversations(t) {
		if c.Title == "Interactive" {
			found = true
		}
	}
	if !found {
		t.Error("interactive session was not saved on quit")
	}
}

func TestCLIAskIndexResolution(t *testing.T) {
	e := newCLIEnv(t)
	e.Ollama.setReply("pure llm reply")

	// No index at all: pure LLM mode.
	// (Status lines are quiet: stdout is not a terminal, so raw mode.)
	r := e.run("ask", "what is go?", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "pure llm", r.Out, "pure llm reply")

	// Question from stdin.
	r = e.runIn("piped question text\n", "ask", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "stdin question", e.Ollama.lastBody("/api/chat"), "piped question text")

	// Exactly one index: it is used.
	e.sampleDocs()
	e.build("only")
	r = e.run("ask", "where is the deploy script?", "--no-cache")
	wantExit(t, r, 0)
	mustContain(t, "single index context", e.Ollama.lastBody("/api/chat"), "Deploy the service with the deploy script")

	// Several indexes and stdin is not a terminal: no picker that cannot be
	// answered; the indexes are listed and the user is told to name one.
	e.build("second")
	r = e.runIn("9\n", "ask", "where is the guide?", "--no-cache")
	wantExit(t, r, 1)
	mustContain(t, "no picker without a terminal", r.Err, "Available indexes:", "1. only", "2. second", "name one: gleann ask <index> <question>")
	if strings.Contains(r.Err, "Select index") {
		t.Errorf("picker shown without a terminal: %s", r.Err)
	}

	// Comma separated indexes.
	r = e.run("ask", "only,second", "deploy?", "--no-cache", "--rerank", "--rerank-url", e.Ollama.URL)
	wantExit(t, r, 0)

	// A --dual pair is addressed by its base name, as in search: "pair" is
	// the index, not the first word of the question.
	e.build("pair-docs")
	r = e.run("ask", "pair", "where is the deploy script?", "--no-cache")
	wantExit(t, r, 0)
	body := e.Ollama.lastBody("/api/chat")
	mustContain(t, "pair context", body, "Deploy the service with the deploy script")
	if strings.Contains(body, "pair where is") {
		t.Errorf("the pair name was sent as part of the question: %s", body)
	}

	// An explicit but missing index fails to load.
	r = e.run("ask", "only,missing", "deploy?", "--no-cache")
	wantExit(t, r, 1)
	mustContain(t, "missing index", r.Err, "error loading indexes")
}

func TestCLIAskErrors(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("ask")
	wantExit(t, r, 1)
	mustContain(t, "usage", r.Err, "usage: gleann ask", "--continue-last")

	r = e.run("ask", "--no-cache")
	wantExit(t, r, 1)
	mustContain(t, "no question", r.Err, "no question provided")

	r = e.run("ask", "q", "--continue-last")
	wantExit(t, r, 1)
	mustContain(t, "no conversation", r.Err, "no previous conversation found")

	e.Ollama.mu.Lock()
	e.Ollama.failChat = true
	e.Ollama.mu.Unlock()
	r = e.run("ask", "q", "--no-cache")
	wantExit(t, r, 1)
	mustContain(t, "llm down", r.Err, "error:", "500")
	r = e.run("ask", "q", "--swarm", "--no-cache")
	wantExit(t, r, 1)
	mustContain(t, "swarm llm down", r.Err, "search agent error")
	r = e.run("ask", "q", "--agent", "--no-cache")
	wantExit(t, r, 1)
	mustContain(t, "agent llm down", r.Err, "agent error")
}

func TestCLIChatErrorsBeforeTheTUI(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("chat", "missing-index")
	wantExit(t, r, 1)
	mustContain(t, "missing index", r.Err, `Loading index "missing-index"`, `error loading index "missing-index"`)

	r = e.run("chat", "a,b")
	wantExit(t, r, 1)
	mustContain(t, "missing indexes", r.Err, "Loading 2 indexes", "error loading indexes")

	e.sampleDocs()
	e.build("demo")
	r = e.run("chat", "demo", "--session", filepath.Join(e.Home, "no-such-session.json"), "--llm-model", "m", "--llm-provider", "ollama", "--repeat-penalty", "1.1", "--api-key", "k")
	wantExit(t, r, 1)
	mustContain(t, "bad session", r.Err, "error loading session")

	// --list goes to the conversation listing, no TUI.
	r = e.run("chat", "--list")
	wantExit(t, r, 0)
}

func TestCLIIndexInfoShowsFilesAndPlugins(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo", "--tags", "a,b", "--desc", "Described")
	meta := filepath.Join(e.IndexDir, "demo", "demo.meta.json")
	b, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["plugin_versions"] = map[string]string{"gleann-plugin-docs": "1.2.3"}
	m["gleann_version"] = "v9.9.9"
	b, _ = json.Marshal(m)
	if err := os.WriteFile(meta, b, 0o644); err != nil {
		t.Fatal(err)
	}
	r := e.run("index", "info", "demo")
	wantExit(t, r, 0)
	mustContain(t, "info", r.Out, "Tags:        a, b", "Description: Described", "Engine Ver:  v9.9.9", "gleann-plugin-docs=1.2.3", ".db:")

	if err := os.WriteFile(meta, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run("index", "info", "demo")
	wantExit(t, r, 1)
	if !strings.Contains(r.Err, "error") {
		t.Errorf("broken meta: %q", r.Err)
	}
}
