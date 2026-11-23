package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// unifiedServer builds a server with block memory, a memory pool backed by a
// per-test directory, and an index directory holding the given mock indexes.
func unifiedServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("GLEANN_MEMORY_DIR", t.TempDir())
	s := newTestServerWithBlockMem(t)
	s.memoryPool = newMemoryPool(s.config.IndexDir)
	t.Cleanup(s.memoryPool.closeAll)
	return s
}

func unifiedPost(s *Server, h http.HandlerFunc, url, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func decodeIngest(t *testing.T, w *httptest.ResponseRecorder) UnifiedIngestResponse {
	t.Helper()
	var resp UnifiedIngestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return resp
}

func TestUnifiedIngest_ClientTargetConstraint(t *testing.T) {
	s := unifiedServer(t)
	hdr := map[string]string{"X-Gleann-Target": "alpha"}

	w := unifiedPost(s, s.handleUnifiedIngest, "/api/memory/ingest", `{"project":"beta","facts":[{"content":"x"}]}`, hdr)
	if w.Code != http.StatusForbidden {
		t.Errorf("mismatching project: %d %s", w.Code, w.Body.String())
	}

	// With no project in the body the header target becomes the project, so the
	// fact is stored in the "project:alpha" scope; the query-string forms work too.
	for name, tc := range map[string]struct {
		url string
		hdr map[string]string
	}{
		"header":         {"/api/memory/ingest", hdr},
		"x-gleann-index": {"/api/memory/ingest", map[string]string{"X-Gleann-Index": "alpha"}},
		"target query":   {"/api/memory/ingest?target=alpha", nil},
		"index query":    {"/api/memory/ingest?index=alpha", nil},
	} {
		w = unifiedPost(s, s.handleUnifiedIngest, tc.url, `{"facts":[{"content":"scoped via `+name+`"}]}`, tc.hdr)
		if w.Code != http.StatusOK || decodeIngest(t, w).FactsStored != 1 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	blocks, err := s.blockMem.ListScoped("project:alpha", "")
	if err != nil || len(blocks) != 4 {
		t.Errorf("scoped blocks = %d (%v), want 4", len(blocks), err)
	}
}

func TestUnifiedIngest_FactOptionsArePersisted(t *testing.T) {
	s := unifiedServer(t)
	body := `{"facts":[{"content":"long lived","tier":"long","tags":["a"],"metadata":{"k":"v"},"char_limit":500,"expires_in":"1h"},` +
		`{"content":"bad ttl ignored","expires_in":"soon"}]}`
	w := unifiedPost(s, s.handleUnifiedIngest, "/api/memory/ingest", body, nil)
	resp := decodeIngest(t, w)
	if w.Code != http.StatusOK || resp.FactsStored != 2 || len(resp.FactIDs) != 2 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	blocks, err := s.blockMem.Search("long lived")
	if err != nil || len(blocks) != 1 {
		t.Fatalf("search: %v %v", blocks, err)
	}
	b := blocks[0]
	if b.Metadata["k"] != "v" || b.CharLimit != 500 || b.ExpiresAt == nil || time.Until(*b.ExpiresAt) < 30*time.Minute {
		t.Errorf("optional fields not persisted: %+v", b)
	}
}

func TestUnifiedIngest_RelationshipsEndToEnd(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
	createMockIndexMeta(t, s.config.IndexDir, "secret", false, nil)

	body := `{"project":"proj","relationships":[` +
		`{"from":"A","to":"B","relation":"CALLS"},` + // default weight, index defaults to project
		`{"from":"C","to":"D","relation":"USES","weight":2.5,"attributes":{"why":"test"}},` +
		`{"from":"E","relation":"X"},` + // missing field
		`{"from":"F","to":"G","relation":"R","index":"secret"}]}` // private index
	w := unifiedPost(s, s.handleUnifiedIngest, "/api/memory/ingest", body, nil)
	resp := decodeIngest(t, w)
	if w.Code != http.StatusOK || resp.EdgesCreated != 2 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	joined := strings.Join(resp.Errors, "|")
	if !strings.Contains(joined, "requires from, to, and relation") || !strings.Contains(joined, `index "secret" is not accessible`) {
		t.Errorf("errors = %v", resp.Errors)
	}

	// The injected edge is traversable through the memory pool.
	svc, err := s.memoryPool.get("proj")
	if err != nil {
		t.Fatal(err)
	}
	nodes, edges, err := svc.Traverse(t.Context(), "A", 1)
	if err != nil || len(nodes) < 2 || len(edges) != 1 || edges[0].Weight != 1.0 {
		t.Errorf("traverse: nodes=%v edges=%v err=%v", nodes, edges, err)
	}
}

func TestUnifiedIngest_RelationshipWithoutAnyIndex(t *testing.T) {
	s := unifiedServer(t) // empty index dir: nothing accessible
	w := unifiedPost(s, s.handleUnifiedIngest, "/api/memory/ingest",
		`{"relationships":[{"from":"A","to":"B","relation":"R"},{"from":"C","to":"D","relation":"R"}]}`, nil)
	resp := decodeIngest(t, w)
	if w.Code != http.StatusUnprocessableEntity || len(resp.Errors) != 1 ||
		!strings.Contains(resp.Errors[0], "no accessible index") {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
}

func TestUnifiedIngest_RelationshipServiceFailure(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
	s.memoryPool.handles["proj"] = failingMemoryService{}
	w := unifiedPost(s, s.handleUnifiedIngest, "/api/memory/ingest",
		`{"project":"proj","relationships":[{"from":"A","to":"B","relation":"R"}]}`, nil)
	resp := decodeIngest(t, w)
	if w.Code != http.StatusUnprocessableEntity || len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], errMemBoom.Error()) {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
}

func TestInjectGraphRelationship_PoolError(t *testing.T) {
	s := unifiedServer(t)
	err := s.injectGraphRelationship(httptest.NewRequest(http.MethodPost, "/", nil), "bad name!", IngestRelationship{From: "a", To: "b", Relation: "R"})
	if err == nil {
		t.Error("invalid index name must fail")
	}
}

func TestUnifiedIngest_FactStoreFailures(t *testing.T) {
	t.Run("block memory unavailable", func(t *testing.T) {
		s := unifiedServer(t)
		s.blockMem = nil
		blockedMemoryDir(t)
		w := unifiedPost(s, s.handleUnifiedIngest, "/api/memory/ingest", `{"facts":[{"content":"x"}]}`, nil)
		resp := decodeIngest(t, w)
		if w.Code != http.StatusUnprocessableEntity || len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "block memory unavailable") {
			t.Errorf("%d %s", w.Code, w.Body.String())
		}
	})
	t.Run("store closed", func(t *testing.T) {
		s := unifiedServer(t)
		_ = s.blockMem.Close()
		for _, body := range []string{
			// The short tier lives in memory, so only persisted tiers can fail.
			`{"facts":[{"content":"x","tier":"long"}]}`,
			`{"scope":"sc","facts":[{"content":"x","tier":"long"}]}`,
		} {
			w := unifiedPost(s, s.handleUnifiedIngest, "/api/memory/ingest", body, nil)
			resp := decodeIngest(t, w)
			if w.Code != http.StatusUnprocessableEntity || len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "fact store error") {
				t.Errorf("%s: %d %s", body, w.Code, w.Body.String())
			}
		}
	})
}

func TestUnifiedRecall_ClientTargetConstraint(t *testing.T) {
	s := unifiedServer(t)
	hdr := map[string]string{"X-Gleann-Target": "alpha"}
	tests := []struct {
		name, body string
		want       int
	}{
		{"project mismatch", `{"query":"q","project":"beta"}`, http.StatusForbidden},
		{"index mismatch", `{"query":"q","index":"beta"}`, http.StatusForbidden},
		{"target alias mismatch", `{"query":"q","target":"beta"}`, http.StatusForbidden},
		{"matching project (case-insensitive)", `{"query":"q","project":"ALPHA"}`, http.StatusOK},
		{"header becomes project", `{"query":"q"}`, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall", tc.body, hdr)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	for _, url := range []string{"/api/memory/recall?target=alpha", "/api/memory/recall?index=alpha"} {
		if w := unifiedPost(s, s.handleUnifiedRecall, url, `{"query":"q","project":"beta"}`, nil); w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", url, w.Code)
		}
	}
	w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall", `{"query":"q","project":"beta"}`, map[string]string{"X-Gleann-Index": "alpha"})
	if w.Code != http.StatusForbidden {
		t.Errorf("X-Gleann-Index mismatch: %d", w.Code)
	}
}

func TestRecallBlocks_FiltersAndFailures(t *testing.T) {
	s := unifiedServer(t)
	for _, f := range []string{`{"content":"alpha fact one","tier":"long","tags":["x","y"]}`, `{"content":"alpha fact two","tier":"short","tags":["x"]}`} {
		if w := unifiedPost(s, s.handleUnifiedIngest, "/api/memory/ingest", `{"project":"p","facts":[`+f+`]}`, nil); w.Code != 200 {
			t.Fatalf("seed: %d %s", w.Code, w.Body.String())
		}
	}
	tests := []struct {
		name string
		req  UnifiedRecallRequest
		want int
	}{
		{"scoped search", UnifiedRecallRequest{Query: "alpha", Scope: "project:p", TopK: 10}, 2},
		{"other scope sees nothing", UnifiedRecallRequest{Query: "alpha", Scope: "project:z", TopK: 10}, 0},
		{"tier filter", UnifiedRecallRequest{Query: "alpha", Scope: "project:p", TopK: 10, Tier: "LONG"}, 1},
		{"tag AND filter", UnifiedRecallRequest{Query: "alpha", Scope: "project:p", TopK: 10, Tags: []string{"x", "y"}}, 1},
		{"top_k cap", UnifiedRecallRequest{Query: "alpha", Scope: "project:p", TopK: 1}, 1},
		{"created after the future", UnifiedRecallRequest{Query: "alpha", Scope: "project:p", TopK: 10, After: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, 0},
		{"created before the past", UnifiedRecallRequest{Query: "alpha", Scope: "project:p", TopK: 10, Before: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(s.recallBlocks(tc.req)); got != tc.want {
				t.Errorf("recallBlocks = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("closed store yields nothing", func(t *testing.T) {
		_ = s.blockMem.Close()
		if got := s.recallBlocks(UnifiedRecallRequest{Query: "alpha", TopK: 5}); got != nil {
			t.Errorf("got %v", got)
		}
		if got := s.recallBlocks(UnifiedRecallRequest{Query: "alpha", Scope: "project:p", TopK: 5}); got != nil {
			t.Errorf("scoped: got %v", got)
		}
	})
	t.Run("unopenable store yields nothing", func(t *testing.T) {
		s.blockMem = nil
		blockedMemoryDir(t)
		if got := s.recallBlocks(UnifiedRecallRequest{Query: "alpha", TopK: 5}); got != nil {
			t.Errorf("got %v", got)
		}
	})
}

func TestRecallVector_IndexSelection(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "secret", false, nil)
	ctx := t.Context()

	if got := s.recallVector(ctx, UnifiedRecallRequest{Query: "q", Index: "secret", TopK: 3}); got != nil {
		t.Errorf("private index must not be searched: %v", got)
	}
	if got := s.recallVector(ctx, UnifiedRecallRequest{Query: "q", Project: "p", TopK: 3}); got != nil {
		t.Errorf("project without index must not fall back to another index: %v", got)
	}
	if got := s.recallVector(ctx, UnifiedRecallRequest{Query: "q", TopK: 3}); got != nil {
		t.Errorf("no accessible index: %v", got)
	}
	// An accessible but unbuilt index fails to open: no hits, no panic.
	createMockIndexMeta(t, s.config.IndexDir, "open", true, nil)
	if got := s.recallVector(ctx, UnifiedRecallRequest{Query: "q", Index: "open", TopK: 3}); got != nil {
		t.Errorf("unbuilt index: %v", got)
	}
	if got := s.recallVector(ctx, UnifiedRecallRequest{Query: "q", TopK: 3}); got != nil {
		t.Errorf("first accessible, unbuilt: %v", got)
	}
}

func TestRecallGraph_EmptyRequestIsNil(t *testing.T) {
	s := unifiedServer(t)
	// No query and no accessible index: the graph layer stays nil so
	// multi-layer recall answers cleanly. Positive graph-layer behaviour is
	// covered in unified_memory_graph_test.go.
	if s.recallGraph(t.Context(), UnifiedRecallRequest{}) != nil {
		t.Error("empty recall request must yield a nil graph")
	}
}
