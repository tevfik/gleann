package gleann

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexMetaGovernance(t *testing.T) {
	// 1. Test IsMCPExposed default behavior
	var meta IndexMeta
	if !meta.IsMCPExposed() {
		t.Errorf("expected default IsMCPExposed() to be true when nil")
	}

	f := false
	meta.MCPExposed = &f
	if meta.IsMCPExposed() {
		t.Errorf("expected IsMCPExposed() to be false when set to false")
	}

	tr := true
	meta.MCPExposed = &tr
	if !meta.IsMCPExposed() {
		t.Errorf("expected IsMCPExposed() to be true when set to true")
	}

	// 2. Test HasTag and HasAnyTag
	meta.Tags = []string{"Backend", "Work", "golang"}
	if !meta.HasTag("backend") {
		t.Errorf("expected HasTag('backend') to be true (case insensitive)")
	}
	if !meta.HasTag("@work") {
		t.Errorf("expected HasTag('@work') to strip '@' and match")
	}
	if meta.HasTag("frontend") {
		t.Errorf("expected HasTag('frontend') to be false")
	}

	if !meta.HasAnyTag([]string{"personal", "work"}) {
		t.Errorf("expected HasAnyTag to match 'work'")
	}
	if meta.HasAnyTag([]string{"personal", "finance"}) {
		t.Errorf("expected HasAnyTag to be false")
	}

	// 3. Test JSON marshaling/unmarshaling
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("failed to marshal meta: %v", err)
	}

	var unmarshaled IndexMeta
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal meta: %v", err)
	}

	if !unmarshaled.IsMCPExposed() {
		t.Errorf("unmarshaled MCPExposed mismatch")
	}
	if len(unmarshaled.Tags) != 3 || !unmarshaled.HasTag("golang") {
		t.Errorf("unmarshaled Tags mismatch: %v", unmarshaled.Tags)
	}
}

func TestIndexMetaFileOperations(t *testing.T) {
	tempDir := t.TempDir()
	indexName := "test-repo"
	indexDir := filepath.Join(tempDir, indexName)
	if err := os.MkdirAll(indexDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	metaFile := filepath.Join(indexDir, indexName+".meta.json")
	initialMeta := IndexMeta{
		Name:           indexName,
		Backend:        "hnsw",
		EmbeddingModel: "bge-m3",
		NumPassages:    42,
	}
	bytes, _ := json.Marshal(initialMeta)
	if err := os.WriteFile(metaFile, bytes, 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Read meta
	loaded, err := GetIndexMeta(tempDir, indexName)
	if err != nil {
		t.Fatalf("GetIndexMeta failed: %v", err)
	}
	if loaded.Name != indexName || loaded.NumPassages != 42 {
		t.Errorf("unexpected loaded meta: %+v", loaded)
	}
	if !loaded.IsMCPExposed() {
		t.Errorf("expected default IsMCPExposed true")
	}

	// Update meta
	err = UpdateIndexMeta(tempDir, indexName, func(m *IndexMeta) {
		m.Tags = []string{"prod", "service"}
		m.Description = "Production microservice"
		exp := false
		m.MCPExposed = &exp
	})
	if err != nil {
		t.Fatalf("UpdateIndexMeta failed: %v", err)
	}

	// Verify update
	updated, err := GetIndexMeta(tempDir, indexName)
	if err != nil {
		t.Fatalf("GetIndexMeta after update failed: %v", err)
	}
	if updated.IsMCPExposed() {
		t.Errorf("expected updated MCPExposed to be false")
	}
	if updated.Description != "Production microservice" {
		t.Errorf("description mismatch: %s", updated.Description)
	}
	if !updated.HasTag("prod") || !updated.HasTag("service") {
		t.Errorf("tags mismatch: %v", updated.Tags)
	}

	// Test ListIndexesByTag
	taggedIndexes, err := ListIndexesByTag(tempDir, "prod")
	if err != nil {
		t.Fatalf("ListIndexesByTag failed: %v", err)
	}
	if len(taggedIndexes) != 1 || taggedIndexes[0].Name != indexName {
		t.Errorf("expected 1 tagged index, got %d", len(taggedIndexes))
	}

	emptyIndexes, err := ListIndexesByTag(tempDir, "nonexistent")
	if err != nil {
		t.Fatalf("ListIndexesByTag empty failed: %v", err)
	}
	if len(emptyIndexes) != 0 {
		t.Errorf("expected 0 tagged indexes, got %d", len(emptyIndexes))
	}
}
