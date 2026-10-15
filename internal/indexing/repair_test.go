package indexing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// corruptLikeOlderVersions reproduces what older versions did on every changed
// or deleted file: the file's passages are removed in place, which leaves a gap
// in the passage IDs, and its vectors are removed from the vector file, which
// moves every later vector to a new position. Vector n then belongs to the n-th
// remaining passage, not to the passage whose ID is n.
//
// The file removed is the one holding passage 0, so every remaining passage
// moves; which file that is depends on the order the directory was walked.
func corruptLikeOlderVersions(t *testing.T, cfg gleann.Config, name, backend string) {
	t.Helper()
	base := filepath.Join(cfg.IndexDir, name, name)
	pm := gleann.NewPassageManager(base)
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	first, err := pm.Get(0)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := first.Metadata["source"].(string)
	if src == "" {
		t.Fatal("passage 0 has no source")
	}
	if _, err := pm.RemoveBySource([]string{src}); err != nil {
		t.Fatal(err)
	}
	remaining := pm.All()
	_ = pm.Close()

	embs := make([][]float32, len(remaining))
	for i, p := range remaining {
		embs[i] = bowEmbed(p.Text)
	}
	factory, err := gleann.GetBackend(backend)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Backend = backend
	data, err := factory.NewBuilder(cfg).Build(context.Background(), embs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+".index", data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// selfRetrievalMisses returns the IDs of the passages that a vector search for
// their own text does not return: not first when strict, not among the top three
// otherwise (approximate backends may rank a near-tie ahead).
func selfRetrievalMisses(t *testing.T, cfg gleann.Config, name string, strict bool) []int64 {
	t.Helper()
	pm := gleann.NewReadOnlyPassageManager(filepath.Join(cfg.IndexDir, name, name))
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	all := pm.All()
	_ = pm.Close()
	if len(all) == 0 {
		t.Fatal("no passages")
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	s := gleann.NewSearcher(cfg, bowComputer{model: cfg.EmbeddingModel})
	if err := s.Load(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var misses []int64
	for _, p := range all {
		// Vector-only (alpha 1): the default hybrid search also asks BM25, which
		// works from the passage text and finds a passage whatever its vector is,
		// hiding exactly the damage this checks for.
		res, err := s.Search(context.Background(), p.Text, gleann.WithTopK(3), gleann.WithHybridAlpha(1.0))
		if err != nil || len(res) == 0 {
			misses = append(misses, p.ID)
			continue
		}
		found := res[0].ID == p.ID
		if !strict {
			for _, r := range res {
				found = found || r.ID == p.ID
			}
		}
		if !found {
			misses = append(misses, p.ID)
		}
	}
	return misses
}

func assertEveryPassageIsItsOwnHit(t *testing.T, cfg gleann.Config, name, backend string) {
	t.Helper()
	if m := selfRetrievalMisses(t, cfg, name, exactSearch(backend)); len(m) > 0 {
		t.Errorf("passages %v are not returned for their own text", m)
	}
}

func TestRepairFixesAnIndexCorruptedByInPlaceRemoval(t *testing.T) {
	for _, backend := range []string{"hnsw", "diskann"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			opts, cfg := equivEnv(t, backend, false)
			docs := equivFixture(t)
			if _, err := Build(ctx, BuildOptions{Options: opts, Name: "r", DocsDir: docs}); err != nil {
				t.Fatal(err)
			}
			corruptLikeOlderVersions(t, cfg, "r", backend)

			// The corruption is real: before the repair, searching a passage's
			// own text does not return it first. (Guards against a fixture
			// that proves nothing.)
			if m := selfRetrievalMisses(t, cfg, "r", true); len(m) == 0 {
				t.Fatal("fixture is not corrupted: every passage is already its own top hit")
			}

			// Checking only reports; the index stays as it is.
			res, err := Repair(ctx, RepairOptions{Options: opts, Name: "r", CheckOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Before.NeedsRepair() || res.Repaired {
				t.Fatalf("check-only must flag the index and change nothing: %+v", res)
			}
			if h, _ := gleann.CheckIndexHealth(cfg.IndexDir, "r"); !h.NeedsRepair() {
				t.Fatal("check-only repaired the index")
			}

			res, err = Repair(ctx, RepairOptions{Options: opts, Name: "r"})
			if err != nil {
				t.Fatalf("Repair: %v", err)
			}
			if !res.Repaired {
				t.Fatalf("index was not repaired: %+v", res)
			}
			h, err := gleann.CheckIndexHealth(cfg.IndexDir, "r")
			if err != nil || h.NeedsRepair() {
				t.Fatalf("still unhealthy after Repair: %+v (%v)", h, err)
			}
			assertEveryPassageIsItsOwnHit(t, cfg, "r", backend)

			// A healthy index is left alone.
			if res, err := Repair(ctx, RepairOptions{Options: opts, Name: "r"}); err != nil || res.Repaired {
				t.Errorf("second Repair should be a no-op: %+v (%v)", res, err)
			}
		})
	}
}

func TestRepairUsesTheIndexOwnBackend(t *testing.T) {
	ctx := context.Background()
	opts, cfg := equivEnv(t, "hnsw", false)
	docs := equivFixture(t)
	if _, err := Build(ctx, BuildOptions{Options: opts, Name: "b", DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	corruptLikeOlderVersions(t, cfg, "b", "hnsw")

	// The caller's default names another backend (or none).
	callerOpts := opts
	callerOpts.Config.Backend = ""
	if _, err := Repair(ctx, RepairOptions{Options: callerOpts, Name: "b"}); err != nil {
		t.Fatalf("Repair with no backend configured: %v", err)
	}
	meta, err := gleann.GetIndexMeta(cfg.IndexDir, "b")
	if err != nil || meta.Backend != "hnsw" {
		t.Errorf("backend changed: %+v (%v)", meta, err)
	}
	assertEveryPassageIsItsOwnHit(t, cfg, "b", "hnsw")
}

func TestRepairRefusesWhenThePassageStoreIsGone(t *testing.T) {
	ctx := context.Background()
	opts, cfg := equivEnv(t, "hnsw", false)
	if _, err := Build(ctx, BuildOptions{Options: opts, Name: "g", DocsDir: equivFixture(t)}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cfg.IndexDir, "g", "g.passages.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := Repair(ctx, RepairOptions{Options: opts, Name: "g"}); !errors.Is(err, ErrNotRepairable) {
		t.Errorf("want ErrNotRepairable, got %v", err)
	}
}

func TestRepairUnknownIndex(t *testing.T) {
	opts, _ := equivEnv(t, "hnsw", false)
	if _, err := Repair(context.Background(), RepairOptions{Options: opts, Name: "nope"}); err == nil {
		t.Error("expected an error for an index that does not exist")
	}
	if _, err := Repair(context.Background(), RepairOptions{Options: opts}); err == nil {
		t.Error("expected an error without a name")
	}
}

func TestRepairReportsWhatItCost(t *testing.T) {
	ctx := context.Background()
	opts, cfg := equivEnv(t, "hnsw", false)
	if _, err := Build(ctx, BuildOptions{Options: opts, Name: "c", DocsDir: equivFixture(t)}); err != nil {
		t.Fatal(err)
	}
	corruptLikeOlderVersions(t, cfg, "c", "hnsw")

	res, err := Repair(ctx, RepairOptions{Options: opts, Name: "c"})
	if err != nil || !res.Repaired {
		t.Fatalf("Repair: %+v (%v)", res, err)
	}
	st := res.Stats
	if st.Mode != gleann.UpdateCompact || st.Passages != res.Before.Passages || st.Embedded != st.Passages {
		t.Errorf("repair rebuilds every stored passage: %+v (before: %d passages)", st, res.Before.Passages)
	}
	if st.Elapsed <= 0 || res.Elapsed <= 0 {
		t.Errorf("timings not recorded: stats %+v, total %v", st, res.Elapsed)
	}
	// Build embedded these very texts, so the cache holds them all.
	if res.CacheLookups != st.Embedded || res.CacheHits != res.CacheLookups {
		t.Errorf("want every lookup to hit the embedding cache, got %d/%d for %d texts", res.CacheHits, res.CacheLookups, st.Embedded)
	}
}

// --force rebuilds an index the check calls healthy (a vector file that is out
// of step while the passage IDs are contiguous cannot be detected); --check
// still changes nothing.
func TestRepairForceRebuildsAHealthyIndex(t *testing.T) {
	ctx := context.Background()
	opts, cfg := equivEnv(t, "hnsw", false)
	if _, err := Build(ctx, BuildOptions{Options: opts, Name: "f", DocsDir: equivFixture(t)}); err != nil {
		t.Fatal(err)
	}

	if res, err := Repair(ctx, RepairOptions{Options: opts, Name: "f"}); err != nil || res.Repaired {
		t.Fatalf("a healthy index is left alone without --force: %+v (%v)", res, err)
	}
	if res, err := Repair(ctx, RepairOptions{Options: opts, Name: "f", Force: true, CheckOnly: true}); err != nil || res.Repaired {
		t.Fatalf("--check must win over --force: %+v (%v)", res, err)
	}
	res, err := Repair(ctx, RepairOptions{Options: opts, Name: "f", Force: true})
	if err != nil || !res.Repaired || res.Stats.Mode != gleann.UpdateCompact || res.Stats.Embedded != res.Stats.Passages {
		t.Fatalf("--force should rebuild every passage: %+v (%v)", res, err)
	}
	assertEveryPassageIsItsOwnHit(t, cfg, "f", "hnsw")
}

func TestRepairForceDoesNothingForAnIndexWithoutPassages(t *testing.T) {
	ctx := context.Background()
	opts, cfg := equivEnv(t, "hnsw", false)
	if _, err := Build(ctx, BuildOptions{Options: opts, Name: "e", DocsDir: equivFixture(t)}); err != nil {
		t.Fatal(err)
	}
	// Remove every passage: the index is emptied, which is a valid state.
	pm := gleann.NewPassageManager(filepath.Join(cfg.IndexDir, "e", "e"))
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, p := range pm.All() {
		sources = append(sources, p.Metadata["source"].(string))
	}
	_ = pm.Close()
	embedder, cleanup, err := newPipelineEnv(ctx, &opts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	builder, err := gleann.NewBuilder(embedder.cfg, embedder.embedder)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.UpdateIndex(ctx, "e", nil, sources); err != nil {
		t.Fatal(err)
	}

	if res, err := Repair(ctx, RepairOptions{Options: opts, Name: "e", Force: true}); err != nil || res.Repaired {
		t.Errorf("nothing to rebuild from: %+v (%v)", res, err)
	}
}

// Loading an index whose passage IDs have gaps warns, so a search does not
// quietly return wrong text; a repaired or healthy index does not.
func TestSearcherWarnsAboutADamagedIndex(t *testing.T) {
	ctx := context.Background()
	opts, cfg := equivEnv(t, "hnsw", false)
	if _, err := Build(ctx, BuildOptions{Options: opts, Name: "w", DocsDir: equivFixture(t)}); err != nil {
		t.Fatal(err)
	}
	warnings := func() []string {
		s := gleann.NewSearcher(cfg, bowComputer{model: cfg.EmbeddingModel})
		if err := s.Load(ctx, "w"); err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		return s.Warnings()
	}

	if w := warnings(); len(w) != 0 {
		t.Fatalf("a healthy index warned: %v", w)
	}
	corruptLikeOlderVersions(t, cfg, "w", "hnsw")
	w := warnings()
	if len(w) != 1 || !strings.Contains(w[0], "not contiguous") || !strings.Contains(w[0], "gleann index repair w") {
		t.Fatalf("a damaged index must warn and name the fix: %v", w)
	}
	if _, err := Repair(ctx, RepairOptions{Options: opts, Name: "w"}); err != nil {
		t.Fatal(err)
	}
	if w := warnings(); len(w) != 0 {
		t.Errorf("a repaired index still warns: %v", w)
	}
}
