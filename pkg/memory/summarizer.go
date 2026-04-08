package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
)

// SummarizerConfig holds configuration for the auto-summarization LLM calls.
type SummarizerConfig struct {
	Provider string // "ollama", "openai", "anthropic"
	Model    string
	BaseURL  string
	APIKey   string
}

// Summarizer generates conversation summaries for memory storage.
type Summarizer struct {
	config SummarizerConfig
}

// NewSummarizer creates a new summarizer with the given configuration.
func NewSummarizer(cfg SummarizerConfig) *Summarizer {
	return &Summarizer{config: cfg}
}

// SummarizeConversation generates a concise summary of a conversation.
// The summary captures key topics, decisions, and important facts.
func (s *Summarizer) SummarizeConversation(ctx context.Context, conv *conversations.Conversation) (*Summary, error) {
	// Build a compact transcript from conversation messages.
	var transcript strings.Builder
	msgCount := 0
	for _, m := range conv.Messages {
		if m.Role == "system" {
			continue
		}
		content := m.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		transcript.WriteString(fmt.Sprintf("%s: %s\n\n", m.Role, content))
		msgCount++
	}

	if transcript.Len() == 0 {
		return nil, fmt.Errorf("conversation has no messages to summarize")
	}

	prompt := fmt.Sprintf(
		"Summarize this conversation in 2-4 sentences. Focus on:\n"+
			"- Key topics discussed\n"+
			"- Decisions made\n"+
			"- Important facts or preferences mentioned\n"+
			"- Action items or conclusions\n\n"+
			"Reply ONLY with the summary, no quotes, no explanation.\n\n"+
			"Conversation:\n%s",
		transcript.String(),
	)

	summaryText, err := s.callLLM(ctx, prompt)
	if err != nil {
		// Fallback: extract first user message as summary.
		summaryText = fallbackSummary(conv)
	}

	summaryText = strings.TrimSpace(summaryText)
	if summaryText == "" {
		summaryText = fallbackSummary(conv)
	}

	return &Summary{
		ConversationID: conv.ID,
		Title:          conv.Title,
		Content:        summaryText,
		MessageCount:   msgCount,
		IndexNames:     conv.Indexes,
		Model:          conv.Model,
		CreatedAt:      time.Now(),
	}, nil
}

// ExtractMemories analyzes a conversation and extracts important facts
// that should be stored as long-term memories.
func (s *Summarizer) ExtractMemories(ctx context.Context, conv *conversations.Conversation) ([]Block, error) {
	var transcript strings.Builder
	for _, m := range conv.Messages {
		if m.Role == "system" {
			continue
		}
		content := m.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		transcript.WriteString(fmt.Sprintf("%s: %s\n\n", m.Role, content))
	}

	if transcript.Len() == 0 {
		return nil, nil
	}

	prompt := fmt.Sprintf(
		"Extract important facts, preferences, and knowledge from this conversation.\n"+
			"Return each fact on a separate line, prefixed with a category label.\n"+
			"Format: LABEL: fact\n"+
			"Valid labels: preference, fact, decision, todo, context\n"+
			"Only extract genuinely important, reusable information.\n"+
			"If nothing important, reply with NONE.\n\n"+
			"Conversation:\n%s",
		transcript.String(),
	)

	response, err := s.callLLM(ctx, prompt)
	if err != nil {
		return nil, err
	}

	response = strings.TrimSpace(response)
	if response == "NONE" || response == "" {
		return nil, nil
	}

	var blocks []Block
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "NONE" {
			continue
		}

		label := "fact"
		content := line

		if idx := strings.Index(line, ":"); idx > 0 && idx < 20 {
			candidate := strings.ToLower(strings.TrimSpace(line[:idx]))
			switch candidate {
			case "preference", "fact", "decision", "todo", "context":
				label = candidate
				content = strings.TrimSpace(line[idx+1:])
			}
		}

		if content != "" {
			blocks = append(blocks, Block{
				Tier:    TierLong,
				Label:   label,
				Content: content,
				Source:  "auto_extract",
				Tags:    []string{"auto", "conversation:" + conv.ID[:8]},
			})
		}
	}

	return blocks, nil
}

// callLLM sends a prompt to the configured LLM and returns the response.
func (s *Summarizer) callLLM(ctx context.Context, prompt string) (string, error) {
	client := &http.Client{Timeout: 60 * time.Second}

	switch strings.ToLower(s.config.Provider) {
	case "ollama", "":
		return s.callOllama(ctx, client, prompt)
	case "openai":
		return s.callOpenAI(ctx, client, prompt)
	case "anthropic":
		return s.callAnthropic(ctx, client, prompt)
	default:
		return s.callOllama(ctx, client, prompt)
	}
}

func (s *Summarizer) callOllama(ctx context.Context, client *http.Client, prompt string) (string, error) {
	baseURL := s.config.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	body := map[string]any{
		"model":  s.config.Model,
		"prompt": prompt,
		"stream": false,
		"options": map[string]any{
			"temperature": 0.3,
			"num_predict": 300,
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/generate", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Response, nil
}

func (s *Summarizer) callOpenAI(ctx context.Context, client *http.Client, prompt string) (string, error) {
	baseURL := s.config.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	body := map[string]any{
		"model": s.config.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.3,
		"max_tokens":  300,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI")
	}
	return result.Choices[0].Message.Content, nil
}

func (s *Summarizer) callAnthropic(ctx context.Context, client *http.Client, prompt string) (string, error) {
	baseURL := s.config.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}

	body := map[string]any{
		"model": s.config.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 300,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/messages", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if s.config.APIKey != "" {
		req.Header.Set("x-api-key", s.config.APIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("no response from Anthropic")
	}
	return result.Content[0].Text, nil
}

func fallbackSummary(conv *conversations.Conversation) string {
	for _, m := range conv.Messages {
		if m.Role == "user" {
			text := m.Content
			if len(text) > 200 {
				text = text[:197] + "..."
			}
			return text
		}
	}
	return "Empty conversation"
}
