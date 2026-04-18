package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// ── parseTier ──────────────────────────────────────────────────

func TestParseTierShort(t *testing.T) {
	if tier := parseTier("short"); tier != "short" {
		t.Errorf("got %q, want short", tier)
	}
}

func TestParseTierMedium(t *testing.T) {
	if tier := parseTier("medium"); tier != "medium" {
		t.Errorf("got %q, want medium", tier)
	}
}

func TestParseTierLong(t *testing.T) {
	if tier := parseTier("long"); tier != "long" {
		t.Errorf("got %q, want long", tier)
	}
}

func TestParseTierDefault(t *testing.T) {
	if tier := parseTier(""); tier != "short" {
		t.Errorf("got %q, want short (default)", tier)
	}
}

func TestParseTierCaseInsensitive(t *testing.T) {
	if tier := parseTier("MEDIUM"); tier != "medium" {
		t.Errorf("got %q, want medium", tier)
	}
}

func TestParseTierUnknown(t *testing.T) {
	if tier := parseTier("unknown"); tier != "short" {
		t.Errorf("got %q, want short (fallback)", tier)
	}
}

// ── truncate ───────────────────────────────────────────────────

func TestTruncateShortUMH(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateLongUMH(t *testing.T) {
	got := truncate("hello world this is long", 10)
	if got != "hello worl..." {
		t.Errorf("got %q", got)
	}
}

func TestTruncateExactUMH(t *testing.T) {
	got := truncate("exact", 5)
	if got != "exact" {
		t.Errorf("got %q", got)
	}
}

// ── layerSet ───────────────────────────────────────────────────

func TestLayerSetEmpty(t *testing.T) {
	m := layerSet(nil)
	if !m["blocks"] || !m["graph"] || !m["vector"] {
		t.Error("empty layers should enable all")
	}
}

func TestLayerSetSpecific(t *testing.T) {
	m := layerSet([]string{"blocks", "vector"})
	if !m["blocks"] || m["graph"] || !m["vector"] {
		t.Error("should only have blocks and vector")
	}
}

func TestLayerSetCaseInsensitive(t *testing.T) {
	m := layerSet([]string{"BLOCKS", "Graph"})
	if !m["blocks"] || !m["graph"] {
		t.Error("should be case insensitive")
	}
}

// ── parseDuration ──────────────────────────────────────────────

func TestParseDurationHours(t *testing.T) {
	d, err := parseDuration("24h")
	if err != nil {
		t.Fatal(err)
	}
	if d != 24*time.Hour {
		t.Errorf("got %v", d)
	}
}

func TestParseDurationDays(t *testing.T) {
	d, err := parseDuration("7d")
	if err != nil {
		t.Fatal(err)
	}
	if d != 7*24*time.Hour {
		t.Errorf("got %v, want 7 days", d)
	}
}

func TestParseDurationWeeks(t *testing.T) {
	d, err := parseDuration("2w")
	if err != nil {
		t.Fatal(err)
	}
	if d != 14*24*time.Hour {
		t.Errorf("got %v, want 14 days", d)
	}
}

func TestParseDurationMinutes(t *testing.T) {
	d, err := parseDuration("30m")
	if err != nil {
		t.Fatal(err)
	}
	if d != 30*time.Minute {
		t.Errorf("got %v", d)
	}
}

func TestParseDurationInvalid(t *testing.T) {
	_, err := parseDuration("invalid")
	if err == nil {
		t.Error("should fail on invalid")
	}
}

// ── parseTimeOrDuration ────────────────────────────────────────

func TestParseTimeOrDurationRFC3339(t *testing.T) {
	ts := "2024-01-15T10:30:00Z"
	got := parseTimeOrDuration(ts)
	if got.Year() != 2024 || got.Month() != 1 || got.Day() != 15 {
		t.Errorf("got %v", got)
	}
}

func TestParseTimeOrDurationGoRelative(t *testing.T) {
	before := time.Now().Add(-25 * time.Hour)
	got := parseTimeOrDuration("24h")
	// Should be approximately 24h ago
	if got.Before(before) || got.After(time.Now()) {
		t.Errorf("got %v, expected ~24h ago", got)
	}
}

func TestParseTimeOrDurationInvalid(t *testing.T) {
	got := parseTimeOrDuration("not-a-time")
	if !got.IsZero() {
		t.Error("should return zero time for invalid input")
	}
}

// ── containsAllTags ────────────────────────────────────────────

func TestContainsAllTagsMatch(t *testing.T) {
	if !containsAllTags([]string{"go", "test", "api"}, []string{"go", "test"}) {
		t.Error("should match")
	}
}

func TestContainsAllTagsMissing(t *testing.T) {
	if containsAllTags([]string{"go", "test"}, []string{"go", "missing"}) {
		t.Error("should not match with missing tag")
	}
}

func TestContainsAllTagsEmpty(t *testing.T) {
	if !containsAllTags([]string{"go"}, nil) {
		t.Error("empty required should always match")
	}
}

func TestContainsAllTagsCaseInsensitive(t *testing.T) {
	if !containsAllTags([]string{"Go", "TEST"}, []string{"go", "test"}) {
		t.Error("should be case insensitive")
	}
}

// ── buildRecallContext ─────────────────────────────────────────

func TestBuildRecallContextEmpty(t *testing.T) {
	got := buildRecallContext(UnifiedRecallResponse{Query: "test"})
	if !strings.Contains(got, "<memory_context>") || !strings.Contains(got, "</memory_context>") {
		t.Error("should have memory_context tags")
	}
}

func TestBuildRecallContextWithBlocks(t *testing.T) {
	resp := UnifiedRecallResponse{
		Query: "test",
		Blocks: []RecallBlock{
			{Tier: "long", Content: "fact1", CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		},
	}
	got := buildRecallContext(resp)
	if !strings.Contains(got, "<facts>") || !strings.Contains(got, "fact1") {
		t.Errorf("got %q", got)
	}
}

func TestBuildRecallContextWithGraph(t *testing.T) {
	resp := UnifiedRecallResponse{
		Query: "test",
		Graph: &RecallGraph{
			Nodes: []RecallGraphNode{{ID: "A", Type: "entity", Content: "Content A"}},
			Edges: []RecallGraphEdge{{From: "A", To: "B", Relation: "DEPENDS_ON"}},
		},
	}
	got := buildRecallContext(resp)
	if !strings.Contains(got, "<relationships>") || !strings.Contains(got, "DEPENDS_ON") {
		t.Errorf("got %q", got)
	}
}

func TestBuildRecallContextWithVector(t *testing.T) {
	resp := UnifiedRecallResponse{
		Query: "test",
		Vector: []RecallHit{
			{Content: "code snippet", Source: "file.go", Score: 0.95},
		},
	}
	got := buildRecallContext(resp)
	if !strings.Contains(got, "<relevant_documents>") || !strings.Contains(got, "code snippet") {
		t.Errorf("got %q", got)
	}
}

func TestBuildRecallContextAllLayers(t *testing.T) {
	resp := UnifiedRecallResponse{
		Query: "test",
		Blocks: []RecallBlock{
			{Tier: "short", Content: "note", CreatedAt: time.Now()},
		},
		Graph: &RecallGraph{
			Nodes: []RecallGraphNode{{ID: "X"}},
			Edges: []RecallGraphEdge{{From: "X", To: "Y", Relation: "CALLS"}},
		},
		Vector: []RecallHit{
			{Content: "result", Score: 0.8, Source: "src.go"},
		},
	}
	got := buildRecallContext(resp)
	if !strings.Contains(got, "<facts>") || !strings.Contains(got, "<relationships>") || !strings.Contains(got, "<relevant_documents>") {
		t.Error("should contain all sections")
	}
}

// ── handleUnifiedIngest ────────────────────────────────────────

func TestHandleUnifiedIngestBadJSON(t *testing.T) {
	s := &Server{config: gleann.Config{IndexDir: t.TempDir()}, searchers: make(map[string]*gleann.LeannSearcher)}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/memory/ingest", strings.NewReader("{bad json"))
	s.handleUnifiedIngest(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleUnifiedIngestEmpty(t *testing.T) {
	s := &Server{config: gleann.Config{IndexDir: t.TempDir()}, searchers: make(map[string]*gleann.LeannSearcher)}
	body, _ := json.Marshal(UnifiedIngestRequest{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewReader(body))
	s.handleUnifiedIngest(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleUnifiedIngestRelationshipMissingFields(t *testing.T) {
	s := &Server{config: gleann.Config{IndexDir: t.TempDir()}, searchers: make(map[string]*gleann.LeannSearcher)}
	body, _ := json.Marshal(UnifiedIngestRequest{
		Relationships: []IngestRelationship{
			{From: "", To: "B", Relation: "REL"},
		},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewReader(body))
	s.handleUnifiedIngest(w, r)
	// Should go through but with errors in response
	var resp UnifiedIngestResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Errors) == 0 {
		t.Error("should report errors")
	}
}

// ── handleUnifiedRecall ────────────────────────────────────────

func TestHandleUnifiedRecallBadJSON(t *testing.T) {
	s := &Server{config: gleann.Config{IndexDir: t.TempDir()}, searchers: make(map[string]*gleann.LeannSearcher)}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/memory/recall", strings.NewReader("not json"))
	s.handleUnifiedRecall(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleUnifiedRecallMissingQuery(t *testing.T) {
	s := &Server{config: gleann.Config{IndexDir: t.TempDir()}, searchers: make(map[string]*gleann.LeannSearcher)}
	body, _ := json.Marshal(UnifiedRecallRequest{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewReader(body))
	s.handleUnifiedRecall(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleUnifiedRecallWithProject(t *testing.T) {
	s := &Server{config: gleann.Config{IndexDir: t.TempDir()}, searchers: make(map[string]*gleann.LeannSearcher)}
	body, _ := json.Marshal(UnifiedRecallRequest{
		Query:   "test",
		Project: "myproj",
		Layers:  []string{"blocks"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewReader(body))
	s.handleUnifiedRecall(w, r)
	// Should succeed (even if no block memory)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHandleUnifiedRecallContextFormat(t *testing.T) {
	s := &Server{config: gleann.Config{IndexDir: t.TempDir()}, searchers: make(map[string]*gleann.LeannSearcher)}
	body, _ := json.Marshal(UnifiedRecallRequest{
		Query:  "test",
		Format: "context",
		Layers: []string{"blocks"},
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewReader(body))
	s.handleUnifiedRecall(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp UnifiedRecallResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.Contains(resp.Context, "<memory_context>") {
		t.Error("context format should include memory_context")
	}
}
