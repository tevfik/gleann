package gleann

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.IndexDir != "." {
		t.Errorf("IndexDir = %q, want %q", cfg.IndexDir, ".")
	}
	if cfg.EmbeddingModel != "bge-m3" {
		t.Errorf("EmbeddingModel = %q, want %q", cfg.EmbeddingModel, "bge-m3")
	}
	if cfg.EmbeddingProvider != "ollama" {
		t.Errorf("EmbeddingProvider = %q, want %q", cfg.EmbeddingProvider, "ollama")
	}
	if cfg.OllamaHost != DefaultOllamaHost {
		t.Errorf("OllamaHost = %q, want %q", cfg.OllamaHost, DefaultOllamaHost)
	}
	if cfg.HNSWConfig.M != 32 {
		t.Errorf("HNSW.M = %d, want 32", cfg.HNSWConfig.M)
	}
	if cfg.HNSWConfig.EfConstruction != 200 {
		t.Errorf("HNSW.EfConstruction = %d, want 200", cfg.HNSWConfig.EfConstruction)
	}
	if cfg.HNSWConfig.EfSearch != 128 {
		t.Errorf("HNSW.EfSearch = %d, want 128", cfg.HNSWConfig.EfSearch)
	}
	if !cfg.HNSWConfig.UseMmap {
		t.Error("HNSW.UseMmap should be true")
	}
	if !cfg.HNSWConfig.UseHeuristic {
		t.Error("HNSW.UseHeuristic should be true")
	}
	if !cfg.HNSWConfig.PruneEmbeddings {
		t.Error("HNSW.PruneEmbeddings should be true")
	}
	if cfg.SearchConfig.TopK != 10 {
		t.Errorf("SearchConfig.TopK = %d, want 10", cfg.SearchConfig.TopK)
	}
	if cfg.SearchConfig.HybridAlpha != 0.7 {
		t.Errorf("SearchConfig.HybridAlpha = %f, want 0.7", cfg.SearchConfig.HybridAlpha)
	}
}

func TestDefaultChunkConfig(t *testing.T) {
	cc := DefaultChunkConfig()
	if cc.ChunkSize != 512 {
		t.Errorf("ChunkSize = %d, want 512", cc.ChunkSize)
	}
	if cc.ChunkOverlap != 50 {
		t.Errorf("ChunkOverlap = %d, want 50", cc.ChunkOverlap)
	}
}

func TestAttributesToJSON(t *testing.T) {
	tests := []struct {
		name    string
		attrs   map[string]any
		want    string
		wantErr bool
	}{
		{"nil map", nil, "{}", false},
		{"empty map", map[string]any{}, "{}", false},
		{"single key", map[string]any{"key": "value"}, `{"key":"value"}`, false},
		{"nested", map[string]any{"a": 1, "b": map[string]any{"c": true}}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AttributesToJSON(tt.attrs)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.want != "" && got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if tt.want == "" && tt.attrs != nil && len(tt.attrs) > 0 {
				// Verify it's valid JSON
				var m map[string]any
				if json.Unmarshal([]byte(got), &m) != nil {
					t.Errorf("invalid JSON: %q", got)
				}
			}
		})
	}
}

func TestJSONToAttributes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
		wantErr bool
	}{
		{"empty string", "", true, false},
		{"empty object", "{}", true, false},
		{"single key", `{"key":"value"}`, false, false},
		{"invalid JSON", `{broken`, false, true},
		{"nested", `{"a":1,"b":{"c":true}}`, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := JSONToAttributes(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantNil && got != nil {
				t.Errorf("expected nil, got %v", got)
			}
			if !tt.wantNil && !tt.wantErr && got == nil {
				t.Error("expected non-nil map")
			}
		})
	}
}

func TestAttributesRoundTripExtended(t *testing.T) {
	original := map[string]any{
		"name":    "test",
		"count":   float64(42),
		"enabled": true,
	}

	jsonStr, err := AttributesToJSON(original)
	if err != nil {
		t.Fatal(err)
	}

	restored, err := JSONToAttributes(jsonStr)
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range original {
		if restored[k] != v {
			t.Errorf("key %q: got %v, want %v", k, restored[k], v)
		}
	}
}

func TestIndexMetaMarshalJSON(t *testing.T) {
	now := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	meta := IndexMeta{
		Name:           "test-index",
		Backend:        "hnsw",
		EmbeddingModel: "bge-m3",
		Dimensions:     1024,
		NumPassages:    500,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        "1.0.0",
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}

	if m["name"] != "test-index" {
		t.Errorf("name = %v, want test-index", m["name"])
	}
	if m["created_at"] != "2024-01-15T10:30:00Z" {
		t.Errorf("created_at = %v, want RFC3339", m["created_at"])
	}
}

func TestDistanceMetricConstants(t *testing.T) {
	if DistanceL2 != "l2" {
		t.Error("DistanceL2 should be l2")
	}
	if DistanceCosine != "cosine" {
		t.Error("DistanceCosine should be cosine")
	}
	if DistanceIP != "ip" {
		t.Error("DistanceIP should be ip")
	}
}

func TestMemoryGraphNodeJSON(t *testing.T) {
	node := MemoryGraphNode{
		ID:      "node-1",
		Type:    "concept",
		Content: "test content",
		Attributes: map[string]any{
			"priority": "high",
		},
	}

	data, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}

	var restored MemoryGraphNode
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}

	if restored.ID != node.ID || restored.Type != node.Type || restored.Content != node.Content {
		t.Errorf("round-trip mismatch: got %+v", restored)
	}
}

func TestMemoryGraphEdgeJSON(t *testing.T) {
	edge := MemoryGraphEdge{
		From:         "a",
		To:           "b",
		RelationType: "DEPENDS_ON",
		Weight:       0.8,
	}

	data, err := json.Marshal(edge)
	if err != nil {
		t.Fatal(err)
	}

	var restored MemoryGraphEdge
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}

	if restored.From != "a" || restored.To != "b" || restored.RelationType != "DEPENDS_ON" {
		t.Errorf("round-trip mismatch: got %+v", restored)
	}
}

func TestGraphInjectionPayloadJSON(t *testing.T) {
	payload := GraphInjectionPayload{
		Nodes: []MemoryGraphNode{
			{ID: "n1", Type: "concept", Content: "test"},
		},
		Edges: []MemoryGraphEdge{
			{From: "n1", To: "n2", RelationType: "RELATED_TO"},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	var restored GraphInjectionPayload
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}

	if len(restored.Nodes) != 1 || len(restored.Edges) != 1 {
		t.Errorf("expected 1 node and 1 edge, got %d and %d", len(restored.Nodes), len(restored.Edges))
	}
}
