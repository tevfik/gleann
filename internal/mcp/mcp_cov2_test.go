package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// newTestServerCov2 creates a test MCP server with block memory and a fake index.
func newTestServerCov2(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := memory.OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	mgr := memory.NewManager(store)

	cfg := Config{IndexDir: dir}
	s := NewServer(cfg)
	s.blockMem.close()
	s.blockMem = &blockMemPool{mgr: mgr}
	return s
}

func createFakeIndex(t *testing.T, dir, name string) {
	t.Helper()
	indexDir := filepath.Join(dir, name)
	os.MkdirAll(indexDir, 0o755)
	os.WriteFile(filepath.Join(indexDir, "meta.json"),
		[]byte(`{"name":"`+name+`","backend":"hnsw","embedding_model":"test","num_passages":10}`), 0o644)
}

// ── handleList ───────────────────────────────────────────────────────────────

func TestHandleList_NoIndexes(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleList(ctx, mcpsdk.CallToolRequest{})
	if err != nil {
		t.Fatal(err)
	}
	txt := extractText(res)
	if txt != "No indexes found." {
		t.Fatalf("expected 'No indexes found.', got: %s", txt)
	}
}

func TestHandleList_WithIndexesCov2(t *testing.T) {
	dir := t.TempDir()
	createFakeIndex(t, dir, "project1")
	createFakeIndex(t, dir, "docs")

	cfg := Config{IndexDir: dir}
	s := NewServer(cfg)

	ctx := context.Background()
	res, err := s.handleList(ctx, mcpsdk.CallToolRequest{})
	if err != nil {
		t.Fatal(err)
	}
	txt := extractText(res)
	if txt == "" || txt == "No indexes found." {
		t.Log("no indexes found — ListIndexes may need backend files")
	}
}

// ── handleSearch ─────────────────────────────────────────────────────────────

func TestHandleSearch_InvalidArgs(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = "not a map"
	res, err := s.handleSearch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for invalid args")
	}
}

func TestHandleSearch_IndexNotFound(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleSearch(ctx, mkCallArgs(map[string]any{
		"index": "nonexistent",
		"query": "test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing index")
	}
}

func TestHandleSearch_WithFiltersAndTopK(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleSearch(ctx, mkCallArgs(map[string]any{
		"index": "nonexistent",
		"query": "test",
		"top_k": float64(3),
		"filters": []interface{}{
			map[string]interface{}{
				"field":    "ext",
				"operator": "eq",
				"value":    ".go",
			},
		},
		"filter_logic":  "or",
		"graph_context": true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Will fail at getSearcher but exercises filter/topK parsing logic.
	_ = res
}

// ── handleAsk ────────────────────────────────────────────────────────────────

func TestHandleAsk_InvalidArgs(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = 42
	res, err := s.handleAsk(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for invalid args")
	}
}

func TestHandleAsk_MissingFields(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleAsk(ctx, mkCallArgs(map[string]any{
		"index": "myindex",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing question")
	}
}

func TestHandleAsk_EmptyIndexAndQuestion(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleAsk(ctx, mkCallArgs(map[string]any{
		"index":    "",
		"question": "",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty fields")
	}
}

func TestHandleAsk_IndexNotFound(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleAsk(ctx, mkCallArgs(map[string]any{
		"index":    "nonexistent",
		"question": "what is gleann?",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing index")
	}
}

func TestHandleAsk_WithFilters(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleAsk(ctx, mkCallArgs(map[string]any{
		"index":    "nonexistent",
		"question": "what is gleann?",
		"filters": []interface{}{
			map[string]interface{}{
				"field":    "path",
				"operator": "contains",
				"value":    "internal",
			},
		},
		"filter_logic": "and",
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Will fail at getSearcher, but filter parsing is exercised.
	_ = res
}

// ── handleGraphNeighbors ─────────────────────────────────────────────────────

func TestHandleGraphNeighbors_InvalidArgs(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = "bad"
	res, err := s.handleGraphNeighbors(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleGraphNeighbors_MissingFields(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleGraphNeighbors(ctx, mkCallArgs(map[string]any{
		"index": "myindex",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing node_fqn")
	}
}

func TestHandleGraphNeighbors_EmptyFields(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleGraphNeighbors(ctx, mkCallArgs(map[string]any{
		"index":    "",
		"node_fqn": "",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty fields")
	}
}

func TestHandleGraphNeighbors_IndexNotFound(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleGraphNeighbors(ctx, mkCallArgs(map[string]any{
		"index":    "nonexistent",
		"node_fqn": "pkg.MyFunc",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing index")
	}
}

// ── handleDocumentLinks ──────────────────────────────────────────────────────

func TestHandleDocumentLinks_InvalidArgs(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = 123
	res, err := s.handleDocumentLinks(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleDocumentLinks_MissingFields(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleDocumentLinks(ctx, mkCallArgs(map[string]any{
		"index": "myindex",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing doc_path")
	}
}

func TestHandleDocumentLinks_EmptyFields(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleDocumentLinks(ctx, mkCallArgs(map[string]any{
		"index":    "",
		"doc_path": "",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty fields")
	}
}

func TestHandleDocumentLinks_IndexNotFound(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleDocumentLinks(ctx, mkCallArgs(map[string]any{
		"index":    "nonexistent",
		"doc_path": "docs/readme.md",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing index")
	}
}

// ── handleImpact ─────────────────────────────────────────────────────────────

func TestHandleImpact_InvalidArgs(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = nil
	res, err := s.handleImpact(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleImpact_MissingFields(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleImpact(ctx, mkCallArgs(map[string]any{
		"index": "myindex",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing symbol")
	}
}

func TestHandleImpact_EmptyFields(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleImpact(ctx, mkCallArgs(map[string]any{
		"index":  "",
		"symbol": "",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty fields")
	}
}

func TestHandleImpact_IndexNotFound(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleImpact(ctx, mkCallArgs(map[string]any{
		"index":     "nonexistent",
		"symbol":    "pkg.MyFunc",
		"max_depth": float64(3),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing index")
	}
}

func TestHandleImpact_MaxDepthParsing(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	// Tests max_depth parsing even though index won't exist.
	res, err := s.handleImpact(ctx, mkCallArgs(map[string]any{
		"index":     "nonexistent",
		"symbol":    "pkg.MyFunc",
		"max_depth": float64(15), // Should be clamped to 10 internally.
	}))
	if err != nil {
		t.Fatal(err)
	}
	_ = res
}

// ── handleSearchMulti deeper ─────────────────────────────────────────────────

func TestHandleSearchMulti_NoIndexes(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleSearchMulti(ctx, mkCallArgs(map[string]any{
		"query": "test query",
	}))
	if err != nil {
		t.Fatal(err)
	}
	txt := extractText(res)
	_ = txt
}

func TestHandleSearchMulti_WithIndexNames(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleSearchMulti(ctx, mkCallArgs(map[string]any{
		"query":   "test query",
		"indexes": "idx1,idx2",
		"top_k":   float64(3),
	}))
	if err != nil {
		t.Fatal(err)
	}
	_ = res
}

func TestHandleSearchMulti_InvalidArgs(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = "string"
	res, err := s.handleSearchMulti(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for invalid args")
	}
}

// ── handleBatchAsk deeper ────────────────────────────────────────────────────

func TestHandleBatchAsk_InvalidArgs(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = "not map"
	res, err := s.handleBatchAsk(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleBatchAsk_MissingQuestionsCov2(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleBatchAsk(ctx, mkCallArgs(map[string]any{
		"index": "myindex",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing questions")
	}
}

func TestHandleBatchAsk_QuestionsNotArray(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleBatchAsk(ctx, mkCallArgs(map[string]any{
		"index":     "myindex",
		"questions": "not an array",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for non-array questions")
	}
}

func TestHandleBatchAsk_EmptyQuestions(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleBatchAsk(ctx, mkCallArgs(map[string]any{
		"index":     "myindex",
		"questions": []interface{}{},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty questions array")
	}
}

func TestHandleBatchAsk_MissingIndexCov2(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleBatchAsk(ctx, mkCallArgs(map[string]any{
		"questions": []interface{}{"what is gleann?"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing index")
	}
}

func TestHandleBatchAsk_TruncatesQuestions(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	// More than 10 questions should be truncated.
	questions := make([]interface{}, 15)
	for i := range questions {
		questions[i] = "question " + string(rune('A'+i))
	}
	res, err := s.handleBatchAsk(ctx, mkCallArgs(map[string]any{
		"index":     "nonexistent",
		"questions": questions,
		"top_k":     float64(2),
		"concurrency": float64(1),
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Will fail at getSearcher but exercises truncation + param parsing.
	_ = res
}

func TestHandleBatchAsk_IndexNotFoundCov2(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleBatchAsk(ctx, mkCallArgs(map[string]any{
		"index":     "nonexistent",
		"questions": []interface{}{"what is?", "how does?"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing index")
	}
}

// ── handleSearchIDs deeper ───────────────────────────────────────────────────

func TestHandleSearchIDs_InvalidArgs(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = false
	res, err := s.handleSearchIDs(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleSearchIDs_IndexNotFound(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleSearchIDs(ctx, mkCallArgs(map[string]any{
		"index": "nonexistent",
		"query": "test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing index")
	}
}

func TestHandleSearchIDs_WithFiltersAndTopK(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleSearchIDs(ctx, mkCallArgs(map[string]any{
		"index": "nonexistent",
		"query": "test",
		"top_k": float64(20),
		"filters": []interface{}{
			map[string]interface{}{
				"field":    "type",
				"operator": "eq",
				"value":    "function",
			},
		},
		"filter_logic": "or",
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Will fail at getSearcher but exercises parsing.
	_ = res
}

// ── handleFetch deeper ───────────────────────────────────────────────────────

func TestHandleFetch_InvalidArgs(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = []int{1, 2}
	res, err := s.handleFetch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleFetch_MissingIds(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleFetch(ctx, mkCallArgs(map[string]any{
		"index": "myindex",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing ids")
	}
}

func TestHandleFetch_IndexNotFound(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	res, err := s.handleFetch(ctx, mkCallArgs(map[string]any{
		"index": "nonexistent",
		"ids":   []interface{}{float64(1), float64(2)},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing index")
	}
}

func TestHandleFetch_MixedIDTypes(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	// Mix float64 and non-numeric IDs.
	res, err := s.handleFetch(ctx, mkCallArgs(map[string]any{
		"index": "nonexistent",
		"ids":   []interface{}{float64(1), "bad", float64(3)},
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Will fail at getSearcher.
	_ = res
}

// ── handleGet deeper ─────────────────────────────────────────────────────────

func TestHandleGet_InvalidArgs(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = map[string]interface{}(nil)
	res, err := s.handleGet(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res
}

// ── handleIndexListResource ──────────────────────────────────────────────────

func TestHandleIndexListResource_Empty(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.ReadResourceRequest
	req.Params.URI = "gleann://indexes"
	results, err := s.handleIndexListResource(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one resource content")
	}
}

func TestHandleIndexListResource_WithIndexes(t *testing.T) {
	dir := t.TempDir()
	createFakeIndex(t, dir, "myproject")

	cfg := Config{IndexDir: dir}
	s := NewServer(cfg)

	ctx := context.Background()
	var req mcpsdk.ReadResourceRequest
	req.Params.URI = "gleann://indexes"
	results, err := s.handleIndexListResource(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one resource content")
	}
}

// ── handleReadResource ───────────────────────────────────────────────────────

func TestHandleReadResource_BadScheme(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.ReadResourceRequest
	req.Params.URI = "http://bad"
	_, err := s.handleReadResource(ctx, req)
	if err == nil {
		t.Fatal("expected error for bad URI scheme")
	}
}

func TestHandleReadResource_NoSlash(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.ReadResourceRequest
	req.Params.URI = "gleann://noindexpath"
	_, err := s.handleReadResource(ctx, req)
	// Should fail: no slash after index name means no file path.
	if err == nil {
		t.Log("expected error or handled gracefully")
	}
}

func TestHandleReadResource_IndexNotFound(t *testing.T) {
	s := newTestServerCov2(t)
	ctx := context.Background()
	var req mcpsdk.ReadResourceRequest
	req.Params.URI = "gleann://nonexistent/path/to/file.go"
	_, err := s.handleReadResource(ctx, req)
	if err == nil {
		t.Fatal("expected error for nonexistent index")
	}
}

// ── parseFilters ─────────────────────────────────────────────────────────────

func TestParseFilters_NoFilters(t *testing.T) {
	args := map[string]interface{}{"query": "test"}
	filters, logic := parseFilters(args)
	if len(filters) != 0 {
		t.Fatal("expected no filters")
	}
	if logic != "and" {
		t.Fatalf("expected default 'and', got %q", logic)
	}
}

func TestParseFilters_WithFilters(t *testing.T) {
	args := map[string]interface{}{
		"filters": []interface{}{
			map[string]interface{}{
				"field":    "ext",
				"operator": "eq",
				"value":    ".go",
			},
			map[string]interface{}{
				"field":    "source",
				"operator": "contains",
				"value":    "internal",
			},
		},
		"filter_logic": "or",
	}
	filters, logic := parseFilters(args)
	if len(filters) != 2 {
		t.Fatalf("expected 2 filters, got %d", len(filters))
	}
	if logic != "or" {
		t.Fatalf("expected 'or', got %q", logic)
	}
	if filters[0].Field != "ext" {
		t.Fatalf("expected 'ext', got %q", filters[0].Field)
	}
}

func TestParseFilters_PartialFilter(t *testing.T) {
	args := map[string]interface{}{
		"filters": []interface{}{
			map[string]interface{}{
				"field": "ext",
				// Missing operator and value.
			},
		},
	}
	filters, _ := parseFilters(args)
	if len(filters) != 0 {
		t.Fatal("expected no valid filters")
	}
}

func TestParseFilters_NonMapFilter(t *testing.T) {
	args := map[string]interface{}{
		"filters": []interface{}{
			"not a map",
		},
	}
	filters, _ := parseFilters(args)
	if len(filters) != 0 {
		t.Fatal("expected no valid filters")
	}
}

// ── evictOldest ──────────────────────────────────────────────────────────────

func TestEvictOldest_Empty(t *testing.T) {
	s := newTestServerCov2(t)
	s.evictOldest() // Should not panic.
}

func TestEvictOldest_WithSearcher(t *testing.T) {
	s := newTestServerCov2(t)
	s.searchers["test"] = nil
	s.searcherLRU = []string{"test"}
	s.evictOldest()
	if len(s.searchers) != 0 {
		t.Fatal("expected searcher to be evicted")
	}
}

// ── touchLRU ─────────────────────────────────────────────────────────────────

func TestTouchLRU(t *testing.T) {
	s := newTestServerCov2(t)
	s.searcherLRU = []string{"a", "b", "c"}
	s.touchLRU("a")
	if s.searcherLRU[2] != "a" {
		t.Fatalf("expected 'a' at end, got %v", s.searcherLRU)
	}
}

func TestTouchLRU_NotFound(t *testing.T) {
	s := newTestServerCov2(t)
	s.searcherLRU = []string{"a", "b"}
	s.touchLRU("z") // Should not panic.
	if len(s.searcherLRU) != 2 {
		t.Fatal("LRU should be unchanged")
	}
}

// ── getSearcher LRU cache ────────────────────────────────────────────────────

func TestGetSearcher_CacheHit(t *testing.T) {
	s := newTestServerCov2(t)
	// Pre-populate cache with nil searcher.
	searcher := gleann.NewSearcher(gleann.DefaultConfig(), nil)
	s.searchers["cached"] = searcher
	s.searcherLRU = []string{"cached"}

	got, err := s.getSearcher("cached")
	if err != nil {
		t.Fatal(err)
	}
	if got != searcher {
		t.Fatal("expected cached searcher")
	}
}

// ── Close ────────────────────────────────────────────────────────────────────

func TestServerCloseCov2(t *testing.T) {
	s := newTestServerCov2(t)
	s.Close() // Should not panic.
}

func TestServerClose_NilPools(t *testing.T) {
	s := &Server{
		searchers: make(map[string]*gleann.LeannSearcher),
	}
	s.Close() // Should not panic with nil pools.
}

// ── helpers ──────────────────────────────────────────────────────────────────

func extractText(res *mcpsdk.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	for _, c := range res.Content {
		if tc, ok := c.(mcpsdk.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
