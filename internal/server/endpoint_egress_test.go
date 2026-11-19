package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// allPathsFake records every request (path and Authorization header).
func allPathsFake(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Authorization"))
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// POST /api/indexes/{name}/ask with "llm_provider": "openai" used to keep the
// Ollama host as the endpoint and fall back to OPENAI_API_KEY, so the
// question and the environment's OpenAI key went to the Ollama host. Without
// an OpenAI endpoint the request must fail and send nothing.
func TestAskProviderSwitchToOpenAIWithoutEndpointSendsNothing(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "sk-env")
	s, h, _ := newAskServer(t)
	ollama, seen := allPathsFake(t)
	s.cfgMu.Lock()
	s.config.OllamaHost = ollama.URL
	s.cfgMu.Unlock()

	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "secret?", "llm_provider": "openai"})
	if w.Code == http.StatusOK || !strings.Contains(w.Body.String(), "openai endpoint not set") {
		t.Errorf("status %d body %s, want the missing-endpoint error", w.Code, w.Body.String())
	}
	if len(*seen) != 0 {
		t.Errorf("requests reached the Ollama host: %v", *seen)
	}
}

// With an endpoint, a request that switches to openai goes there with the
// configured OpenAI key.
func TestAskProviderSwitchToOpenAIUsesTheOpenAIEndpoint(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	s, h, rec := newAskServer(t)
	compat, seen := allPathsFake(t)
	s.cfgMu.Lock()
	s.config.OpenAIBaseURL = compat.URL + "/v1"
	s.config.OpenAIAPIKey = "sk-cfg"
	s.cfgMu.Unlock()

	_ = postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "q", "llm_provider": "openai"})
	if len(*seen) == 0 || (*seen)[len(*seen)-1] != "/v1/chat/completions Bearer sk-cfg" {
		t.Errorf("openai-compatible server got %v", *seen)
	}
	if rec.calls() != 0 {
		t.Errorf("the Ollama fake got %d chat requests", rec.calls())
	}
}

// A stored OpenAI key alone must not switch the proxy to openai (it used to,
// which sent chats to api.openai.com when a key was left in the config).
func TestProxyLLMConfigKeyAloneDoesNotSelectOpenAI(t *testing.T) {
	t.Setenv(gleann.OllamaAPIKeyEnv, "")
	s := &Server{config: gleann.Config{OllamaHost: "http://ollama:11434", OllamaAPIKey: "ok", OpenAIAPIKey: "sk"}}
	cc := s.proxyLLMConfig()
	if cc.Provider != gleann.LLMOllama || cc.BaseURL != "http://ollama:11434" || cc.APIKey != "ok" {
		t.Errorf("proxy config = %q %q %q", cc.Provider, cc.BaseURL, cc.APIKey)
	}

	// Configured openai without a base URL: no endpoint, never the Ollama host.
	s = &Server{config: gleann.Config{LLMProvider: "openai", OllamaHost: "http://ollama:11434", OpenAIAPIKey: "sk"}}
	if cc = s.proxyLLMConfig(); cc.Provider != gleann.LLMOpenAI || cc.BaseURL != "" || cc.APIKey != "sk" {
		t.Errorf("openai proxy config = %q %q %q", cc.Provider, cc.BaseURL, cc.APIKey)
	}
}

// Pass-through mode with provider openai and no endpoint answers 503 and
// forwards nothing.
func TestPassthroughWithoutOpenAIEndpointIs503(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	up := &fakeLLM{status: http.StatusOK, body: "{}"}
	s := newPassthroughServer(t, up)
	s.config.OpenAIBaseURL = ""
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"llm","messages":[{"role":"user","content":"secret"}]}`)))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "openai endpoint not set") {
		t.Errorf("status %d body %s", w.Code, w.Body.String())
	}
	if len(up.paths) != 0 {
		t.Errorf("forwarded to %v", up.paths)
	}
}

// HyDE expansion uses the chat provider's endpoint: openai without a base URL
// gets none (it used to get the Ollama host with the OpenAI key).
func TestQueryExpanderEndpointFollowsTheProvider(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	s := &Server{}
	cc := s.queryExpander(gleann.Config{LLMProvider: "openai", OllamaHost: "http://ollama:11434", OpenAIAPIKey: "sk"}).Chat.Config()
	if cc.BaseURL != "" || cc.APIKey != "sk" {
		t.Errorf("expander endpoint = %q key %q", cc.BaseURL, cc.APIKey)
	}
}
