package a2a

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testServer() *Server {
	card := DefaultAgentCard("test-1.0", "http://localhost:8080")
	s := NewServer(card)

	// Register test skill handlers.
	s.RegisterSkill("semantic-search", func(ctx SkillContext) (string, error) {
		return fmt.Sprintf("Search results for: %s", ctx.Query), nil
	})
	s.RegisterSkill("ask-rag", func(ctx SkillContext) (string, error) {
		return fmt.Sprintf("Answer: based on the context, %s", ctx.Query), nil
	})
	s.RegisterSkill("memory-management", func(ctx SkillContext) (string, error) {
		return "Memory stored successfully", nil
	})
	s.RegisterSkill("code-analysis", func(ctx SkillContext) (string, error) {
		return "Found 3 callers", nil
	})

	return s
}

func TestAgentCard(t *testing.T) {
	s := testServer()
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("GET", "/.well-known/agent-card.json", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var card AgentCard
	if err := json.Unmarshal(w.Body.Bytes(), &card); err != nil {
		t.Fatal("invalid JSON:", err)
	}

	if card.Name != "gleann" {
		t.Errorf("expected name=gleann, got %s", card.Name)
	}
	if card.Version != "test-1.0" {
		t.Errorf("expected version=test-1.0, got %s", card.Version)
	}
	if len(card.Skills) != 4 {
		t.Errorf("expected 4 skills, got %d", len(card.Skills))
	}
	if len(card.SupportedInterfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(card.SupportedInterfaces))
	}
	if card.SupportedInterfaces[0].ProtocolBinding != "HTTP+JSON" {
		t.Errorf("expected HTTP+JSON binding, got %s", card.SupportedInterfaces[0].ProtocolBinding)
	}
}

func TestSendMessage_SearchRouting(t *testing.T) {
	s := testServer()
	mux := http.NewServeMux()
	s.Mount(mux)

	reqBody := SendMessageRequest{
		Message: Message{
			MessageID: "msg-1",
			Role:      "ROLE_USER",
			Parts:     []Part{{Text: "Search for authentication patterns"}},
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/a2a/v1/message:send", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp SendMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	if resp.Task == nil {
		t.Fatal("expected task in response")
	}
	if resp.Task.Status.State != TaskStateCompleted {
		t.Errorf("expected COMPLETED, got %s", resp.Task.Status.State)
	}
	if len(resp.Task.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(resp.Task.Artifacts))
	}
	if resp.Task.Artifacts[0].Parts[0].Text == "" {
		t.Error("expected non-empty artifact text")
	}
}

func TestSendMessage_MemoryRouting(t *testing.T) {
	s := testServer()
	mux := http.NewServeMux()
	s.Mount(mux)

	reqBody := SendMessageRequest{
		Message: Message{
			MessageID: "msg-2",
			Role:      "ROLE_USER",
			Parts:     []Part{{Text: "Remember that the API uses JWT tokens"}},
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/a2a/v1/message:send", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp SendMessageResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Task == nil {
		t.Fatal("expected task")
	}
	if resp.Task.Status.State != TaskStateCompleted {
		t.Errorf("expected COMPLETED, got %s", resp.Task.Status.State)
	}
}

func TestSendMessage_ExplicitSkill(t *testing.T) {
	s := testServer()
	mux := http.NewServeMux()
	s.Mount(mux)

	reqBody := SendMessageRequest{
		Message: Message{
			MessageID: "msg-3",
			Role:      "ROLE_USER",
			Parts:     []Part{{Text: "analyze this code"}},
		},
		Metadata: map[string]interface{}{
			"skill": "code-analysis",
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/a2a/v1/message:send", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp SendMessageResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Task == nil || resp.Task.Status.State != TaskStateCompleted {
		t.Fatal("expected completed task")
	}
	if resp.Task.Artifacts[0].Parts[0].Text != "Found 3 callers" {
		t.Errorf("unexpected artifact: %s", resp.Task.Artifacts[0].Parts[0].Text)
	}
}

func TestSendMessage_FailedHandler(t *testing.T) {
	card := DefaultAgentCard("test", "")
	s := NewServer(card)
	s.RegisterSkill("ask-rag", func(ctx SkillContext) (string, error) {
		return "", fmt.Errorf("LLM unavailable")
	})

	mux := http.NewServeMux()
	s.Mount(mux)

	reqBody := SendMessageRequest{
		Message: Message{
			MessageID: "msg-fail",
			Role:      "ROLE_USER",
			Parts:     []Part{{Text: "explain something"}},
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/a2a/v1/message:send", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp SendMessageResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Task == nil {
		t.Fatal("expected task even on failure")
	}
	if resp.Task.Status.State != TaskStateFailed {
		t.Errorf("expected FAILED, got %s", resp.Task.Status.State)
	}
}

func TestGetTask(t *testing.T) {
	s := testServer()
	mux := http.NewServeMux()
	s.Mount(mux)

	// First, create a task.
	reqBody := SendMessageRequest{
		Message: Message{
			MessageID: "msg-get",
			Role:      "ROLE_USER",
			Parts:     []Part{{Text: "Search for tests"}},
		},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/a2a/v1/message:send", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp SendMessageResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	taskID := resp.Task.ID

	// Now GET the task.
	req2 := httptest.NewRequest("GET", "/a2a/v1/tasks/"+taskID, nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	var task Task
	json.Unmarshal(w2.Body.Bytes(), &task)
	if task.ID != taskID {
		t.Errorf("expected task ID %s, got %s", taskID, task.ID)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	s := testServer()
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("GET", "/a2a/v1/tasks/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestSendMessage_EmptyBody(t *testing.T) {
	s := testServer()
	mux := http.NewServeMux()
	s.Mount(mux)

	req := httptest.NewRequest("POST", "/a2a/v1/message:send", bytes.NewReader([]byte("{}")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestDefaultAgentCard(t *testing.T) {
	card := DefaultAgentCard("2.0.0", "http://myhost:9090")

	if card.Name != "gleann" {
		t.Errorf("expected name=gleann, got %s", card.Name)
	}
	if card.Version != "2.0.0" {
		t.Errorf("expected version=2.0.0, got %s", card.Version)
	}
	if card.SupportedInterfaces[0].URL != "http://myhost:9090/a2a/v1" {
		t.Errorf("unexpected URL: %s", card.SupportedInterfaces[0].URL)
	}
	if len(card.Skills) != 4 {
		t.Errorf("expected 4 skills, got %d", len(card.Skills))
	}
}

func TestRouteSkill_Keywords(t *testing.T) {
	s := testServer()

	tests := []struct {
		query    string
		expected string
	}{
		{"Search for auth patterns", "semantic-search"},
		{"Find something", "semantic-search"},
		{"How does this work?", "ask-rag"},
		{"Explain the system", "ask-rag"},
		{"Remember this fact", "memory-management"},
		{"What are the callers of main?", "code-analysis"},
		{"random text without keywords", "ask-rag"}, // default fallback
	}

	for _, tt := range tests {
		got := s.routeSkill(tt.query, nil)
		if got != tt.expected {
			t.Errorf("routeSkill(%q) = %s, want %s", tt.query, got, tt.expected)
		}
	}
}

func TestExtractText(t *testing.T) {
	parts := []Part{
		{Text: "hello"},
		{Text: "world"},
		{MediaType: "image/png"}, // no text
	}
	got := extractText(parts)
	if got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}
