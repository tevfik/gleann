package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// newProxyTestServer returns a Server with a temp index dir for proxy tests.
func newProxyTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir
	return &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
		addr:      ":8080",
		version:   "proxy-test",
	}
}

// ---------------------------------------------------------------------------
// parseIndexFromModel
// ---------------------------------------------------------------------------

func TestParseIndexFromModel(t *testing.T) {
	cases := []struct {
		model    string
		expected []string
	}{
		{"gleann/my-docs", []string{"my-docs"}},
		{"gleann/a,b", []string{"a", "b"}},
		{"gleann/a, b , c", []string{"a", "b", "c"}},
		{"gleann/", nil},
		{"gpt-4o", nil},
		{"llama3.2:3b", nil},
		{"gleann/single", []string{"single"}},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			got := parseIndexFromModel(tc.model)
			if len(got) != len(tc.expected) {
				t.Fatalf("parseIndexFromModel(%q) = %v, want %v", tc.model, got, tc.expected)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tc.expected[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// lastUserContent
// ---------------------------------------------------------------------------

func TestLastUserContent(t *testing.T) {
	msgs := []oaiMessage{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second question"},
	}
	got := lastUserContent(msgs)
	if got != "second question" {
		t.Errorf("got %q, want %q", got, "second question")
	}
}

func TestLastUserContentEmpty(t *testing.T) {
	msgs := []oaiMessage{
		{Role: "system", Content: "only system"},
	}
	got := lastUserContent(msgs)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestLastUserContentNone(t *testing.T) {
	if lastUserContent(nil) != "" {
		t.Error("expected empty string for nil messages")
	}
}

// ---------------------------------------------------------------------------
// buildProxyMessages — no index (pass-through)
// ---------------------------------------------------------------------------

func TestBuildProxyMessagesNoIndex(t *testing.T) {
	s := newProxyTestServer(t)
	msgs := []oaiMessage{
		{Role: "user", Content: "hello"},
	}
	out, err := s.buildProxyMessages(context.Background(), msgs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Errorf("expected 1 message (no context injection), got %d", len(out))
	}
	if out[0].Content != "hello" {
		t.Errorf("unexpected content: %q", out[0].Content)
	}
}

// ---------------------------------------------------------------------------
// buildProxyMessages — unknown index returns error
// ---------------------------------------------------------------------------

func TestBuildProxyMessagesUnknownIndex(t *testing.T) {
	s := newProxyTestServer(t)
	msgs := []oaiMessage{
		{Role: "user", Content: "question"},
	}
	_, err := s.buildProxyMessages(context.Background(), msgs, []string{"nonexistent"}, nil)
	if err == nil {
		t.Fatal("expected error for non-existent index, got nil")
	}
}

// ---------------------------------------------------------------------------
// GET /v1/models
// ---------------------------------------------------------------------------

func TestHandleListModels_Empty(t *testing.T) {
	s := newProxyTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	s.handleListModels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp oaiModelList
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Object != "list" {
		t.Errorf("expected object=list, got %q", resp.Object)
	}
	// At minimum the "gleann/" (pure LLM) entry should exist.
	if len(resp.Data) == 0 {
		t.Fatal("expected at least one model entry")
	}
	found := false
	for _, m := range resp.Data {
		if m.ID == "gleann/" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'gleann/' entry in model list")
	}
}

func TestHandleListModels_ContentType(t *testing.T) {
	s := newProxyTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	s.handleListModels(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestHandleListModels_ModelFields(t *testing.T) {
	s := newProxyTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	s.handleListModels(w, req)

	var resp oaiModelList
	json.NewDecoder(w.Body).Decode(&resp)

	for _, m := range resp.Data {
		if m.Object != "model" {
			t.Errorf("model %q: expected object=model, got %q", m.ID, m.Object)
		}
		if m.OwnedBy != "gleann" {
			t.Errorf("model %q: expected owned_by=gleann, got %q", m.ID, m.OwnedBy)
		}
		if m.Created == 0 {
			t.Errorf("model %q: expected non-zero created timestamp", m.ID)
		}
	}
}

// ---------------------------------------------------------------------------
// POST /v1/chat/completions — bad requests
// ---------------------------------------------------------------------------

func TestHandleChatCompletions_InvalidJSON(t *testing.T) {
	s := newProxyTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", w.Code)
	}
}

func TestHandleChatCompletions_EmptyMessages(t *testing.T) {
	s := newProxyTestServer(t)

	body, _ := json.Marshal(oaiChatRequest{
		Model:    "gleann/",
		Messages: []oaiMessage{},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty messages, got %d", w.Code)
	}
}

func TestHandleChatCompletions_UnknownIndex(t *testing.T) {
	s := newProxyTestServer(t)

	body, _ := json.Marshal(oaiChatRequest{
		Model: "gleann/does-not-exist",
		Messages: []oaiMessage{
			{Role: "user", Content: "hello"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for unknown index, got %d", w.Code)
	}

	var errResp map[string]string
	json.NewDecoder(w.Body).Decode(&errResp)
	if !strings.Contains(errResp["error"], "RAG retrieval failed") {
		t.Errorf("unexpected error message: %q", errResp["error"])
	}
}

func TestHandleChatCompletions_StreamUnknownIndex(t *testing.T) {
	s := newProxyTestServer(t)

	body, _ := json.Marshal(oaiChatRequest{
		Model:    "gleann/no-such-index",
		Messages: []oaiMessage{{Role: "user", Content: "hi"}},
		Stream:   true,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	// RAG retrieval fails → 500 before streaming starts.
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for stream with unknown index, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// proxyLLMConfig — smoke test (no panics)
// ---------------------------------------------------------------------------

func TestProxyLLMConfig_Defaults(t *testing.T) {
	s := newProxyTestServer(t)
	cfg := s.proxyLLMConfig()
	if cfg.Model == "" {
		t.Error("expected default LLM model to be set")
	}
}

// ---------------------------------------------------------------------------
// Integration: /v1/models via mux
// ---------------------------------------------------------------------------

func TestListModelsRoute(t *testing.T) {
	s := newProxyTestServer(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", s.handleListModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body oaiModelList
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Object != "list" {
		t.Errorf("expected object=list, got %q", body.Object)
	}
}

// ---------------------------------------------------------------------------
// OpenAPI spec — proxy paths and schemas present
// ---------------------------------------------------------------------------

func TestOpenAPISpecProxyPaths(t *testing.T) {
	s := newProxyTestServer(t)
	spec := s.openAPISpec()

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("missing paths in spec")
	}

	for _, path := range []string{"/v1/models", "/v1/chat/completions"} {
		if _, ok := paths[path]; !ok {
			t.Errorf("missing proxy path %q in OpenAPI spec", path)
		}
	}
}

func TestOpenAPISpecProxySchemas(t *testing.T) {
	s := newProxyTestServer(t)
	spec := s.openAPISpec()

	components := spec["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)

	for _, schema := range []string{
		"ChatCompletionRequest",
		"ChatCompletionResponse",
		"ChatMessage",
		"ModelObject",
		"ModelList",
	} {
		if _, ok := schemas[schema]; !ok {
			t.Errorf("missing schema %q in OpenAPI spec", schema)
		}
	}
}

// ---------------------------------------------------------------------------
// SSE chunk serialization
// ---------------------------------------------------------------------------

func TestOAIChunkSerialization(t *testing.T) {
	stop := "stop"
	chunk := oaiChunk{
		ID:      "chatcmpl-test",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gleann/my-docs",
		Choices: []oaiStreamChoice{{
			Index:        0,
			Delta:        oaiDelta{Content: "hello"},
			FinishReason: &stop,
		}},
	}

	data, err := json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}

	line := "data: " + string(data)
	if !strings.HasPrefix(line, "data: ") {
		t.Error("expected SSE line to start with 'data: '")
	}

	trimmed := strings.TrimPrefix(line, "data: ")
	var out oaiChunk
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		t.Fatalf("failed to unmarshal SSE chunk: %v", err)
	}
	if out.Choices[0].Delta.Content != "hello" {
		t.Errorf("expected content 'hello', got %q", out.Choices[0].Delta.Content)
	}
	if out.Choices[0].FinishReason == nil || *out.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop'")
	}
}

func TestSSEDoneSentinel(t *testing.T) {
	raw := "data: {\"id\":\"x\"}\n\ndata: [DONE]\n\n"
	scanner := bufio.NewScanner(strings.NewReader(raw))
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			lines = append(lines, strings.TrimPrefix(line, "data: "))
		}
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 data lines, got %d", len(lines))
	}
	if lines[1] != "[DONE]" {
		t.Errorf("expected '[DONE]', got %q", lines[1])
	}
}

// ---------------------------------------------------------------------------
// Header override parsing — invalid value should not panic
// ---------------------------------------------------------------------------

func TestTopKHeaderOverride_Invalid(t *testing.T) {
	s := newProxyTestServer(t)

	body, _ := json.Marshal(oaiChatRequest{
		Model:    "gleann/nonexistent",
		Messages: []oaiMessage{{Role: "user", Content: "test"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gleann-Top-K", "not-a-number") // invalid — should be ignored
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	// Must return a response (not panic).
	if w.Code == 0 {
		t.Error("expected a response code")
	}
}

func TestMinScoreHeaderOverride_Invalid(t *testing.T) {
	s := newProxyTestServer(t)

	body, _ := json.Marshal(oaiChatRequest{
		Model:    "gleann/nonexistent",
		Messages: []oaiMessage{{Role: "user", Content: "test"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gleann-Min-Score", "banana") // invalid — should be ignored
	w := httptest.NewRecorder()

	s.handleChatCompletions(w, req)

	if w.Code == 0 {
		t.Error("expected a response code")
	}
}
