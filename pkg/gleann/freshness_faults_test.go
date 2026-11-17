package gleann

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fxLoaded builds the standard index (default incremental limits, so an
// update publishes a delta) and returns it with a loaded searcher.
func fxLoaded(t *testing.T, emb EmbeddingComputer) (string, string, *LeannBuilder, *LeannSearcher) {
	t.Helper()
	dir, base, b := fxIncremental(t)
	if emb == nil {
		emb = &testEmbedder{dim: 8}
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return dir, base, b, s
}

// A delta-only change is absorbed in place by Refresh: the searcher reports
// that it refreshed and finds the new passage without a reload.
func TestRefreshAbsorbsADeltaOnlyChange(t *testing.T) {
	_, _, b, s := fxLoaded(t, nil)
	ctx := context.Background()
	if err := b.UpdateIndex(ctx, "idx", f2UpdateItems(), nil); err != nil {
		t.Fatal(err)
	}
	if c, err := s.Change(ctx); err != nil || c != IndexDeltaChanged {
		t.Fatalf("Change = %v, %v; want a delta-only change", c, err)
	}
	if stale, err := s.Stale(ctx); err != nil || !stale {
		t.Fatalf("Stale = %v, %v", stale, err)
	}
	did, err := s.Refresh(ctx)
	if err != nil || !did {
		t.Fatalf("Refresh = %v, %v", did, err)
	}
	if c, _ := s.Change(ctx); c != IndexUnchanged {
		t.Errorf("Change after Refresh = %v, want unchanged", c)
	}
	rs, err := s.Search(ctx, "date syrup cake", WithTopK(1), WithHybridAlpha(1.0))
	if err != nil || len(rs) != 1 || rs[0].Text != "date syrup cake" {
		t.Fatalf("Search after Refresh = %+v, %v", rs, err)
	}
}

// A full reload through Refresh drops a custom in-memory scorer's documents
// (it must not stack the new passages on the old ones).
func TestRefreshReloadsAndResetsACustomBM25Scorer(t *testing.T) {
	_, _, b, s := fxLoaded(t, nil)
	ctx := context.Background()
	s.SetScorer(NewBM25Adapter())
	if _, err := b.Compact(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	if c, err := s.Change(ctx); err != nil || c != IndexFullReload {
		t.Fatalf("Change = %v, %v; want a full reload", c, err)
	}
	did, err := s.Refresh(ctx)
	if err != nil || !did {
		t.Fatalf("Refresh = %v, %v", did, err)
	}
	if _, ok := s.scorer.(*BM25Adapter); !ok || !s.customScorer {
		t.Fatalf("scorer after the reload = %T (custom %v), want the caller's BM25 scorer kind", s.scorer, s.customScorer)
	}
	rs, err := s.SearchBM25(ctx, "banana", 3)
	if err != nil || len(rs) != 1 {
		t.Errorf("SearchBM25 after the reload = %d hits, %v; want exactly one (no stacked documents)", len(rs), err)
	}
}

// A searcher that lost its database reports a full reload with the error; a
// searcher with no name cannot reload and returns that error.
func TestFreshnessReportsAClosedDatabase(t *testing.T) {
	_, _, _, s := fxLoaded(t, nil)
	ctx := context.Background()
	s.passages.(*storePassages).st.Close()

	if c, err := s.Change(ctx); c != IndexFullReload || err == nil {
		t.Errorf("Change = %v, %v; want a full reload with the error", c, err)
	}
	if stale, err := s.Stale(ctx); !stale || err == nil {
		t.Errorf("Stale = %v, %v", stale, err)
	}
	if changed, err := s.mainChanged(ctx); !changed || err == nil {
		t.Errorf("mainChanged = %v, %v", changed, err)
	}
	s.name = ""
	if did, err := s.Refresh(ctx); did || err == nil {
		t.Errorf("Refresh without a name = %v, %v; want the freshness error", did, err)
	}
}

// The chat's retrieval refuses to search a searcher it cannot refresh.
func TestSearchFreshReportsARefreshFailure(t *testing.T) {
	_, _, _, s := fxLoaded(t, nil)
	s.passages.(*storePassages).st.Close()
	s.name = ""
	c := &LeannChat{searcher: s}
	if _, err := c.searchFresh(context.Background(), "banana"); err == nil {
		t.Fatal("searchFresh must report the refresh failure")
	}
}

// A publication that lands while a chat question is being embedded is
// detected, and the chat refreshes and retries once.
func TestSearchFreshRetriesAfterAPublicationDuringTheQuery(t *testing.T) {
	var b *LeannBuilder
	var fired bool
	hook := &hookEmbedder{testEmbedder: &testEmbedder{dim: 8}}
	_, _, builder, s := fxLoaded(t, hook)
	b = builder
	hook.onCompute = func() {
		if fired {
			return
		}
		fired = true
		// Another process compacts the index while this query is in flight.
		if _, err := b.Compact(context.Background(), "idx"); err != nil {
			t.Errorf("Compact: %v", err)
		}
	}
	// The builder embeds through the same hook; only the first call fires.
	c := &LeannChat{searcher: s}
	rs, err := c.searchFresh(context.Background(), "banana bread", WithTopK(2), WithHybridAlpha(1.0))
	if err != nil {
		t.Fatalf("searchFresh: %v", err)
	}
	if len(rs) == 0 {
		t.Error("the retried search returned nothing")
	}
	if !fired {
		t.Error("the publication hook never ran")
	}
}

// Recording the freshness rolls a pending publication forward first and
// reports a publication it cannot read.
func TestRecordFreshnessResolvesAPendingPublication(t *testing.T) {
	ctx := context.Background()
	t.Run("pending publication rolled forward", func(t *testing.T) {
		_, base, _, s := fxLoaded(t, nil)
		st := s.passages.(*storePassages).st
		if err := os.WriteFile(base+".delta.tmp", newVectorDelta(8, deltaDTypeF32, "").Marshal(), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMeta(ctx, "pending_vector_file", "idx.delta.tmp"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMeta(ctx, "pending_vector_generation", "2"); err != nil {
			t.Fatal(err)
		}
		if err := s.recordFreshness(ctx); err != nil {
			t.Fatalf("recordFreshness: %v", err)
		}
		if got, _ := st.Meta(ctx, "pending_vector_file"); got != "" {
			t.Errorf("pending_vector_file = %q after recording", got)
		}
		if _, err := os.Stat(base + ".delta"); err != nil {
			t.Errorf("the staged delta was not rolled forward: %v", err)
		}
	})
	t.Run("pending publication unreadable", func(t *testing.T) {
		_, _, _, s := fxLoaded(t, nil)
		st := s.passages.(*storePassages).st
		if err := st.SetMeta(ctx, "pending_vector_file", "idx.delta.tmp"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMeta(ctx, "pending_vector_generation", "junk"); err != nil {
			t.Fatal(err)
		}
		if err := s.recordFreshness(ctx); err == nil || !strings.Contains(err.Error(), "recovering vector publication") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("freshness unreadable", func(t *testing.T) {
		_, _, _, s := fxLoaded(t, nil)
		if err := s.recordFreshness(fxCancelled()); err == nil || !strings.Contains(err.Error(), "recording index freshness") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("document probe fails", func(t *testing.T) {
		_, base, _, s := fxLoaded(t, nil)
		fxExec(t, base, `DROP TABLE doc_nodes`)
		if _, err := s.passages.(*storePassages).freshness(ctx); err == nil {
			t.Fatal("a missing document table must fail the freshness read")
		}
	})
	t.Run("custom passages are not recorded", func(t *testing.T) {
		s := NewSearcher(Config{IndexDir: t.TempDir()}, nil)
		if err := s.recordFreshness(ctx); err != nil {
			t.Fatal(err)
		}
		if _, known := s.loadFresh(); known {
			t.Error("a searcher without a store must not record a state")
		}
	})
}

// RefreshDelta reports a publication it cannot finish and a delta it cannot
// read, and asks for a full reload when the main file changed.
func TestRefreshDeltaFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("pending publication unreadable", func(t *testing.T) {
		_, _, _, s := fxLoaded(t, nil)
		st := s.passages.(*storePassages).st
		if err := st.SetMeta(ctx, "pending_vector_file", "idx.delta.tmp"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMeta(ctx, "pending_vector_generation", "junk"); err != nil {
			t.Fatal(err)
		}
		if err := s.RefreshDelta(ctx); err == nil || !strings.Contains(err.Error(), "recovering vector publication") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("delta unreadable", func(t *testing.T) {
		_, base, _, s := fxLoaded(t, nil)
		if err := os.WriteFile(base+".delta", []byte("not a delta"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.RefreshDelta(ctx); err == nil || !strings.Contains(err.Error(), "repair the index") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("main file replaced", func(t *testing.T) {
		_, base, _, s := fxLoaded(t, nil)
		fxExec(t, base, `UPDATE meta SET value = '41' WHERE key = 'vector_main_generation'`)
		if err := s.RefreshDelta(ctx); err != errNeedsFullReload {
			t.Fatalf("err = %v, want errNeedsFullReload", err)
		}
	})
}

// The delta state of a searcher without a store is empty, and a delta of the
// wrong width or an unreadable slot table is refused.
func TestLoadDeltaStateFailures(t *testing.T) {
	ctx := context.Background()
	if ds, err := (&LeannSearcher{}).loadDeltaState(ctx, "x"); ds != nil || err != nil {
		t.Errorf("loadDeltaState without a store = %v, %v", ds, err)
	}
	t.Run("delta of another width", func(t *testing.T) {
		_, base, _, s := fxLoaded(t, nil)
		d := newVectorDelta(4, deltaDTypeF32, "")
		if err := d.Append(9, make([]float32, 4)); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base+".delta", d.Marshal(), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.loadDeltaState(ctx, base); err == nil || !strings.Contains(err.Error(), "4-dimensional") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("slot span unreadable", func(t *testing.T) {
		_, base, _, s := fxLoaded(t, nil)
		s.passages.(*storePassages).st.Close()
		if _, err := s.loadDeltaState(ctx, base); err == nil {
			t.Fatal("a closed database must fail the delta state")
		}
	})
	t.Run("slot counter unreadable", func(t *testing.T) {
		_, base, _, s := fxLoaded(t, nil)
		fxExec(t, base, `ALTER TABLE meta RENAME TO meta_x`)
		if _, err := s.loadDeltaState(ctx, base); err == nil {
			t.Fatal("an unreadable meta table must fail the delta state")
		}
	})
}

// Loading refuses vector files it cannot read, with the backend path it took.
func TestLoadVectorFilesFailures(t *testing.T) {
	ctx := context.Background()
	noMmap := false
	for _, tc := range []struct {
		name  string
		mmap  *bool
		write []byte
	}{
		{"mapped file damaged", nil, []byte("garbage")},
		{"file in memory damaged", &noMmap, []byte("garbage")},
		{"file in memory missing", &noMmap, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, base, _ := fxIncremental(t)
			if tc.write != nil {
				if err := os.WriteFile(base+".index", tc.write, 0o644); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(base + ".index"); err != nil {
				t.Fatal(err)
			}
			s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw", UseMmap: tc.mmap}, &testEmbedder{dim: 8})
			err := s.Load(ctx, "idx")
			if err == nil {
				s.Close()
				t.Skip("this backend build tolerates the damaged file")
			}
			if s.backend != nil {
				t.Error("a failed load must not leave a half-loaded backend")
			}
		})
	}
}

// A publication that lands while the vector files are read is noticed after
// the load and the state is recorded again.
func TestLoadVectorFilesNoticesAPublicationDuringTheLoad(t *testing.T) {
	ctx := context.Background()
	for _, key := range []string{"vector_generation", "vector_main_generation"} {
		t.Run(key, func(t *testing.T) {
			dir, base, _, s := fxLoaded(t, nil)
			fxExec(t, base, `UPDATE meta SET value = '7' WHERE key = '`+key+`'`)
			if err := s.loadVectorFiles(ctx, filepath.Join(dir, "idx", "idx")); err != nil {
				t.Fatalf("loadVectorFiles: %v", err)
			}
			if c, err := s.Change(ctx); err != nil || c != IndexUnchanged {
				t.Errorf("Change after the load = %v, %v; want the new state recorded", c, err)
			}
		})
	}
	t.Run("no recorded state", func(t *testing.T) {
		dir, _, _, s := fxLoaded(t, nil)
		s.storeFresh(indexFreshness{}, false)
		if err := s.loadVectorFiles(ctx, filepath.Join(dir, "idx", "idx")); err != nil {
			t.Fatal(err)
		}
	})
}

// Delta merging reports a slot lookup that fails.
func TestMergeDeltaHitsReportsALookupFailure(t *testing.T) {
	_, _, _, s := fxLoaded(t, nil)
	s.passages.(*storePassages).st.Close()
	_, _, err := s.mergeDeltaHits(context.Background(), &deltaState{dead: 2, live: 2}, nil, []int64{0, 1}, []float32{0.1, 0.2}, 2, 2)
	if err == nil {
		t.Fatal("a closed database must fail the dead-slot filter")
	}
}
