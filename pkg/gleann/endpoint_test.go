package gleann

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNormalizeOpenAIBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "",
		"  ":                         "",
		"https://api.openai.com":     "https://api.openai.com",
		"https://api.openai.com/v1":  "https://api.openai.com",
		"https://api.openai.com/v1/": "https://api.openai.com",
		" http://vllm:8000/v1 ":      "http://vllm:8000",
		"http://localhost:11434/v1":  "http://localhost:11434",
		"http://proxy/openai/v1":     "http://proxy/openai",
		"http://proxy/v10":           "http://proxy/v10",
	} {
		if got := NormalizeOpenAIBaseURL(in); got != want {
			t.Errorf("NormalizeOpenAIBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveOpenAIBaseURLNeverDefaultsToTheCloud(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	if got := ResolveOpenAIBaseURL(""); got != "" {
		t.Fatalf("no configuration resolved to %q, want empty", got)
	}
	t.Setenv("OPENAI_BASE_URL", "http://env:1/v1")
	if got := ResolveOpenAIBaseURL(""); got != "http://env:1" {
		t.Errorf("env fallback = %q", got)
	}
	if got := ResolveOpenAIBaseURL("http://cfg:2"); got != "http://cfg:2" {
		t.Errorf("configured value must win over the env: %q", got)
	}
}

func TestIsOpenAICloud(t *testing.T) {
	for u, want := range map[string]bool{
		"https://api.openai.com/v1": true,
		"https://API.openai.com":    true,
		"http://vllm:8000/v1":       false,
		"":                          false,
	} {
		if got := IsOpenAICloud(u); got != want {
			t.Errorf("IsOpenAICloud(%q) = %v", u, got)
		}
	}
}

// Each provider gets only its own endpoint and key; in particular the Ollama
// host is never an OpenAI endpoint and the OpenAI key never goes elsewhere.
func TestChatEndpointPairsEndpointAndKeyPerProvider(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv(OllamaAPIKeyEnv, "")
	full := Config{OllamaHost: "http://ollama:11434", OllamaAPIKey: "ok", OpenAIBaseURL: "http://vllm:8000/v1", OpenAIAPIKey: "sk"}
	cases := []struct {
		name      string
		cfg       Config
		p         LLMProvider
		base, key string
	}{
		{"ollama", full, LLMOllama, "http://ollama:11434", "ok"},
		{"empty provider is ollama", full, "", "http://ollama:11434", "ok"},
		{"openai", full, LLMOpenAI, "http://vllm:8000", "sk"},
		{"openai without base url", Config{OllamaHost: "http://ollama:11434", OpenAIAPIKey: "sk"}, LLMOpenAI, "", "sk"},
		{"anthropic gets nothing from Config", full, LLMAnthropic, "", ""},
		{"eif", full, LLMEIF, "", ""},
		{"ollama default host", Config{}, LLMOllama, DefaultOllamaHost, ""},
		{"auto-scan placeholder is not a host", Config{OllamaHost: "(auto-scan default dirs)"}, LLMOllama, DefaultOllamaHost, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, key := tc.cfg.ChatEndpoint(tc.p)
			if base != tc.base || key != tc.key {
				t.Errorf("ChatEndpoint(%q) = %q, %q; want %q, %q", tc.p, base, key, tc.base, tc.key)
			}
		})
	}

	t.Setenv("OLLAMA_HOST", "http://env-ollama:1")
	if base, _ := (Config{}).ChatEndpoint(LLMOllama); base != "http://env-ollama:1" {
		t.Errorf("OLLAMA_HOST fallback = %q", base)
	}
}

// ApplyChatEndpoint replaces a previous provider's endpoint and key.
func TestApplyChatEndpointDropsThePreviousProvidersValues(t *testing.T) {
	cfg := Config{OllamaHost: "http://ollama:11434", OllamaAPIKey: "ok", OpenAIAPIKey: "sk"}
	cc := ChatConfig{Provider: LLMOpenAI, BaseURL: "http://ollama:11434", APIKey: "ok"}
	cfg.ApplyChatEndpoint(&cc)
	if cc.BaseURL != "" || cc.APIKey != "sk" {
		t.Errorf("openai after ollama: base=%q key=%q", cc.BaseURL, cc.APIKey)
	}
}

func TestEmbeddingEndpointPairsEndpointAndKeyPerProvider(t *testing.T) {
	t.Setenv(OllamaAPIKeyEnv, "")
	base := Config{OllamaHost: "http://ollama:11434", OllamaAPIKey: "ok", OpenAIBaseURL: "http://vllm:8000/v1", OpenAIAPIKey: "sk"}
	cases := []struct {
		provider, base, key string
		noOpenAIURL         bool
	}{
		{provider: "", base: "http://ollama:11434", key: "ok"},
		{provider: "ollama", base: "http://ollama:11434", key: "ok"},
		{provider: "openai", base: "http://vllm:8000", key: "sk"},
		{provider: "openai", base: "", key: "sk", noOpenAIURL: true},
		{provider: "llamacpp", base: "http://vllm:8000", key: "sk"},
		{provider: "llamacpp", base: "", key: "", noOpenAIURL: true},
		// The OpenAI key used to be sent to Google as the Gemini key.
		{provider: "gemini", base: "", key: ""},
		{provider: "eif", base: "", key: ""},
		{provider: "sentence-transformers", base: "http://ollama:11434", key: ""},
	}
	for _, tc := range cases {
		cfg := base
		cfg.EmbeddingProvider = tc.provider
		if tc.noOpenAIURL {
			cfg.OpenAIBaseURL = ""
		}
		b, k := cfg.EmbeddingEndpoint()
		if b != tc.base || k != tc.key {
			t.Errorf("EmbeddingEndpoint(%q, openai url %v) = %q, %q; want %q, %q", tc.provider, !tc.noOpenAIURL, b, k, tc.base, tc.key)
		}
	}
}

// Without an endpoint, every OpenAI chat path fails before a request exists:
// nothing is sent to api.openai.com or anywhere else.
func TestOpenAIChatWithoutEndpointSendsNothing(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	var hits atomic.Int32
	c := NewChat(NullSearcher{}, ChatConfig{Provider: LLMOpenAI, Model: "m", APIKey: "sk"})
	// Any request would go through these clients.
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		return nil, errors.New("must not be called: " + r.URL.String())
	})
	c.client = &http.Client{Transport: rt}
	c.streamClient = &http.Client{Transport: rt}

	ctx := context.Background()
	if _, err := c.Ask(ctx, "q"); !errors.Is(err, ErrOpenAIEndpointUnset) {
		t.Errorf("Ask err = %v", err)
	}
	if _, err := c.AskStream(ctx, "q", func(string) {}); !errors.Is(err, ErrOpenAIEndpointUnset) {
		t.Errorf("AskStream err = %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("%d requests were sent", hits.Load())
	}
}

// The documented form ".../v1" works: requests go to /v1/chat/completions,
// not /v1/v1/chat/completions.
func TestOpenAIChatAcceptsBaseURLWithV1(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()
	c := NewChat(NullSearcher{}, ChatConfig{Provider: LLMOpenAI, Model: "m", BaseURL: srv.URL + "/v1/"})
	if out, err := c.Ask(context.Background(), "q"); err != nil || out != "ok" {
		t.Fatalf("Ask = %q, %v", out, err)
	}
	if path != "/v1/chat/completions" {
		t.Errorf("path = %q", path)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
