package memsync_test

import (
	"context"
	"hash/fnv"
	"testing"

	"github.com/tevfik/gleann/internal/memsync"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

// hashEmbedder gives every text its own deterministic vector.
type hashEmbedder struct{ dim int }

func (e hashEmbedder) Compute(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, e.dim)
		for j := range v {
			h := fnv.New32a()
			_, _ = h.Write([]byte{byte(j)})
			_, _ = h.Write([]byte(t))
			v[j] = float32(h.Sum32()%1000) / 1000
		}
		out[i] = v
	}
	return out, nil
}

func (e hashEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	v, err := e.Compute(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

func (e hashEmbedder) Dimensions() int   { return e.dim }
func (e hashEmbedder) ModelName() string { return "hash-test" }

func TestLeannSyncer_NilBuilder_NoOp(t *testing.T) {
	syncer := memsync.NewLeannSyncer(nil, "test", nil)

	ctx := context.Background()
	if err := syncer.AddContent(ctx, "n1", "hello", nil); err != nil {
		t.Fatalf("AddContent with nil builder should not error: %v", err)
	}
	if err := syncer.DeleteContent(ctx, "n1"); err != nil {
		t.Fatalf("DeleteContent with nil builder should not error: %v", err)
	}
}

func TestLeannSyncer_EmptyContent_NoOp(t *testing.T) {
	syncer := memsync.NewLeannSyncer(nil, "test", nil)
	if err := syncer.AddContent(context.Background(), "n2", "", nil); err != nil {
		t.Fatalf("AddContent with empty content should not error: %v", err)
	}
}

// The syncer builds the vector index on the first entity, appends later ones
// and removes an entity's passages by its memory_engine:<id> source. It runs
// in every build, CGO_ENABLED=0 included.
func TestLeannSyncer_AddAndDeleteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	cfg.Backend = "hnsw"
	emb := hashEmbedder{dim: 8}
	b, err := gleann.NewBuilder(cfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	const name = "notes_memory_vectors"
	syncer := memsync.NewLeannSyncer(b, name, emb)
	ctx := context.Background()

	if err := syncer.AddContent(ctx, "e1", "machine learning is a subset of AI", map[string]any{"type": "concept"}); err != nil {
		t.Fatalf("AddContent e1: %v", err)
	}
	if err := syncer.AddContent(ctx, "e2", "neural networks recognise patterns", nil); err != nil {
		t.Fatalf("AddContent e2: %v", err)
	}
	if got := passages(t, dir, name); got != 2 {
		t.Fatalf("after two adds: %d passages, want 2", got)
	}

	if err := syncer.DeleteContent(ctx, "e1"); err != nil {
		t.Fatalf("DeleteContent: %v", err)
	}
	if got := passages(t, dir, name); got != 1 {
		t.Fatalf("after delete: %d passages, want 1", got)
	}
}

func passages(t *testing.T, dir, name string) int {
	t.Helper()
	metas, err := gleann.ListIndexes(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.Name == name {
			return m.NumPassages
		}
	}
	t.Fatalf("index %q not found", name)
	return 0
}
