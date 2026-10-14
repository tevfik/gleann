package gleann

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// authCapture is a fake Ollama server (chat, chat-stream, embed) that records
// the Authorization header of the last request.
type authCapture struct {
	mu   sync.Mutex
	auth string
	hits int
}

func (a *authCapture) get() (string, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.auth, a.hits
}

func newOllamaAuthServer(t *testing.T) (*httptest.Server, *authCapture) {
	t.Helper()
	ac := &authCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac.mu.Lock()
		ac.auth = r.Header.Get("Authorization")
		ac.hits++
		ac.mu.Unlock()
		switch r.URL.Path {
		case "/api/chat":
			var req ollamaChatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Stream {
				_, _ = w.Write([]byte(`{"message":{"content":"hi"},"done":false}` + "\n"))
				_, _ = w.Write([]byte(`{"message":{"content":""},"done":true}` + "\n"))
				return
			}
			_ = json.NewEncoder(w).Encode(ollamaChatResponse{
				Message: ChatMessage{Role: "assistant", Content: "ok"},
			})
		case "/api/embed":
			var req ollamaEmbedRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			embs := make([][]float32, len(req.Input))
			for i := range embs {
				embs[i] = []float32{1, float32(i)}
			}
			_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: embs})
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, ac
}

func TestConfigAPIKeyForProvider(t *testing.T) {
	t.Setenv(OllamaAPIKeyEnv, "")
	cfg := Config{OllamaAPIKey: "ollama-k", OpenAIAPIKey: "sk-openai"}
	cases := map[string]string{
		"":          "ollama-k",
		"ollama":    "ollama-k",
		"openai":    "sk-openai",
		"anthropic": "sk-openai", // unchanged legacy behaviour
		"gemini":    "sk-openai",
	}
	for prov, want := range cases {
		if got := cfg.APIKeyForProvider(prov); got != want {
			t.Errorf("APIKeyForProvider(%q) = %q, want %q", prov, got, want)
		}
	}

	// The OpenAI key must never leak to Ollama, even when no Ollama key is set.
	noOllama := Config{OpenAIAPIKey: "sk-openai"}
	if got := noOllama.APIKeyForProvider("ollama"); got != "" {
		t.Errorf("ollama key without config/env = %q, want empty", got)
	}
	t.Setenv(OllamaAPIKeyEnv, "env-k")
	if got := noOllama.APIKeyForProvider("ollama"); got != "env-k" {
		t.Errorf("ollama key from env = %q, want env-k", got)
	}
	if got := cfg.APIKeyForProvider("ollama"); got != "ollama-k" {
		t.Errorf("explicit key should win over env, got %q", got)
	}
}

func TestChatOllamaBearerFromConfigNotOpenAIKey(t *testing.T) {
	t.Setenv(OllamaAPIKeyEnv, "")
	srv, ac := newOllamaAuthServer(t)
	cfg := Config{OllamaAPIKey: "ollama-k", OpenAIAPIKey: "sk-openai"}

	chatCfg := DefaultChatConfig()
	chatCfg.BaseURL = srv.URL
	chatCfg.Model = "m"
	chatCfg.APIKey = cfg.APIKeyForProvider(string(chatCfg.Provider))
	chat := NewChat(NullSearcher{}, chatCfg)

	if _, err := chat.Ask(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ac.get(); got != "Bearer ollama-k" {
		t.Errorf("chat Authorization = %q, want Bearer ollama-k", got)
	}

	if _, err := chat.AskStream(context.Background(), "q2", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if got, _ := ac.get(); got != "Bearer ollama-k" {
		t.Errorf("stream Authorization = %q, want Bearer ollama-k", got)
	}
}

func TestChatOllamaBearerFromEnv(t *testing.T) {
	t.Setenv(OllamaAPIKeyEnv, "env-ollama")
	t.Setenv("OPENAI_API_KEY", "sk-should-not-be-used")
	srv, ac := newOllamaAuthServer(t)

	chat := NewChat(NullSearcher{}, ChatConfig{Provider: LLMOllama, BaseURL: srv.URL, Model: "m"})
	if _, err := chat.Ask(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	if got, _ := ac.get(); got != "Bearer env-ollama" {
		t.Errorf("Authorization = %q, want Bearer env-ollama", got)
	}
	if _, err := chat.AskStream(context.Background(), "q", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if got, _ := ac.get(); got != "Bearer env-ollama" {
		t.Errorf("stream Authorization = %q, want Bearer env-ollama", got)
	}

	// OLLAMA_API_KEY is not picked up by other providers.
	oa := NewChat(NullSearcher{}, ChatConfig{Provider: LLMOpenAI, BaseURL: srv.URL})
	if oa.config.APIKey != "sk-should-not-be-used" {
		t.Errorf("openai provider key = %q, want OPENAI_API_KEY value", oa.config.APIKey)
	}
}

func TestChatOllamaNoKeyNoHeader(t *testing.T) {
	t.Setenv(OllamaAPIKeyEnv, "")
	srv, ac := newOllamaAuthServer(t)
	chat := NewChat(NullSearcher{}, ChatConfig{Provider: LLMOllama, BaseURL: srv.URL, Model: "m"})
	if _, err := chat.Ask(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	if got, hits := ac.get(); got != "" || hits == 0 {
		t.Errorf("Authorization = %q (hits=%d), want none", got, hits)
	}
}

func TestRerankerOllamaBearer(t *testing.T) {
	srv, ac := newOllamaAuthServer(t)
	results := []SearchResult{{Text: "a"}, {Text: "b"}}

	t.Setenv(OllamaAPIKeyEnv, "env-rr")
	r := NewReranker(RerankerConfig{Provider: RerankerOllama, Model: "rr", BaseURL: srv.URL})
	if _, err := r.Rerank(context.Background(), "q", results, 2); err != nil {
		t.Fatal(err)
	}
	if got, _ := ac.get(); got != "Bearer env-rr" {
		t.Errorf("reranker Authorization = %q, want Bearer env-rr", got)
	}

	r = NewReranker(RerankerConfig{Provider: RerankerOllama, Model: "rr", BaseURL: srv.URL, APIKey: "cfg-rr"})
	if _, err := r.Rerank(context.Background(), "q", results, 2); err != nil {
		t.Fatal(err)
	}
	if got, _ := ac.get(); got != "Bearer cfg-rr" {
		t.Errorf("reranker Authorization = %q, want Bearer cfg-rr", got)
	}
}

func TestOllamaGetPostHelpers(t *testing.T) {
	srv, ac := newOllamaAuthServer(t)

	resp, err := OllamaGet(nil, srv.URL+"/api/tags", "k1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got, _ := ac.get(); got != "Bearer k1" {
		t.Errorf("GET Authorization = %q", got)
	}

	resp, err = OllamaPost(srv.Client(), srv.URL+"/api/chat", "application/json", strings.NewReader(`{}`), "")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got, _ := ac.get(); got != "" {
		t.Errorf("POST with empty key should not set Authorization, got %q", got)
	}
}

func TestMaskSecret(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"short":             "********",
		"12345678":          "********",
		"sk-abcdefghijklmn": "sk-…",
	}
	for in, want := range cases {
		if got := MaskSecret(in); got != want {
			t.Errorf("MaskSecret(%q) = %q, want %q", in, got, want)
		}
	}
}
