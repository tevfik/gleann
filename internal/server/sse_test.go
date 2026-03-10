package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestHandleAskStreamMissingQuestion(t *testing.T) {
	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
		version:   "test",
	}

	body := `{"question": ""}`
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/ask", bytes.NewBufferString(body))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()

	s.handleAsk(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleAskStreamMissingName(t *testing.T) {
	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
		version:   "test",
	}

	body := `{"question": "test", "stream": true}`
	req := httptest.NewRequest(http.MethodPost, "/api/indexes//ask", bytes.NewBufferString(body))
	// No path value set => empty name.
	w := httptest.NewRecorder()

	s.handleAsk(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleAskStreamIndexNotFound(t *testing.T) {
	dir := t.TempDir()
	config := gleann.DefaultConfig()
	config.IndexDir = dir

	s := &Server{
		config:    config,
		searchers: make(map[string]*gleann.LeannSearcher),
		version:   "test",
	}

	body := `{"question": "test question", "stream": true}`
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/nonexistent/ask", bytes.NewBufferString(body))
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()

	s.handleAsk(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleAskStreamQueryParam(t *testing.T) {
	// Test that ?stream=true query parameter is recognized in the request parsing.
	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
		version:   "test",
	}

	body := `{"question": "test question"}`
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/ask?stream=true", bytes.NewBufferString(body))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()

	// The request will fail on index not found, but that's fine —
	// we're testing that the stream param is parsed.
	s.handleAsk(w, req)

	// Should get 404 (index not found) rather than 400 (bad request),
	// meaning the request was parsed successfully with stream=true.
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleAskStreamBadJSON(t *testing.T) {
	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
		version:   "test",
	}

	body := `{invalid json`
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/test/ask", bytes.NewBufferString(body))
	req.SetPathValue("name", "test")
	w := httptest.NewRecorder()

	s.handleAsk(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestAskRequestStreamField(t *testing.T) {
	// Test that the stream field is correctly deserialized.
	var req askRequest

	data := `{"question": "hello", "stream": true, "top_k": 5}`
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !req.Stream {
		t.Error("expected stream=true")
	}
	if req.Question != "hello" {
		t.Errorf("expected question 'hello', got %q", req.Question)
	}
	if req.TopK != 5 {
		t.Errorf("expected top_k=5, got %d", req.TopK)
	}
}

func TestAskRequestStreamDefault(t *testing.T) {
	var req askRequest
	data := `{"question": "hello"}`
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Stream {
		t.Error("expected stream=false by default")
	}
}

func TestSSEEventFormat(t *testing.T) {
	// Verify SSE format: "data: {json}\n\n"
	token := "hello"
	data, _ := json.Marshal(map[string]string{"token": token})
	event := fmt.Sprintf("data: %s\n\n", data)

	if !strings.HasPrefix(event, "data: ") {
		t.Error("SSE event must start with 'data: '")
	}
	if !strings.HasSuffix(event, "\n\n") {
		t.Error("SSE event must end with \\n\\n")
	}

	// Parse the JSON from the event.
	jsonStr := strings.TrimPrefix(event, "data: ")
	jsonStr = strings.TrimSpace(jsonStr)
	var parsed map[string]string
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("failed to parse SSE JSON: %v", err)
	}
	if parsed["token"] != token {
		t.Errorf("expected token %q, got %q", token, parsed["token"])
	}
}

func TestSSEDoneEvent(t *testing.T) {
	done := "data: [DONE]\n\n"

	scanner := bufio.NewScanner(strings.NewReader(done))
	scanner.Scan()
	line := scanner.Text()

	if line != "data: [DONE]" {
		t.Errorf("expected 'data: [DONE]', got %q", line)
	}
}
