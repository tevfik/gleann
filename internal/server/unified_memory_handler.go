package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// mountUnifiedMemory registers the unified memory API endpoints.
func (s *Server) mountUnifiedMemory(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/memory/ingest", s.handleUnifiedIngest)
	mux.HandleFunc("POST /api/memory/recall", s.handleUnifiedRecall)
}

// --- Ingest ---

// UnifiedIngestRequest combines facts, relationships, and documents into one call.
type UnifiedIngestRequest struct {
	// Facts are short-term memory blocks (auto-tiered).
	Facts []IngestFact `json:"facts,omitempty"`
	// Relationships are knowledge graph edges (Entity → Entity).
	Relationships []IngestRelationship `json:"relationships,omitempty"`
	// Scope isolates facts to a conversation/agent (default: global).
	Scope string `json:"scope,omitempty"`
	// Project sets scope to "project:{name}" and targets matching index.
	// Overrides Scope if both are set.
	Project string `json:"project,omitempty"`
}

// IngestFact is a piece of knowledge to remember.
type IngestFact struct {
	Content   string            `json:"content"`
	Tags      []string          `json:"tags,omitempty"`
	Label     string            `json:"label,omitempty"`
	Tier      string            `json:"tier,omitempty"`       // short|medium|long (default: short)
	Metadata  map[string]string `json:"metadata,omitempty"`   // Arbitrary key-value pairs
	ExpiresIn string            `json:"expires_in,omitempty"` // Go duration (e.g. "24h", "7d") for TTL
	CharLimit int               `json:"char_limit,omitempty"` // Per-block character limit (0 = unlimited)
}

// IngestRelationship links two entities in the knowledge graph.
type IngestRelationship struct {
	From       string         `json:"from"`
	To         string         `json:"to"`
	Relation   string         `json:"relation"` // e.g. DEPENDS_ON, IMPLEMENTS
	Weight     float64        `json:"weight,omitempty"`
	Index      string         `json:"index,omitempty"`      // target index (default: first available)
	Attributes map[string]any `json:"attributes,omitempty"` // Edge metadata (timestamps, confidence, etc.)
}

// UnifiedIngestResponse reports what was stored.
type UnifiedIngestResponse struct {
	FactsStored  int      `json:"facts_stored"`
	EdgesCreated int      `json:"edges_created"`
	FactIDs      []string `json:"fact_ids,omitempty"`
	Errors       []string `json:"errors,omitempty"`
}

func (s *Server) handleUnifiedIngest(w http.ResponseWriter, r *http.Request) {
	var req UnifiedIngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if len(req.Facts) == 0 && len(req.Relationships) == 0 {
		writeError(w, http.StatusBadRequest, "at least one fact or relationship required")
		return
	}

	// Resolve project shorthand → scope + default index for relationships.
	if req.Project != "" {
		req.Scope = "project:" + req.Project
		// Set default index for relationships that don't specify one.
		for i := range req.Relationships {
			if req.Relationships[i].Index == "" {
				req.Relationships[i].Index = req.Project
			}
		}
	}

	resp := UnifiedIngestResponse{}

	// 1. Store facts in block memory.
	if len(req.Facts) > 0 {
		mgr, err := s.blockManager()
		if err != nil {
			resp.Errors = append(resp.Errors, "block memory unavailable: "+err.Error())
		} else {
			for _, f := range req.Facts {
				if f.Content == "" {
					resp.Errors = append(resp.Errors, "empty fact content skipped")
					continue
				}
				tier := parseTier(f.Tier)
				label := f.Label
				if label == "" {
					label = truncate(f.Content, 50)
				}
				var block *memory.Block
				var ferr error
				if req.Scope != "" {
					block, ferr = mgr.AddScopedNote(req.Scope, tier, label, f.Content, f.Tags...)
				} else {
					block, ferr = mgr.AddNote(tier, label, f.Content, f.Tags...)
				}
				if ferr != nil {
					resp.Errors = append(resp.Errors, "fact store error: "+ferr.Error())
					continue
				}
				// Apply optional metadata, TTL, and char limit after creation.
				needsUpdate := false
				if len(f.Metadata) > 0 {
					block.Metadata = f.Metadata
					needsUpdate = true
				}
				if f.CharLimit > 0 {
					block.CharLimit = f.CharLimit
					needsUpdate = true
				}
				if f.ExpiresIn != "" {
					if d, derr := parseDuration(f.ExpiresIn); derr == nil {
						exp := time.Now().Add(d)
						block.ExpiresAt = &exp
						needsUpdate = true
					}
				}
				if needsUpdate {
					// Delete old copy first, then re-add with updated fields.
					_ = mgr.Store().Delete(block.ID)
					_ = mgr.Store().Add(block)
				}
				resp.FactsStored++
				resp.FactIDs = append(resp.FactIDs, block.ID)
			}
		}
	}

	// 2. Store relationships in knowledge graph.
	if len(req.Relationships) > 0 {
		for _, rel := range req.Relationships {
			if rel.From == "" || rel.To == "" || rel.Relation == "" {
				resp.Errors = append(resp.Errors, "relationship requires from, to, and relation")
				continue
			}
			indexName := rel.Index
			if indexName == "" {
				indexName = s.firstIndexName()
			}
			if indexName == "" {
				resp.Errors = append(resp.Errors, "no index available for graph relationships")
				break
			}

			if err := s.injectGraphRelationship(r, indexName, rel); err != nil {
				resp.Errors = append(resp.Errors, err.Error())
			} else {
				resp.EdgesCreated++
			}
		}
	}

	status := http.StatusOK
	if resp.FactsStored == 0 && resp.EdgesCreated == 0 {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, resp)
}

// --- Recall ---

// UnifiedRecallRequest queries all memory layers and merges results.
type UnifiedRecallRequest struct {
	Query     string   `json:"query"`
	Scope     string   `json:"scope,omitempty"`     // Restrict block search to scope
	Index     string   `json:"index,omitempty"`     // Index for vector + graph search
	Layers    []string `json:"layers,omitempty"`    // blocks|graph|vector (default: all)
	TopK      int      `json:"top_k,omitempty"`     // Max results per layer (default: 5)
	Depth     int      `json:"depth,omitempty"`     // Graph traversal depth (default: 2)
	Format    string   `json:"format,omitempty"`    // json|context (default: json)
	Tier      string   `json:"tier,omitempty"`      // Filter blocks by tier (short|medium|long)
	Tags      []string `json:"tags,omitempty"`      // Filter blocks by tags (AND logic)
	After     string   `json:"after,omitempty"`     // Filter blocks created after (RFC3339 or duration like "24h")
	Before    string   `json:"before,omitempty"`    // Filter blocks created before (RFC3339 or duration like "7d")
	Relations []string `json:"relations,omitempty"` // Filter graph edges by relation types
	// Project sets scope to "project:{name}" and index to matching name.
	Project string `json:"project,omitempty"`
}

// UnifiedRecallResponse merges results from all memory layers.
type UnifiedRecallResponse struct {
	Query   string        `json:"query"`
	Blocks  []RecallBlock `json:"blocks,omitempty"`
	Graph   *RecallGraph  `json:"graph,omitempty"`
	Vector  []RecallHit   `json:"vector,omitempty"`
	Context string        `json:"context,omitempty"` // Pre-formatted for LLM injection
}

// RecallBlock is a block memory result.
type RecallBlock struct {
	ID        string            `json:"id"`
	Tier      string            `json:"tier"`
	Label     string            `json:"label"`
	Content   string            `json:"content"`
	Tags      []string          `json:"tags,omitempty"`
	Scope     string            `json:"scope,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	Source    string            `json:"source,omitempty"`
}

// RecallGraph is a knowledge graph traversal result.
type RecallGraph struct {
	Nodes []RecallGraphNode `json:"nodes"`
	Edges []RecallGraphEdge `json:"edges"`
}

// RecallGraphNode is a graph node.
type RecallGraphNode struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

// RecallGraphEdge links two graph nodes.
type RecallGraphEdge struct {
	From     string  `json:"from"`
	To       string  `json:"to"`
	Relation string  `json:"relation"`
	Weight   float64 `json:"weight,omitempty"`
}

// RecallHit is a vector search result.
type RecallHit struct {
	Content string  `json:"content"`
	Source  string  `json:"source"`
	Score   float64 `json:"score"`
	ChunkID int     `json:"chunk_id,omitempty"`
}

func (s *Server) handleUnifiedRecall(w http.ResponseWriter, r *http.Request) {
	var req UnifiedRecallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}

	if req.TopK <= 0 {
		req.TopK = 5
	}
	if req.Depth <= 0 {
		req.Depth = 2
	}

	// Resolve project shorthand → scope + index.
	if req.Project != "" {
		if req.Scope == "" {
			req.Scope = "project:" + req.Project
		}
		if req.Index == "" {
			req.Index = req.Project
		}
	}

	layers := layerSet(req.Layers)
	resp := UnifiedRecallResponse{Query: req.Query}

	// Run searches in parallel across layers.
	var wg sync.WaitGroup
	var mu sync.Mutex

	// 1. Block memory search.
	if layers["blocks"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			blocks := s.recallBlocks(req)
			mu.Lock()
			resp.Blocks = blocks
			mu.Unlock()
		}()
	}

	// 2. Knowledge graph traversal.
	if layers["graph"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			graph := s.recallGraph(r.Context(), req)
			mu.Lock()
			resp.Graph = graph
			mu.Unlock()
		}()
	}

	// 3. Vector search.
	if layers["vector"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hits := s.recallVector(r.Context(), req)
			mu.Lock()
			resp.Vector = hits
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Build LLM-ready context if requested.
	if req.Format == "context" {
		resp.Context = buildRecallContext(resp)
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Helpers ---

func (s *Server) recallBlocks(req UnifiedRecallRequest) []RecallBlock {
	mgr, err := s.blockManager()
	if err != nil {
		return nil
	}

	var blocks []memory.Block
	if req.Scope != "" {
		blocks, err = mgr.SearchScoped(req.Scope, req.Query)
	} else {
		blocks, err = mgr.Search(req.Query)
	}
	if err != nil {
		return nil
	}

	// Parse temporal filters.
	var afterTime, beforeTime time.Time
	if req.After != "" {
		afterTime = parseTimeOrDuration(req.After)
	}
	if req.Before != "" {
		beforeTime = parseTimeOrDuration(req.Before)
	}
	tierFilter := strings.ToLower(req.Tier)

	var results []RecallBlock
	for _, b := range blocks {
		if len(results) >= req.TopK {
			break
		}
		// Tier filter.
		if tierFilter != "" && string(b.Tier) != tierFilter {
			continue
		}
		// Tag filter (AND logic: all requested tags must be present).
		if len(req.Tags) > 0 && !containsAllTags(b.Tags, req.Tags) {
			continue
		}
		// Date-range filter.
		if !afterTime.IsZero() && b.CreatedAt.Before(afterTime) {
			continue
		}
		if !beforeTime.IsZero() && b.CreatedAt.After(beforeTime) {
			continue
		}
		results = append(results, RecallBlock{
			ID:        b.ID,
			Tier:      string(b.Tier),
			Label:     b.Label,
			Content:   b.Content,
			Tags:      b.Tags,
			Scope:     b.Scope,
			Metadata:  b.Metadata,
			CreatedAt: b.CreatedAt,
			Source:    b.Source,
		})
	}
	return results
}

// recallGraph is only available with the treesitter build tag.
// The actual implementation is in unified_memory_graph.go (treesitter build)
// and unified_memory_graph_stub.go (!treesitter build).
// We use a method variable so it compiles in both modes.
func (s *Server) recallGraph(_ context.Context, _ UnifiedRecallRequest) *RecallGraph {
	return s.recallGraphImpl()
}

func (s *Server) recallVector(ctx context.Context, req UnifiedRecallRequest) []RecallHit {
	indexName := req.Index
	if indexName == "" {
		indexName = s.firstIndexName()
	}
	if indexName == "" {
		return nil
	}

	searcher, err := s.getSearcher(ctx, indexName)
	if err != nil {
		return nil
	}

	results, err := searcher.Search(ctx, req.Query, gleann.WithTopK(req.TopK))
	if err != nil {
		return nil
	}

	var hits []RecallHit
	for _, r := range results {
		source := ""
		if r.Metadata != nil {
			if s, ok := r.Metadata["source"].(string); ok {
				source = s
			}
		}
		hits = append(hits, RecallHit{
			Content: r.Text,
			Source:  source,
			Score:   float64(r.Score),
		})
	}
	return hits
}

// firstIndexName returns the name of the first available index.
func (s *Server) firstIndexName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for name := range s.searchers {
		return name
	}
	return ""
}

func parseTier(tier string) memory.Tier {
	switch strings.ToLower(tier) {
	case "medium":
		return memory.TierMedium
	case "long":
		return memory.TierLong
	default:
		return memory.TierShort
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func layerSet(layers []string) map[string]bool {
	if len(layers) == 0 {
		return map[string]bool{"blocks": true, "graph": true, "vector": true}
	}
	m := make(map[string]bool, len(layers))
	for _, l := range layers {
		m[strings.ToLower(l)] = true
	}
	return m
}

func buildRecallContext(resp UnifiedRecallResponse) string {
	var b strings.Builder
	b.WriteString("<memory_context>\n")

	if len(resp.Blocks) > 0 {
		b.WriteString("<facts>\n")
		for _, block := range resp.Blocks {
			fmt.Fprintf(&b, "- [%s] (%s) %s\n", block.Tier, block.CreatedAt.Format("2006-01-02"), block.Content)
		}
		b.WriteString("</facts>\n")
	}

	if resp.Graph != nil && len(resp.Graph.Nodes) > 0 {
		b.WriteString("<relationships>\n")
		for _, e := range resp.Graph.Edges {
			fmt.Fprintf(&b, "- %s -[%s]-> %s\n", e.From, e.Relation, e.To)
		}
		b.WriteString("</relationships>\n")
	}

	if len(resp.Vector) > 0 {
		b.WriteString("<relevant_documents>\n")
		for i, hit := range resp.Vector {
			fmt.Fprintf(&b, "--- Document %d (score: %.2f, source: %s) ---\n%s\n",
				i+1, hit.Score, hit.Source, hit.Content)
		}
		b.WriteString("</relevant_documents>\n")
	}

	b.WriteString("</memory_context>")
	return b.String()
}

// parseTimeOrDuration parses an RFC3339 timestamp or a Go duration string
// relative to now. Durations like "24h" or "168h" mean "this long ago".
func parseTimeOrDuration(s string) time.Time {
	// Try RFC3339 first.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	// Try Go duration (relative to now).
	if d, err := parseDuration(s); err == nil {
		return time.Now().Add(-d) // "24h" → 24 hours ago
	}
	return time.Time{}
}

// parseDuration extends time.ParseDuration with day/week shorthand.
func parseDuration(s string) (time.Duration, error) {
	// Handle "Nd" and "Nw" shorthand (not in stdlib).
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		s = strings.TrimSuffix(s, "d") + "h"
		if d, err := time.ParseDuration(s); err == nil {
			return d * 24, nil
		}
	}
	if strings.HasSuffix(s, "w") {
		s = strings.TrimSuffix(s, "w") + "h"
		if d, err := time.ParseDuration(s); err == nil {
			return d * 24 * 7, nil
		}
	}
	return time.ParseDuration(s)
}

// containsAllTags returns true if blockTags contains all required tags.
func containsAllTags(blockTags, required []string) bool {
	tagSet := make(map[string]bool, len(blockTags))
	for _, t := range blockTags {
		tagSet[strings.ToLower(t)] = true
	}
	for _, r := range required {
		if !tagSet[strings.ToLower(r)] {
			return false
		}
	}
	return true
}
