package gleann

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hookEmbedder runs a hook inside Compute, the moment another process
// could change the index between a builder's read and its write.
type hookEmbedder struct {
	*testEmbedder
	onCompute func()
	progress  func(done, total int)
}

func (e *hookEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	if e.onCompute != nil {
		e.onCompute()
	}
	if e.progress != nil {
		e.progress(len(texts), len(texts))
	}
	return e.testEmbedder.Compute(ctx, texts)
}

func (e *hookEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.Compute(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (e *hookEmbedder) SetProgressCallback(cb func(done, total int)) { e.progress = cb }

// fxChanged rewrites b.md.
func fxChanged() []Item {
	return []Item{{Text: "banana bread with walnuts", Metadata: map[string]any{"source": "b.md", "chunk_index": 0}}}
}

// fxIncremental builds the standard index with the default limits, so an
// update takes the incremental (delta) path.
func fxIncremental(t *testing.T) (string, string, *LeannBuilder) {
	t.Helper()
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	if err := b.Build(context.Background(), "idx", f2Items()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return dir, filepath.Join(dir, "idx", "idx"), b
}

// updatedHealthy runs an update that the incremental path hands back to
// the full rebuild and checks the index is healthy and searchable afterwards.
func updatedHealthy(t *testing.T, dir string, b *LeannBuilder, items []Item, remove []string, texts []string) {
	t.Helper()
	if err := b.UpdateIndex(context.Background(), "idx", items, remove); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}
	f2Healthy(t, dir)
	f2SelfRetrieval(t, dir, &testEmbedder{dim: 8}, texts)
}

// An update the incremental path cannot apply falls back to the full rebuild,
// which produces the same correct index.
func TestIncrementalFallsBackToTheFullRebuild(t *testing.T) {
	want := f2UpdateTexts()
	cases := []struct {
		name   string
		damage func(t *testing.T, base string)
	}{
		{"main vector file missing", func(t *testing.T, base string) {
			if err := os.Remove(base + ".index"); err != nil {
				t.Fatal(err)
			}
		}},
		{"delta file unreadable", func(t *testing.T, base string) {
			if err := os.WriteFile(base+".delta", []byte("not a delta"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"delta of another width", func(t *testing.T, base string) {
			if err := os.WriteFile(base+".delta", newVectorDelta(4, deltaDTypeF32, "").Marshal(), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, base, b := fxIncremental(t)
			tc.damage(t, base)
			updatedHealthy(t, dir, b, f2UpdateItems(), nil, want)
		})
	}
	t.Run("no passages left in a vector-ready index", func(t *testing.T) {
		dir, base, b := fxIncremental(t)
		fxExec(t, base, `DELETE FROM passages`)
		items := f2UpdateItems()
		if err := b.UpdateIndex(context.Background(), "idx", items, nil); err != nil {
			t.Fatalf("UpdateIndex: %v", err)
		}
		if got := len(f2Rows(t, base)); got != len(items) {
			t.Errorf("passages = %d, want %d", got, len(items))
		}
		f2Healthy(t, dir)
	})
}

// The incremental path reports a database it cannot read.
func TestIncrementalReportsUnreadableSlots(t *testing.T) {
	cases := []struct {
		name   string
		damage string
		items  []Item
		remove []string
		want   string
	}{
		{"slot span", `ALTER TABLE passages RENAME COLUMN vec_slot TO vec_slot_x`, f2UpdateItems(), nil, "slot span"},
		{"slots of removed sources", `ALTER TABLE passages RENAME COLUMN source TO source_x`, nil, []string{"c.md"}, "listing slots of sources"},
		{"slots of changed sources", `ALTER TABLE passages RENAME COLUMN source TO source_x`, f2UpdateItems(), nil, "listing slots of sources"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, base, b := fxIncremental(t)
			fxExec(t, base, tc.damage)
			err := b.UpdateIndex(context.Background(), "idx", tc.items, tc.remove)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// A recorded float16 delta stays float16 across an incremental update.
func TestIncrementalKeepsTheRecordedDeltaDType(t *testing.T) {
	dir, base, b := fxIncremental(t)
	if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.VectorDType = deltaDTypeF16 }); err != nil {
		t.Fatal(err)
	}
	st, err := b.UpdateIndexWithStats(context.Background(), "idx", f2UpdateItems(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode != UpdateInPlace {
		t.Fatalf("mode = %v, want in place", st.Mode)
	}
	d, err := readVectorDelta(base + ".delta")
	if err != nil || d == nil || d.Len() == 0 {
		t.Fatalf("delta after the update: %+v, %v", d, err)
	}
}

// Embeddings that do not fit the index are refused before any file changes,
// on the incremental path and on the merge path alike.
func TestIncrementalRejectsBadEmbeddings(t *testing.T) {
	cases := []struct {
		name string
		emb  *badEmbedder
		want string
	}{
		{"error", &badEmbedder{err: os.ErrDeadlineExceeded}, "compute embeddings"},
		{"too few vectors", &badEmbedder{vecs: func(n int) [][]float32 { return make([][]float32, n-1) }}, "vectors for"},
		{"other model dimensions", &badEmbedder{vecs: vecsOfDims(4)}, "dimension"},
		{"ragged", &badEmbedder{vecs: vecsOfDims(8, 3)}, "embedding row 1 has 3 dims"},
	}
	for _, mode := range []string{"incremental", "merge"} {
		for _, tc := range cases {
			t.Run(mode+" "+tc.name, func(t *testing.T) {
				var b *LeannBuilder
				if mode == "merge" {
					_, _, b = fxBuilt(t)
				} else {
					_, _, b = fxIncremental(t)
				}
				var phases []string
				b.SetProgressCallback(func(phase string, done, total int) { phases = append(phases, phase) })
				b.embedder = tc.emb
				err := b.UpdateIndex(context.Background(), "idx", f2UpdateItems(), nil)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want %q", err, tc.want)
				}
				if tc.emb.err == nil && len(phases) == 0 {
					t.Error("the embedder's progress was not forwarded")
				}
			})
		}
	}
}

// A writer that slips in between the read and the write of an update is
// detected, and the update reports it instead of reusing taken slots.
func TestUpdateDetectsAConcurrentWriter(t *testing.T) {
	for _, mode := range []string{"incremental", "merge"} {
		t.Run(mode, func(t *testing.T) {
			var base string
			var b *LeannBuilder
			if mode == "merge" {
				_, base, b = fxBuilt(t)
			} else {
				_, base, b = fxIncremental(t)
			}
			b.embedder = &hookEmbedder{testEmbedder: &testEmbedder{dim: 8}, onCompute: func() {
				fxExec(t, base, `UPDATE meta SET value = '99' WHERE key = 'vector_slots'`)
			}}
			err := b.UpdateIndex(context.Background(), "idx", f2UpdateItems(), nil)
			if err == nil || !strings.Contains(err.Error(), "the index changed while it was being updated") {
				t.Fatalf("err = %v", err)
			}
			if got := f2Meta(t, base, "pending_vector_file"); got != "" {
				t.Errorf("pending_vector_file = %q after the refused update", got)
			}
		})
	}
}

// Each database step of the incremental transaction names its failure.
func TestIncrementalTransactionFailures(t *testing.T) {
	cases := []struct {
		name   string
		ddl    string
		items  []Item
		remove []string
		want   string
	}{
		{"removing a dropped source", fxFail("_del", "DELETE", "passages", "OLD.source = 'c.md'"), nil, []string{"c.md"}, `removing source "c.md"`},
		{"removing a changed source", fxFail("_del", "DELETE", "passages", "OLD.source = 'b.md'"), fxChanged(), nil, `removing source "b.md"`},
		{"writing a new source", fxFail("_ins", "INSERT", "passages", "NEW.source = 'z.md'"), []Item{{Text: "zucchini loaf", Metadata: map[string]any{"source": "z.md"}}}, nil, `applying source "z.md"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, base, b := fxIncremental(t)
			fxExec(t, base, tc.ddl)
			err := b.UpdateIndex(context.Background(), "idx", tc.items, tc.remove)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if got := f2Meta(t, base, "vector_generation"); got != "1" {
				t.Errorf("vector_generation = %q after a rolled-back update, want 1", got)
			}
		})
	}
}

// The merge path (delta over its limit) fails per step and falls back to the
// full rebuild when the stored vectors cannot be reused.
func TestMergeFailuresAndFallbacks(t *testing.T) {
	changed := fxChanged()
	t.Run("transaction steps", func(t *testing.T) {
		cases := []struct {
			name   string
			ddl    string
			items  []Item
			remove []string
			want   string
		}{
			{"removing a dropped source", fxFail("_del", "DELETE", "passages", "OLD.source = 'c.md'"), changed, []string{"c.md"}, `removing source "c.md"`},
			{"removing a changed source", fxFail("_del", "DELETE", "passages", "OLD.source = 'b.md'"), changed, nil, `removing source "b.md"`},
			{"renumbering the slots", fxFail("_upd", "UPDATE", "passages", ""), changed, nil, "reassigning slots"},
			{"writing the new passages", fxFail("_ins", "INSERT", "passages", "NEW.source = 'b.md'"), changed, nil, `applying source "b.md"`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, base, b := fxBuilt(t)
				fxExec(t, base, tc.ddl)
				err := b.UpdateIndex(context.Background(), "idx", tc.items, tc.remove)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v, want %q", err, tc.want)
				}
			})
		}
	})
	t.Run("backend fails", func(t *testing.T) {
		_, _, b := fxBuilt(t)
		b.backend = failingBuildBackend{b.backend}
		err := b.UpdateIndex(context.Background(), "idx", changed, nil)
		if err == nil || !strings.Contains(err.Error(), "build index: backend refused") {
			t.Fatalf("err = %v", err)
		}
	})
	fallbacks := []struct {
		name   string
		damage func(t *testing.T, b *LeannBuilder, base string)
	}{
		{"unknown backend", func(t *testing.T, b *LeannBuilder, base string) { b.config.Backend = "no-such-backend" }},
		{"main file damaged", func(t *testing.T, b *LeannBuilder, base string) {
			if err := os.WriteFile(base+".index", []byte("garbage"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"main file shorter than the slots", func(t *testing.T, b *LeannBuilder, base string) {
			vecs, _ := (&testEmbedder{dim: 8}).Compute(context.Background(), []string{"x", "yy"})
			data, err := b.backend.Build(context.Background(), vecs)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(base+".index", data, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range fallbacks {
		t.Run("falls back: "+tc.name, func(t *testing.T) {
			_, base, b := fxBuilt(t)
			tc.damage(t, b, base)
			st, err := b.UpdateIndexWithStats(context.Background(), "idx", changed, nil)
			if err != nil {
				t.Fatalf("UpdateIndex: %v", err)
			}
			if st.Mode == UpdateMerge {
				t.Errorf("mode = %v: the merge cannot reuse these stored vectors", st.Mode)
			}
			if rows := f2Rows(t, base); len(rows) != 4 {
				t.Errorf("passages = %d, want 4", len(rows))
			}
		})
	}
}
