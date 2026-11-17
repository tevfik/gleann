package gleann

import (
	"context"
	"strings"
	"testing"
)

// fxPassages opens the passage store of the standard index; the returned
// store is closed with the test.
func fxPassages(t *testing.T, base string, readOnly bool) *storePassages {
	t.Helper()
	ps, err := openStorePassages(base, readOnly)
	if err != nil {
		t.Fatalf("openStorePassages: %v", err)
	}
	sp := ps.(*storePassages)
	t.Cleanup(func() { sp.Close() })
	return sp
}

// A passage store whose database went away reports it from every operation
// that reads or writes, rather than behaving like an empty index.
func TestPassageStoreReportsAClosedDatabase(t *testing.T) {
	_, base, _ := fxBuilt(t)
	sp := fxPassages(t, base, false)
	if n := sp.Count(); n != 4 {
		t.Fatalf("Count = %d, want 4", n)
	}
	if err := sp.st.Close(); err != nil {
		t.Fatal(err)
	}

	if n := sp.Count(); n != 0 {
		t.Errorf("Count on a closed store = %d, want 0", n)
	}
	calls := map[string]func() error{
		"Get slot":    func() error { _, err := sp.Get(1); return err },
		"Get rowid":   func() error { _, err := sp.Get(-1); return err },
		"GetBatch":    func() error { _, err := sp.GetBatch([]int64{0, 1}); return err },
		"GetBatchRow": func() error { _, err := sp.GetBatch([]int64{-1}); return err },
		"GetTexts":    func() error { _, err := sp.GetTexts([]int64{0}); return err },
		"LoadAll":     func() error { return sp.LoadAll() },
		"HasSources":  func() error { _, err := sp.HasSources([]string{"a.md"}); return err },
		"idSpan":      func() error { _, _, err := sp.idSpan(); return err },
		"IsDense":     func() error { _, err := sp.IsDense(); return err },
		"RemoveBySource": func() error {
			_, err := sp.RemoveBySource([]string{"a.md"})
			return err
		},
		"Add": func() error {
			_, err := sp.Add([]Item{{Text: "x", Metadata: map[string]any{"source": "q.md"}}})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s on a closed store returned no error", name)
		}
	}
	if got := sp.All(); len(got) != 0 {
		t.Errorf("All on a closed store = %d passages, want none", len(got))
	}
}

// A removal that fails halfway reports the source and keeps reporting it.
func TestPassageStoreRemoveBySourceNamesTheFailingSource(t *testing.T) {
	_, base, _ := fxBuilt(t)
	sp := fxPassages(t, base, false)
	fxExec(t, base, fxFail("_del", "DELETE", "passages", "OLD.source = 'b.md'"))
	_, err := sp.RemoveBySource([]string{"b.md"})
	if err == nil || !strings.Contains(err.Error(), `removing source "b.md"`) {
		t.Fatalf("err = %v", err)
	}
}

// Adding to a source whose write fails names the source; a read-only store
// refuses before touching anything.
func TestPassageStoreAddFailures(t *testing.T) {
	_, base, _ := fxBuilt(t)
	sp := fxPassages(t, base, false)
	fxExec(t, base, fxFail("_ins", "INSERT", "passages", "NEW.source = 'q.md'"))
	_, err := sp.Add([]Item{{Text: "quince paste", Metadata: map[string]any{"source": "q.md"}}})
	if err == nil || !strings.Contains(err.Error(), `adding source "q.md"`) {
		t.Fatalf("err = %v", err)
	}
	ro := fxPassages(t, base, true)
	if _, err := ro.Add([]Item{{Text: "x"}}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("Add on a read-only store: %v", err)
	}
	if _, err := ro.RemoveBySource([]string{"a.md"}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("RemoveBySource on a read-only store: %v", err)
	}
	if err := ro.Delete(); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("Delete on a read-only store: %v", err)
	}
}

// Opening a passage store finishes an interrupted publication first, and
// refuses when that is impossible.
func TestOpenStorePassagesRefusesAnUnrecoverablePublication(t *testing.T) {
	_, base, _ := fxBuilt(t)
	st := fxOpen(t, base)
	ctx := context.Background()
	if err := st.SetMeta(ctx, "pending_vector_file", "idx.index.tmp"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMeta(ctx, "pending_vector_generation", "junk"); err != nil {
		t.Fatal(err)
	}
	if _, err := openStorePassages(base, true); err == nil || !strings.Contains(err.Error(), "recovering vector publication") {
		t.Fatalf("err = %v", err)
	}
}

// Metadata that cannot be serialised is dropped, the source survives.
func TestSplitItemMetaSurvivesUnserialisableMetadata(t *testing.T) {
	src, js := splitItemMeta(map[string]any{"source": "a.md", "chunk_index": 3, "bad": make(chan int)})
	if src != "a.md" || js != "{}" {
		t.Errorf("splitItemMeta = (%q, %q), want the source and an empty object", src, js)
	}
	src, js = splitItemMeta(map[string]any{"source": "a.md", "lang": "go"})
	if src != "a.md" || js != `{"lang":"go"}` {
		t.Errorf("splitItemMeta = (%q, %q)", src, js)
	}
}

// A searcher without a format-2 passage store has no lexical table to open.
func TestOpenFormat2ScorerNeedsAFormat2Store(t *testing.T) {
	s := &LeannSearcher{meta: IndexMeta{Name: "x"}}
	if _, err := s.openFormat2Scorer(context.Background()); err == nil || !strings.Contains(err.Error(), "no format-2 passage store") {
		t.Fatalf("err = %v", err)
	}
}
