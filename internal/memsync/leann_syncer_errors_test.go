package memsync_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/memsync"
	"github.com/tevfik/gleann/pkg/gleann"
)

// failingEmbedder fails every embedding call, so a write that reaches the
// embedding step reports the error instead of publishing anything.
type failingEmbedder struct{ hashEmbedder }

var errEmbed = errors.New("embedder down")

func (failingEmbedder) Compute(context.Context, []string) ([][]float32, error) {
	return nil, errEmbed
}

func (failingEmbedder) ComputeSingle(context.Context, string) ([]float32, error) {
	return nil, errEmbed
}

func newSyncer(t *testing.T, emb gleann.EmbeddingComputer, name string) (*memsync.LeannSyncer, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	cfg.Backend = "hnsw"
	b, err := gleann.NewBuilder(cfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	return memsync.NewLeannSyncer(b, name, emb), dir
}

func TestLeannSyncer_AddContentsNoOps(t *testing.T) {
	ctx := context.Background()
	// No builder: nothing to do, no error.
	if err := memsync.NewLeannSyncer(nil, "x", nil).AddContents(ctx, []memsync.Content{{NodeID: "a", Content: "t"}}); err != nil {
		t.Fatalf("nil builder: %v", err)
	}
	// Only empty contents: no index is created.
	s, dir := newSyncer(t, failingEmbedder{hashEmbedder{dim: 8}}, "empty_memory_vectors")
	if err := s.AddContents(ctx, []memsync.Content{{NodeID: "a"}, {NodeID: "b"}}); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	metas, err := gleann.ListIndexes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("an empty batch created %d indexes", len(metas))
	}
}

// When the first write of an index fails (the index does not exist yet, so
// the syncer falls back to Build), the build error is reported with the node
// it was for. Build is two-phase (passages first, then vectors), so what is
// left behind is at most a lexical index that is not marked vector-ready.
func TestLeannSyncer_AddContentBuildErrorIsReported(t *testing.T) {
	ctx := context.Background()
	s, dir := newSyncer(t, failingEmbedder{hashEmbedder{dim: 8}}, "fail_memory_vectors")
	err := s.AddContent(ctx, "n1", "some content", nil)
	if err == nil {
		t.Fatal("AddContent succeeded with a failing embedder")
	}
	if !errors.Is(err, errEmbed) || !strings.Contains(err.Error(), `"n1"`) {
		t.Fatalf("error %q does not wrap the embedder error or name the node", err)
	}
	err = s.AddContents(ctx, []memsync.Content{{NodeID: "a", Content: "x"}, {NodeID: "b", Content: "y"}})
	if !errors.Is(err, errEmbed) || !strings.Contains(err.Error(), "2 entities") {
		t.Fatalf("AddContents error = %v, want embedder error for 2 entities", err)
	}
	metas, _ := gleann.ListIndexes(dir)
	for _, m := range metas {
		if m.Name == "fail_memory_vectors" && m.VectorReady {
			t.Fatal("a failed embedding left the index marked vector-ready")
		}
	}
}

// Deleting an entity from an index that does not exist yet is not an error:
// there is nothing to remove.
func TestLeannSyncer_DeleteFromMissingIndex(t *testing.T) {
	s, _ := newSyncer(t, hashEmbedder{dim: 8}, "never_built_memory_vectors")
	if err := s.DeleteContent(context.Background(), "ghost"); err != nil {
		t.Fatalf("DeleteContent on a missing index: %v", err)
	}
}

// Attributes may not override the reserved source and node-id keys, which
// tie a passage back to its entity: deleting by node id still finds it.
func TestLeannSyncer_ReservedAttrsAreNotOverridden(t *testing.T) {
	ctx := context.Background()
	const name = "attrs_memory_vectors"
	s, dir := newSyncer(t, hashEmbedder{dim: 8}, name)
	attrs := map[string]any{"source": "spoofed", "memory_node_id": "other", "kind": "fact"}
	if err := s.AddContent(ctx, "e1", "first entity", attrs); err != nil {
		t.Fatal(err)
	}
	if err := s.AddContents(ctx, []memsync.Content{{NodeID: "e2", Content: "second entity", Attrs: attrs}}); err != nil {
		t.Fatal(err)
	}
	if got := passages(t, dir, name); got != 2 {
		t.Fatalf("%d passages, want 2", got)
	}
	for _, id := range []string{"e1", "e2"} {
		if err := s.DeleteContent(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if got := passages(t, dir, name); got != 0 {
		t.Fatalf("after deleting both entities by node id: %d passages, want 0", got)
	}
}

// A cancelled context makes an update of an existing index fail; the error
// is wrapped with the node it was for.
func TestLeannSyncer_ErrorsOnExistingIndex(t *testing.T) {
	const name = "cancel_memory_vectors"
	s, _ := newSyncer(t, hashEmbedder{dim: 8}, name)
	if err := s.AddContent(context.Background(), "e1", "seed", nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.AddContent(ctx, "e2", "more", nil); err == nil || !strings.Contains(err.Error(), `"e2"`) {
		t.Fatalf("AddContent with cancelled ctx = %v, want error naming e2", err)
	}
	if err := s.DeleteContent(ctx, "e1"); err == nil || !strings.Contains(err.Error(), `"e1"`) {
		t.Fatalf("DeleteContent with cancelled ctx = %v, want error naming e1", err)
	}
}
