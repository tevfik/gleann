package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// egressRecorder is a fake LLM server that records every request it gets
// (path and Authorization header) and answers both the Ollama and the OpenAI
// chat protocol.
type egressRecorder struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
	auth  []string
}

func newEgressRecorder(t *testing.T) *egressRecorder {
	t.Helper()
	r := &egressRecorder{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.paths = append(r.paths, req.URL.Path)
		r.auth = append(r.auth, req.Header.Get("Authorization"))
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"from openai-compatible"}}]}`))
		case "/api/chat":
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"from ollama"},"done":true}`))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.Close)
	return r
}

// leaks returns the requests that carried data or a foreign key: any chat
// request, or any request with the given key. Model listings (the chat
// screen lists the reranker's Ollama models with the Ollama key) are fine.
func (r *egressRecorder) leaks(foreignKey string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for i, p := range r.paths {
		if p == "/api/chat" || p == "/api/generate" || p == "/v1/chat/completions" || r.auth[i] == "Bearer "+foreignKey {
			out = append(out, p+" "+r.auth[i])
		}
	}
	return out
}

func (r *egressRecorder) calls() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...), append([]string(nil), r.auth...)
}

// tuiChatConfig runs the TUI chat flow (pure LLM mode) with saved and returns
// the chat it opened.
func tuiChatConfig(t *testing.T, saved OnboardResult) *gleann.LeannChat {
	t.Helper()
	h2bHome(t)
	h2bClassicChat(t)
	if err := SaveConfig(saved); err != nil {
		t.Fatal(err)
	}
	var chat ChatModel
	h2bScript(t, h2bPickNoIndex, h2bChatScreen(&chat))
	if err := RunChatFlow(); err != nil {
		t.Fatalf("RunChatFlow: %v", err)
	}
	return chat.chat
}

// The TUI used to apply ollama_host to every provider: an openai chat was sent
// to the Ollama server with the OpenAI key. Each provider must get only its
// own endpoint and key.
func TestTUIChatEndpointFollowsTheProvider(t *testing.T) {
	cases := []struct {
		name     string
		saved    OnboardResult
		wantBase string
		wantKey  string
	}{
		{
			name: "openai uses openai_base_url, not the Ollama host",
			saved: OnboardResult{LLMProvider: "openai", LLMModel: "m", OllamaHost: "http://ollama.test:11434",
				OllamaAPIKey: "ollama-k", OpenAIBaseURL: "http://vllm.test:8000/v1/", OpenAIKey: "sk-openai"},
			wantBase: "http://vllm.test:8000", wantKey: "sk-openai",
		},
		{
			name: "openai without an endpoint gets none",
			saved: OnboardResult{LLMProvider: "openai", LLMModel: "m", OllamaHost: "http://ollama.test:11434",
				OpenAIKey: "sk-openai"},
			wantBase: "", wantKey: "sk-openai",
		},
		{
			name: "explicit OpenAI cloud URL is kept",
			saved: OnboardResult{LLMProvider: "openai", LLMModel: "m", OllamaHost: "http://ollama.test:11434",
				OpenAIBaseURL: "https://api.openai.com/v1", OpenAIKey: "sk-openai"},
			wantBase: "https://api.openai.com", wantKey: "sk-openai",
		},
		{
			name: "ollama never gets the OpenAI key",
			saved: OnboardResult{LLMProvider: "ollama", LLMModel: "m", OllamaHost: "http://ollama.test:11434",
				OpenAIBaseURL: "http://vllm.test:8000", OpenAIKey: "sk-openai"},
			wantBase: "http://ollama.test:11434", wantKey: "",
		},
		{
			name: "anthropic gets its own key and endpoint",
			saved: OnboardResult{LLMProvider: "anthropic", LLMModel: "m", OllamaHost: "http://ollama.test:11434",
				OpenAIKey: "sk-openai", AnthropicKey: "sk-ant"},
			wantBase: "https://api.anthropic.com", wantKey: "sk-ant",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENAI_BASE_URL", "")
			t.Setenv("OPENAI_API_KEY", "")
			t.Setenv("ANTHROPIC_API_KEY", "")
			t.Setenv(gleann.OllamaAPIKeyEnv, "")
			c := tuiChatConfig(t, tc.saved).Config()
			if c.BaseURL != tc.wantBase || c.APIKey != tc.wantKey {
				t.Errorf("base=%q key=%q, want base=%q key=%q", c.BaseURL, c.APIKey, tc.wantBase, tc.wantKey)
			}
		})
	}
}

// End to end through the TUI flow: a question goes to the configured
// OpenAI-compatible server with the OpenAI key, and nothing reaches Ollama.
func TestTUIChatOpenAIReachesOnlyTheConfiguredServer(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	ollama := newEgressRecorder(t)
	compat := newEgressRecorder(t)
	chat := tuiChatConfig(t, OnboardResult{LLMProvider: "openai", LLMModel: "m",
		OllamaHost: ollama.URL, OllamaAPIKey: "ollama-k", OpenAIBaseURL: compat.URL + "/v1", OpenAIKey: "sk-openai"})

	answer, err := chat.Ask(context.Background(), "hello")
	if err != nil || answer != "from openai-compatible" {
		t.Fatalf("Ask = %q, %v", answer, err)
	}
	if l := ollama.leaks("sk-openai"); len(l) != 0 {
		t.Errorf("the Ollama server got a chat request or the OpenAI key with provider openai: %v", l)
	}
	// The configured server gets the chat (and model-info lookups), always
	// with the OpenAI key.
	p, a := compat.calls()
	chats := 0
	for i := range p {
		if p[i] == "/v1/chat/completions" {
			chats++
		}
		if a[i] != "Bearer sk-openai" {
			t.Errorf("%s sent with auth %q", p[i], a[i])
		}
	}
	if chats != 1 {
		t.Errorf("openai-compatible calls = %v", p)
	}
}

// Without an endpoint the TUI chat fails before sending anything: it neither
// falls back to OpenAI's cloud nor sends the question (and the OpenAI key) to
// the Ollama host, which the old code did.
func TestTUIChatOpenAIWithoutEndpointSendsNothing(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	ollama := newEgressRecorder(t)
	chat := tuiChatConfig(t, OnboardResult{LLMProvider: "openai", LLMModel: "m",
		OllamaHost: ollama.URL, OpenAIKey: "sk-openai"})

	if _, err := chat.Ask(context.Background(), "secret question"); !errors.Is(err, gleann.ErrOpenAIEndpointUnset) {
		t.Fatalf("Ask err = %v, want ErrOpenAIEndpointUnset", err)
	}
	_, err := chat.AskStream(context.Background(), "secret question", func(string) {})
	if !errors.Is(err, gleann.ErrOpenAIEndpointUnset) {
		t.Fatalf("AskStream err = %v, want ErrOpenAIEndpointUnset", err)
	}
	if l := ollama.leaks("sk-openai"); len(l) != 0 {
		t.Errorf("the question or the OpenAI key reached the Ollama host: %v", l)
	}
}

// Listing models for the chat screen must not contact OpenAI's cloud (and
// send it the key) when no endpoint is configured.
func TestFetchOpenAIModelsNeedsAnEndpoint(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	if _, err := fetchModels("openai", "", "sk-openai"); !errors.Is(err, gleann.ErrOpenAIEndpointUnset) {
		t.Fatalf("err = %v, want ErrOpenAIEndpointUnset", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer sk-openai" {
			http.Error(w, r.URL.Path, http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer srv.Close()
	// The documented form ".../v1" reaches /v1/models, not /v1/v1/models.
	models, err := fetchModels("openai", srv.URL+"/v1", "sk-openai")
	if err != nil || len(models) != 1 || models[0].Name != "m1" {
		t.Fatalf("models = %+v, %v", models, err)
	}
}
