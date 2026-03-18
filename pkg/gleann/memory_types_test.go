package gleann

import (
	"encoding/json"
	"reflect"
	"testing"
)

// ── AttributesToJSON ──────────────────────────────────────────────────────────

func TestAttributesToJSON_Nil(t *testing.T) {
	got, err := AttributesToJSON(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "{}" {
		t.Errorf("nil map should marshal to {}, got %q", got)
	}
}

func TestAttributesToJSON_Empty(t *testing.T) {
	got, err := AttributesToJSON(map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "{}" {
		t.Errorf("empty map should marshal to {}, got %q", got)
	}
}

func TestAttributesToJSON_SimpleValues(t *testing.T) {
	attrs := map[string]any{
		"name":   "Alice",
		"score":  42.5,
		"active": true,
	}
	s, err := AttributesToJSON(attrs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Round-trip: unmarshal back and compare.
	var got map[string]any
	if err := json.Unmarshal([]byte(s), &got); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if got["name"] != "Alice" {
		t.Errorf("name = %v, want Alice", got["name"])
	}
	if got["active"] != true {
		t.Errorf("active = %v, want true", got["active"])
	}
}

func TestAttributesToJSON_NestedMap(t *testing.T) {
	attrs := map[string]any{
		"meta": map[string]any{"k": "v"},
	}
	s, err := AttributesToJSON(attrs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == "" || s == "{}" {
		t.Error("nested map should produce non-empty JSON")
	}
}

// ── JSONToAttributes ──────────────────────────────────────────────────────────

func TestJSONToAttributes_EmptyString(t *testing.T) {
	got, err := JSONToAttributes("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("empty string should return nil, got %v", got)
	}
}

func TestJSONToAttributes_EmptyObject(t *testing.T) {
	got, err := JSONToAttributes("{}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("{} should return nil, got %v", got)
	}
}

func TestJSONToAttributes_ValidJSON(t *testing.T) {
	s := `{"key":"value","num":7}`
	got, err := JSONToAttributes(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["key"] != "value" {
		t.Errorf("key = %v, want value", got["key"])
	}
}

func TestJSONToAttributes_InvalidJSON(t *testing.T) {
	_, err := JSONToAttributes("{not valid}")
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// ── AttributesToJSON / JSONToAttributes round-trip ────────────────────────────

func TestAttributesRoundTrip(t *testing.T) {
	original := map[string]any{
		"id":      "req-001",
		"version": float64(3), // JSON numbers decode as float64
		"tags":    []any{"backend", "auth"},
	}

	s, err := AttributesToJSON(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got, err := JSONToAttributes(s)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(original["id"], got["id"]) {
		t.Errorf("id mismatch after round-trip: got %v", got["id"])
	}
	if !reflect.DeepEqual(original["version"], got["version"]) {
		t.Errorf("version mismatch after round-trip: got %v", got["version"])
	}
}

// ── MemoryGraphNode zero-value safety ─────────────────────────────────────────

func TestMemoryGraphNode_JSONMarshal(t *testing.T) {
	n := MemoryGraphNode{ID: "n1", Type: "concept"}
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["id"] != "n1" {
		t.Errorf("id = %v, want n1", got["id"])
	}
	if got["type"] != "concept" {
		t.Errorf("type = %v, want concept", got["type"])
	}
}

func TestMemoryGraphEdge_JSONMarshal(t *testing.T) {
	e := MemoryGraphEdge{
		From:         "a",
		To:           "b",
		RelationType: "DEPENDS_ON",
		Weight:       1.5,
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["from"] != "a" || got["to"] != "b" {
		t.Errorf("from/to mismatch: %v", got)
	}
	if got["relation_type"] != "DEPENDS_ON" {
		t.Errorf("relation_type = %v, want DEPENDS_ON", got["relation_type"])
	}
	if got["weight"].(float64) != 1.5 {
		t.Errorf("weight = %v, want 1.5", got["weight"])
	}
}

// ── GraphInjectionPayload ─────────────────────────────────────────────────────

func TestGraphInjectionPayload_Empty(t *testing.T) {
	p := GraphInjectionPayload{}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got GraphInjectionPayload
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Nodes) != 0 || len(got.Edges) != 0 {
		t.Error("empty payload should round-trip to empty nodes and edges")
	}
}

func TestGraphInjectionPayload_JSONRoundTrip(t *testing.T) {
	payload := GraphInjectionPayload{
		Nodes: []MemoryGraphNode{
			{ID: "n1", Type: "requirement", Content: "User can log in"},
			{ID: "n2", Type: "code_symbol", Attributes: map[string]any{"file": "auth.go"}},
		},
		Edges: []MemoryGraphEdge{
			{From: "n1", To: "n2", RelationType: "IMPLEMENTED_BY", Weight: 1.0},
		},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got GraphInjectionPayload
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Nodes) != 2 {
		t.Errorf("nodes len = %d, want 2", len(got.Nodes))
	}
	if len(got.Edges) != 1 {
		t.Errorf("edges len = %d, want 1", len(got.Edges))
	}
	if got.Edges[0].RelationType != "IMPLEMENTED_BY" {
		t.Errorf("edge relation_type = %q, want IMPLEMENTED_BY", got.Edges[0].RelationType)
	}
}
