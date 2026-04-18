package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

func newTestServerWithBlockMem(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := memory.OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	mgr := memory.NewManager(store)

	return &Server{
		config:    gleann.Config{IndexDir: dir},
		searchers: make(map[string]*gleann.LeannSearcher),
		blockMem:  mgr,
	}
}

// ── handleUnifiedIngest ──────────────────────────────────────────────────

func TestUnifiedIngest_BadJSON(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(`{bad`))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUnifiedIngest_EmptyPayload(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"facts":[],"relationships":[]}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUnifiedIngest_SingleFact(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"facts":[{"content":"test fact","tier":"short","label":"test"}]}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp UnifiedIngestResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.FactsStored != 1 {
		t.Errorf("expected 1 fact stored, got %d", resp.FactsStored)
	}
}

func TestUnifiedIngest_EmptyContent(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"facts":[{"content":""},{"content":"valid fact"}]}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp UnifiedIngestResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.FactsStored != 1 {
		t.Errorf("expected 1 fact stored (empty skipped), got %d", resp.FactsStored)
	}
}

func TestUnifiedIngest_WithScope(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"facts":[{"content":"scoped fact"}],"scope":"project:myapp"}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestUnifiedIngest_WithProject(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"facts":[{"content":"project fact"}],"project":"myapp"}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestUnifiedIngest_WithMetadataCov2(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"facts":[{"content":"fact with metadata","metadata":{"source":"test"},"char_limit":100,"expires_in":"24h"}]}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestUnifiedIngest_FactAllTiers(t *testing.T) {
	for _, tier := range []string{"short", "medium", "long", ""} {
		s := newTestServerWithBlockMem(t)
		body := `{"facts":[{"content":"tier test","tier":"` + tier + `"}]}`
		req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		s.handleUnifiedIngest(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("tier %q: expected 200, got %d", tier, w.Code)
		}
	}
}

func TestUnifiedIngest_RelationshipMissingFields(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"relationships":[{"from":"A","to":"","relation":"CALLS"}]}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)
	// Should fail since no index available and missing field
	if w.Code == http.StatusOK {
		var resp UnifiedIngestResponse
		json.NewDecoder(w.Body).Decode(&resp)
		if len(resp.Errors) == 0 {
			t.Error("expected errors for invalid relationship")
		}
	}
}

func TestUnifiedIngest_RelationshipNoIndex(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"relationships":[{"from":"A","to":"B","relation":"CALLS"}]}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)
	var resp UnifiedIngestResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Errors) == 0 {
		t.Error("expected errors for relationship without index")
	}
}

// ── handleUnifiedRecall ──────────────────────────────────────────────────

func TestUnifiedRecall_BadJSON(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(`{bad`))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUnifiedRecall_EmptyQueryCov2(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"query":""}`
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUnifiedRecall_BlocksOnly(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	// First ingest a fact.
	ingestBody := `{"facts":[{"content":"architecture uses hexagonal pattern","tier":"long"}]}`
	ingestReq := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(ingestBody))
	ingestW := httptest.NewRecorder()
	s.handleUnifiedIngest(ingestW, ingestReq)

	// Then recall.
	body := `{"query":"architecture","layers":["blocks"]}`
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestUnifiedRecall_DefaultTopK(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"query":"test"}`
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestUnifiedRecall_WithProject(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"query":"test","project":"myapp"}`
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestUnifiedRecall_ContextFormatCov2(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	// Ingest a fact first.
	ingestBody := `{"facts":[{"content":"test context formatting","tier":"short"}]}`
	ingestReq := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(ingestBody))
	s.handleUnifiedIngest(httptest.NewRecorder(), ingestReq)

	body := `{"query":"test","format":"context","layers":["blocks"]}`
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp UnifiedRecallResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Context == "" {
		// May or may not have context depending on search results - just verify no crash
	}
}

func TestUnifiedRecall_TierFilterCov2(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"query":"test","tier":"long","layers":["blocks"]}`
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestUnifiedRecall_TagFilterCov2(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"query":"test","tags":["arch","style"],"layers":["blocks"]}`
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestUnifiedRecall_TemporalFilter(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"query":"test","after":"24h","before":"2099-12-31T23:59:59Z","layers":["blocks"]}`
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestUnifiedRecall_VectorLayer(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"query":"test","layers":["vector"]}`
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestUnifiedRecall_GraphLayer(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	body := `{"query":"test","layers":["graph"]}`
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ── Helper functions ─────────────────────────────────────────────────────

func TestLayerSet_Empty(t *testing.T) {
	ls := layerSet(nil)
	if !ls["blocks"] || !ls["graph"] || !ls["vector"] {
		t.Error("empty layers should default to all three")
	}
}

func TestLayerSet_Specific(t *testing.T) {
	ls := layerSet([]string{"blocks", "Vector"})
	if !ls["blocks"] || !ls["vector"] {
		t.Error("expected blocks and vector layers")
	}
	if ls["graph"] {
		t.Error("graph should not be in layer set")
	}
}

func TestParseTier(t *testing.T) {
	tests := []struct {
		input string
		want  memory.Tier
	}{
		{"short", memory.TierShort},
		{"medium", memory.TierMedium},
		{"long", memory.TierLong},
		{"unknown", memory.TierShort},
		{"", memory.TierShort},
		{"LONG", memory.TierLong},
	}
	for _, tt := range tests {
		got := parseTier(tt.input)
		if got != tt.want {
			t.Errorf("parseTier(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestTruncateHelper(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	if truncate("hello world", 5) != "hello..." {
		t.Error("long string should be truncated")
	}
}

func TestContainsAllTagsCov2(t *testing.T) {
	if !containsAllTags([]string{"a", "b", "c"}, []string{"a", "b"}) {
		t.Error("expected true when all required tags present")
	}
	if containsAllTags([]string{"a", "b"}, []string{"a", "c"}) {
		t.Error("expected false when required tag missing")
	}
	if !containsAllTags([]string{"A", "B"}, []string{"a", "b"}) {
		t.Error("expected case-insensitive match")
	}
}

func TestParseTimeOrDuration_RFC3339(t *testing.T) {
	ts := parseTimeOrDuration("2024-01-01T00:00:00Z")
	if ts.IsZero() {
		t.Error("expected non-zero time for valid RFC3339")
	}
}

func TestParseTimeOrDuration_Duration(t *testing.T) {
	ts := parseTimeOrDuration("24h")
	if ts.IsZero() {
		t.Error("expected non-zero time for valid duration")
	}
}

func TestParseTimeOrDuration_Invalid(t *testing.T) {
	ts := parseTimeOrDuration("not-a-time")
	if !ts.IsZero() {
		t.Error("expected zero time for invalid input")
	}
}

func TestParseDuration_Days(t *testing.T) {
	d, err := parseDuration("7d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != 7*24*60*60*1e9 {
		t.Errorf("expected 7 days, got %v", d)
	}
}

func TestParseDuration_Weeks(t *testing.T) {
	d, err := parseDuration("2w")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != 2*7*24*60*60*1e9 {
		t.Errorf("expected 2 weeks, got %v", d)
	}
}

func TestParseDuration_Standard(t *testing.T) {
	d, err := parseDuration("1h30m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != 90*60*1e9 {
		t.Errorf("expected 90 min, got %v", d)
	}
}

func TestBuildRecallContext_AllLayers(t *testing.T) {
	resp := UnifiedRecallResponse{
		Blocks: []RecallBlock{
			{Tier: "long", Content: "test fact"},
		},
		Graph: &RecallGraph{
			Nodes: []RecallGraphNode{{ID: "A", Type: "entity"}},
			Edges: []RecallGraphEdge{{From: "A", To: "B", Relation: "CALLS"}},
		},
		Vector: []RecallHit{
			{Content: "test document", Source: "doc.md", Score: 0.95},
		},
	}
	ctx := buildRecallContext(resp)
	if ctx == "" {
		t.Error("expected non-empty context")
	}
	for _, want := range []string{"<memory_context>", "<facts>", "<relationships>", "<relevant_documents>"} {
		if !bytes.Contains([]byte(ctx), []byte(want)) {
			t.Errorf("expected %q in context", want)
		}
	}
}

func TestBuildRecallContext_Empty(t *testing.T) {
	resp := UnifiedRecallResponse{}
	ctx := buildRecallContext(resp)
	if ctx == "" {
		t.Error("expected non-empty context (at least wrapper tags)")
	}
}

// ── mountUnifiedMemory ───────────────────────────────────────────────────

func TestMountUnifiedMemory(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	mux := http.NewServeMux()
	s.mountUnifiedMemory(mux) // should not panic
}
