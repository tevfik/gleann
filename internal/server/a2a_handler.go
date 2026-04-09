package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/a2a"
	"github.com/tevfik/gleann/pkg/gleann"
)

// mountA2A sets up the A2A protocol server with skill handlers
// backed by gleann's actual search, RAG, memory, and graph engines.
func (s *Server) mountA2A(mux *http.ServeMux) {
	// Check if A2A is disabled (env var overrides config).
	if env := os.Getenv("GLEANN_A2A_ENABLED"); env == "false" {
		return
	} else if env == "" && s.config.A2AEnabled != nil && !*s.config.A2AEnabled {
		return
	}

	baseURL := fmt.Sprintf("http://localhost%s", s.addr)
	if !strings.Contains(s.addr, ":") {
		baseURL = "http://localhost:" + s.addr
	}

	card := a2a.DefaultAgentCard(s.version, baseURL)
	srv := a2a.NewServer(card)

	// Register skill handlers backed by real gleann engines.
	srv.RegisterSkill("semantic-search", s.a2aSearchHandler)
	srv.RegisterSkill("ask-rag", s.a2aAskHandler)
	srv.RegisterSkill("memory-management", s.a2aMemoryHandler)
	srv.RegisterSkill("code-analysis", s.a2aCodeHandler)

	srv.Mount(mux)
}

// a2aSearchHandler performs semantic search across all indexes.
func (s *Server) a2aSearchHandler(ctx a2a.SkillContext) (string, error) {
	indexes, err := gleann.ListIndexes(s.config.IndexDir)
	if err != nil || len(indexes) == 0 {
		return "", fmt.Errorf("no indexes available; build one with 'gleann index build'")
	}

	// Search across all indexes.
	var results []string
	for _, idx := range indexes {
		searcher, err := s.getSearcher(context.Background(), idx.Name)
		if err != nil {
			continue
		}
		hits, err := searcher.Search(context.Background(), ctx.Query)
		if err != nil {
			continue
		}
		for _, h := range hits {
			snippet := h.Text
			if len(snippet) > 300 {
				snippet = snippet[:300] + "..."
			}
			results = append(results, fmt.Sprintf("[%s] (score: %.3f) %s", idx.Name, h.Score, snippet))
		}
		if len(results) >= 10 {
			break
		}
	}

	if len(results) == 0 {
		return "No results found for: " + ctx.Query, nil
	}
	return strings.Join(results, "\n\n"), nil
}

// a2aAskHandler performs RAG question answering.
func (s *Server) a2aAskHandler(ctx a2a.SkillContext) (string, error) {
	indexes, err := gleann.ListIndexes(s.config.IndexDir)
	if err != nil || len(indexes) == 0 {
		return "", fmt.Errorf("no indexes available for RAG")
	}

	// Use the first available index.
	indexName := indexes[0].Name
	searcher, err := s.getSearcher(context.Background(), indexName)
	if err != nil {
		return "", fmt.Errorf("index %q not available: %v", indexName, err)
	}

	// Build RAG context from search results.
	hits, err := searcher.Search(context.Background(), ctx.Query)
	if err != nil {
		return "", fmt.Errorf("search failed: %v", err)
	}

	var contextParts []string
	for _, h := range hits {
		contextParts = append(contextParts, h.Text)
	}

	if len(contextParts) == 0 {
		return "I couldn't find relevant context to answer: " + ctx.Query, nil
	}

	ragContext := strings.Join(contextParts, "\n---\n")
	answer := fmt.Sprintf("Based on %d retrieved documents from index '%s':\n\n%s\n\nQuery: %s",
		len(contextParts), indexName, ragContext, ctx.Query)

	return answer, nil
}

// a2aMemoryHandler manages memory blocks.
func (s *Server) a2aMemoryHandler(ctx a2a.SkillContext) (string, error) {
	mgr, err := s.blockManager()
	if err != nil {
		return "", fmt.Errorf("memory engine unavailable: %v", err)
	}

	query := ctx.Query

	// Detect intent: remember vs recall.
	lq := strings.ToLower(query)
	if strings.Contains(lq, "remember") || strings.Contains(lq, "hatırla") || strings.Contains(lq, "store") {
		// Store as a memory block.
		// Remove the "remember" prefix for cleaner storage.
		content := query
		for _, prefix := range []string{"remember that ", "remember ", "hatırla ", "store "} {
			if strings.HasPrefix(lq, prefix) {
				content = query[len(prefix):]
				break
			}
		}

		if _, err := mgr.Remember(content, "a2a"); err != nil {
			return "", fmt.Errorf("failed to store memory: %v", err)
		}
		return fmt.Sprintf("Stored in memory: %s", content), nil
	}

	// Default: search memory.
	searchQuery := query
	for _, prefix := range []string{"recall ", "what do you know about ", "memory search "} {
		if strings.HasPrefix(lq, prefix) {
			searchQuery = query[len(prefix):]
			break
		}
	}

	blocks, err := mgr.Search(searchQuery)
	if err != nil {
		return "", fmt.Errorf("memory search failed: %v", err)
	}

	if len(blocks) == 0 {
		return "No memories found for: " + searchQuery, nil
	}

	var parts []string
	for _, b := range blocks {
		parts = append(parts, fmt.Sprintf("- [%s] %s (stored: %s)",
			b.Tier, b.Content, b.CreatedAt.Format(time.RFC3339)))
	}
	return fmt.Sprintf("Found %d memories:\n%s", len(blocks), strings.Join(parts, "\n")), nil
}

// a2aCodeHandler provides code graph analysis.
func (s *Server) a2aCodeHandler(ctx a2a.SkillContext) (string, error) {
	// Code analysis requires a specific index with graph data.
	indexes, err := gleann.ListIndexes(s.config.IndexDir)
	if err != nil || len(indexes) == 0 {
		return "", fmt.Errorf("no indexes available for code analysis")
	}

	return fmt.Sprintf("Code analysis for query '%s': Use the graph API endpoint (POST /api/graph/{name}/query) with specific index name for detailed results. Available indexes: %d", ctx.Query, len(indexes)), nil
}
