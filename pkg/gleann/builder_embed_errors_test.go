package gleann

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// badEmbedder returns whatever vectors (or error) the test sets, and reports
// progress like the real embedders.
type badEmbedder struct {
	vecs     func(n int) [][]float32
	err      error
	progress func(int, int)
}

func (e *badEmbedder) Compute(_ context.Context, texts []string) ([][]float32, error) {
	if e.progress != nil {
		e.progress(len(texts), len(texts))
	}
	if e.err != nil {
		return nil, e.err
	}
	return e.vecs(len(texts)), nil
}
func (e *badEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	v, err := e.Compute(ctx, []string{text})
	if err != nil || len(v) == 0 {
		return nil, err
	}
	return v[0], nil
}
func (e *badEmbedder) Dimensions() int                              { return 4 }
func (e *badEmbedder) ModelName() string                            { return "bad-model" }
func (e *badEmbedder) SetProgressCallback(cb func(done, total int)) { e.progress = cb }

func vecsOfDims(dims ...int) func(int) [][]float32 {
	return func(n int) [][]float32 {
		out := make([][]float32, n)
		for i := range out {
			out[i] = make([]float32, dims[i%len(dims)])
			for j := range out[i] {
				out[i][j] = float32(i+j) + 1
			}
		}
		return out
	}
}

// A broken embedder fails the vector phase with a clear error, and the
// index stays lexical-only: its passages are searchable by keyword and no
// vector file is published.
func TestBuildRejectsBadEmbeddings(t *testing.T) {
	cases := []struct {
		name string
		emb  *badEmbedder
		want string
	}{
		{"embedder error", &badEmbedder{err: errors.New("provider down")}, "provider down"},
		{"no vectors", &badEmbedder{vecs: func(int) [][]float32 { return nil }}, "returned 0 vectors"},
		{"zero dimensions", &badEmbedder{vecs: vecsOfDims(0)}, "zero-dimensional"},
		{"ragged dimensions", &badEmbedder{vecs: vecsOfDims(4, 4, 3)}, "embedding row 2 has 3 dims"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			initTestHNSW()
			b, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, tc.emb)
			if err != nil {
				t.Fatal(err)
			}
			var phases []string
			b.SetProgressCallback(func(phase string, done, total int) { phases = append(phases, phase) })
			err = b.Build(context.Background(), "bad", f2Items())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(phases) == 0 {
				t.Error("the embedder's progress was not forwarded")
			}
			m, err := GetIndexMeta(dir, "bad")
			if err != nil {
				t.Fatal(err)
			}
			if m.VectorReady || !m.LexicalOnly {
				t.Fatalf("meta after a failed vector phase: ready %v lexical-only %v", m.VectorReady, m.LexicalOnly)
			}
			if h, err := CheckIndexHealth(dir, "bad"); err != nil || h.NeedsRepair() {
				t.Fatalf("a lexical-only index after a failed embed must be healthy: %+v, %v", h, err)
			}
		})
	}
}

func TestBuilderArgumentErrors(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if _, err := b.BuildPassages("x", nil); err == nil {
		t.Fatal("no items accepted")
	}
	if _, err := b.BuildPassages("../x", f2Items()); err == nil {
		t.Fatal("a path name accepted")
	}
	if err := b.BuildVectors(ctx, "../x", f2Items(), nil); err == nil {
		t.Fatal("BuildVectors accepted a path name")
	}
	if err := b.BuildVectors(ctx, "absent", f2Items(), []int64{1, 2, 3, 4}); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("BuildVectors of a missing index: %v", err)
	}
	if err := b.UpdateIndex(ctx, "../x", nil, nil); err == nil {
		t.Fatal("UpdateIndex accepted a path name")
	}
	if err := b.UpdateIndex(ctx, "absent", nil, []string{"a.md"}); err != nil {
		t.Fatalf("removing from a missing index is a no-op: %v", err)
	}
	if err := b.UpdateIndex(ctx, "absent", f2Items(), nil); !errors.Is(err, ErrIndexNotFound) {
		t.Fatalf("adding to a missing index: %v", err)
	}
	if _, err := b.Compact(ctx, "../x"); err == nil {
		t.Fatal("Compact accepted a path name")
	}

	noEmb, _ := newFormat2Builder(t, dir, 8)
	noEmb.embedder = nil
	if err := noEmb.BuildVectors(ctx, "x", f2Items(), nil); err == nil || !strings.Contains(err.Error(), "no embedder") {
		t.Fatalf("BuildVectors without embedder: %v", err)
	}
	if err := noEmb.UpdateIndex(ctx, "x", nil, nil); err == nil || !strings.Contains(err.Error(), "no embedder") {
		t.Fatalf("UpdateIndex without embedder: %v", err)
	}
	if _, err := noEmb.Compact(ctx, "x"); err == nil || !strings.Contains(err.Error(), "no embedder") {
		t.Fatalf("Compact without embedder: %v", err)
	}

	if _, err := NewBuilder(Config{IndexDir: dir, Backend: "no-such-backend"}, nil); err == nil {
		t.Fatal("an unknown backend was accepted")
	}
	initTestHNSW()
	bad := Config{IndexDir: dir, Backend: "hnsw"}
	bad.HNSWConfig.VectorDType = "f13"
	if _, err := NewBuilder(bad, nil); err == nil {
		t.Fatal("an unknown vector dtype was accepted")
	}
}

func TestBuildFromTextsAndChunker(t *testing.T) {
	dir := t.TempDir()
	b, emb := newFormat2Builder(t, dir, 8)
	b.SetChunker(nil) // the default chunker stays in charge
	if err := b.BuildFromTexts(context.Background(), "texts", []string{"first plain text", "second plain text, longer"}); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
	if err := s.Load(context.Background(), "texts"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rs, err := s.SearchBM25(context.Background(), "longer", 3)
	if err != nil || len(rs) != 1 || rs[0].Text != "second plain text, longer" {
		t.Fatalf("SearchBM25 = %+v, %v", rs, err)
	}
}
