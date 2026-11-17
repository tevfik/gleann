package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

// h2LLM is a fake LLM endpoint speaking both the Ollama (/api/chat) and the
// OpenAI (/v1/chat/completions) protocol. It records the request path and
// the last user message of every call and answers with reply (or fails with
// status when status is non-zero).
type h2LLM struct {
	mu     sync.Mutex
	paths  []string
	users  []string
	auth   []string
	reply  string
	status int
}

func (l *h2LLM) handler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	l.mu.Lock()
	l.paths = append(l.paths, r.URL.Path)
	l.auth = append(l.auth, r.Header.Get("Authorization"))
	var last string
	for _, m := range body.Messages {
		if m.Role == "user" {
			last = m.Content
		}
	}
	l.users = append(l.users, last)
	reply, status := l.reply, l.status
	l.mu.Unlock()
	if status != 0 {
		http.Error(w, "llm exploded", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/chat":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"role": "assistant", "content": reply},
			"done":    true,
		})
	case "/v1/chat/completions":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": reply}}},
		})
	default:
		http.NotFound(w, r)
	}
}

func (l *h2LLM) calls() ([]string, []string, []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.paths...), append([]string(nil), l.users...), append([]string(nil), l.auth...)
}

// h2LLMServer builds a small HNSW index "idx" and a server whose LLM points
// at a fake endpoint. mutate may adjust the server Config before NewServer.
func h2LLMServer(t *testing.T, mutate func(*Config)) (*Server, *h2LLM) {
	t.Helper()
	llm := &h2LLM{reply: "the answer is 42"}
	hs := httptest.NewServer(http.HandlerFunc(llm.handler))
	t.Cleanup(hs.Close)

	dir := t.TempDir()
	emb := &testBudgetEmbedder{dim: 8}
	glCfg := gleann.DefaultConfig()
	glCfg.IndexDir = dir
	glCfg.Backend = "hnsw"
	b, err := gleann.NewBuilder(glCfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	items := []gleann.Item{
		{Text: "the payment service retries failed charges", Metadata: map[string]any{"source": "pay/retry.go", "ext": ".go"}},
		{Text: "user login uses JWT tokens", Metadata: map[string]any{"source": "auth/login.go", "ext": ".go"}},
		{Text: "deployment guide for the cluster", Metadata: map[string]any{"source": "docs/deploy.md", "ext": ".md"}},
	}
	if err := b.Build(context.Background(), "idx", items); err != nil {
		t.Fatalf("Build: %v", err)
	}
	cfg := Config{
		IndexDir:          dir,
		EmbeddingProvider: "custom",
		EmbeddingModel:    "test-model",
		LLMProvider:       "ollama",
		LLMModel:          "fake-model",
		OllamaHost:        hs.URL,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	srv := NewServer(cfg)
	srv.embedder = emb
	t.Cleanup(srv.Close)
	return srv, llm
}

func h2Call(t *testing.T, h toolHandler, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	return res
}

func TestH2Ask_AnswersFromLLMWithRetrievedContext(t *testing.T) {
	srv, llm := h2LLMServer(t, nil)
	res := h2Call(t, srv.handleAsk, map[string]any{
		"index":    "idx",
		"question": "how are failed charges handled?",
		"filters":  []any{map[string]any{"field": "ext", "operator": "eq", "value": ".go"}},
	})
	if res.IsError {
		t.Fatalf("ask failed: %s", extractText(res))
	}
	if got := extractText(res); got != "the answer is 42" {
		t.Errorf("answer = %q", got)
	}
	paths, users, _ := llm.calls()
	if len(paths) != 1 || paths[0] != "/api/chat" {
		t.Fatalf("LLM paths = %v, want one /api/chat call", paths)
	}
	if !strings.Contains(users[0], "how are failed charges handled?") {
		t.Errorf("question not forwarded to the LLM: %q", users[0])
	}
	if strings.Contains(users[0], "deployment guide") {
		t.Errorf("the .go filter did not drop the markdown passage: %q", users[0])
	}
}

func TestH2Ask_LLMFailureIsToolError(t *testing.T) {
	srv, llm := h2LLMServer(t, nil)
	llm.status = http.StatusInternalServerError
	res := h2Call(t, srv.handleAsk, map[string]any{"index": "idx", "question": "anything"})
	if !res.IsError || !strings.Contains(extractText(res), "Error asking question") {
		t.Fatalf("want an 'Error asking question' tool error, got %q", extractText(res))
	}
}

func TestH2Ask_OpenAIProviderUsesOpenAIBaseURLAndKey(t *testing.T) {
	var openAI *httptest.Server
	llm2 := &h2LLM{reply: "from openai"}
	openAI = httptest.NewServer(http.HandlerFunc(llm2.handler))
	t.Cleanup(openAI.Close)
	srv, ollama := h2LLMServer(t, func(c *Config) {
		c.LLMProvider = "openai"
		c.OpenAIBaseURL = openAI.URL
		c.OpenAIAPIKey = "sk-test"
	})
	res := h2Call(t, srv.handleAsk, map[string]any{"index": "idx", "question": "who logs in?"})
	if res.IsError || extractText(res) != "from openai" {
		t.Fatalf("ask = %q (error %v)", extractText(res), res.IsError)
	}
	if p, _, _ := ollama.calls(); len(p) != 0 {
		t.Errorf("the Ollama host was called with provider openai: %v", p)
	}
	paths, _, auth := llm2.calls()
	if len(paths) != 1 || paths[0] != "/v1/chat/completions" || auth[0] != "Bearer sk-test" {
		t.Errorf("openai calls = %v auth %v", paths, auth)
	}
}

func TestH2BatchAsk_RunsEveryQuestionAndReportsErrors(t *testing.T) {
	srv, llm := h2LLMServer(t, func(c *Config) {
		c.LLMProvider = "openai"
		c.OpenAIAPIKey = "sk-batch"
	})
	// With provider openai and no OpenAIBaseURL the base URL stays the
	// configured Ollama host: the fake answers on both protocols.
	res := h2Call(t, srv.handleBatchAsk, map[string]any{
		"index":       "idx",
		"questions":   []any{"first?", "second?", "third?"},
		"concurrency": 2.0,
		"top_k":       2.0,
	})
	if res.IsError {
		t.Fatalf("batch failed: %s", extractText(res))
	}
	out := extractText(res)
	for _, want := range []string{`index "idx" (3 questions, concurrency 2)`, "Q1: first?", "Q2: second?", "Q3: third?", "A: the answer is 42"} {
		if !strings.Contains(out, want) {
			t.Errorf("batch output lacks %q:\n%s", want, out)
		}
	}
	paths, _, auth := llm.calls()
	if len(paths) != 3 {
		t.Fatalf("LLM called %d times, want 3", len(paths))
	}
	for i := range paths {
		if paths[i] != "/v1/chat/completions" || auth[i] != "Bearer sk-batch" {
			t.Errorf("call %d: %s %q", i, paths[i], auth[i])
		}
	}

	llm.mu.Lock()
	llm.status = http.StatusBadGateway
	llm.mu.Unlock()
	res = h2Call(t, srv.handleBatchAsk, map[string]any{"index": "idx", "questions": []any{"broken?"}})
	if res.IsError {
		t.Fatalf("a failing question must not fail the batch: %s", extractText(res))
	}
	if out := extractText(res); !strings.Contains(out, "ERROR:") {
		t.Errorf("per-question error not reported:\n%s", out)
	}
}

func TestH2BatchAsk_UnknownIndex(t *testing.T) {
	srv, _ := h2LLMServer(t, nil)
	res := h2Call(t, srv.handleBatchAsk, map[string]any{"index": "nope", "questions": []any{"q"}})
	if !res.IsError || !strings.Contains(extractText(res), `index "nope" not found`) {
		t.Fatalf("got %q", extractText(res))
	}
}

func TestH2Search_ExpandUsesTheLLMOnce(t *testing.T) {
	srv, llm := h2LLMServer(t, func(c *Config) { c.LLMModel = "chat-model" })
	srv.config.SearchConfig.ExpandModel = "expand-model"
	for i := 0; i < 2; i++ {
		res := h2Call(t, srv.handleSearch, map[string]any{"index": "idx", "query": "payment retries", "expand": true})
		if res.IsError {
			t.Fatalf("search failed: %s", extractText(res))
		}
	}
	paths, _, _ := llm.calls()
	if len(paths) == 0 {
		t.Fatal("expand=true never called the LLM")
	}
	first := srv.expander
	if first == nil || srv.queryExpander() != first {
		t.Error("query expander is not created once and shared")
	}
}

func TestH2Prompts_DeepRefactorAndBugHunter(t *testing.T) {
	srv, _ := h2LLMServer(t, nil)
	ctx := context.Background()

	req := mcpsdk.GetPromptRequest{}
	req.Params.Arguments = map[string]string{"index": "idx", "component": "payment service"}
	got, err := srv.handleDeepRefactorPrompt(ctx, req)
	if err != nil {
		t.Fatalf("deep refactor: %v", err)
	}
	text := got.Messages[0].Content.(mcpsdk.TextContent).Text
	if got.Description != "Deep Refactor Plan" || !strings.Contains(text, `"payment service"`) || !strings.Contains(text, "--- Source: ") {
		t.Errorf("deep refactor prompt:\n%s", text)
	}

	req.Params.Arguments = map[string]string{"index": "idx", "error_description": "JWT token rejected"}
	got, err = srv.handleBugHunterPrompt(ctx, req)
	if err != nil {
		t.Fatalf("bug hunter: %v", err)
	}
	text = got.Messages[0].Content.(mcpsdk.TextContent).Text
	if got.Description != "Bug Hunter Analysis" || !strings.Contains(text, "JWT token rejected") || !strings.Contains(text, "--- Source: ") {
		t.Errorf("bug hunter prompt:\n%s", text)
	}

	for _, tc := range []struct {
		name string
		h    func(context.Context, mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error)
		args map[string]string
		want string
	}{
		{"refactor missing component", srv.handleDeepRefactorPrompt, map[string]string{"index": "idx"}, "component argument is required"},
		{"refactor unknown index", srv.handleDeepRefactorPrompt, map[string]string{"index": "nope", "component": "x"}, `failed to load index "nope"`},
		{"bug missing description", srv.handleBugHunterPrompt, map[string]string{"index": "idx"}, "error_description argument is required"},
		{"bug unknown index", srv.handleBugHunterPrompt, map[string]string{"index": "nope", "error_description": "x"}, `failed to load index "nope"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := mcpsdk.GetPromptRequest{}
			r.Params.Arguments = tc.args
			if _, err := tc.h(ctx, r); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
