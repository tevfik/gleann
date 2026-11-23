package gleann

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

func TestHasCodeGraphAndOpenGraphStore(t *testing.T) {
	dir := t.TempDir()

	// No index at all.
	if HasCodeGraph(dir, "none") {
		t.Fatal("absent index reported a graph")
	}
	if g, err := OpenGraphStore(dir, "none"); g != nil || err != nil {
		t.Fatalf("OpenGraphStore(absent) = %v, %v; want nil, nil", g, err)
	}

	// Index in the old store format: it has no code graph, with or without <name>_graph.
	if err := os.MkdirAll(filepath.Join(dir, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if HasCodeGraph(dir, "old") {
		t.Fatal("old-format index without sidecar reported a graph")
	}
	if err := os.WriteFile(filepath.Join(dir, "old_graph"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// RFC 004 A3: an old Kùzu sidecar is never read, so it is no code graph.
	if HasCodeGraph(dir, "old") {
		t.Fatal("old-format index with an old Kùzu sidecar must not report a graph")
	}

	// Format-2 index: graph data in <name>.db.
	idx := filepath.Join(dir, "new")
	if err := os.MkdirAll(idx, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idx, "new.meta.json"), []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if HasCodeGraph(dir, "new") {
		t.Fatal("format-2 index without graph rows reported a graph")
	}
	st, err := store.Open(filepath.Join(idx, "new.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	delta := graphstore.FileDelta{File: "a.go", Symbols: []graphstore.Symbol{
		{FQN: "p.A", Name: "A", Kind: "function", File: "a.go", StartLine: 1, EndLine: 2, StartCol: 1, ByteOffset: 1},
	}}
	if err := graphstore.New(st).ApplyFileDelta(context.Background(), delta); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if !HasCodeGraph(dir, "new") {
		t.Fatal("format-2 index with symbols must report a graph")
	}
	g, err := OpenGraphStore(dir, "new")
	if err != nil || g == nil {
		t.Fatalf("OpenGraphStore = %v, %v", g, err)
	}
	defer g.Close()
	if s, err := g.Stats(); err != nil || s.Symbols != 1 {
		t.Fatalf("Stats = %+v, %v; want 1 symbol", s, err)
	}
}

func TestOpenGraphDBExposesTreeSitterChecker(t *testing.T) {
	dir := t.TempDir()
	idx := filepath.Join(dir, "idx")
	if err := os.MkdirAll(idx, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idx, "idx.meta.json"), []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(idx, "idx.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	delta := graphstore.FileDelta{File: "a.go", Symbols: []graphstore.Symbol{
		{FQN: "p.A", Name: "A", Kind: "function", File: "a.go", StartLine: 1, EndLine: 2, StartCol: 1, ByteOffset: 1},
	}}
	if err := graphstore.New(st).ApplyFileDelta(context.Background(), delta); err != nil {
		t.Fatal(err)
	}
	st.Close()

	db, err := OpenGraphDB(dir, "idx")
	if err != nil || db == nil {
		t.Fatalf("OpenGraphDB = %v, %v", db, err)
	}
	defer db.Close()

	checker, ok := db.(interface{ HasTreeSitterEdges() bool })
	if !ok {
		t.Fatal("BUG: OpenGraphDB returns *gleann.ownedGraphDB, which does not expose HasTreeSitterEdges; the notice's type assertion falls through and the meta key is never read on the CLI path")
	}
	_ = checker.HasTreeSitterEdges()

	resolver, ok := db.(interface {
		ResolveSymbolFiles(string, int) ([]string, error)
	})
	if !ok {
		t.Fatal("BUG: OpenGraphDB returns *gleann.ownedGraphDB, which does not expose ResolveSymbolFiles(string, int)")
	}
	files, err := resolver.ResolveSymbolFiles("A", DirectionCallers)
	if err != nil || len(files) != 1 || files[0] != "a.go" {
		t.Fatalf("ResolveSymbolFiles(\"A\", DirectionCallers) = %v, %v; want [\"a.go\"], nil", files, err)
	}
}
