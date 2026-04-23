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
	if len(card.Skills) != 8 {
		t.Errorf("expected 8 skills, got %d", len(card.Skills))
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
	if len(card.Skills) != 8 {
		t.Errorf("expected 8 skills, got %d", len(card.Skills))
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
		// Regression: doc queries containing "memory" must NOT route to memory-management (issue #routing-memory)
		{"How does the gleann memory API work?", "ask-rag"},
		{"Explain memory management in the codebase", "ask-rag"},
		{"What is the vector memory store architecture?", "ask-rag"},
	}

	for _, tt := range tests {
		got := s.routeSkill(tt.query, nil)
		if got != tt.expected {
			t.Errorf("routeSkill(%q) = %s, want %s", tt.query, got, tt.expected)
		}
	}
}

func TestRouteSkill_ExpandedKeywords(t *testing.T) {
	s := testServer()

	tests := []struct {
		query    string
		expected string
	}{
		// Code analysis — expanded keywords
		{"who calls handlePayment?", "code-analysis"},
		{"what does this depends on?", "code-analysis"},
		{"show me the call graph", "code-analysis"},
		{"find references to UserService", "code-analysis"},
		// Memory management — expanded keywords
		{"store this note for later", "memory-management"},
		{"save this decision", "memory-management"},
		{"note: we use JWT", "memory-management"},
		{"forget that old approach", "memory-management"},
		// Semantic search — expanded keywords
		{"grep for authentication", "semantic-search"},
		{"locate the config file", "semantic-search"},
		{"where is the main entry point?", "semantic-search"},
		// Ask-RAG — Turkish keywords
		{"özetle bu kodu", "ask-rag"},
		{"nasıl çalışıyor?", "ask-rag"},
	}

	for _, tt := range tests {
		got := s.routeSkill(tt.query, nil)
		if got != tt.expected {
			t.Errorf("routeSkill(%q) = %s, want %s", tt.query, got, tt.expected)
		}
	}
}

func TestRouteSkill_ExplicitMetadata(t *testing.T) {
	s := testServer()

	// Explicit skill via metadata overrides keyword routing.
	tests := []struct {
		query    string
		skill    string
		expected string
	}{
		{"random query", "code-analysis", "code-analysis"},
		{"remember this", "semantic-search", "semantic-search"}, // override the memory keyword
		{"search for x", "ask-rag", "ask-rag"},                  // override the search keyword
	}

	for _, tt := range tests {
		meta := map[string]interface{}{"skill": tt.skill}
		got := s.routeSkill(tt.query, meta)
		if got != tt.expected {
			t.Errorf("routeSkill(%q, skill=%s) = %s, want %s", tt.query, tt.skill, got, tt.expected)
		}
	}
}

func TestRouteSkill_ScoringFallback(t *testing.T) {
	s := testServer()

	// Queries that don't hit any exact keyword but should route via scoring.
	// "callers" and "impact" are multi-word concepts — partial scoring should
	// still prefer code-analysis over ask-rag for code-flavoured queries.
	tests := []struct {
		query    string
		expected string
	}{
		// No exact keyword, but words contain code-analysis prefix chars
		{"sym structure", "code-analysis"},
		// No exact keyword — ask-rag has the most general coverage
		{"please synthesize", "ask-rag"},
	}

	for _, tt := range tests {
		got := s.routeSkill(tt.query, nil)
		if got != tt.expected {
			t.Errorf("routeSkill(%q) scoring fallback = %s, want %s", tt.query, got, tt.expected)
		}
	}
}

func TestSplitWords(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"hello world", []string{"hello", "world"}},
		{"Who calls main?", []string{"who", "calls", "main"}},
		{"  spaces  between  ", []string{"spaces", "between"}},
		{"camelCase_and-hyphen", []string{"camelcase_and", "hyphen"}},
		{"", nil},
	}

	for _, tt := range tests {
		got := splitWords(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitWords(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitWords(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
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
