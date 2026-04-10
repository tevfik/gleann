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

// a2aAskHandler performs RAG question answering using the LLM.
func (s *Server) a2aAskHandler(ctx a2a.SkillContext) (string, error) {
	indexes, err := gleann.ListIndexes(s.config.IndexDir)
	if err != nil || len(indexes) == 0 {
		return "", fmt.Errorf("no indexes available for RAG")
	}

	// Use the first available index.
	indexName := indexes[0].Name
	if idx, ok := ctx.Metadata["index"].(string); ok && idx != "" {
		indexName = idx
	}

	searcher, err := s.getSearcher(context.Background(), indexName)
	if err != nil {
		return "", fmt.Errorf("index %q not available: %v", indexName, err)
	}

	// Build LLM config from server settings.
	chatCfg := s.proxyLLMConfig()
	if model, ok := ctx.Metadata["llm_model"].(string); ok && model != "" {
		chatCfg.Model = model
	}

	chat := gleann.NewChat(searcher, chatCfg)

	answer, err := chat.Ask(context.Background(), ctx.Query)
	if err != nil {
		return "", fmt.Errorf("RAG ask failed: %v", err)
	}
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

// a2aCodeHandler provides code graph analysis using the KuzuDB graph pool.
func (s *Server) a2aCodeHandler(ctx a2a.SkillContext) (string, error) {
	if s.graphPool == nil {
		return "", fmt.Errorf("code graph not available (build with -tags treesitter)")
	}

	// Resolve index name: prefer metadata, then first available index.
	indexName, _ := ctx.Metadata["index"].(string)
	if indexName == "" {
		indexes, err := gleann.ListIndexes(s.config.IndexDir)
		if err != nil || len(indexes) == 0 {
			return "", fmt.Errorf("no indexes available for code analysis")
		}
		indexName = indexes[0].Name
	}

	db, err := s.graphPool.get(indexName)
	if err != nil {
		return "", fmt.Errorf("graph index %q not found: %v", indexName, err)
	}

	// Resolve symbol: prefer metadata["symbol"], else use the raw query.
	symbol, _ := ctx.Metadata["symbol"].(string)
	if symbol == "" {
		symbol = ctx.Query
	}

	// Detect query intent: callers vs callees vs symbols_in_file.
	lq := strings.ToLower(ctx.Query)
	var nodes []GraphNode
	var queryType string

	switch {
	case strings.Contains(lq, "who calls") || strings.Contains(lq, "callers") ||
		strings.Contains(lq, "kim çağırıyor") || strings.Contains(lq, "references"):
		nodes, err = db.Callers(symbol)
		queryType = "callers"

	case strings.Contains(lq, "callees") || strings.Contains(lq, "depends on") ||
		strings.Contains(lq, "bağımlılık") || strings.Contains(lq, "calls") ||
		strings.Contains(lq, "impact"):
		if strings.Contains(lq, "impact") {
			impact, ierr := db.Impact(symbol, 3)
			if ierr != nil {
				return "", fmt.Errorf("impact analysis failed: %v", ierr)
			}
			return fmt.Sprintf(
				"Impact of %q (index: %s):\n  Direct callers: %d\n  Transitive callers: %d\n  Affected files: %d\n  Depth searched: %d",
				symbol, indexName,
				len(impact.DirectCallers), len(impact.TransitiveCallers),
				len(impact.AffectedFiles), impact.Depth,
			), nil
		}
		nodes, err = db.Callees(symbol)
		queryType = "callees"

	default:
		// Default to callees.
		nodes, err = db.Callees(symbol)
		queryType = "callees"
	}

	if err != nil {
		return "", fmt.Errorf("graph query (%s %q) failed: %v", queryType, symbol, err)
	}

	if len(nodes) == 0 {
		return fmt.Sprintf("No %s found for symbol %q in index %q.", queryType, symbol, indexName), nil
	}

	var lines []string
	for _, n := range nodes {
		lines = append(lines, fmt.Sprintf("  - %s (%s)", n.FQN, n.Kind))
	}
	return fmt.Sprintf("%s of %q (index: %s):\n%s",
		strings.Title(queryType), symbol, indexName, strings.Join(lines, "\n")), nil
}
