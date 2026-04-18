package gleann

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chatMockSearcher returns canned search results for testing.
type chatMockSearcher struct {
	results []SearchResult
	err     error
}

func (m *chatMockSearcher) Search(_ context.Context, _ string, _ ...SearchOption) ([]SearchResult, error) {
	return m.results, m.err
}
func (m *chatMockSearcher) Close() error { return nil }

func newTestChat(provider LLMProvider, baseURL string) *LeannChat {
	cfg := ChatConfig{
		Provider:     provider,
		Model:        "test-model",
		BaseURL:      baseURL,
		Temperature:  0.5,
		MaxTokens:    100,
		SystemPrompt: "You are a test assistant.",
	}
	return NewChat(&chatMockSearcher{
		results: []SearchResult{
			{Text: "doc1 content", Score: 0.9},
			{Text: "doc2 content", Score: 0.8},
		},
	}, cfg)
}

// --- chatOllama tests ---

func TestChatOllama_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var req ollamaChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "test-model" {
			t.Errorf("expected model test-model, got %s", req.Model)
		}
		resp := ollamaChatResponse{
			Message: ChatMessage{Role: "assistant", Content: "Test answer from Ollama"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	chat := newTestChat(LLMOllama, srv.URL)
	answer, err := chat.chatOllama(context.Background(), []ChatMessage{
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Test answer from Ollama" {
		t.Errorf("unexpected answer: %s", answer)
	}
}

func TestChatOllama_ModelNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`model 'bad-model' not found`))
	}))
	defer srv.Close()

	chat := newTestChat(LLMOllama, srv.URL)
	_, err := chat.chatOllama(context.Background(), []ChatMessage{
		{Role: "user", Content: "hello"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %s", err)
	}
}

func TestChatOllama_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	chat := newTestChat(LLMOllama, srv.URL)
	_, err := chat.chatOllama(context.Background(), []ChatMessage{
		{Role: "user", Content: "hello"},
	})
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %s", err)
	}
}

func TestChatOllama_MaxTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if _, ok := req.Options["num_predict"]; !ok {
			t.Error("expected num_predict in options")
		}
		resp := ollamaChatResponse{Message: ChatMessage{Content: "ok"}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	chat := newTestChat(LLMOllama, srv.URL)
	_, err := chat.chatOllama(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestChatOllama_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	chat := newTestChat(LLMOllama, srv.URL)
	_, err := chat.chatOllama(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("expected decode in error, got: %s", err)
	}
}

// --- chatOpenAI tests ---

func TestChatOpenAI_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("expected Authorization header, got: %s", auth)
		}
		resp := openAIChatResponse{
			Choices: []struct {
				Message ChatMessage `json:"message"`
			}{
				{Message: ChatMessage{Role: "assistant", Content: "OpenAI answer"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := ChatConfig{
		Provider:     LLMOpenAI,
		Model:        "gpt-4",
		BaseURL:      srv.URL,
		APIKey:       "test-key",
		Temperature:  0.5,
		MaxTokens:    100,
		SystemPrompt: "test",
	}
	chat := NewChat(NullSearcher{}, cfg)
	answer, err := chat.chatOpenAI(context.Background(), []ChatMessage{
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer != "OpenAI answer" {
		t.Errorf("unexpected: %s", answer)
	}
}

func TestChatOpenAI_NoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(openAIChatResponse{})
	}))
	defer srv.Close()

	chat := newTestChat(LLMOpenAI, srv.URL)
	_, err := chat.chatOpenAI(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error for no choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("expected 'no choices', got: %s", err)
	}
}

func TestChatOpenAI_WithImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIChatResponse{
			Choices: []struct {
				Message ChatMessage `json:"message"`
			}{
				{Message: ChatMessage{Content: "I see an image"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	chat := newTestChat(LLMOpenAI, srv.URL)
	answer, err := chat.chatOpenAI(context.Background(), []ChatMessage{
		{Role: "user", Content: "describe this", Images: []string{"base64data"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer != "I see an image" {
		t.Errorf("unexpected: %s", answer)
	}
}

func TestChatOpenAI_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	chat := newTestChat(LLMOpenAI, srv.URL)
	_, err := chat.chatOpenAI(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected 400, got: %s", err)
	}
}

// --- chatAnthropic tests ---

func TestChatAnthropic_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if v := r.Header.Get("anthropic-version"); v != "2023-06-01" {
			t.Errorf("expected anthropic-version header, got: %s", v)
		}
		if key := r.Header.Get("x-api-key"); key != "test-key" {
			t.Errorf("expected x-api-key header, got: %s", key)
		}
		resp := anthropicResponse{
			Content: []struct {
				Text string `json:"text"`
			}{
				{Text: "Anthropic answer"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := ChatConfig{
		Provider:     LLMAnthropic,
		Model:        "claude-3",
		BaseURL:      srv.URL,
		APIKey:       "test-key",
		Temperature:  0.5,
		MaxTokens:    100,
		SystemPrompt: "test",
	}
	chat := NewChat(NullSearcher{}, cfg)
	answer, err := chat.chatAnthropic(context.Background(), []ChatMessage{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Anthropic answer" {
		t.Errorf("unexpected: %s", answer)
	}
}

func TestChatAnthropic_NoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(anthropicResponse{})
	}))
	defer srv.Close()

	chat := newTestChat(LLMAnthropic, srv.URL)
	_, err := chat.chatAnthropic(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error for no content")
	}
	if !strings.Contains(err.Error(), "no content") {
		t.Errorf("expected 'no content', got: %s", err)
	}
}

func TestChatAnthropic_WithImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicResponse{
			Content: []struct {
				Text string `json:"text"`
			}{
				{Text: "I see the image"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	chat := newTestChat(LLMAnthropic, srv.URL)
	answer, err := chat.chatAnthropic(context.Background(), []ChatMessage{
		{Role: "user", Content: "describe", Images: []string{"imgdata"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer != "I see the image" {
		t.Errorf("unexpected: %s", answer)
	}
}

func TestChatAnthropic_DefaultMaxTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req anthropicRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.MaxTokens != 2048 {
			t.Errorf("expected default max_tokens 2048, got %d", req.MaxTokens)
		}
		resp := anthropicResponse{Content: []struct{ Text string `json:"text"` }{{Text: "ok"}}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := ChatConfig{
		Provider:     LLMAnthropic,
		Model:        "claude-3",
		BaseURL:      srv.URL,
		MaxTokens:    0, // should default to 2048
		SystemPrompt: "test",
	}
	chat := NewChat(NullSearcher{}, cfg)
	_, err := chat.chatAnthropic(context.Background(), []ChatMessage{
		{Role: "user", Content: "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// --- Ask integration (with mock HTTP + mock searcher) ---

func TestAsk_OllamaIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaChatResponse{
			Message: ChatMessage{Role: "assistant", Content: "RAG answer based on docs"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	chat := newTestChat(LLMOllama, srv.URL)
	answer, err := chat.Ask(context.Background(), "what is gleann?")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "RAG answer based on docs" {
		t.Errorf("unexpected answer: %s", answer)
	}
	// Check history was updated.
	h := chat.History()
	if len(h) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(h))
	}
	if h[0].Role != "user" {
		t.Errorf("expected user role, got %s", h[0].Role)
	}
	if h[1].Role != "assistant" {
		t.Errorf("expected assistant role, got %s", h[1].Role)
	}
}

func TestAsk_OpenAIIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIChatResponse{
			Choices: []struct {
				Message ChatMessage `json:"message"`
			}{
				{Message: ChatMessage{Content: "OpenAI RAG answer"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := ChatConfig{
		Provider:     LLMOpenAI,
		Model:        "gpt-4",
		BaseURL:      srv.URL,
		Temperature:  0.5,
		MaxTokens:    100,
		SystemPrompt: "test",
	}
	chat := NewChat(&chatMockSearcher{results: []SearchResult{{Text: "doc1", Score: 0.9}}}, cfg)
	answer, err := chat.Ask(context.Background(), "test question")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "OpenAI RAG answer" {
		t.Errorf("unexpected: %s", answer)
	}
}

func TestAsk_AnthropicIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicResponse{
			Content: []struct{ Text string `json:"text"` }{{Text: "Anthropic RAG"}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := ChatConfig{
		Provider:     LLMAnthropic,
		Model:        "claude-3",
		BaseURL:      srv.URL,
		Temperature:  0.5,
		MaxTokens:    100,
		SystemPrompt: "test",
	}
	chat := NewChat(&chatMockSearcher{results: []SearchResult{{Text: "doc1", Score: 0.9}}}, cfg)
	answer, err := chat.Ask(context.Background(), "test question")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Anthropic RAG" {
		t.Errorf("unexpected: %s", answer)
	}
}

func TestAsk_WithMemoryContext(t *testing.T) {
	var receivedMessages []ChatMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		receivedMessages = req.Messages
		resp := ollamaChatResponse{Message: ChatMessage{Content: "answer"}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	chat := newTestChat(LLMOllama, srv.URL)
	chat.SetMemoryContext("Memory: user prefers concise answers")
	_, err := chat.Ask(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	// Memory context should be injected as second system message.
	if len(receivedMessages) < 2 {
		t.Fatal("expected at least 2 messages")
	}
	found := false
	for _, m := range receivedMessages {
		if m.Role == "system" && strings.Contains(m.Content, "Memory:") {
			found = true
		}
	}
	if !found {
		t.Error("memory context not found in messages")
	}
}

func TestAsk_HistorySlidingWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaChatResponse{Message: ChatMessage{Content: "ok"}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	chat := newTestChat(LLMOllama, srv.URL)
	// Add 30 history entries (15 pairs) — exceeds historyLimit of 20.
	for i := 0; i < 15; i++ {
		chat.AppendHistory(ChatMessage{Role: "user", Content: "q"})
		chat.AppendHistory(ChatMessage{Role: "assistant", Content: "a"})
	}
	_, err := chat.Ask(context.Background(), "latest question")
	if err != nil {
		t.Fatal(err)
	}
	// History should now have 30 + 2 = 32 entries.
	if len(chat.History()) != 32 {
		t.Errorf("expected 32 history entries, got %d", len(chat.History()))
	}
}

func TestAsk_SearchError(t *testing.T) {
	cfg := ChatConfig{
		Provider: LLMOllama,
		Model:    "test",
		BaseURL:  "http://localhost:1",
	}
	chat := NewChat(&chatMockSearcher{err: context.DeadlineExceeded}, cfg)
	_, err := chat.Ask(context.Background(), "test")
	if err == nil {
		t.Fatal("expected search error")
	}
	if !strings.Contains(err.Error(), "search") {
		t.Errorf("expected search error, got: %s", err)
	}
}

// --- AskWithImages ---

func TestAskWithImages_Ollama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaChatResponse{Message: ChatMessage{Content: "I see the image"}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	chat := newTestChat(LLMOllama, srv.URL)
	answer, err := chat.AskWithImages(context.Background(), "describe this", []string{"base64img"})
	if err != nil {
		t.Fatal(err)
	}
	if answer != "I see the image" {
		t.Errorf("unexpected: %s", answer)
	}
	if len(chat.History()) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(chat.History()))
	}
}

// --- AskStreamWithMedia audio detection ---

func TestAskStreamWithMedia_AudioFallsBackToNonStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaChatResponse{Message: ChatMessage{Content: "audio response"}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// Create a temp audio file.
	tmpDir := t.TempDir()
	audioFile := filepath.Join(tmpDir, "test.mp3")
	os.WriteFile(audioFile, []byte("fake audio"), 0o644)

	chat := newTestChat(LLMOllama, srv.URL)
	var tokens []string
	err := chat.AskStreamWithMedia(context.Background(), "what do you hear?", []string{audioFile}, func(token string) {
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatal(err)
	}
	// Audio should fall back to non-streaming, single callback with full answer.
	if len(tokens) != 1 || tokens[0] != "audio response" {
		t.Errorf("expected single callback with full answer, got %d tokens", len(tokens))
	}
}

// --- chat() router ---

func TestChat_UnsupportedProvider(t *testing.T) {
	cfg := ChatConfig{Provider: "unknown", BaseURL: "http://localhost:1"}
	chat := NewChat(NullSearcher{}, cfg)
	_, err := chat.chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected 'unsupported', got: %s", err)
	}
}

// --- getStreamClient ---

func TestGetStreamClient_Default(t *testing.T) {
	chat := &LeannChat{}
	client := chat.getStreamClient()
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestGetStreamClient_Custom(t *testing.T) {
	custom := &http.Client{}
	chat := &LeannChat{streamClient: custom}
	if chat.getStreamClient() != custom {
		t.Error("expected custom client")
	}
}

// --- SetReranker with MultiSearcher ---

func TestSetReranker_MultiSearcher(t *testing.T) {
	s1 := &LeannSearcher{}
	s2 := &LeannSearcher{}
	ms := NewMultiSearcher(map[string]*LeannSearcher{"a": s1, "b": s2})
	chat := NewChat(ms, DefaultChatConfig())

	// This should not panic.
	chat.SetReranker(nil)
}

// --- SaveSession edge case ---

func TestSaveSession_CreatesDir(t *testing.T) {
	chat := &LeannChat{
		history: []ChatMessage{{Role: "user", Content: "test"}},
	}
	dir := filepath.Join(t.TempDir(), "subdir", "sessions")
	path, err := chat.SaveSession(dir, "myindex")
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

// --- LoadMediaFiles ---

func TestLoadMediaFiles_TooLarge(t *testing.T) {
	// Create a file > 50MB check.
	tmpDir := t.TempDir()
	big := filepath.Join(tmpDir, "big.png")
	f, _ := os.Create(big)
	f.Seek(51<<20, 0)
	f.Write([]byte{0})
	f.Close()

	_, err := LoadMediaFiles([]string{big})
	if err == nil {
		t.Fatal("expected error for large file")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large', got: %s", err)
	}
}

// --- AskStream with mock Ollama NDJSON streaming ---

func TestAskStream_OllamaIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stream NDJSON chunks like Ollama.
		chunks := []string{
			`{"message":{"content":"Hello"},"done":false}`,
			`{"message":{"content":" world"},"done":false}`,
			`{"message":{"content":""},"done":true}`,
		}
		for _, c := range chunks {
			w.Write([]byte(c + "\n"))
		}
	}))
	defer srv.Close()

	chat := newTestChat(LLMOllama, srv.URL)
	var tokens []string
	err := chat.AskStream(context.Background(), "test question", func(token string) {
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 {
		t.Errorf("expected 2 tokens, got %d: %v", len(tokens), tokens)
	}
	// History should be updated.
	h := chat.History()
	if len(h) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(h))
	}
	if h[1].Content != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", h[1].Content)
	}
}

func TestAskStream_OpenAIIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// SSE format for OpenAI streaming
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\" there\"}}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	cfg := ChatConfig{
		Provider:     LLMOpenAI,
		Model:        "gpt-4",
		BaseURL:      srv.URL,
		SystemPrompt: "test",
	}
	chat := NewChat(&chatMockSearcher{results: []SearchResult{{Text: "doc", Score: 0.9}}}, cfg)
	var tokens []string
	err := chat.AskStream(context.Background(), "test", func(token string) {
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) < 1 {
		t.Error("expected at least 1 token")
	}
}

func TestAskStream_AnthropicIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Anthropic SSE: event line + data line + blank line
		lines := []string{
			"event: content_block_delta",
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}",
			"",
			"event: content_block_delta",
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\" from Claude\"}}",
			"",
			"event: message_stop",
			"data: {\"type\":\"message_stop\"}",
			"",
		}
		for _, l := range lines {
			w.Write([]byte(l + "\n"))
		}
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer srv.Close()

	cfg := ChatConfig{
		Provider:     LLMAnthropic,
		Model:        "claude",
		BaseURL:      srv.URL,
		APIKey:       "test-key",
		SystemPrompt: "test",
	}
	chat := NewChat(&chatMockSearcher{results: []SearchResult{{Text: "doc", Score: 0.9}}}, cfg)
	var tokens []string
	err := chat.AskStream(context.Background(), "test", func(token string) {
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) < 1 {
		t.Error("expected at least 1 token")
	}
}

// --- AskStreamWithMedia (non-audio, should stream) ---

func TestAskStreamWithMedia_ImageStreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunks := []string{
			`{"message":{"content":"I see"},"done":false}`,
			`{"message":{"content":" an image"},"done":false}`,
			`{"message":{"content":""},"done":true}`,
		}
		for _, c := range chunks {
			w.Write([]byte(c + "\n"))
		}
	}))
	defer srv.Close()

	// Create a temp image file.
	tmpDir := t.TempDir()
	imgFile := filepath.Join(tmpDir, "test.png")
	os.WriteFile(imgFile, []byte("fake png data"), 0o644)

	chat := newTestChat(LLMOllama, srv.URL)
	var tokens []string
	err := chat.AskStreamWithMedia(context.Background(), "describe this", []string{imgFile}, func(token string) {
		tokens = append(tokens, token)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) < 1 {
		t.Error("expected at least 1 token")
	}
}

// --- NewChat env var handling ---

func TestNewChat_OllamaHostEnv(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://custom:11434")
	cfg := ChatConfig{Provider: LLMOllama, Model: "test"}
	chat := NewChat(NullSearcher{}, cfg)
	if chat.config.BaseURL != "http://custom:11434" {
		t.Errorf("expected custom host, got: %s", chat.config.BaseURL)
	}
}

func TestNewChat_OpenAIBaseURLEnv(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "http://custom-openai")
	cfg := ChatConfig{Provider: LLMOpenAI, Model: "gpt-4"}
	chat := NewChat(NullSearcher{}, cfg)
	if chat.config.BaseURL != "http://custom-openai" {
		t.Errorf("expected custom base URL, got: %s", chat.config.BaseURL)
	}
}

func TestNewChat_OpenAIAPIKeyEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-testkey")
	cfg := ChatConfig{Provider: LLMOpenAI, Model: "gpt-4", BaseURL: "http://test"}
	chat := NewChat(NullSearcher{}, cfg)
	if chat.config.APIKey != "sk-testkey" {
		t.Errorf("expected env API key, got: %s", chat.config.APIKey)
	}
}

func TestNewChat_AnthropicAPIKeyEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	cfg := ChatConfig{Provider: LLMAnthropic, Model: "claude", BaseURL: "http://test"}
	chat := NewChat(NullSearcher{}, cfg)
	if chat.config.APIKey != "sk-ant-test" {
		t.Errorf("expected env API key, got: %s", chat.config.APIKey)
	}
}

func TestNewChat_CustomTimeout(t *testing.T) {
	cfg := ChatConfig{Provider: LLMOllama, Model: "test", BaseURL: "http://test", Timeout: 5 * 1e9}
	chat := NewChat(NullSearcher{}, cfg)
	if chat.client.Timeout != 5*1e9 {
		t.Errorf("expected custom timeout, got: %v", chat.client.Timeout)
	}
}

// --- chatOllama connection errors ---

func TestChatOllama_ConnectionRefused(t *testing.T) {
	chat := &LeannChat{
		config: ChatConfig{
			Provider: LLMOllama,
			Model:    "test",
			BaseURL:  "http://127.0.0.1:1",
		},
		client: &http.Client{},
	}
	_, err := chat.chatOllama(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestChatOllama_ThinkField(t *testing.T) {
	think := true
	var gotThink *bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		gotThink = req.Think
		resp := ollamaChatResponse{Message: ChatMessage{Content: "ok"}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := ChatConfig{
		Provider: LLMOllama,
		Model:    "test",
		BaseURL:  srv.URL,
		Think:    &think,
	}
	chat := NewChat(NullSearcher{}, cfg)
	chat.chatOllama(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}})
	if gotThink == nil || !*gotThink {
		t.Error("expected Think=true to be sent")
	}
}
