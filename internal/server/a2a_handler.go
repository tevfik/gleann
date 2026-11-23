package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/a2a"
	"github.com/tevfik/gleann/internal/auth"
	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/multimodal"
	"github.com/tevfik/gleann/internal/pathpolicy"
	"github.com/tevfik/gleann/pkg/gleann"
)

// mountA2A sets up the A2A protocol server with skill handlers
// backed by gleann's actual search, RAG, memory, and graph engines.
func (s *Server) mountA2A(mux *http.ServeMux) {
	// Check if A2A is disabled (env var overrides config).
	if env := os.Getenv("GLEANN_A2A_ENABLED"); env == "false" {
		return
	} else if env == "" && s.cfg().A2AEnabled != nil && !*s.cfg().A2AEnabled {
		return
	}

	card := a2a.DefaultAgentCard(s.version, s.a2aBaseURL())
	srv := a2a.NewServer(card)

	// Register skill handlers backed by real gleann engines.
	srv.RegisterSkill("semantic-search", s.a2aSearchHandler)
	srv.RegisterSkill("ask-rag", s.a2aAskHandler)
	srv.RegisterSkill("memory-management", s.a2aMemoryHandler)
	srv.RegisterSkill("code-analysis", s.a2aCodeHandler)
	srv.RegisterSkill("code-communities", s.a2aCommunitiesHandler)
	srv.RegisterSkill("repo-map", s.a2aRepoMapHandler)
	srv.RegisterSkill("risk-analysis", s.a2aRiskHandler)
	srv.RegisterSkill("multimodal-analyze", s.a2aMultimodalHandler)

	srv.Mount(mux)
}

// a2aBaseURL resolves the base URL the agent card advertises (the card
// appends /a2a/v1). Precedence: GLEANN_A2A_BASE_URL > config a2a_base_url >
// the URL derived from the bound address. Both explicit values go through
// gleann.NormalizeA2ABaseURL; an invalid one is skipped with a warning that
// names it and the base URL used instead.
func (s *Server) a2aBaseURL() string {
	type source struct{ name, val string }
	var ignored []string
	used, usedFrom := "", ""
	for _, src := range []source{
		{"GLEANN_A2A_BASE_URL", os.Getenv("GLEANN_A2A_BASE_URL")},
		{"config a2a_base_url", s.cfg().A2ABaseURL},
	} {
		u, err := gleann.NormalizeA2ABaseURL(src.val)
		if err != nil {
			ignored = append(ignored, fmt.Sprintf("%s %q: %v", src.name, redactURL(src.val), err))
			continue
		}
		if u != "" {
			used, usedFrom = u, src.name
			break
		}
	}
	if used == "" {
		used = gleann.AdvertisedBaseURL(s.addr)
		usedFrom = "derived from the server address " + s.addr
	}
	for _, msg := range ignored {
		log.Printf("warning: A2A agent card: ignoring %s; using %s (%s)", msg, used, usedFrom)
	}
	return used
}

// redactURL hides the password of a URL with userinfo, so a rejected value
// does not leak it into the log.
func redactURL(raw string) string {
	if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.User != nil {
		return u.Redacted()
	}
	return raw
}

// a2aSearchHandler performs semantic search across targeted or accessible indexes.
func (s *Server) a2aSearchHandler(ctx a2a.SkillContext) (string, error) {
	target := ""
	if t, ok := ctx.Metadata["target"].(string); ok && t != "" {
		target = t
	} else if idx, ok := ctx.Metadata["index"].(string); ok && idx != "" {
		target = idx
	} else if p, ok := ctx.Metadata["project"].(string); ok && p != "" {
		target = p
	}

	var targetIndexes []string
	if target != "" {
		if !s.isIndexAccessible(target) {
			return "", fmt.Errorf("access denied or index %q not accessible", target)
		}
		targetIndexes = []string{target}
	} else {
		indexes, err := gleann.ListIndexes(s.cfg().IndexDir)
		if err != nil || len(indexes) == 0 {
			return "", fmt.Errorf("no indexes available; build one with 'gleann index build'")
		}
		for _, idx := range indexes {
			if s.isIndexAccessibleMeta(&idx) {
				targetIndexes = append(targetIndexes, idx.Name)
			}
		}
		if len(targetIndexes) == 0 {
			return "No accessible indexes found", nil
		}
	}

	// Search across targetIndexes.
	var results []string
	for _, idxName := range targetIndexes {
		searcher, release, err := s.acquireSearcher(context.Background(), idxName)
		if err != nil {
			continue
		}
		hits, err := searcher.Search(context.Background(), ctx.Query)
		release()
		if err != nil {
			continue
		}
		for _, h := range hits {
			snippet := h.Text
			if len(snippet) > 300 {
				snippet = snippet[:300] + "..."
			}
			results = append(results, fmt.Sprintf("[%s] (score: %.3f) %s", idxName, h.Score, snippet))
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
	targetIndex := ""
	if t, ok := ctx.Metadata["target"].(string); ok && t != "" {
		targetIndex = t
	} else if idx, ok := ctx.Metadata["index"].(string); ok && idx != "" {
		targetIndex = idx
	} else if p, ok := ctx.Metadata["project"].(string); ok && p != "" {
		targetIndex = p
	}

	if targetIndex != "" {
		if !s.isIndexAccessible(targetIndex) {
			return "", fmt.Errorf("index %q not available or access denied", targetIndex)
		}
	} else {
		indexes, err := gleann.ListIndexes(s.cfg().IndexDir)
		if err != nil || len(indexes) == 0 {
			return "", fmt.Errorf("no indexes available for RAG")
		}
		for _, idx := range indexes {
			if s.isIndexAccessibleMeta(&idx) {
				targetIndex = idx.Name
				break
			}
		}
		if targetIndex == "" {
			return "", fmt.Errorf("no accessible index available for RAG")
		}
	}

	searcher, release, err := s.acquireSearcher(context.Background(), targetIndex)
	if err != nil {
		return "", fmt.Errorf("index %q not available: %v", targetIndex, err)
	}
	defer release()

	// Build LLM config from server settings.
	chatCfg := s.proxyLLMConfig()
	if model, ok := ctx.Metadata["llm_model"].(string); ok && model != "" {
		chatCfg.Model = model
	}
	if provider, ok := ctx.Metadata["llm_provider"].(string); ok && provider != "" && gleann.LLMProvider(provider) != chatCfg.Provider {
		// A client-chosen provider gets that provider's configured endpoint
		// and key, not the ones resolved for the configured provider.
		chatCfg.Provider = gleann.LLMProvider(provider)
		s.cfg().ApplyChatEndpoint(&chatCfg)
	}

	chat := gleann.NewChat(searcher, chatCfg)

	// If A2A message includes file attachments (images/audio), use multimodal path.
	if len(ctx.Files) > 0 {
		var images []string
		for _, f := range ctx.Files {
			if f.Bytes != "" {
				images = append(images, f.Bytes)
			}
		}
		if len(images) > 0 {
			answer, err := chat.AskWithImages(context.Background(), ctx.Query, images)
			if err != nil {
				return "", fmt.Errorf("multimodal RAG ask failed: %v", err)
			}
			return answer, nil
		}
	}

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
		// Writing memory is an admin operation on every surface; a query
		// token must not plant blocks that later get injected into agents.
		if ctx.Scope != string(auth.ScopeAdmin) {
			return "", fmt.Errorf("storing memory requires an admin token; scope %q may only read memory", ctx.Scope)
		}
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

// a2aCodeHandler provides code graph analysis using the graph pool.
func (s *Server) a2aCodeHandler(ctx a2a.SkillContext) (string, error) {
	if s.graphPool == nil {
		return "", fmt.Errorf("code graph not available")
	}

	targetIndex := ""
	if t, ok := ctx.Metadata["target"].(string); ok && t != "" {
		targetIndex = t
	} else if idx, ok := ctx.Metadata["index"].(string); ok && idx != "" {
		targetIndex = idx
	} else if p, ok := ctx.Metadata["project"].(string); ok && p != "" {
		targetIndex = p
	}

	if targetIndex != "" {
		if !s.isIndexAccessible(targetIndex) {
			return "", fmt.Errorf("access denied or index %q not accessible", targetIndex)
		}
	} else {
		indexes, err := gleann.ListIndexes(s.cfg().IndexDir)
		if err != nil || len(indexes) == 0 {
			return "", fmt.Errorf("no indexes available for code analysis")
		}
		for _, idx := range indexes {
			if s.isIndexAccessibleMeta(&idx) {
				targetIndex = idx.Name
				break
			}
		}
		if targetIndex == "" {
			return "", fmt.Errorf("no accessible index available for code analysis")
		}
	}
	indexName := targetIndex

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
			msg := fmt.Sprintf(
				"Impact of %q (index: %s):\n  Direct callers: %d\n  Transitive callers: %d\n  Affected files: %d\n  Depth searched: %d",
				symbol, indexName,
				len(impact.DirectCallers), len(impact.TransitiveCallers),
				len(impact.AffectedFiles), impact.Depth,
			)
			if notice := a2aNotice(symbol, db, gleann.DirectionCallers); notice != "" {
				msg += "\n" + notice
			}
			return msg, nil
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

	dir := gleann.DirectionCallees
	if queryType == "callers" {
		dir = gleann.DirectionCallers
	}

	if len(nodes) == 0 {
		msg := fmt.Sprintf("No %s found for symbol %q in index %q.", queryType, symbol, indexName)
		if notice := a2aNotice(symbol, db, dir); notice != "" {
			msg += " " + notice
		}
		return msg, nil
	}

	var lines []string
	for _, n := range nodes {
		lines = append(lines, fmt.Sprintf("  - %s (%s)", n.FQN, n.Kind))
	}
	res := fmt.Sprintf("%s of %q (index: %s):\n%s",
		titleASCII(queryType), symbol, indexName, strings.Join(lines, "\n"))
	if notice := a2aNotice(symbol, db, dir); notice != "" {
		res += "\n" + notice
	}
	return res, nil
}

func a2aNotice(symbol string, db graphDBHandle, dir int) string {
	if sh, ok := db.(interface{ GraphDB() gleann.GraphDB }); ok {
		return indexing.NonGoSymbolNotice(symbol, sh.GraphDB(), dir)
	}
	return ""
}

// titleASCII upper-cases the first byte of an ASCII string. Replaces the
// deprecated strings.Title which mishandles Unicode word boundaries; we only
// ever feed it short ASCII tokens like "deps" or "callers".
func titleASCII(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 'a' - 'A'
	}
	return string(b)
}

// a2aMultimodalHandler analyzes files using vision-capable LLMs.
func (s *Server) a2aMultimodalHandler(ctx a2a.SkillContext) (string, error) {
	if len(ctx.Files) == 0 && ctx.Query == "" {
		return "", fmt.Errorf("provide a file path in the query or attach files")
	}

	ollamaHost := s.cfg().OllamaHost
	if ollamaHost == "" {
		ollamaHost = "http://localhost:11434"
	}
	model := s.cfg().MultimodalModel
	if model == "" {
		model = "gemma4"
	}

	// If files are attached, process them directly.
	if len(ctx.Files) > 0 {
		var results []string
		for _, f := range ctx.Files {
			results = append(results, fmt.Sprintf("[%s] Content provided via attachment", f.Name))
		}
		return strings.Join(results, "\n"), nil
	}

	// Otherwise, treat query as a file path.
	filePath := strings.TrimSpace(ctx.Query)

	// Reading a server-local file by path is an admin operation: a query
	// token must not have local media read and summarised through the model.
	// Attachments above carry their own bytes and stay query-allowed.
	if ctx.Scope != string(auth.ScopeAdmin) {
		return "", fmt.Errorf("analyzing a local file path requires an admin token; scope %q may only attach files", ctx.Scope)
	}

	// The query names a file the server reads and sends to the model, so it
	// is held to allowed_roots like any other client-supplied path.
	resolved, cerr := pathpolicy.Contain(filePath, s.allowedRoots())
	if cerr != nil {
		return "", fmt.Errorf("path %q is not inside the directories this server may read (allowed_roots)", filePath)
	}
	filePath = resolved

	// Use multimodal processor.
	proc := multimodal.NewProcessor(ollamaHost, model)
	if k := s.cfg().APIKeyForProvider("ollama"); k != "" {
		proc.APIKey = k
	}

	if strings.ToLower(filepath.Ext(filePath)) == ".pdf" {
		analysis, err := proc.AnalyzePDF(filePath, multimodal.DefaultPDFConfig())
		if err != nil {
			return "", fmt.Errorf("PDF analysis failed: %v", err)
		}

		var b strings.Builder
		fmt.Fprintf(&b, "Analysis of PDF %s (%d pages):\n\n", filePath, analysis.TotalPages)
		for _, page := range analysis.Pages {
			fmt.Fprintf(&b, "--- Page %d ---\n", page.PageNum)
			if page.HasTable {
				fmt.Fprintln(&b, "[Table Detected]")
			}
			if page.HasChart {
				fmt.Fprintln(&b, "[Chart Detected]")
			}
			fmt.Fprintln(&b, page.Description)
			if page.Tables != nil {
				for _, t := range page.Tables.Tables {
					fmt.Fprintln(&b, t.Markdown)
				}
			}
			fmt.Fprintln(&b)
		}
		return b.String(), nil
	}

	result := proc.ProcessFile(filePath)
	if result.Error != nil {
		return "", fmt.Errorf("analysis failed: %v", result.Error)
	}

	return fmt.Sprintf("Analysis of %s (%v):\n\n%s",
		filePath, result.MediaType, result.Description), nil
}
