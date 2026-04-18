package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// ── buildProxyMessages ─────────────────────────────────────────

func TestBuildProxyMessages_NoIndexes(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	msgs := []oaiMessage{{Role: "user", Content: "hello"}}
	got, err := s.buildProxyMessages(t.Context(), msgs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 message (passthrough), got %d", len(got))
	}
}

func TestBuildProxyMessages_EmptyQuery(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	msgs := []oaiMessage{{Role: "system", Content: "you are helpful"}} // no user message
	got, err := s.buildProxyMessages(t.Context(), msgs, []string{"idx"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 message (no user query), got %d", len(got))
	}
}

// ── syncChatCompletions ────────────────────────────────────────

func TestSyncChatCompletions_Integration(t *testing.T) {
	// Mock LLM backend.
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": "test answer",
			},
			"done": true,
		})
	}))
	defer llm.Close()

	config := gleann.DefaultConfig()
	config.OllamaHost = llm.URL
	s := &Server{config: config, searchers: map[string]*gleann.LeannSearcher{}}

	chatCfg := gleann.ChatConfig{
		Provider: gleann.LLMOllama,
		BaseURL:  llm.URL,
		Model:    "test-model",
	}
	messages := []gleann.ChatMessage{
		{Role: "user", Content: "hello"},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	s.syncChatCompletions(w, req, chatCfg, messages, "gleann/test", "chatcmpl-1", time.Now().Unix())

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp oaiChatResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "test answer" {
		t.Errorf("expected 'test answer', got %q", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("expected finish_reason=stop, got %q", resp.Choices[0].FinishReason)
	}
}

func TestSyncChatCompletions_LLMError(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("model error"))
	}))
	defer llm.Close()

	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	chatCfg := gleann.ChatConfig{
		Provider: gleann.LLMOllama,
		BaseURL:  llm.URL,
		Model:    "bad-model",
	}
	messages := []gleann.ChatMessage{{Role: "user", Content: "hello"}}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	s.syncChatCompletions(w, req, chatCfg, messages, "gleann/test", "chatcmpl-2", time.Now().Unix())

	if w.Code == http.StatusOK {
		t.Error("expected non-200 for LLM error")
	}
}

// ── streamChatCompletions ──────────────────────────────────────

func TestStreamChatCompletions_Integration(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stream NDJSON (Ollama format)
		for _, token := range []string{"hello", " world"} {
			json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]any{"content": token},
				"done":    false,
			})
		}
		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"content": ""},
			"done":    true,
		})
	}))
	defer llm.Close()

	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	chatCfg := gleann.ChatConfig{
		Provider: gleann.LLMOllama,
		BaseURL:  llm.URL,
		Model:    "test-model",
	}
	messages := []gleann.ChatMessage{{Role: "user", Content: "hello"}}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	s.streamChatCompletions(w, req, chatCfg, messages, "gleann/test", "chatcmpl-3", time.Now().Unix())

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected event-stream, got %q", ct)
	}
	body := w.Body.String()
	if !containsSubstring(body, "data:") {
		t.Error("expected SSE data: lines in response")
	}
	if !containsSubstring(body, "[DONE]") {
		t.Error("expected [DONE] sentinel")
	}
}

// ── handleChatCompletions full flow ────────────────────────────

func TestHandleChatCompletions_SyncNoIndex(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"role": "assistant", "content": "pong"},
			"done":    true,
		})
	}))
	defer llm.Close()

	config := gleann.DefaultConfig()
	config.OllamaHost = llm.URL
	s := &Server{config: config, searchers: map[string]*gleann.LeannSearcher{}}

	body, _ := json.Marshal(oaiChatRequest{
		Model:    "gpt-4o", // no "gleann/" prefix → no RAG
		Messages: []oaiMessage{{Role: "user", Content: "ping"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleChatCompletions_BadJSONCov(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString("{bad"))
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleChatCompletions_EmptyMessagesCov(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	body, _ := json.Marshal(oaiChatRequest{Model: "gpt-4o", Messages: nil})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleChatCompletions_WithHeaderOptions(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"role": "assistant", "content": "reply"},
			"done":    true,
		})
	}))
	defer llm.Close()

	config := gleann.DefaultConfig()
	config.OllamaHost = llm.URL
	s := &Server{config: config, searchers: map[string]*gleann.LeannSearcher{}}

	body, _ := json.Marshal(oaiChatRequest{
		Model:    "gpt-4o",
		Messages: []oaiMessage{{Role: "user", Content: "hello"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("X-Gleann-Top-K", "5")
	req.Header.Set("X-Gleann-Min-Score", "0.5")
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ── handleListConversations ────────────────────────────────────

func TestHandleListConversationsCov(t *testing.T) {
	config := gleann.DefaultConfig()
	s := &Server{config: config, searchers: map[string]*gleann.LeannSearcher{}}

	req := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	w := httptest.NewRecorder()
	s.handleListConversations(w, req)

	// Just verify it returns 200 and valid JSON (uses default store)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ── handleGetConversation ──────────────────────────────────────

func TestHandleGetConversation_NotFound(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	req := httptest.NewRequest(http.MethodGet, "/api/conversations/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	s.handleGetConversation(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleGetConversation_MissingID(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	req := httptest.NewRequest(http.MethodGet, "/api/conversations/", nil)
	// Don't set path value → empty ID
	w := httptest.NewRecorder()
	s.handleGetConversation(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ── handleDeleteConversation ───────────────────────────────────

func TestHandleDeleteConversation_NotFound(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	req := httptest.NewRequest(http.MethodDelete, "/api/conversations/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	s.handleDeleteConversation(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ── handleSearch ───────────────────────────────────────────────

func TestHandleSearch_MissingName(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	body, _ := json.Marshal(searchRequest{Query: "test"})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes//search", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", w.Code)
	}
}

func TestHandleSearch_BadJSON(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/search", bytes.NewBufferString("{bad"))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d", w.Code)
	}
}

func TestHandleSearch_EmptyQuery(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	body, _ := json.Marshal(searchRequest{Query: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/search", bytes.NewReader(body))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty query, got %d", w.Code)
	}
}

func TestHandleSearch_IndexNotFound(t *testing.T) {
	config := gleann.DefaultConfig()
	config.IndexDir = t.TempDir()
	s := &Server{config: config, searchers: map[string]*gleann.LeannSearcher{}}
	body, _ := json.Marshal(searchRequest{Query: "test"})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/nonexistent/search", bytes.NewReader(body))
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleSearch(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing index, got %d", w.Code)
	}
}

// ── handleAsk ──────────────────────────────────────────────────

func TestHandleAsk_MissingName(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	body, _ := json.Marshal(askRequest{Question: "what?"})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes//ask", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", w.Code)
	}
}

func TestHandleAsk_BadJSON(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/ask", bytes.NewBufferString("not json"))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d", w.Code)
	}
}

func TestHandleAsk_EmptyQuestion(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	body, _ := json.Marshal(askRequest{Question: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/ask", bytes.NewReader(body))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty question, got %d", w.Code)
	}
}

func TestHandleAsk_IndexNotFound(t *testing.T) {
	config := gleann.DefaultConfig()
	config.IndexDir = t.TempDir()
	s := &Server{config: config, searchers: map[string]*gleann.LeannSearcher{}}
	body, _ := json.Marshal(askRequest{Question: "what?"})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/nonexistent/ask", bytes.NewReader(body))
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing index, got %d", w.Code)
	}
}

// ── handleBuild ────────────────────────────────────────────────

func TestHandleBuild_MissingName(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	body, _ := json.Marshal(buildRequest{Texts: []string{"hello"}})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes//build", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleBuild(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", w.Code)
	}
}

func TestHandleBuild_BadJSON(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/build", bytes.NewBufferString("nope"))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleBuild(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d", w.Code)
	}
}

func TestHandleBuild_EmptyTexts(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	body, _ := json.Marshal(buildRequest{Texts: nil, Items: nil})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/build", bytes.NewReader(body))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()
	s.handleBuild(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty texts, got %d", w.Code)
	}
}

// ── handleDeleteIndex ──────────────────────────────────────────

func TestHandleDeleteIndex_MissingName(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	req := httptest.NewRequest(http.MethodDelete, "/api/indexes/", nil)
	w := httptest.NewRecorder()
	s.handleDeleteIndex(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleDeleteIndex_NonexistentIndex(t *testing.T) {
	config := gleann.DefaultConfig()
	config.IndexDir = t.TempDir()
	s := &Server{config: config, searchers: map[string]*gleann.LeannSearcher{}}
	req := httptest.NewRequest(http.MethodDelete, "/api/indexes/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleDeleteIndex(w, req)
	// May return 200 (no error if dir doesn't exist) or 500
	// Just verify it doesn't panic
}

// ── handleAskStream param validation ───────────────────────────

func TestHandleAsk_StreamQueryParam(t *testing.T) {
	config := gleann.DefaultConfig()
	config.IndexDir = t.TempDir()
	s := &Server{config: config, searchers: map[string]*gleann.LeannSearcher{}}
	body, _ := json.Marshal(askRequest{Question: "hello?"})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/idx/ask?stream=true", bytes.NewReader(body))
	req.SetPathValue("name", "idx")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	// Will fail with "index not found" but exercises the stream query param path
	if w.Code == http.StatusOK {
		t.Error("expected non-200 since index doesn't exist")
	}
}

// ── handleAsk with role ────────────────────────────────────────

func TestHandleAsk_WithRole(t *testing.T) {
	config := gleann.DefaultConfig()
	config.IndexDir = t.TempDir()
	s := &Server{config: config, searchers: map[string]*gleann.LeannSearcher{}}
	body, _ := json.Marshal(askRequest{Question: "hello?", Role: "researcher"})
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/idx/ask", bytes.NewReader(body))
	req.SetPathValue("name", "idx")
	w := httptest.NewRecorder()
	s.handleAsk(w, req)
	// Will fail with "index not found" but exercises role resolution
	if w.Code == http.StatusOK {
		t.Error("expected non-200 since index doesn't exist")
	}
}

// ── Metrics recording ──────────────────────────────────────────

func TestMetrics_RecordAll(t *testing.T) {
	m := &metrics{startTime: time.Now()}

	m.RecordSearch(100*time.Millisecond, false)
	m.RecordSearch(200*time.Millisecond, true)
	m.RecordMultiSearch()
	m.RecordBuild(500*time.Millisecond, false)
	m.RecordBuild(300*time.Millisecond, true)
	m.RecordAsk()
	m.RecordDelete()
	m.RecordWebhook()

	if m.searchRequests.Load() != 2 {
		t.Errorf("expected 2 search requests, got %d", m.searchRequests.Load())
	}
	if m.searchErrors.Load() != 1 {
		t.Errorf("expected 1 search error, got %d", m.searchErrors.Load())
	}
	if m.multiSearchRequests.Load() != 1 {
		t.Errorf("expected 1 multi search, got %d", m.multiSearchRequests.Load())
	}
	if m.buildRequests.Load() != 2 {
		t.Errorf("expected 2 build requests, got %d", m.buildRequests.Load())
	}
	if m.buildErrors.Load() != 1 {
		t.Errorf("expected 1 build error, got %d", m.buildErrors.Load())
	}
	if m.askRequests.Load() != 1 {
		t.Errorf("expected 1 ask, got %d", m.askRequests.Load())
	}
	if m.deleteRequests.Load() != 1 {
		t.Errorf("expected 1 delete, got %d", m.deleteRequests.Load())
	}
	if m.webhooksFired.Load() != 1 {
		t.Errorf("expected 1 webhook, got %d", m.webhooksFired.Load())
	}
}

// ── handleMetrics format ───────────────────────────────────────

func TestHandleMetrics_PrometheusFormat(t *testing.T) {
	config := gleann.DefaultConfig()
	s := &Server{config: config, searchers: map[string]*gleann.LeannSearcher{}}

	// Record some metrics first
	serverMetrics.RecordSearch(50*time.Millisecond, false)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	s.handleMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !containsSubstring(body, "gleann_up 1") {
		t.Error("expected gleann_up gauge")
	}
	if !containsSubstring(body, "gleann_uptime_seconds") {
		t.Error("expected uptime metric")
	}
	if !containsSubstring(body, "gleann_search_requests_total") {
		t.Error("expected search requests counter")
	}
	if !containsSubstring(body, "gleann_cached_searchers") {
		t.Error("expected cached_searchers gauge")
	}
}

// ── writeError ─────────────────────────────────────────────────

func TestWriteError_FormatCov(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "test error")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "test error" {
		t.Errorf("expected 'test error', got %q", resp["error"])
	}
}

// ── withMiddleware ─────────────────────────────────────────────

func TestWithMiddleware_CORSHeaders(t *testing.T) {
	handler := withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS origin header")
	}
}

func TestWithMiddleware_OptionsRequest(t *testing.T) {
	handler := withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // Should not reach here
	}))

	req := httptest.NewRequest(http.MethodOptions, "/test", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for OPTIONS, got %d", w.Code)
	}
}

// ── clientIP / sanitizeIP / isPrivate ──────────────────────────

func TestClientIP_DirectConnectionCov(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.50:12345"
	got := clientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected 203.0.113.50, got %s", got)
	}
}

func TestClientIP_TrustsXFFFromPrivate(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 10.0.0.1")
	got := clientIP(req)
	if got != "198.51.100.1" {
		t.Errorf("expected 198.51.100.1 from XFF, got %s", got)
	}
}

func TestClientIP_IgnoresXFFFromPublic(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.50:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	got := clientIP(req)
	if got != "203.0.113.50" {
		t.Errorf("expected 203.0.113.50 (ignore XFF from public), got %s", got)
	}
}

func TestClientIP_XRealIPFromLoopback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Real-IP", "192.168.1.100")
	got := clientIP(req)
	if got != "192.168.1.100" {
		t.Errorf("expected 192.168.1.100 from X-Real-IP, got %s", got)
	}
}

func TestSanitizeIPCov(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"192.168.1.1", "192.168.1.1"},
		{"  10.0.0.1", "10.0.0.1"},
		{"not-an-ip", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeIP(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeIP(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsPrivateCov(t *testing.T) {
	tests := []struct {
		addr     string
		expected bool
	}{
		{"127.0.0.1:8080", true},
		{"192.168.1.1:8080", true},
		{"10.0.0.1:8080", true},
		{"203.0.113.50:8080", false},
		{"8.8.8.8:53", false},
	}
	for _, tt := range tests {
		got := isPrivate(tt.addr)
		if got != tt.expected {
			t.Errorf("isPrivate(%q) = %v, want %v", tt.addr, got, tt.expected)
		}
	}
}

// helper
func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && fmt.Sprintf("%s", s) != "" && findSubstring(s, sub))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
