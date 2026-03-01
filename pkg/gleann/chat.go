// Package gleann provides LLM chat integration for RAG-based Q&A.
package gleann

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	Provider    LLMProvider `json:"provider"`
	Model       string      `json:"model"`
	BaseURL     string      `json:"base_url,omitempty"`
	APIKey      string      `json:"api_key,omitempty"`
	Temperature float64     `json:"temperature,omitempty"`
	MaxTokens   int         `json:"max_tokens,omitempty"`
	SystemPrompt string     `json:"system_prompt,omitempty"`
}

// DefaultChatConfig returns default chat configuration.
func DefaultChatConfig() ChatConfig {
	return ChatConfig{
		Provider:    LLMOllama,
		Model:       "llama3.2",
		BaseURL:     "http://localhost:11434",
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
type LeannChat struct {
	searcher *LeannSearcher
	config   ChatConfig
	history  []ChatMessage
	client   *http.Client
}

// NewChat creates a new LeannChat instance.
func NewChat(searcher *LeannSearcher, config ChatConfig) *LeannChat {
	if config.BaseURL == "" {
		switch config.Provider {
		case LLMOllama:
			if host := os.Getenv("OLLAMA_HOST"); host != "" {
				config.BaseURL = host
			} else {
				config.BaseURL = "http://localhost:11434"
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

	return &LeannChat{
		searcher: searcher,
		config:   config,
		client:   &http.Client{Timeout: 120 * time.Second},
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
		source := ""
		if s, ok := r.Metadata["source"]; ok {
			source = fmt.Sprintf(" (source: %v)", s)
		}
		contextParts = append(contextParts, fmt.Sprintf("[%d]%s %s", i+1, source, r.Text))
	}
	context_text := strings.Join(contextParts, "\n\n")

	// Step 3: Build prompt.
	userPrompt := fmt.Sprintf("Context:\n%s\n\nQuestion: %s\n\nAnswer based on the context above:", context_text, question)

	// Step 4: Generate answer.
	messages := []ChatMessage{
		{Role: "system", Content: c.config.SystemPrompt},
	}
	// Add conversation history.
	messages = append(messages, c.history...)
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

// ClearHistory clears conversation history.
func (c *LeannChat) ClearHistory() {
	c.history = nil
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

// Searcher returns the underlying searcher instance.
func (c *LeannChat) Searcher() *LeannSearcher {
	return c.searcher
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
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

type ollamaChatResponse struct {
	Message ChatMessage `json:"message"`
}

func (c *LeannChat) chatOllama(ctx context.Context, messages []ChatMessage) (string, error) {
	reqBody := ollamaChatRequest{
		Model:    c.config.Model,
		Messages: messages,
		Stream:   false,
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
		return "", fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, string(respBody))
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
		respBody, _ := io.ReadAll(resp.Body)
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
		respBody, _ := io.ReadAll(resp.Body)
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
