package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// ---------------------------------------------------------------------------
// OpenAI-compatible types
// ---------------------------------------------------------------------------

type oaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaiChatRequest struct {
	Model    string       `json:"model"`
	Messages []oaiMessage `json:"messages"`
	Stream   bool         `json:"stream"`
	// Optional passthrough fields (forwarded to real LLM).
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
}

type oaiChoice struct {
	Index        int        `json:"index"`
	Message      oaiMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type oaiChatResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []oaiChoice `json:"choices"`
}

type oaiDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type oaiStreamChoice struct {
	Index        int      `json:"index"`
	Delta        oaiDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason"`
}

type oaiChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []oaiStreamChoice `json:"choices"`
}

type oaiModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type oaiModelList struct {
	Object string     `json:"object"`
	Data   []oaiModel `json:"data"`
}

// ---------------------------------------------------------------------------
// GET /v1/models
// ---------------------------------------------------------------------------

// handleListModels returns all gleann indexes as OpenAI model objects.
// Each index is exposed as "gleann/<name>". A special "gleann/" entry
// represents pure-LLM mode (no RAG).
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	indexes, _ := gleann.ListIndexes(s.config.IndexDir)

	now := time.Now().Unix()
	models := []oaiModel{
		// Pure-LLM entry (no index).
		{ID: "gleann/", Object: "model", Created: now, OwnedBy: "gleann"},
	}
	for _, idx := range indexes {
		models = append(models, oaiModel{
			ID:      "gleann/" + idx.Name,
			Object:  "model",
			Created: now,
			OwnedBy: "gleann",
		})
	}

	writeJSON(w, http.StatusOK, oaiModelList{Object: "list", Data: models})
}

// ---------------------------------------------------------------------------
// POST /v1/chat/completions
// ---------------------------------------------------------------------------

// handleChatCompletions is the OpenAI-compatible RAG proxy endpoint.
// It accepts standard chat completion requests, performs RAG retrieval
// based on the model name prefix, injects context, and streams or
// returns the LLM response in OpenAI format.
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req oaiChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages array is required")
		return
	}

	// Parse index names from model field.
	indexNames := parseIndexFromModel(req.Model)

	// Per-request RAG option overrides from headers.
	var searchOpts []gleann.SearchOption
	if v := r.Header.Get("X-Gleann-Top-K"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			searchOpts = append(searchOpts, gleann.WithTopK(n))
		}
	} else if s.config.SearchConfig.TopK > 0 {
		searchOpts = append(searchOpts, gleann.WithTopK(s.config.SearchConfig.TopK))
	}
	if v := r.Header.Get("X-Gleann-Min-Score"); v != "" {
		if f, err := strconv.ParseFloat(v, 32); err == nil {
			searchOpts = append(searchOpts, gleann.WithMinScore(float32(f)))
		}
	}

	// Build LLM chat config from server config.
	chatCfg := s.proxyLLMConfig()
	if req.Temperature != nil {
		chatCfg.Temperature = *req.Temperature
	}
	if req.MaxTokens != nil {
		chatCfg.MaxTokens = *req.MaxTokens
	}

	// Build messages with RAG context injected.
	messages, err := s.buildProxyMessages(r.Context(), req.Messages, indexNames, searchOpts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RAG retrieval failed: "+err.Error())
		return
	}

	// Convert to gleann ChatMessage slice.
	gcMessages := make([]gleann.ChatMessage, len(messages))
	for i, m := range messages {
		gcMessages[i] = gleann.ChatMessage{Role: m.Role, Content: m.Content}
	}

	reqID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	now := time.Now().Unix()

	if req.Stream {
		s.streamChatCompletions(w, r, chatCfg, gcMessages, req.Model, reqID, now)
		return
	}
	s.syncChatCompletions(w, r, chatCfg, gcMessages, req.Model, reqID, now)
}

// ---------------------------------------------------------------------------
// Sync (non-streaming) response
// ---------------------------------------------------------------------------

func (s *Server) syncChatCompletions(
	w http.ResponseWriter, r *http.Request,
	chatCfg gleann.ChatConfig, messages []gleann.ChatMessage,
	model, reqID string, created int64,
) {
	chat := gleann.NewChat(gleann.NullSearcher{}, chatCfg)
	// Pre-load all messages as history except the last user message.
	for _, m := range messages[:len(messages)-1] {
		chat.AppendHistory(m)
	}
	lastMsg := messages[len(messages)-1]

	answer, err := chat.Ask(r.Context(), lastMsg.Content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LLM error: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, oaiChatResponse{
		ID:      reqID,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []oaiChoice{{
			Index:        0,
			Message:      oaiMessage{Role: "assistant", Content: answer},
			FinishReason: "stop",
		}},
	})
}

// ---------------------------------------------------------------------------
// Streaming response (SSE)
// ---------------------------------------------------------------------------

func (s *Server) streamChatCompletions(
	w http.ResponseWriter, r *http.Request,
	chatCfg gleann.ChatConfig, messages []gleann.ChatMessage,
	model, reqID string, created int64,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sendChunk := func(content string, finishReason *string) {
		chunk := oaiChunk{
			ID:      reqID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []oaiStreamChoice{{
				Index:        0,
				Delta:        oaiDelta{Content: content},
				FinishReason: finishReason,
			}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// Send role chunk first.
	roleChunk := oaiChunk{
		ID:      reqID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []oaiStreamChoice{{
			Index: 0,
			Delta: oaiDelta{Role: "assistant"},
		}},
	}
	data, _ := json.Marshal(roleChunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	// Stream tokens.
	chat := gleann.NewChat(gleann.NullSearcher{}, chatCfg)
	for _, m := range messages[:len(messages)-1] {
		chat.AppendHistory(m)
	}
	lastMsg := messages[len(messages)-1]

	err := chat.AskStream(r.Context(), lastMsg.Content, func(token string) {
		sendChunk(token, nil)
	})

	stop := "stop"
	if err != nil {
		errStr := "error"
		sendChunk("[error: "+err.Error()+"]", &errStr)
	} else {
		sendChunk("", &stop)
	}

	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseIndexFromModel extracts index names from model strings like:
//
//	"gleann/my-docs"  → ["my-docs"]
//	"gleann/a,b"      → ["a","b"]
//	"gleann/"         → nil  (pure LLM)
//	"gpt-4o"          → nil  (pass-through, no prefix)
func parseIndexFromModel(model string) []string {
	const prefix = "gleann/"
	if !strings.HasPrefix(model, prefix) {
		return nil
	}
	rest := strings.TrimPrefix(model, prefix)
	if rest == "" {
		return nil
	}
	parts := strings.Split(rest, ",")
	var names []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			names = append(names, p)
		}
	}
	return names
}

// buildProxyMessages performs RAG retrieval (if indexNames non-empty) and
// injects context into the message list as a leading system message.
func (s *Server) buildProxyMessages(
	ctx context.Context,
	messages []oaiMessage,
	indexNames []string,
	searchOpts []gleann.SearchOption,
) ([]oaiMessage, error) {
	if len(indexNames) == 0 {
		return messages, nil
	}

	// Use last user message as the search query.
	query := lastUserContent(messages)
	if query == "" {
		return messages, nil
	}

	// Load searcher(s) and search.
	var contextParts []string
	if len(indexNames) == 1 {
		searcher, err := s.getSearcher(ctx, indexNames[0])
		if err != nil {
			return nil, fmt.Errorf("load index %q: %w", indexNames[0], err)
		}
		results, err := searcher.Search(ctx, query, searchOpts...)
		if err != nil {
			return nil, fmt.Errorf("search: %w", err)
		}
		for i, r := range results {
			src := ""
			if v, ok := r.Metadata["source"]; ok {
				src = fmt.Sprintf(" (source: %v)", v)
			}
			contextParts = append(contextParts, fmt.Sprintf("[%d]%s %s", i+1, src, r.Text))
		}
	} else {
		// Multi-index: search each individually, merge results.
		all := make(map[string][]gleann.SearchResult)
		for _, name := range indexNames {
			searcher, err := s.getSearcher(ctx, name)
			if err != nil {
				continue // skip unavailable indexes
			}
			results, err := searcher.Search(ctx, query, searchOpts...)
			if err != nil {
				continue
			}
			all[name] = results
		}
		idx := 1
		for idxName, results := range all {
			for _, r := range results {
				src := fmt.Sprintf("(index: %s", idxName)
				if v, ok := r.Metadata["source"]; ok {
					src += fmt.Sprintf(", source: %v", v)
				}
				src += ")"
				contextParts = append(contextParts, fmt.Sprintf("[%d] %s %s", idx, src, r.Text))
				idx++
			}
		}
	}

	if len(contextParts) == 0 {
		return messages, nil
	}

	contextBlock := strings.Join(contextParts, "\n\n")
	systemMsg := oaiMessage{
		Role: "system",
		Content: "You are a helpful assistant. Use the following context to answer the user's question.\n\n" +
			"Context:\n" + contextBlock,
	}

	// Prepend context system message; keep any existing system messages after it.
	return append([]oaiMessage{systemMsg}, messages...), nil
}

// lastUserContent returns the content of the last user message.
func lastUserContent(messages []oaiMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

// proxyLLMConfig builds a ChatConfig from server saved config (same as TUI flow).
func (s *Server) proxyLLMConfig() gleann.ChatConfig {
	cfg := gleann.DefaultChatConfig()

	// Apply saved user config.
	if s.config.OllamaHost != "" && !strings.Contains(s.config.OllamaHost, "(auto-scan") {
		cfg.BaseURL = s.config.OllamaHost
	}
	if s.config.OpenAIAPIKey != "" {
		cfg.APIKey = s.config.OpenAIAPIKey
	}
	if s.config.OpenAIBaseURL != "" {
		cfg.BaseURL = s.config.OpenAIBaseURL
		cfg.Provider = gleann.LLMOpenAI
	}

	return cfg
}
