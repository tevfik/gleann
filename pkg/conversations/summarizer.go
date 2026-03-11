package conversations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Summarizer generates conversation titles using an LLM.
type Summarizer struct {
	Provider string // "ollama", "openai"
	Model    string
	BaseURL  string
	APIKey   string
}

// SummarizeTitle generates a short title for the conversation from its messages.
// Returns the auto-generated title, or falls back to first-user-message truncation on error.
func (s *Summarizer) SummarizeTitle(ctx context.Context, conv *Conversation) string {
	// Build a compact prompt from the first few user/assistant exchanges.
	var msgPreview strings.Builder
	count := 0
	for _, m := range conv.Messages {
		if m.Role == "system" {
			continue
		}
		content := m.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		msgPreview.WriteString(fmt.Sprintf("%s: %s\n", m.Role, content))
		count++
		if count >= 4 { // limit to first 4 non-system messages
			break
		}
	}

	if msgPreview.Len() == 0 {
		return autoTitle(conv)
	}

	prompt := fmt.Sprintf(
		"Generate a very short title (max 6 words) for this conversation. "+
			"Reply ONLY with the title, no quotes, no explanation.\n\n%s",
		msgPreview.String(),
	)

	title, err := s.callLLM(ctx, prompt)
	if err != nil || strings.TrimSpace(title) == "" {
		return autoTitle(conv)
	}

	// Clean up: remove quotes, trailing period, trim whitespace.
	title = strings.TrimSpace(title)
	title = strings.Trim(title, "\"'`")
	title = strings.TrimRight(title, ".")
	title = strings.TrimSpace(title)

	// Enforce max length.
	if len(title) > 60 {
		title = title[:57] + "..."
	}

	return title
}

func (s *Summarizer) callLLM(ctx context.Context, prompt string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	switch strings.ToLower(s.Provider) {
	case "ollama", "":
		return s.callOllama(ctx, client, prompt)
	case "openai":
		return s.callOpenAI(ctx, client, prompt)
	default:
		return s.callOllama(ctx, client, prompt)
	}
}

func (s *Summarizer) callOllama(ctx context.Context, client *http.Client, prompt string) (string, error) {
	baseURL := s.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	body := map[string]any{
		"model":  s.Model,
		"prompt": prompt,
		"stream": false,
		"options": map[string]any{
			"temperature": 0.3,
			"num_predict": 30,
			"stop":        []string{"\n"},
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
	baseURL := s.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	body := map[string]any{
		"model": s.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.3,
		"max_tokens":  30,
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
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
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

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("no response")
}
