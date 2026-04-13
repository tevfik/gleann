package gleann

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPassageManagerAddAndGet(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "test")
	pm := NewPassageManager(basePath)
	defer pm.Close()

	items := []Item{
		{Text: "Hello world", Metadata: map[string]any{"source": "test.txt"}},
		{Text: "Foo bar baz", Metadata: map[string]any{"source": "test2.txt"}},
		{Text: "Lorem ipsum dolor sit amet"},
	}

	ids, err := pm.Add(items)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	if len(ids) != 3 {
		t.Errorf("expected 3 IDs, got %d", len(ids))
	}
	if ids[0] != 0 || ids[1] != 1 || ids[2] != 2 {
		t.Errorf("expected IDs [0,1,2], got %v", ids)
	}

	// Get by ID.
	p, err := pm.Get(0)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", p.Text)
	}
	if p.Metadata["source"] != "test.txt" {
		t.Errorf("expected source 'test.txt', got %v", p.Metadata["source"])
	}

	p2, err := pm.Get(2)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p2.Text != "Lorem ipsum dolor sit amet" {
		t.Errorf("wrong text: %q", p2.Text)
	}
}

func TestPassageManagerGetBatch(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()

	items := []Item{
		{Text: "first"},
		{Text: "second"},
		{Text: "third"},
	}
	pm.Add(items)

	passages, err := pm.GetBatch([]int64{0, 2})
	if err != nil {
		t.Fatalf("getBatch: %v", err)
	}
	if len(passages) != 2 {
		t.Errorf("expected 2 passages, got %d", len(passages))
	}
	if passages[0].Text != "first" || passages[1].Text != "third" {
		t.Error("wrong passages returned")
	}
}

func TestPassageManagerGetTexts(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()

	pm.Add([]Item{{Text: "hello"}, {Text: "world"}})

	texts, err := pm.GetTexts([]int64{0, 1})
	if err != nil {
		t.Fatalf("getTexts: %v", err)
	}
	if texts[0] != "hello" || texts[1] != "world" {
		t.Errorf("wrong texts: %v", texts)
	}
}

func TestPassageManagerLoad(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "test")

	// Create and populate.
	pm1 := NewPassageManager(basePath)
	pm1.Add([]Item{
		{Text: "alpha"},
		{Text: "beta"},
		{Text: "gamma"},
	})
	pm1.Close()

	// Load from disk.
	pm2 := NewPassageManager(basePath)
	defer pm2.Close()
	if err := pm2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	if pm2.Count() != 3 {
		t.Errorf("expected 3 passages, got %d", pm2.Count())
	}

	p, err := pm2.Get(1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.Text != "beta" {
		t.Errorf("expected 'beta', got %q", p.Text)
	}
}

func TestPassageManagerAppend(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()

	// First batch.
	ids1, _ := pm.Add([]Item{{Text: "first"}, {Text: "second"}})
	if ids1[0] != 0 || ids1[1] != 1 {
		t.Errorf("first batch IDs wrong: %v", ids1)
	}

	// Second batch (should append).
	ids2, _ := pm.Add([]Item{{Text: "third"}, {Text: "fourth"}})
	if ids2[0] != 2 || ids2[1] != 3 {
		t.Errorf("second batch IDs wrong: %v", ids2)
	}

	if pm.Count() != 4 {
		t.Errorf("expected 4 passages, got %d", pm.Count())
	}
}

func TestPassageManagerAll(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()
	pm.Add([]Item{{Text: "a"}, {Text: "b"}})

	all := pm.All()
	if len(all) != 2 {
		t.Errorf("expected 2 passages, got %d", len(all))
	}
}

func TestPassageManagerDelete(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "test")
	pm := NewPassageManager(basePath)
	defer pm.Close()

	pm.Add([]Item{{Text: "delete me"}})

	if err := pm.Delete(); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Files should be removed.
	if _, err := os.Stat(pm.dbPath()); !os.IsNotExist(err) {
		t.Errorf("DB file should be deleted, err: %v", err)
	}

	// Calling Count() after Delete() recreates an empty DB.
	if pm.Count() != 0 {
		t.Error("expected 0 after delete")
	}
}

func TestPassageManagerOutOfRange(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()
	pm.Add([]Item{{Text: "only one"}})

	_, err := pm.Get(5)
	if err == nil {
		t.Error("expected error for out of range ID")
	}
}

func TestPassageManagerLoadEmpty(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "nonexistent"))
	defer pm.Close()

	if err := pm.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	if pm.Count() != 0 {
		t.Errorf("expected 0, got %d", pm.Count())
	}
}

func TestPassageManagerRemoveBySource(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()

	items := []Item{
		{Text: "chunk1 from a.md", Metadata: map[string]any{"source": "a.md"}},
		{Text: "chunk2 from a.md", Metadata: map[string]any{"source": "a.md"}},
		{Text: "chunk1 from b.md", Metadata: map[string]any{"source": "b.md"}},
		{Text: "chunk1 from c.md", Metadata: map[string]any{"source": "c.md"}},
	}

	ids, err := pm.Add(items)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(ids) != 4 {
		t.Fatalf("expected 4 IDs, got %d", len(ids))
	}

	// Remove passages from a.md.
	removedIDs, err := pm.RemoveBySource([]string{"a.md"})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(removedIDs) != 2 {
		t.Errorf("expected 2 removed IDs, got %d: %v", len(removedIDs), removedIDs)
	}

	// Count should be 2 now.
	if pm.Count() != 2 {
		t.Errorf("expected count 2, got %d", pm.Count())
	}

	// b.md and c.md passages should still be accessible.
	p, err := pm.Get(ids[2])
	if err != nil {
		t.Fatalf("get b.md passage: %v", err)
	}
	if p.Text != "chunk1 from b.md" {
		t.Errorf("expected 'chunk1 from b.md', got %q", p.Text)
	}

	// a.md passages should be gone.
	_, err = pm.Get(ids[0])
	if err == nil {
		t.Error("expected error getting removed passage, got nil")
	}

	// Remove multiple sources at once.
	removedIDs, err = pm.RemoveBySource([]string{"b.md", "c.md"})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(removedIDs) != 2 {
		t.Errorf("expected 2 removed, got %d", len(removedIDs))
	}
	if pm.Count() != 0 {
		t.Errorf("expected count 0, got %d", pm.Count())
	}

	// Remove non-existent source is a no-op.
	removedIDs, err = pm.RemoveBySource([]string{"nonexistent.md"})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(removedIDs) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removedIDs))
	}
}

func TestPassageManagerRemoveBySourceThenAdd(t *testing.T) {
	dir := t.TempDir()
	pm := NewPassageManager(filepath.Join(dir, "test"))
	defer pm.Close()

	// Add initial items.
	items := []Item{
		{Text: "old chunk 1", Metadata: map[string]any{"source": "file.md"}},
		{Text: "old chunk 2", Metadata: map[string]any{"source": "file.md"}},
		{Text: "keep this", Metadata: map[string]any{"source": "other.md"}},
	}
	_, err := pm.Add(items)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Remove file.md passages.
	removedIDs, err := pm.RemoveBySource([]string{"file.md"})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(removedIDs) != 2 {
		t.Fatalf("expected 2 removed, got %d", len(removedIDs))
	}

	// Add new passages for file.md (simulates re-chunking after edit).
	newItems := []Item{
		{Text: "new chunk 1", Metadata: map[string]any{"source": "file.md"}},
		{Text: "new chunk 2", Metadata: map[string]any{"source": "file.md"}},
		{Text: "new chunk 3", Metadata: map[string]any{"source": "file.md"}},
	}
	newIDs, err := pm.Add(newItems)
	if err != nil {
		t.Fatalf("add new: %v", err)
	}

	// New IDs should be higher than the original max (2).
	for _, id := range newIDs {
		if id <= 2 {
			t.Errorf("expected new ID > 2, got %d", id)
		}
	}

	// Total count: 1 (other.md) + 3 (new file.md) = 4.
	if pm.Count() != 4 {
		t.Errorf("expected count 4, got %d", pm.Count())
	}

	// New passages should be accessible.
	p, err := pm.Get(newIDs[0])
	if err != nil {
		t.Fatalf("get new passage: %v", err)
	}
	if p.Text != "new chunk 1" {
		t.Errorf("expected 'new chunk 1', got %q", p.Text)
	}
}
