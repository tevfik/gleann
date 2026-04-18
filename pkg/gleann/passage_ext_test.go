package gleann

import (
	"path/filepath"
	"testing"
)

// ── PassageManager: Add, Get, GetBatch, GetTexts, All, Count ──

func TestPassageManagerAddAndGetExt2(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	items := []Item{
		{Text: "Hello world", Metadata: map[string]any{"source": "a.txt"}},
		{Text: "Goodbye world", Metadata: map[string]any{"source": "b.txt"}},
	}

	ids, err := pm.Add(items)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("got %d ids", len(ids))
	}

	// Get individual.
	p, err := pm.Get(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if p.Text != "Hello world" {
		t.Errorf("text = %q", p.Text)
	}
}

func TestPassageManagerGetBatchExt2(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	ids, _ := pm.Add([]Item{
		{Text: "alpha"},
		{Text: "beta"},
		{Text: "gamma"},
	})

	passages, err := pm.GetBatch(ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(passages) != 3 {
		t.Fatalf("got %d", len(passages))
	}
	if passages[1].Text != "beta" {
		t.Errorf("text[1] = %q", passages[1].Text)
	}
}

func TestPassageManagerGetTextsExt2(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	ids, _ := pm.Add([]Item{
		{Text: "first"},
		{Text: "second"},
	})

	texts, err := pm.GetTexts(ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 2 {
		t.Fatalf("got %d", len(texts))
	}
	if texts[0] != "first" || texts[1] != "second" {
		t.Errorf("texts = %v", texts)
	}
}

func TestPassageManagerAllExt2(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	pm.Add([]Item{{Text: "one"}, {Text: "two"}})
	all := pm.All()
	if len(all) != 2 {
		t.Errorf("all = %d", len(all))
	}
}

func TestPassageManagerCount(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	pm.Add([]Item{{Text: "one"}, {Text: "two"}, {Text: "three"}})
	if c := pm.Count(); c != 3 {
		t.Errorf("count = %d", c)
	}
}

// ── Load / LoadAll ─────────────────────────────────────────────

func TestPassageManagerLoadExt2(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	// Should be able to add after Load.
	_, err := pm.Add([]Item{{Text: "after load"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPassageManagerLoadAll(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	pm.Add([]Item{{Text: "a"}, {Text: "b"}})

	// LoadAll should cache.
	if err := pm.LoadAll(); err != nil {
		t.Fatal(err)
	}

	// Subsequent Get should use cache.
	p, err := pm.Get(0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Text != "a" {
		t.Errorf("text = %q", p.Text)
	}

	// Count should use cache.
	if c := pm.Count(); c != 2 {
		t.Errorf("count after LoadAll = %d", c)
	}

	// All should use cache.
	all := pm.All()
	if len(all) != 2 {
		t.Errorf("all after LoadAll = %d", len(all))
	}
}

func TestPassageManagerLoadEmptyExt2(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	if err := pm.LoadAll(); err != nil {
		t.Fatal(err)
	}

	if c := pm.Count(); c != 0 {
		t.Errorf("count = %d, want 0", c)
	}
}

// ── RemoveBySource ─────────────────────────────────────────────

func TestPassageManagerRemoveBySourceExt2(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	pm.Add([]Item{
		{Text: "from a", Metadata: map[string]any{"source": "a.txt"}},
		{Text: "from b", Metadata: map[string]any{"source": "b.txt"}},
		{Text: "from a too", Metadata: map[string]any{"source": "a.txt"}},
	})

	removed, err := pm.RemoveBySource([]string{"a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Errorf("removed = %d, want 2", len(removed))
	}

	// Only b.txt should remain.
	all := pm.All()
	if len(all) != 1 {
		t.Errorf("remaining = %d, want 1", len(all))
	}
	if all[0].Text != "from b" {
		t.Errorf("text = %q", all[0].Text)
	}
}

func TestPassageManagerRemoveBySourceNoMatch(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	pm.Add([]Item{{Text: "data", Metadata: map[string]any{"source": "x.txt"}}})

	removed, err := pm.RemoveBySource([]string{"nonexistent.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %d, want 0", len(removed))
	}
}

// ── Delete ─────────────────────────────────────────────────────

func TestPassageManagerDeleteExt2(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))

	pm.Add([]Item{{Text: "data"}})
	pm.Close()

	if err := pm.Delete(); err != nil {
		t.Fatal(err)
	}

	// After delete, a new manager should have 0 passages.
	pm2 := NewPassageManager(filepath.Join(t.TempDir(), "test2"))
	defer pm2.Close()

	if c := pm2.Count(); c != 0 {
		t.Errorf("count = %d after delete", c)
	}
}

// ── dbPath ─────────────────────────────────────────────────────

func TestPassageManagerDbPath(t *testing.T) {
	pm := NewPassageManager("/tmp/test-base")
	if got := pm.dbPath(); got != "/tmp/test-base.passages.db" {
		t.Errorf("dbPath = %q", got)
	}
}

// ── Append (Add after initial Add) ────────────────────────────

func TestPassageManagerAppendExt2(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	ids1, _ := pm.Add([]Item{{Text: "first"}})
	ids2, _ := pm.Add([]Item{{Text: "second"}})

	if ids2[0] <= ids1[0] {
		t.Errorf("second ID %d should be > first ID %d", ids2[0], ids1[0])
	}

	if pm.Count() != 2 {
		t.Errorf("count = %d", pm.Count())
	}
}

// ── GetOutOfRange ──────────────────────────────────────────────

func TestPassageManagerOutOfRangeExt2(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	pm.Add([]Item{{Text: "only one"}})

	_, err := pm.Get(999)
	if err == nil {
		t.Error("expected error for out-of-range ID")
	}
}

// ── Close idempotent ───────────────────────────────────────────

func TestPassageManagerCloseIdempotent(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	pm.Load()

	if err := pm.Close(); err != nil {
		t.Fatal(err)
	}
	// Second close should not error.
	if err := pm.Close(); err != nil {
		t.Fatal(err)
	}
}

// ── RemoveBySource then Add ────────────────────────────────────

func TestPassageManagerRemoveBySourceThenAddExt2(t *testing.T) {
	pm := NewPassageManager(filepath.Join(t.TempDir(), "test"))
	defer pm.Close()

	pm.Add([]Item{
		{Text: "from a", Metadata: map[string]any{"source": "a.txt"}},
	})
	pm.RemoveBySource([]string{"a.txt"})

	// Add new items.
	ids, err := pm.Add([]Item{{Text: "new item"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Errorf("got %d ids", len(ids))
	}
}
