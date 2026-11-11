package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeLLM records the requests it gets and answers with a fixed status and body.
type fakeLLM struct {
	mu     sync.Mutex
	paths  []string
	bodies [][]byte
	status int
	body   string
}

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.bodies = append(f.bodies, b)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(f.status)
	_, _ = io.WriteString(w, f.body)
}

func newPassthroughServer(t *testing.T, upstream *fakeLLM) *Server {
	t.Helper()
	ts := httptest.NewServer(upstream)
	t.Cleanup(ts.Close)
	s := newProxyTestServer(t)
	s.config.ProxyMode = "passthrough"
	s.config.LLMProvider = "openai"
	s.config.OpenAIBaseURL = ts.URL + "/v1" // a trailing /v1 is accepted
	s.config.LLMModel = "llm"
	return s
}

func TestPassthroughForwardsRequestAndStreamUnchanged(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}],\"ttft\":1.5}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"},\"finish_reason\":\"stop\"}],\"tps\":12.5}\n\n"
	up := &fakeLLM{status: http.StatusOK, body: stream}
	s := newPassthroughServer(t, up)
	reqBody := `{"model":"llm","stream":true,"chat_template_kwargs":{"enable_thinking":true},` +
		`"messages":[{"role":"system","content":"Be brief."},{"role":"user","content":"Hello"}]}`
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody)))

	if w.Code != http.StatusOK || w.Body.String() != stream {
		t.Fatalf("status %d, body %q", w.Code, w.Body.String())
	}
	if len(up.paths) != 1 || up.paths[0] != "/v1/chat/completions" {
		t.Fatalf("upstream paths %v", up.paths)
	}
	var got map[string]any
	if err := json.Unmarshal(up.bodies[0], &got); err != nil {
		t.Fatal(err)
	}
	kwargs, _ := got["chat_template_kwargs"].(map[string]any)
	if kwargs["enable_thinking"] != true || got["stream"] != true || got["model"] != "llm" {
		t.Fatalf("forwarded body %s", up.bodies[0])
	}
	if msgs := got["messages"].([]any); len(msgs) != 2 {
		t.Fatalf("messages changed without an index: %s", up.bodies[0])
	}
}

func TestPassthroughKeepsUpstreamStatusAndForwardsStop(t *testing.T) {
	busy := `{"error":{"message":"busy: another request is running","type":"busy"}}`
	up := &fakeLLM{status: http.StatusServiceUnavailable, body: busy}
	s := newPassthroughServer(t, up)
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"llm","messages":[{"role":"user","content":"Hi"}]}`)))
	if w.Code != http.StatusServiceUnavailable || w.Body.String() != busy {
		t.Fatalf("status %d, body %q", w.Code, w.Body.String())
	}

	up.status, up.body = http.StatusOK, ""
	w = httptest.NewRecorder()
	s.handleProxyStop(w, httptest.NewRequest(http.MethodPost, "/stop", nil))
	if w.Code != http.StatusOK || up.paths[len(up.paths)-1] != "/stop" {
		t.Fatalf("stop: status %d, upstream paths %v", w.Code, up.paths)
	}
}

func TestPassthroughStopNeedsPassthroughMode(t *testing.T) {
	s := newProxyTestServer(t)
	w := httptest.NewRecorder()
	s.handleProxyStop(w, httptest.NewRequest(http.MethodPost, "/stop", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
}

func TestPassthroughUnknownIndexFails(t *testing.T) {
	up := &fakeLLM{status: http.StatusOK}
	s := newPassthroughServer(t, up)
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gleann/missing","messages":[{"role":"user","content":"Hi"}]}`)))
	if w.Code != http.StatusInternalServerError || len(up.paths) != 0 {
		t.Fatalf("status %d, upstream calls %d", w.Code, len(up.paths))
	}
}

func TestInjectContextKeepsOneSystemMessage(t *testing.T) {
	msgs := []map[string]any{{"role": "system", "content": "Be brief."}, {"role": "user", "content": "Q"}}
	out := injectContext(msgs, "CTX")
	if len(out) != 2 || out[0]["content"] != "Be brief.\n\nCTX" || msgs[0]["content"] != "Be brief." {
		t.Fatalf("system string: %v (input %v)", out, msgs)
	}
	out = injectContext([]map[string]any{{"role": "user", "content": "Q"}}, "CTX")
	if len(out) != 2 || out[0]["role"] != "system" || out[0]["content"] != "CTX" {
		t.Fatalf("no system: %v", out)
	}
	parts := []any{map[string]any{"type": "text", "text": "Sys"}}
	out = injectContext([]map[string]any{{"role": "system", "content": parts}}, "CTX")
	if got := out[0]["content"].([]any); len(got) != 2 || len(parts) != 1 {
		t.Fatalf("system parts: %v", out)
	}
}

func TestLastUserText(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "first"},
		{"role": "assistant", "content": "answer"},
		{"role": "user", "content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:"}},
			map[string]any{"type": "text", "text": "what is"},
			map[string]any{"type": "text", "text": "this?"},
		}},
	}
	if got := lastUserText(msgs); got != "what is\nthis?" {
		t.Fatalf("got %q", got)
	}
	if got := lastUserText([]map[string]any{{"role": "system", "content": "x"}}); got != "" {
		t.Fatalf("got %q", got)
	}
}
