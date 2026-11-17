package gleann

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// scriptedOllama answers successive /api/chat calls with the given replies
// and records the user message of every call.
func scriptedOllama(t *testing.T, replies ...string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var prompts []string
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		for _, m := range req.Messages {
			if m.Role == "user" {
				prompts = append(prompts, m.Content)
			}
		}
		reply := replies[len(replies)-1]
		if n < len(replies) {
			reply = replies[n]
		}
		n++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(ollamaChatResponse{Message: ChatMessage{Role: "assistant", Content: reply}})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), prompts...)
	}
}

func TestReActAgentRunsToolsUntilFinalAnswer(t *testing.T) {
	srv, prompts := scriptedOllama(t,
		"Thought: look it up\nAction: lookup\nAction Input: capital of France",
		"Thought: try the other tool\nAction: broken\nAction Input: x",
		"Thought: guess a tool\nAction: teleport\nAction Input: Paris",
		"Thought: I now have the answer.\nFinal Answer: Paris",
	)
	var calls []string
	tools := []ReActTool{
		{Name: "lookup", Description: "looks things up", Execute: func(_ context.Context, in string) (string, error) {
			calls = append(calls, in)
			return "Paris is the capital", nil
		}},
		{Name: "broken", Description: "always fails", Execute: func(context.Context, string) (string, error) {
			return "", errors.New("tool offline")
		}},
	}
	agent := NewReActAgent(newTestChat(LLMOllama, srv.URL), tools, 6)
	answer, steps, err := agent.Run(context.Background(), "What is the capital of France?")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Paris" || len(steps) != 3 {
		t.Fatalf("answer %q after %d steps", answer, len(steps))
	}
	if len(calls) != 1 || calls[0] != "capital of France" {
		t.Fatalf("lookup calls = %v", calls)
	}
	if steps[0].Observation != "Paris is the capital" || steps[1].Observation != "Error: tool offline" || steps[2].Observation != "Unknown tool: teleport" {
		t.Fatalf("observations = %q / %q / %q", steps[0].Observation, steps[1].Observation, steps[2].Observation)
	}
	// Each step sees the observations of the previous ones.
	p := prompts()
	if len(p) != 4 || !strings.Contains(p[3], "Observation: Paris is the capital") || !strings.Contains(p[3], "Unknown tool: teleport") {
		t.Fatalf("the last prompt lacks the earlier observations: %q", p[len(p)-1])
	}
	if !strings.Contains(agent.chat.config.SystemPrompt, "- lookup: looks things up") {
		t.Fatalf("system prompt lacks the tool list: %q", agent.chat.config.SystemPrompt)
	}
}

func TestReActAgentStopsAtMaxStepsAndOnLLMError(t *testing.T) {
	srv, _ := scriptedOllama(t, "Thought: keep going\nAction: none\nAction Input: -")
	agent := NewReActAgent(newTestChat(LLMOllama, srv.URL), nil, 2)
	answer, steps, err := agent.Run(context.Background(), "endless?")
	if err != nil || len(steps) != 2 || !strings.Contains(answer, "maximum number of steps") {
		t.Fatalf("answer %q, %d steps, %v", answer, len(steps), err)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer down.Close()
	agent = NewReActAgent(newTestChat(LLMOllama, down.URL), nil, 3)
	if _, _, err := agent.Run(context.Background(), "q"); err == nil || !strings.Contains(err.Error(), "step 1") {
		t.Fatalf("LLM failure: %v", err)
	}
}

// docGraph serves FullDocument for the read tool.
type docGraph struct {
	*mockGraphDB
	docs map[string]string
}

func (d docGraph) FullDocument(path string) (string, error) {
	if t, ok := d.docs[path]; ok {
		return t, nil
	}
	return "", errors.New("no such document")
}

func TestReActSearchAndReadTools(t *testing.T) {
	dir := t.TempDir()
	b, emb := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	long := strings.Repeat("x", 400)
	items := append(f2Items(), Item{Text: "zucchini " + long, Metadata: map[string]any{"source": "z.md"}})
	if err := b.Build(ctx, "react", items); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
	if err := s.Load(ctx, "react"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	search := NewSearchTool(s)
	out, err := search.Execute(ctx, "banana bread")
	if err != nil || !strings.Contains(out, "[b.md]") || !strings.Contains(out, "banana bread") {
		t.Fatalf("search tool = %q, %v", out, err)
	}
	out, err = search.Execute(ctx, "zucchini")
	if err != nil || !strings.Contains(out, "x...") || strings.Contains(out, long) {
		t.Fatalf("long passages must be cut to 300 bytes: %q, %v", out, err)
	}

	read := NewReadDocumentTool(s)
	if out, _ := read.Execute(ctx, "  "); !strings.Contains(out, "cannot be empty") {
		t.Fatalf("empty vpath: %q", out)
	}
	s.graphDB = nil
	if out, _ := read.Execute(ctx, "docs/a.md"); !strings.Contains(out, "not enabled") {
		t.Fatalf("no graph: %q", out)
	}
	s.graphDB = docGraph{mockGraphDB: newMockGraphDB(), docs: map[string]string{
		"docs/a.md":   "short document",
		"docs/big.md": strings.Repeat("y", 17000),
	}}
	defer func() { s.graphDB = nil }()
	if out, _ := read.Execute(ctx, " docs/a.md "); out != "short document" {
		t.Fatalf("read = %q", out)
	}
	if out, _ := read.Execute(ctx, "docs/big.md"); len(out) > 16100 || !strings.HasSuffix(out, "[Document truncated due to length]...") {
		t.Fatalf("a long document must be truncated: %d bytes", len(out))
	}
	if out, _ := read.Execute(ctx, "docs/none.md"); !strings.Contains(out, "no such document") {
		t.Fatalf("missing document: %q", out)
	}
}
