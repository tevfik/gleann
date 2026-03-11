package conversations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSummarizer_OllamaProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if body["model"] != "test-model" {
			t.Errorf("expected model test-model, got %v", body["model"])
		}

		json.NewEncoder(w).Encode(map[string]string{
			"response": "Setup Guide Discussion",
		})
	}))
	defer server.Close()

	s := &Summarizer{
		Provider: "ollama",
		Model:    "test-model",
		BaseURL:  server.URL,
	}

	conv := &Conversation{
		Messages: []Message{
			{Role: "user", Content: "How do I set up the project?"},
			{Role: "assistant", Content: "First install the dependencies..."},
		},
		CreatedAt: time.Now(),
	}

	title := s.SummarizeTitle(context.Background(), conv)
	if title != "Setup Guide Discussion" {
		t.Errorf("expected 'Setup Guide Discussion', got %q", title)
	}
}

func TestSummarizer_OpenAIProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %q", auth)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "API Key Configuration"}},
			},
		})
	}))
	defer server.Close()

	s := &Summarizer{
		Provider: "openai",
		Model:    "gpt-4",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	}

	conv := &Conversation{
		Messages: []Message{
			{Role: "user", Content: "How do I configure my API key?"},
		},
		CreatedAt: time.Now(),
	}

	title := s.SummarizeTitle(context.Background(), conv)
	if title != "API Key Configuration" {
		t.Errorf("expected 'API Key Configuration', got %q", title)
	}
}

func TestSummarizer_FallbackOnError(t *testing.T) {
	// Server that returns errors.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	s := &Summarizer{
		Provider: "ollama",
		Model:    "test",
		BaseURL:  server.URL,
	}

	conv := &Conversation{
		Messages: []Message{
			{Role: "user", Content: "Tell me about Go programming"},
		},
		CreatedAt: time.Now(),
	}

	title := s.SummarizeTitle(context.Background(), conv)
	// Should fall back to autoTitle based on first user message.
	if title == "" {
		t.Error("expected non-empty fallback title")
	}
}

func TestSummarizer_EmptyConversation(t *testing.T) {
	s := &Summarizer{
		Provider: "ollama",
		Model:    "test",
		BaseURL:  "http://unreachable:99999",
	}

	conv := &Conversation{
		Messages:  []Message{{Role: "system", Content: "You are helpful"}},
		CreatedAt: time.Now(),
	}

	title := s.SummarizeTitle(context.Background(), conv)
	// With only system messages, should return auto title.
	if title == "" {
		t.Error("expected non-empty auto title")
	}
}

func TestSummarizer_CleansUpTitle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return title with quotes and trailing period.
		json.NewEncoder(w).Encode(map[string]string{
			"response": `"Understanding Go Modules."`,
		})
	}))
	defer server.Close()

	s := &Summarizer{
		Provider: "ollama",
		Model:    "test",
		BaseURL:  server.URL,
	}

	conv := &Conversation{
		Messages: []Message{
			{Role: "user", Content: "Explain Go modules"},
		},
		CreatedAt: time.Now(),
	}

	title := s.SummarizeTitle(context.Background(), conv)
	if title != "Understanding Go Modules" {
		t.Errorf("expected cleaned title, got %q", title)
	}
}

func TestSummarizer_TruncatesLongTitle(t *testing.T) {
	longTitle := "This Is An Extremely Long Title That Definitely Exceeds The Maximum Allowed Length Of Sixty Characters For Titles"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"response": longTitle,
		})
	}))
	defer server.Close()

	s := &Summarizer{
		Provider: "ollama",
		Model:    "test",
		BaseURL:  server.URL,
	}

	conv := &Conversation{
		Messages: []Message{
			{Role: "user", Content: "Tell me everything"},
		},
		CreatedAt: time.Now(),
	}

	title := s.SummarizeTitle(context.Background(), conv)
	if len(title) > 60 {
		t.Errorf("title too long: %d chars (%q)", len(title), title)
	}
}
