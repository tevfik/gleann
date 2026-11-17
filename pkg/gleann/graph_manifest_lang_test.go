package gleann

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

func TestOpenGraphDB(t *testing.T) {
	dir := t.TempDir()
	if g, err := OpenGraphDB(dir, "none"); g != nil || err != nil {
		t.Fatalf("absent index = %v, %v", g, err)
	}

	idx := filepath.Join(dir, "g")
	if err := os.MkdirAll(idx, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idx, "g.meta.json"), []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(idx, "g.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if g, err := OpenGraphDB(dir, "g"); g != nil || err != nil {
		t.Fatalf("index without graph rows = %v, %v; want nil, nil", g, err)
	}

	st, err = store.Open(filepath.Join(idx, "g.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	gs := graphstore.New(st)
	sym := func(fqn string, line int64) graphstore.Symbol {
		return graphstore.Symbol{FQN: fqn, Name: fqn[strings.LastIndex(fqn, ".")+1:], Kind: "function", File: "a.go", StartLine: line, EndLine: line + 1, StartCol: 1, ByteOffset: line}
	}
	if err := gs.ApplyFileDelta(context.Background(), graphstore.FileDelta{File: "a.go",
		Symbols: []graphstore.Symbol{sym("p.Main", 1), sym("p.Helper", 5)},
		Edges:   []graphstore.Edge{{Src: "p.Main", Dst: "p.Helper"}}}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	g, err := OpenGraphDB(dir, "g")
	if err != nil || g == nil {
		t.Fatalf("OpenGraphDB = %v, %v", g, err)
	}
	callers, err := g.Callers("p.Helper")
	if err != nil || len(callers) != 1 || callers[0].FQN != "p.Main" {
		t.Fatalf("Callers = %+v, %v", callers, err)
	}
	// Close releases the store it opened: the database can be removed and
	// reopened afterwards.
	g.Close()
	if err := removeDBFiles(filepath.Join(idx, "g.db")); err != nil {
		t.Fatal(err)
	}

	// A damaged database is an error, not "no graph".
	if err := os.WriteFile(filepath.Join(idx, "g.db"), []byte(strings.Repeat("not sqlite ", 500)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenGraphDB(dir, "g"); err == nil || !strings.Contains(err.Error(), "opening index store") {
		t.Fatalf("damaged database: %v", err)
	}
	if _, err := OpenGraphStore(dir, "g"); err == nil {
		t.Fatal("OpenGraphStore accepted a damaged database")
	}
}

// The manifest of a format-2 index is its files table: the hashes committed
// with the passages, keyed by source, plus the source directory from meta.
func TestLoadIndexManifestFormat2(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadIndexManifest(dir, "none"); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("missing index: %v", err)
	}
	b, _ := newFormat2Builder(t, dir, 8)
	if err := b.Build(context.Background(), "man", f2Items()); err != nil {
		t.Fatal(err)
	}
	if err := UpdateIndexMeta(dir, "man", func(m *IndexMeta) { m.SourceDir = "/src/tree" }); err != nil {
		t.Fatal(err)
	}
	m, err := LoadIndexManifest(dir, "man")
	if err != nil {
		t.Fatal(err)
	}
	if m.IndexName != "man" || m.SourceDir != "/src/tree" || len(m.Entries) != 3 {
		t.Fatalf("manifest = %+v", m)
	}
	for _, src := range []string{"a.md", "b.md", "c.md"} {
		if e, ok := m.Entries[src]; !ok || e.Path != src {
			t.Errorf("entry %s = %+v, %v", src, e, ok)
		}
	}
	if m.RootHash == "" || m.RootHash != ComputeRootHash(m.Entries) {
		t.Fatalf("root hash %q does not match the entries", m.RootHash)
	}

	// meta.json says format 2 but the database is gone: refused with the
	// rebuild hint, never an empty manifest (which would re-add every file).
	if err := removeDBFiles(filepath.Join(dir, "man", "man.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIndexManifest(dir, "man"); err == nil || !strings.Contains(err.Error(), "rebuild") {
		t.Fatalf("lost database: %v", err)
	}
}

func TestChangedKeys(t *testing.T) {
	a := DefaultConfig()
	b := a
	if got := ChangedKeys(a, b); len(got) != 0 {
		t.Fatalf("identical configs differ in %v", got)
	}
	b.Backend = "some-other-backend"
	b.EmbeddingModel = "other-model"
	got := ChangedKeys(a, b)
	if strings.Join(got, ",") != "backend,embedding_model" {
		t.Fatalf("ChangedKeys = %v", got)
	}
	// A key present on one side only (omitempty) counts as changed.
	c := a
	c.StorageMode = StorageStore
	if got := ChangedKeys(a, c); len(got) != 1 || got[0] != "storage_mode" {
		t.Fatalf("one-sided key: %v", got)
	}
	if got := ChangedKeys(c, a); len(got) != 1 || got[0] != "storage_mode" {
		t.Fatalf("one-sided key, reversed: %v", got)
	}
}

func TestDominantLanguage(t *testing.T) {
	dir := t.TempDir()
	b, emb := newFormat2Builder(t, dir, 8)
	var items []Item
	add := func(src string, n int) {
		for i := 0; i < n; i++ {
			items = append(items, Item{Text: src + " chunk " + strings.Repeat("z", i+len(items)), Metadata: map[string]any{"source": src}})
		}
	}
	add("vendor/github.com/x/lib.c", 6) // vendored code never decides
	add("node_modules/pkg/index.js", 6)
	add("cmd/main.go", 2)
	add("internal/a.go", 2)
	add("scripts/tool.py", 3)
	add("README.md", 4)
	if err := b.Build(context.Background(), "lang", items); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
	if got := s.DominantLanguage(0); got != "" {
		t.Fatalf("before Load = %q", got)
	}
	if err := s.Load(context.Background(), "lang"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.DominantLanguage(0); got != "Go" {
		t.Fatalf("DominantLanguage = %q, want Go (4 first-party Go passages vs 3 Python)", got)
	}
	// The answer is computed once.
	if got := s.DominantLanguage(1); got != "Go" {
		t.Fatalf("second call = %q", got)
	}

	// A capped scan only sees the first passages.
	s2 := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
	if err := s2.Load(context.Background(), "lang"); err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.DominantLanguage(12); got != "" {
		t.Fatalf("a scan capped at the vendored passages = %q, want none", got)
	}
}
