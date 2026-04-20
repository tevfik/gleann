package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/conversations"
)

// --- callOllama ---

func TestCallOllama_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]string{"response": "test response"})
	}))
	defer srv.Close()

	s := NewSummarizer(SummarizerConfig{Provider: "ollama", Model: "test", BaseURL: srv.URL})
	resp, err := s.callOllama(context.Background(), &http.Client{}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if resp != "test response" {
		t.Errorf("unexpected: %s", resp)
	}
}

func TestCallOllama_DefaultBaseURL(t *testing.T) {
	// Verify default URL is used when BaseURL is empty.
	// Use an invalid port to guarantee connection failure.
	s := NewSummarizer(SummarizerConfig{Provider: "ollama", Model: "test", BaseURL: "http://127.0.0.1:1"})
	_, err := s.callOllama(context.Background(), &http.Client{}, "hello")
	if err == nil {
		t.Fatal("expected connection error")
	}
}

// --- callOpenAI ---

func TestCallOpenAI_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("missing auth: %s", auth)
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "openai response"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	s := NewSummarizer(SummarizerConfig{Provider: "openai", Model: "gpt-4", BaseURL: srv.URL, APIKey: "test-key"})
	resp, err := s.callOpenAI(context.Background(), &http.Client{}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if resp != "openai response" {
		t.Errorf("unexpected: %s", resp)
	}
}

func TestCallOpenAI_NoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	}))
	defer srv.Close()

	s := NewSummarizer(SummarizerConfig{BaseURL: srv.URL})
	_, err := s.callOpenAI(context.Background(), &http.Client{}, "hello")
	if err == nil || !strings.Contains(err.Error(), "no response") {
		t.Errorf("expected 'no response' error, got: %v", err)
	}
}

// --- callAnthropic ---

func TestCallAnthropic_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if v := r.Header.Get("anthropic-version"); v != "2023-06-01" {
			t.Errorf("missing anthropic-version: %s", v)
		}
		if key := r.Header.Get("x-api-key"); key != "test-key" {
			t.Errorf("missing x-api-key: %s", key)
		}
		resp := map[string]any{
			"content": []map[string]string{{"text": "anthropic response"}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	s := NewSummarizer(SummarizerConfig{Provider: "anthropic", Model: "claude", BaseURL: srv.URL, APIKey: "test-key"})
	resp, err := s.callAnthropic(context.Background(), &http.Client{}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if resp != "anthropic response" {
		t.Errorf("unexpected: %s", resp)
	}
}

func TestCallAnthropic_NoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"content": []any{}})
	}))
	defer srv.Close()

	s := NewSummarizer(SummarizerConfig{BaseURL: srv.URL})
	_, err := s.callAnthropic(context.Background(), &http.Client{}, "hello")
	if err == nil || !strings.Contains(err.Error(), "no response") {
		t.Errorf("expected 'no response' error, got: %v", err)
	}
}

// --- callLLM routing ---

func TestCallLLM_Routes(t *testing.T) {
	for _, tc := range []struct {
		provider string
		path     string
	}{
		{"ollama", "/api/generate"},
		{"openai", "/v1/chat/completions"},
		{"anthropic", "/v1/messages"},
		{"", "/api/generate"},        // default to ollama
		{"unknown", "/api/generate"}, // default to ollama
	} {
		t.Run(tc.provider, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				// Return valid response for any provider.
				json.NewEncoder(w).Encode(map[string]any{
					"response": "ok",
					"choices":  []map[string]any{{"message": map[string]string{"content": "ok"}}},
					"content":  []map[string]string{{"text": "ok"}},
				})
			}))
			defer srv.Close()

			s := NewSummarizer(SummarizerConfig{Provider: tc.provider, Model: "test", BaseURL: srv.URL})
			_, err := s.callLLM(context.Background(), "test")
			if err != nil {
				t.Fatal(err)
			}
			if gotPath != tc.path {
				t.Errorf("expected path %s, got %s", tc.path, gotPath)
			}
		})
	}
}

// --- SummarizeConversation ---

func TestSummarizeConversation_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"response": "This conversation discussed testing patterns."})
	}))
	defer srv.Close()

	s := NewSummarizer(SummarizerConfig{Provider: "ollama", Model: "test", BaseURL: srv.URL})
	conv := &conversations.Conversation{
		ID:      "abc12345",
		Title:   "Test Chat",
		Indexes: []string{"docs"},
		Model:   "llama3",
		Messages: []conversations.Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "How do I test code?"},
			{Role: "assistant", Content: "You can use unit tests."},
		},
	}

	summary, err := s.SummarizeConversation(context.Background(), conv)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ConversationID != "abc12345" {
		t.Errorf("wrong conv ID: %s", summary.ConversationID)
	}
	if summary.Title != "Test Chat" {
		t.Errorf("wrong title: %s", summary.Title)
	}
	if summary.MessageCount != 2 { // system excluded
		t.Errorf("expected 2 messages, got %d", summary.MessageCount)
	}
	if summary.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestSummarizeConversation_EmptyMessages(t *testing.T) {
	s := NewSummarizer(SummarizerConfig{Provider: "ollama"})
	conv := &conversations.Conversation{Messages: []conversations.Message{
		{Role: "system", Content: "sys"},
	}}
	_, err := s.SummarizeConversation(context.Background(), conv)
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestSummarizeConversation_LLMFailure_Fallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := NewSummarizer(SummarizerConfig{Provider: "ollama", Model: "test", BaseURL: srv.URL})
	conv := &conversations.Conversation{
		ID: "abc12345",
		Messages: []conversations.Message{
			{Role: "user", Content: "First user question here"},
		},
	}

	summary, err := s.SummarizeConversation(context.Background(), conv)
	if err != nil {
		t.Fatal(err)
	}
	// Should fallback to first user message.
	if summary.Content != "First user question here" {
		t.Errorf("expected fallback, got: %s", summary.Content)
	}
}

func TestSummarizeConversation_LongMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"response": "Summary of long conversation"})
	}))
	defer srv.Close()

	s := NewSummarizer(SummarizerConfig{Provider: "ollama", Model: "test", BaseURL: srv.URL})
	conv := &conversations.Conversation{
		ID: "abc12345",
		Messages: []conversations.Message{
			{Role: "user", Content: strings.Repeat("x", 1000)}, // > 500 chars, should be truncated
		},
	}

	summary, err := s.SummarizeConversation(context.Background(), conv)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Content == "" {
		t.Error("expected non-empty content")
	}
}

// --- ExtractMemories ---

func TestExtractMemories_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"response": "preference: User prefers concise answers\nfact: Project uses Go\ndecision: Use BBolt for storage",
		})
	}))
	defer srv.Close()

	s := NewSummarizer(SummarizerConfig{Provider: "ollama", Model: "test", BaseURL: srv.URL})
	conv := &conversations.Conversation{
		ID: "abc12345678",
		Messages: []conversations.Message{
			{Role: "user", Content: "I prefer concise answers. The project uses Go."},
		},
	}

	blocks, err := s.ExtractMemories(context.Background(), conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}

	labels := map[string]bool{}
	for _, b := range blocks {
		labels[b.Label] = true
		if b.Tier != TierLong {
			t.Errorf("expected TierLong, got %v", b.Tier)
		}
		if b.Source != "auto_extract" {
			t.Errorf("expected auto_extract source, got %s", b.Source)
		}
	}
	if !labels["preference"] || !labels["fact"] || !labels["decision"] {
		t.Errorf("missing expected labels: %v", labels)
	}
}

func TestExtractMemories_NONE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"response": "NONE"})
	}))
	defer srv.Close()

	s := NewSummarizer(SummarizerConfig{Provider: "ollama", BaseURL: srv.URL})
	conv := &conversations.Conversation{
		ID:       "abc12345678",
		Messages: []conversations.Message{{Role: "user", Content: "nothing important"}},
	}

	blocks, err := s.ExtractMemories(context.Background(), conv)
	if err != nil {
		t.Fatal(err)
	}
	if blocks != nil {
		t.Errorf("expected nil blocks, got %d", len(blocks))
	}
}

func TestExtractMemories_NoMessages(t *testing.T) {
	s := NewSummarizer(SummarizerConfig{})
	conv := &conversations.Conversation{
		Messages: []conversations.Message{{Role: "system", Content: "sys"}},
	}
	blocks, err := s.ExtractMemories(context.Background(), conv)
	if err != nil {
		t.Fatal(err)
	}
	if blocks != nil {
		t.Error("expected nil")
	}
}

func TestExtractMemories_UnlabeledLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"response": "Some fact without a label prefix\ntodo: Fix the bug",
		})
	}))
	defer srv.Close()

	s := NewSummarizer(SummarizerConfig{Provider: "ollama", BaseURL: srv.URL})
	conv := &conversations.Conversation{
		ID:       "abc12345678",
		Messages: []conversations.Message{{Role: "user", Content: "test"}},
	}

	blocks, err := s.ExtractMemories(context.Background(), conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	// First block should default to "fact" label.
	if blocks[0].Label != "fact" {
		t.Errorf("expected 'fact' for unlabeled, got %s", blocks[0].Label)
	}
	if blocks[1].Label != "todo" {
		t.Errorf("expected 'todo', got %s", blocks[1].Label)
	}
}

// --- fallbackSummary ---

func TestFallbackSummary_FirstUser(t *testing.T) {
	conv := &conversations.Conversation{
		Messages: []conversations.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "My question here"},
		},
	}
	if fallbackSummary(conv) != "My question here" {
		t.Error("unexpected fallback")
	}
}

func TestFallbackSummary_Long(t *testing.T) {
	conv := &conversations.Conversation{
		Messages: []conversations.Message{
			{Role: "user", Content: strings.Repeat("x", 300)},
		},
	}
	result := fallbackSummary(conv)
	if len(result) > 200 {
		t.Errorf("expected truncated to 200, got %d", len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Error("expected ellipsis")
	}
}

func TestFallbackSummary_NoUser(t *testing.T) {
	conv := &conversations.Conversation{
		Messages: []conversations.Message{{Role: "system", Content: "sys"}},
	}
	if fallbackSummary(conv) != "Empty conversation" {
		t.Error("expected 'Empty conversation'")
	}
}
