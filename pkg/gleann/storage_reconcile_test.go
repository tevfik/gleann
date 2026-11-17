package gleann

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestInspectVectorStorage(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "idx", "idx")
	writeSized(t, base+".db", 1000)
	writeSized(t, base+".db-wal", 24)

	// 10 passages x 4 dims x 4 bytes = 160 bytes with every vector.
	meta := IndexMeta{Name: "idx", Backend: "hnsw", NumPassages: 10, Dimensions: 4, VectorReady: true}

	// No vector file yet: keyword search only.
	vs := InspectVectorStorage(dir, meta)
	if vs.Mode != "none" || vs.Convertible || vs.PassageBytes != 1024 || vs.FullBytes != 160 {
		t.Fatalf("no index file: %+v", vs)
	}

	cases := []struct {
		name        string
		indexBytes  int
		meta        func(IndexMeta) IndexMeta
		mode        string
		convertible bool
	}{
		{"vectors stored", 200, func(m IndexMeta) IndexMeta { return m }, StorageStore, true},
		{"exactly the 70% threshold", 112, func(m IndexMeta) IndexMeta { return m }, StorageStore, true},
		{"vectors pruned", 40, func(m IndexMeta) IndexMeta { return m }, StorageRecompute, true},
		{"diskann always stores", 40, func(m IndexMeta) IndexMeta { m.Backend = "diskann"; return m }, StorageStore, false},
		{"no passages recorded", 40, func(m IndexMeta) IndexMeta { m.NumPassages = 0; return m }, "unknown", true},
		{"lexical-only index", 200, func(m IndexMeta) IndexMeta { m.LexicalOnly = true; return m }, "none", false},
		{"staged, vectors never built", 200, func(m IndexMeta) IndexMeta { m.VectorReady = false; m.Dimensions = 0; return m }, "none", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeSized(t, base+".index", tc.indexBytes)
			vs := InspectVectorStorage(dir, tc.meta(meta))
			if vs.Mode != tc.mode || vs.Convertible != tc.convertible {
				t.Fatalf("got %+v, want mode %q convertible %v", vs, tc.mode, tc.convertible)
			}
			if vs.Mode != "none" && vs.IndexBytes != int64(tc.indexBytes) {
				t.Fatalf("IndexBytes = %d, want %d", vs.IndexBytes, tc.indexBytes)
			}
			if vs.Mode != "unknown" && vs.Note == "" {
				t.Fatal("no note for the UI")
			}
		})
	}
}

func TestApplyStorageMode(t *testing.T) {
	c := Config{}
	c.HNSWConfig.PruneEmbeddings = true
	c.HNSWConfig.PruneKeepFraction = 0.3
	if err := c.ApplyStorageMode(); err != nil || !c.HNSWConfig.PruneEmbeddings || c.HNSWConfig.PruneKeepFraction != 0.3 {
		t.Fatalf("empty mode must leave the settings: %+v, %v", c.HNSWConfig, err)
	}
	c.StorageMode = StorageStore
	if err := c.ApplyStorageMode(); err != nil || c.HNSWConfig.PruneEmbeddings {
		t.Fatalf("store: %+v, %v", c.HNSWConfig, err)
	}
	c.StorageMode = StorageRecompute
	c.HNSWConfig.PruneKeepFraction = 0.3
	if err := c.ApplyStorageMode(); err != nil || !c.HNSWConfig.PruneEmbeddings || c.HNSWConfig.PruneKeepFraction != 0 {
		t.Fatalf("recompute: %+v, %v", c.HNSWConfig, err)
	}
	c.StorageMode = "zip"
	if err := c.ApplyStorageMode(); err == nil || !strings.Contains(err.Error(), "zip") {
		t.Fatalf("unknown mode: %v", err)
	}
}

func TestReconcileRemovesPassagesOfDeletedFiles(t *testing.T) {
	dir := t.TempDir()
	docs := t.TempDir()
	builder, _ := newTestBuilder(t, dir, 8)
	ctx := context.Background()
	for _, f := range []string{"keep.md", "gone.md", "also-gone.md"} {
		writeSized(t, filepath.Join(docs, f), 1)
	}
	items := []Item{
		{Text: "kept passage one", Metadata: map[string]any{"source": "keep.md"}},
		{Text: "gone passage one", Metadata: map[string]any{"source": "gone.md"}},
		{Text: "gone passage two", Metadata: map[string]any{"source": "gone.md"}},
		{Text: "also gone", Metadata: map[string]any{"source": "also-gone.md"}},
		{Text: "sourceless passage"},
	}
	if err := builder.Build(ctx, "rec", items); err != nil {
		t.Fatal(err)
	}

	// Nothing deleted yet: a scan only.
	res, err := builder.Reconcile(ctx, "rec", docs)
	if err != nil || res.ScannedPassages != 5 || len(res.OrphanSources) != 0 || res.RemovedPassages != 0 {
		t.Fatalf("clean reconcile = %+v, %v", res, err)
	}

	for _, f := range []string{"gone.md", "also-gone.md"} {
		if err := os.Remove(filepath.Join(docs, f)); err != nil {
			t.Fatal(err)
		}
	}

	// The read-only check reports the orphans without touching the index.
	pm := testPassageStore(filepath.Join(dir, "rec", "rec"))
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	orphans, total := ReconcilePassageManager(pm, docs)
	pm.Close()
	sort.Strings(orphans)
	if total != 5 || strings.Join(orphans, ",") != "also-gone.md,gone.md" {
		t.Fatalf("ReconcilePassageManager = %v, %d", orphans, total)
	}

	res, err = builder.Reconcile(ctx, "rec", docs)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(res.OrphanSources)
	if strings.Join(res.OrphanSources, ",") != "also-gone.md,gone.md" || res.RemovedPassages != 3 || res.RemovedVectors != 3 {
		t.Fatalf("reconcile = %+v", res)
	}

	pm = testPassageStore(filepath.Join(dir, "rec", "rec"))
	defer pm.Close()
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	if pm.Count() != 2 {
		t.Fatalf("%d passages after reconcile, want 2 (kept + sourceless)", pm.Count())
	}
	_ = pm.ForEachPassage(func(p Passage) error {
		if src, _ := p.Metadata["source"].(string); strings.Contains(src, "gone") {
			t.Errorf("passage of a deleted file survived: %+v", p)
		}
		return nil
	})
}

func TestReconcileErrors(t *testing.T) {
	builder, _ := newTestBuilder(t, t.TempDir(), 8)
	if _, err := builder.Reconcile(context.Background(), "../escape", t.TempDir()); err == nil {
		t.Fatal("a path name was accepted")
	}
	if _, err := builder.Reconcile(context.Background(), "missing", t.TempDir()); err == nil || !strings.Contains(err.Error(), "load passages") {
		t.Fatalf("missing index: %v", err)
	}
}
