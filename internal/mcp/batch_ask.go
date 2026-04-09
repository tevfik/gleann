// Package mcp — gleann_batch_ask tool.
//
// Runs multiple questions concurrently against a single Gleann index and
// returns all answers in one response.  Useful for agents that need to
// explore a topic from several angles without making N sequential calls.
//
// Inputs:
//
//	questions   []string  — 1-10 questions to answer (required)
//	index       string    — target Gleann index name (required)
//	top_k       int       — RAG results per question (default 5)
//	concurrency int       — parallel questions allowed (default 3, max 5)
//
// Output: numbered list of question/answer pairs with latency info.
package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

const (
	batchMaxQuestions   = 10 // hard limit to prevent abuse
	batchMaxConcurrency = 5  // max parallel LLM calls
)

// batchResult holds the outcome of a single question within a batch.
type batchResult struct {
	idx      int
	question string
	answer   string
	queryMs  int64
	err      string
}

func (s *Server) buildBatchAskTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name: "gleann_batch_ask",
		Description: "Run multiple questions concurrently against a Gleann index and " +
			"return all answers in one response. Up to " + fmt.Sprint(batchMaxQuestions) +
			" questions per call. Useful for exploring a topic from several angles " +
			"or performing parallel research across a codebase.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"questions": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": fmt.Sprintf("List of questions to answer (1–%d)", batchMaxQuestions),
					"minItems":    1,
					"maxItems":    batchMaxQuestions,
				},
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the Gleann index to search against",
				},
				"top_k": map[string]interface{}{
					"type":        "number",
					"description": "Number of RAG context chunks per question (default 5)",
					"default":     5,
				},
				"concurrency": map[string]interface{}{
					"type":        "number",
					"description": fmt.Sprintf("Parallel question slots (1–%d, default 3)", batchMaxConcurrency),
					"default":     3,
				},
			},
			Required: []string{"questions", "index"},
		},
	}
}

func (s *Server) handleBatchAsk(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcpsdk.NewToolResultError("invalid arguments"), nil
	}

	// Parse questions.
	rawQ, ok := args["questions"]
	if !ok {
		return mcpsdk.NewToolResultError("questions is required"), nil
	}
	rawSlice, ok := rawQ.([]interface{})
	if !ok || len(rawSlice) == 0 {
		return mcpsdk.NewToolResultError("questions must be a non-empty array"), nil
	}
	if len(rawSlice) > batchMaxQuestions {
		rawSlice = rawSlice[:batchMaxQuestions]
	}
	questions := make([]string, 0, len(rawSlice))
	for _, v := range rawSlice {
		if q, ok := v.(string); ok && strings.TrimSpace(q) != "" {
			questions = append(questions, q)
		}
	}
	if len(questions) == 0 {
		return mcpsdk.NewToolResultError("no valid questions provided"), nil
	}

	// Parse index name.
	indexName, _ := args["index"].(string)
	if indexName == "" {
		return mcpsdk.NewToolResultError("index is required"), nil
	}

	// Parse optional params.
	topK := 5
	if v, ok := args["top_k"].(float64); ok && v > 0 {
		topK = int(v)
	}
	concurrency := 3
	if v, ok := args["concurrency"].(float64); ok && v > 0 {
		c := int(v)
		if c > batchMaxConcurrency {
			c = batchMaxConcurrency
		}
		concurrency = c
	}

	// Load searcher.
	searcher, err := s.getSearcherMCP(ctx, indexName)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("index %q not found: %v", indexName, err)), nil
	}

	// Run questions concurrently with a semaphore.
	results := make([]batchResult, len(questions))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, q := range questions {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, question string) {
			defer wg.Done()
			defer func() { <-sem }()

			start := time.Now()
			chatCfg := gleann.DefaultChatConfig()
			chat := gleann.NewChat(searcher, chatCfg)

			opts := []gleann.SearchOption{gleann.WithTopK(topK)}
			answer, err := chat.Ask(ctx, question, opts...)

			br := batchResult{
				idx:      idx,
				question: question,
				queryMs:  time.Since(start).Milliseconds(),
			}
			if err != nil {
				br.err = err.Error()
			} else {
				br.answer = answer
			}
			results[idx] = br
		}(i, q)
	}
	wg.Wait()

	// Format output.
	var sb strings.Builder
	fmt.Fprintf(&sb, "Batch results for index %q (%d questions, concurrency %d):\n\n",
		indexName, len(questions), concurrency)

	for _, r := range results {
		fmt.Fprintf(&sb, "── Q%d: %s\n", r.idx+1, r.question)
		if r.err != "" {
			fmt.Fprintf(&sb, "   ERROR: %s\n\n", r.err)
		} else {
			fmt.Fprintf(&sb, "   A: %s\n   [%dms]\n\n", r.answer, r.queryMs)
		}
	}

	return mcpsdk.NewToolResultText(sb.String()), nil
}

// getSearcherMCP loads (and caches) a LeannSearcher for MCP tool use.
// We mirror the existing s.getSearcher() which manages the LRU cache.
func (s *Server) getSearcherMCP(ctx context.Context, name string) (*gleann.LeannSearcher, error) {
	return s.getSearcher(name)
}

// cacheSearcher is unused — calls go through getSearcher instead.
// Kept to satisfy the call-site reference in older IDE sessions.
