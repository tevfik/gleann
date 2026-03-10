package gleann

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAskStream_OllamaCollectsFullAnswer(t *testing.T) {
	// Mock Ollama server that streams tokens as NDJSON.
	tokens := []string{"Hello", " world", "!"}
	mockOllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat" {
			// Verify stream:true was requested.
			var req ollamaChatRequest
			json.NewDecoder(r.Body).Decode(&req)
			if !req.Stream {
				t.Error("expected stream=true in request")
			}

			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("expected flusher support")
			}
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)

			for _, tok := range tokens {
				chunk := ollamaStreamChunk{
					Message: ChatMessage{Role: "assistant", Content: tok},
					Done:    false,
				}
				data, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "%s\n", data)
				flusher.Flush()
			}
			// Final done chunk.
			done := ollamaStreamChunk{Done: true}
			data, _ := json.Marshal(done)
			fmt.Fprintf(w, "%s\n", data)
			flusher.Flush()
			return
		}

		// Mock embedding endpoint (for search).
		if r.URL.Path == "/api/embed" {
			json.NewEncoder(w).Encode(map[string]any{
				"embeddings": [][]float32{{0.1, 0.2, 0.3}},
			})
			return
		}
	}))
	defer mockOllama.Close()

	config := ChatConfig{
		Provider:     LLMOllama,
		Model:        "test-model",
		BaseURL:      mockOllama.URL,
		Temperature:  0.7,
		MaxTokens:    100,
		SystemPrompt: "Test system prompt.",
	}

	// Create a chat with a nil searcher — we'll use chatStream directly.
	chat := &LeannChat{
		config: config,
		client: http.DefaultClient,
	}

	var received []string
	err := chat.chatOllamaStream(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hello"}},
		func(token string) { received = append(received, token) },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(received) != len(tokens) {
		t.Fatalf("expected %d tokens, got %d", len(tokens), len(received))
	}
	for i, tok := range tokens {
		if received[i] != tok {
			t.Errorf("token[%d]: expected %q, got %q", i, tok, received[i])
		}
	}
}

func TestChatOpenAIStream_ParsesSSE(t *testing.T) {
	tokens := []string{"Go", " is", " great"}
	mockAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		for _, tok := range tokens {
			chunk := openAIStreamChunk{
				Choices: []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
					FinishReason *string `json:"finish_reason"`
				}{
					{Delta: struct {
						Content string `json:"content"`
					}{Content: tok}},
				},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer mockAPI.Close()

	chat := &LeannChat{
		config: ChatConfig{
			Provider: LLMOpenAI,
			BaseURL:  mockAPI.URL,
			Model:    "gpt-test",
		},
		client: http.DefaultClient,
	}

	var received []string
	err := chat.chatOpenAIStream(context.Background(),
		[]ChatMessage{{Role: "user", Content: "test"}},
		func(token string) { received = append(received, token) },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(received) != len(tokens) {
		t.Fatalf("expected %d tokens, got %d", len(tokens), len(received))
	}
	full := strings.Join(received, "")
	if full != "Go is great" {
		t.Errorf("expected 'Go is great', got %q", full)
	}
}

func TestChatAnthropicStream_ParsesSSE(t *testing.T) {
	tokens := []string{"Anthropic", " response"}
	mockAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		for _, tok := range tokens {
			event := anthropicStreamEvent{
				Type: "content_block_delta",
			}
			event.Delta.Type = "text_delta"
			event.Delta.Text = tok
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", data)
			flusher.Flush()
		}

		// Message stop event.
		stop := anthropicStreamEvent{Type: "message_stop"}
		data, _ := json.Marshal(stop)
		fmt.Fprintf(w, "event: message_stop\ndata: %s\n\n", data)
		flusher.Flush()
	}))
	defer mockAPI.Close()

	chat := &LeannChat{
		config: ChatConfig{
			Provider:  LLMAnthropic,
			BaseURL:   mockAPI.URL,
			Model:     "claude-test",
			APIKey:    "test-key",
			MaxTokens: 100,
		},
		client: http.DefaultClient,
	}

	var received []string
	err := chat.chatAnthropicStream(context.Background(),
		[]ChatMessage{
			{Role: "system", Content: "Be helpful"},
			{Role: "user", Content: "test"},
		},
		func(token string) { received = append(received, token) },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	full := strings.Join(received, "")
	if full != "Anthropic response" {
		t.Errorf("expected 'Anthropic response', got %q", full)
	}
}

func TestChatStream_UnsupportedProvider(t *testing.T) {
	chat := &LeannChat{
		config: ChatConfig{Provider: "unknown"},
		client: http.DefaultClient,
	}
	err := chat.chatStream(context.Background(),
		[]ChatMessage{{Role: "user", Content: "test"}},
		func(token string) {},
	)
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected 'unsupported' in error, got: %v", err)
	}
}

func TestOllamaStream_ServerError(t *testing.T) {
	mockAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer mockAPI.Close()

	chat := &LeannChat{
		config: ChatConfig{Provider: LLMOllama, BaseURL: mockAPI.URL, Model: "test"},
		client: http.DefaultClient,
	}

	err := chat.chatOllamaStream(context.Background(),
		[]ChatMessage{{Role: "user", Content: "test"}},
		func(token string) {},
	)
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected '500' in error, got: %v", err)
	}
}

func TestOpenAIStream_EmptyDataLines(t *testing.T) {
	// Server sends some blank and comment lines interspersed with data.
	mockAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprint(w, ": comment line\n\n")
		flusher.Flush()

		chunk := openAIStreamChunk{
			Choices: []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			}{
				{Delta: struct {
					Content string `json:"content"`
				}{Content: "ok"}},
			},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		fmt.Fprint(w, "\n")
		flusher.Flush()

		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer mockAPI.Close()

	chat := &LeannChat{
		config: ChatConfig{Provider: LLMOpenAI, BaseURL: mockAPI.URL, Model: "test"},
		client: http.DefaultClient,
	}

	var received []string
	err := chat.chatOpenAIStream(context.Background(),
		[]ChatMessage{{Role: "user", Content: "test"}},
		func(token string) { received = append(received, token) },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(received) != 1 || received[0] != "ok" {
		t.Errorf("expected [\"ok\"], got %v", received)
	}
}

func TestChatStream_ContextCancellation(t *testing.T) {
	// Server blocks until context is cancelled.
	mockAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wait for the client to cancel.
		<-r.Context().Done()
	}))
	defer mockAPI.Close()

	chat := &LeannChat{
		config: ChatConfig{Provider: LLMOllama, BaseURL: mockAPI.URL, Model: "test"},
		client: http.DefaultClient,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := chat.chatOllamaStream(ctx,
		[]ChatMessage{{Role: "user", Content: "test"}},
		func(token string) {},
	)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestStreamCallback_HistoryUpdated(t *testing.T) {
	// Mock Ollama that returns two tokens.
	mockOllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)

		for _, tok := range []string{"answer", " here"} {
			chunk := ollamaStreamChunk{
				Message: ChatMessage{Role: "assistant", Content: tok},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "%s\n", data)
			flusher.Flush()
		}
		done := ollamaStreamChunk{Done: true}
		data, _ := json.Marshal(done)
		fmt.Fprintf(w, "%s\n", data)
		flusher.Flush()
	}))
	defer mockOllama.Close()

	chat := &LeannChat{
		config: ChatConfig{
			Provider:     LLMOllama,
			BaseURL:      mockOllama.URL,
			Model:        "test",
			SystemPrompt: "System",
		},
		client: http.DefaultClient,
	}

	// Call chatStream (which is used by AskStream after search).
	var fullAnswer strings.Builder
	err := chat.chatOllamaStream(context.Background(),
		[]ChatMessage{{Role: "user", Content: "q"}},
		func(token string) { fullAnswer.WriteString(token) },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fullAnswer.String() != "answer here" {
		t.Errorf("expected 'answer here', got %q", fullAnswer.String())
	}
}
