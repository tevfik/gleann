// Package gleann provides LLM chat integration for RAG-based Q&A.
package gleann

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LLMProvider specifies the LLM provider type.
type LLMProvider string

const (
	LLMOllama    LLMProvider = "ollama"
	LLMOpenAI    LLMProvider = "openai"
	LLMAnthropic LLMProvider = "anthropic"
)

// ChatConfig holds configuration for LLM chat.
type ChatConfig struct {
	Provider     LLMProvider   `json:"provider"`
	Model        string        `json:"model"`
	BaseURL      string        `json:"base_url,omitempty"`
	APIKey       string        `json:"api_key,omitempty"`
	Temperature  float64       `json:"temperature,omitempty"`
	MaxTokens    int           `json:"max_tokens,omitempty"`
	SystemPrompt string        `json:"system_prompt,omitempty"`
	Timeout      time.Duration `json:"timeout,omitempty"` // HTTP client timeout; 0 uses DefaultChatTimeout
	Think        *bool         `json:"think,omitempty"`   // Ollama: nil=model default, false=disable thinking
}

// DefaultChatTimeout is the default HTTP timeout for LLM chat requests.
const DefaultChatTimeout = 10 * time.Minute

// DefaultChatConfig returns default chat configuration.
func DefaultChatConfig() ChatConfig {
	return ChatConfig{
		Provider:    LLMOllama,
		Model:       "llama3.2:3b-instruct-q4_K_M",
		BaseURL:     DefaultOllamaHost,
		Temperature: 0.7,
		MaxTokens:   2048,
		SystemPrompt: "You are a helpful assistant. Answer questions based on the provided context. " +
			"If the context doesn't contain enough information, say so clearly.",
	}
}

// ChatMessage represents a single message in a conversation.
type ChatMessage struct {
	Role    string `json:"role"` // "system", "user", "assistant"
	Content string `json:"content"`
}

// LeannChat provides RAG-based Q&A over indexed data.
// The searcher field accepts the Searcher interface, so LeannChat works
// transparently with a single LeannSearcher or a MultiSearcher.
type LeannChat struct {
	searcher     Searcher
	config       ChatConfig
	history      []ChatMessage
	client       *http.Client
	streamClient *http.Client // No timeout; context handles cancellation for streaming
}

func (c *LeannChat) getStreamClient() *http.Client {
	if c.streamClient != nil {
		return c.streamClient
	}
	return &http.Client{}
}

// NewChat creates a new LeannChat instance.
// The searcher can be a *LeannSearcher (single index) or a *MultiSearcher (multi-index).
func NewChat(searcher Searcher, config ChatConfig) *LeannChat {
	if config.BaseURL == "" {
		switch config.Provider {
		case LLMOllama:
			if host := os.Getenv("OLLAMA_HOST"); host != "" {
				config.BaseURL = host
			} else {
				config.BaseURL = DefaultOllamaHost
			}
		case LLMOpenAI:
			if url := os.Getenv("OPENAI_BASE_URL"); url != "" {
				config.BaseURL = url
			} else {
				config.BaseURL = "https://api.openai.com"
			}
		case LLMAnthropic:
			config.BaseURL = "https://api.anthropic.com"
		}
	}
	if config.APIKey == "" {
		switch config.Provider {
		case LLMOpenAI:
			config.APIKey = os.Getenv("OPENAI_API_KEY")
		case LLMAnthropic:
			config.APIKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = DefaultChatTimeout
	}

	return &LeannChat{
		searcher:     searcher,
		config:       config,
		client:       &http.Client{Timeout: timeout},
		streamClient: &http.Client{},
	}
}

// Ask performs RAG: retrieves relevant context and generates an answer.
func (c *LeannChat) Ask(ctx context.Context, question string, opts ...SearchOption) (string, error) {
	// Step 1: Retrieve relevant context.
	results, err := c.searcher.Search(ctx, question, opts...)
	if err != nil {
		return "", fmt.Errorf("search: %w", err)
	}

	// Step 2: Build context from results.
	var contextParts []string
	for i, r := range results {
		contextParts = append(contextParts, formatResult(r, i+1))
	}
	context_text := strings.Join(contextParts, "\n\n")

	// Step 3: Build prompt.
	userPrompt := fmt.Sprintf("Context:\n%s\n\nQuestion: %s\n\nAnswer based on the context above:", context_text, question)

	// Step 4: Generate answer.
	messages := []ChatMessage{
		{Role: "system", Content: c.config.SystemPrompt},
	}

	// Add conversation history (Sliding Window: Keep last N messages to prevent context overflow).
	const historyLimit = 20 // 10 conversation turns (user + assistant)
	startIdx := 0
	if len(c.history) > historyLimit {
		startIdx = len(c.history) - historyLimit
	}
	messages = append(messages, c.history[startIdx:]...)

	messages = append(messages, ChatMessage{Role: "user", Content: userPrompt})

	answer, err := c.chat(ctx, messages)

	if err != nil {
		return "", fmt.Errorf("chat: %w", err)
	}

	// Save to history.
	c.history = append(c.history,
		ChatMessage{Role: "user", Content: question},
		ChatMessage{Role: "assistant", Content: answer},
	)

	return answer, nil
}

// StreamCallback is called for each token received from the LLM.
// Implementations must not block — the streaming goroutine waits for each call to return.
type StreamCallback func(token string)

// AskStream performs RAG: retrieves relevant context and streams the LLM answer
// token-by-token through the callback. It behaves like Ask but delivers partial
// results as they become available from the LLM provider.
// The full assembled answer is also appended to conversation history.
func (c *LeannChat) AskStream(ctx context.Context, question string, callback StreamCallback, opts ...SearchOption) error {
	// Step 1: Retrieve relevant context.
	results, err := c.searcher.Search(ctx, question, opts...)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}

	// Step 2: Build context from results.
	var contextParts []string
	for i, r := range results {
		contextParts = append(contextParts, formatResult(r, i+1))
	}
	contextText := strings.Join(contextParts, "\n\n")

	// Step 3: Build prompt.
	userPrompt := fmt.Sprintf("Context:\n%s\n\nQuestion: %s\n\nAnswer based on the context above:", contextText, question)

	// Step 4: Build messages.
	messages := []ChatMessage{
		{Role: "system", Content: c.config.SystemPrompt},
	}

	const historyLimit = 20
	startIdx := 0
	if len(c.history) > historyLimit {
		startIdx = len(c.history) - historyLimit
	}
	messages = append(messages, c.history[startIdx:]...)
	messages = append(messages, ChatMessage{Role: "user", Content: userPrompt})

	// Step 5: Stream the answer, collecting the full text.
	var fullAnswer strings.Builder
	wrappedCB := func(token string) {
		fullAnswer.WriteString(token)
		callback(token)
	}

	if err := c.chatStream(ctx, messages, wrappedCB); err != nil {
		return fmt.Errorf("chat stream: %w", err)
	}

	// Save to history.
	c.history = append(c.history,
		ChatMessage{Role: "user", Content: question},
		ChatMessage{Role: "assistant", Content: fullAnswer.String()},
	)

	return nil
}

// chatStream sends messages to the LLM provider and streams tokens via callback.
func (c *LeannChat) chatStream(ctx context.Context, messages []ChatMessage, callback StreamCallback) error {
	switch c.config.Provider {
	case LLMOllama:
		return c.chatOllamaStream(ctx, messages, callback)
	case LLMOpenAI:
		return c.chatOpenAIStream(ctx, messages, callback)
	case LLMAnthropic:
		return c.chatAnthropicStream(ctx, messages, callback)
	default:
		return fmt.Errorf("unsupported LLM provider for streaming: %s", c.config.Provider)
	}
}

// ClearHistory clears conversation history.
func (c *LeannChat) ClearHistory() {
	c.history = nil
}

// History returns the current chat history.
func (c *LeannChat) History() []ChatMessage {
	return c.history
}

// AppendHistory adds a message to the conversation history without
// triggering a search or LLM call. Used to restore saved conversations.
func (c *LeannChat) AppendHistory(msg ChatMessage) {
	c.history = append(c.history, msg)
}

// SaveSession saves the current chat history to a JSON file.
// It creates the specified directory if it doesn't exist.
func (c *LeannChat) SaveSession(dir, indexName string) (string, error) {
	if len(c.history) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	filename := fmt.Sprintf("%s_%s.json", indexName, time.Now().Format("20060102_150405"))
	path := filepath.Join(dir, filename)
	data, err := json.MarshalIndent(c.history, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o644)
}

// LoadSession loads chat history from a JSON file.
func (c *LeannChat) LoadSession(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var loadedHistory []ChatMessage
	if err := json.Unmarshal(data, &loadedHistory); err != nil {
		return err
	}
	c.history = loadedHistory
	return nil
}

// Config returns a copy of the current chat config.
func (c *LeannChat) Config() ChatConfig {
	return c.config
}

// SetTemperature updates the temperature for subsequent requests.
func (c *LeannChat) SetTemperature(t float64) {
	c.config.Temperature = t
}

// SetMaxTokens updates the max tokens for subsequent requests.
func (c *LeannChat) SetMaxTokens(n int) {
	c.config.MaxTokens = n
}

// SetSystemPrompt updates the system prompt for subsequent requests.
func (c *LeannChat) SetSystemPrompt(prompt string) {
	c.config.SystemPrompt = prompt
}

// SetModel updates the model for subsequent requests.
func (c *LeannChat) SetModel(model string) {
	c.config.Model = model
}

// GetSearcher returns the underlying searcher instance.
func (c *LeannChat) GetSearcher() Searcher {
	return c.searcher
}

// SetReranker configures reranking on the underlying searcher(s).
// For a single LeannSearcher it sets the reranker directly.
// For a MultiSearcher it sets the reranker on every sub-searcher.
func (c *LeannChat) SetReranker(r Reranker) {
	switch s := c.searcher.(type) {
	case *LeannSearcher:
		s.SetReranker(r)
	case *MultiSearcher:
		for _, sub := range s.searchers {
			sub.SetReranker(r)
		}
	}
}

// chat sends messages to the LLM provider and returns the response.
func (c *LeannChat) chat(ctx context.Context, messages []ChatMessage) (string, error) {
	switch c.config.Provider {
	case LLMOllama:
		return c.chatOllama(ctx, messages)
	case LLMOpenAI:
		return c.chatOpenAI(ctx, messages)
	case LLMAnthropic:
		return c.chatAnthropic(ctx, messages)
	default:
		return "", fmt.Errorf("unsupported LLM provider: %s", c.config.Provider)
	}
}

// --- Ollama Chat ---

type ollamaChatRequest struct {
	Model    string         `json:"model"`
	Messages []ChatMessage  `json:"messages"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
	Think    *bool          `json:"think,omitempty"`
}

type ollamaChatResponse struct {
	Message ChatMessage `json:"message"`
}

func (c *LeannChat) chatOllama(ctx context.Context, messages []ChatMessage) (string, error) {
	reqBody := ollamaChatRequest{
		Model:    c.config.Model,
		Messages: messages,
		Stream:   false,
		Think:    c.config.Think,
		Options: map[string]any{
			"temperature": c.config.Temperature,
		},
	}
	if c.config.MaxTokens > 0 {
		reqBody.Options["num_predict"] = c.config.MaxTokens
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := c.config.BaseURL + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			return "", fmt.Errorf("cannot connect to Ollama at %s — is it running?\n  Fix: ollama serve   (or: systemctl start ollama)\n  Check: gleann doctor", c.config.BaseURL)
		}
		return "", fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			respBody = []byte("(body unreadable)")
		}
		bodyStr := string(respBody)
		if resp.StatusCode == http.StatusNotFound && strings.Contains(bodyStr, "not found") {
			return "", fmt.Errorf("model '%s' not found in Ollama\n  Fix: ollama pull %s\n  Available models: ollama list", c.config.Model, c.config.Model)
		}
		return "", fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, bodyStr)
	}

	var result ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return result.Message.Content, nil
}

// --- OpenAI Chat ---

type openAIChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
}

func (c *LeannChat) chatOpenAI(ctx context.Context, messages []ChatMessage) (string, error) {
	reqBody := openAIChatRequest{
		Model:       c.config.Model,
		Messages:    messages,
		Temperature: c.config.Temperature,
		MaxTokens:   c.config.MaxTokens,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := c.config.BaseURL + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			respBody = []byte("(body unreadable)")
		}
		return "", fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, string(respBody))
	}

	var result openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return result.Choices[0].Message.Content, nil
}

// --- Anthropic Chat ---

type anthropicRequest struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	System      string        `json:"system,omitempty"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
}

type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func (c *LeannChat) chatAnthropic(ctx context.Context, messages []ChatMessage) (string, error) {
	// Extract system message.
	var system string
	var userMessages []ChatMessage
	for _, m := range messages {
		if m.Role == "system" {
			system = m.Content
		} else {
			userMessages = append(userMessages, m)
		}
	}

	maxTokens := c.config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}

	reqBody := anthropicRequest{
		Model:       c.config.Model,
		MaxTokens:   maxTokens,
		System:      system,
		Messages:    userMessages,
		Temperature: c.config.Temperature,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := c.config.BaseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if c.config.APIKey != "" {
		req.Header.Set("x-api-key", c.config.APIKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			respBody = []byte("(body unreadable)")
		}
		return "", fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, string(respBody))
	}

	var result anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.Content) == 0 {
		return "", fmt.Errorf("no content in response")
	}

	var parts []string
	for _, c := range result.Content {
		parts = append(parts, c.Text)
	}
	return strings.Join(parts, ""), nil
}

// --- Ollama Streaming ---

// ollamaStreamChunk is a single NDJSON line from Ollama streaming response.
type ollamaStreamChunk struct {
	Message ChatMessage `json:"message"`
	Done    bool        `json:"done"`
}

func (c *LeannChat) chatOllamaStream(ctx context.Context, messages []ChatMessage, callback StreamCallback) error {
	reqBody := ollamaChatRequest{
		Model:    c.config.Model,
		Messages: messages,
		Stream:   true,
		Think:    c.config.Think,
		Options: map[string]any{
			"temperature": c.config.Temperature,
		},
	}
	if c.config.MaxTokens > 0 {
		reqBody.Options["num_predict"] = c.config.MaxTokens
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := c.config.BaseURL + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Use the shared stream client (no timeout; context handles cancellation).
	resp, err := c.getStreamClient().Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			return fmt.Errorf("cannot connect to Ollama at %s — is it running?\n  Fix: ollama serve   (or: systemctl start ollama)\n  Check: gleann doctor", c.config.BaseURL)
		}
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			respBody = []byte("(body unreadable)")
		}
		bodyStr := string(respBody)
		if resp.StatusCode == http.StatusNotFound && strings.Contains(bodyStr, "not found") {
			return fmt.Errorf("model '%s' not found in Ollama\n  Fix: ollama pull %s\n  Available models: ollama list", c.config.Model, c.config.Model)
		}
		return fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, bodyStr)
	}

	// Ollama streams NDJSON: one JSON object per line.
	scanner := bufio.NewScanner(resp.Body)
	// Increase buffer for large JSON lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var chunk ollamaStreamChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue // Skip malformed lines.
		}

		if chunk.Message.Content != "" {
			callback(chunk.Message.Content)
		}

		if chunk.Done {
			break
		}
	}

	return scanner.Err()
}

// --- OpenAI Streaming ---

// openAIStreamChunk is a single SSE data line from OpenAI streaming response.
type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

func (c *LeannChat) chatOpenAIStream(ctx context.Context, messages []ChatMessage, callback StreamCallback) error {
	reqBody := map[string]any{
		"model":       c.config.Model,
		"messages":    messages,
		"temperature": c.config.Temperature,
		"stream":      true,
	}
	if c.config.MaxTokens > 0 {
		reqBody["max_tokens"] = c.config.MaxTokens
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := c.config.BaseURL + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	resp, err := c.getStreamClient().Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			respBody = []byte("(body unreadable)")
		}
		return fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, string(respBody))
	}

	// OpenAI sends SSE: "data: {json}\n\n" lines, ending with "data: [DONE]".
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				callback(choice.Delta.Content)
			}
		}
	}

	return scanner.Err()
}

// --- Anthropic Streaming ---

// anthropicStreamEvent represents a streaming event from the Anthropic API.
type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

func (c *LeannChat) chatAnthropicStream(ctx context.Context, messages []ChatMessage, callback StreamCallback) error {
	var system string
	var userMessages []ChatMessage
	for _, m := range messages {
		if m.Role == "system" {
			system = m.Content
		} else {
			userMessages = append(userMessages, m)
		}
	}

	maxTokens := c.config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}

	reqBody := map[string]any{
		"model":       c.config.Model,
		"max_tokens":  maxTokens,
		"messages":    userMessages,
		"temperature": c.config.Temperature,
		"stream":      true,
	}
	if system != "" {
		reqBody["system"] = system
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := c.config.BaseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if c.config.APIKey != "" {
		req.Header.Set("x-api-key", c.config.APIKey)
	}

	resp, err := c.getStreamClient().Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			respBody = []byte("(body unreadable)")
		}
		return fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, string(respBody))
	}

	// Anthropic sends SSE: "event: content_block_delta\ndata: {...}\n\n".
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		var event anthropicStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "content_block_delta":
			if event.Delta.Text != "" {
				callback(event.Delta.Text)
			}
		case "message_stop":
			return nil
		}
	}

	return scanner.Err()
}

// formatResult formats a single SearchResult into a text string for the LLM context.
func formatResult(r SearchResult, idx int) string {
	var sb strings.Builder
	source := ""
	if s, ok := r.Metadata["source"]; ok {
		source = fmt.Sprintf(" (source: %v)", s)
	}
	sb.WriteString(fmt.Sprintf("[%d]%s\n", idx, source))

	if r.GraphContext != nil {
		if dc := r.GraphContext.DocumentContext; dc != nil {
			sb.WriteString(fmt.Sprintf("Document: %s | Folder: %s\nSummary: %s\n", dc.Name, dc.FolderName, dc.Summary))
		}

		if len(r.GraphContext.Symbols) > 0 {
			sb.WriteString("Code Context:\n")
			for _, sym := range r.GraphContext.Symbols {
				sb.WriteString(fmt.Sprintf("- Symbol: %s (%s)\n", sym.FQN, sym.Kind))
				if len(sym.Callers) > 0 {
					sb.WriteString(fmt.Sprintf("  Callers: %s\n", strings.Join(sym.Callers, ", ")))
				}
				if len(sym.Callees) > 0 {
					sb.WriteString(fmt.Sprintf("  Callees: %s\n", strings.Join(sym.Callees, ", ")))
				}
			}
		}
	}

	sb.WriteString("Content:\n")
	sb.WriteString(r.Text)
	return sb.String()
}
