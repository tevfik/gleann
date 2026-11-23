package memsync_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tevfik/gleann/internal/memsync"
	"github.com/tevfik/gleann/internal/store"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// newInjectedMemoryService wires a real LeannSyncer to an EntityService in a
// temporary directory, the way the REST and MCP memory pools do.
func newInjectedMemoryService(t *testing.T, ns, name string, emb gleann.EmbeddingComputer) (*memory.EntityService, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	cfg.Backend = "hnsw"
	b, err := gleann.NewBuilder(cfg, emb)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	st, err := memory.OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.EntityService(ns, memsync.NewLeannSyncer(b, name, emb)), dir
}

func memoryIndexMeta(t *testing.T, dir, name string) *gleann.IndexMeta {
	t.Helper()
	meta, err := gleann.GetIndexMeta(dir, name)
	if err != nil {
		t.Fatalf("GetIndexMeta(%q): %v", name, err)
	}
	return meta
}

// passagesOf reads the passage rows of a format-2 index back from its store.
func passagesOf(t *testing.T, dir, name string) []store.Passage {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, name, name+".db"), store.Options{})
	if err != nil {
		t.Fatalf("open passage store: %v", err)
	}
	defer st.Close()
	var out []store.Passage
	if err := st.ForEach(context.Background(), func(p store.Passage) error {
		out = append(out, p)
		return nil
	}); err != nil {
		t.Fatalf("read passages: %v", err)
	}
	return out
}

// One inject request is one vector publication, however many entities it
// carries (RFC 005 risk list: "the memory syncer batches entities per tick").
// Every publication bumps vector_generation, which makes cached searchers
// reload the delta; a per-entity publication is the defect this test pins.
func TestInjectEntitiesPublishesVectorIndexOncePerBatch(t *testing.T) {
	emb := hashEmbedder{dim: 8}
	const name = "notes_memory_vectors"
	svc, dir := newInjectedMemoryService(t, "notes", name, emb)

	const n = 1000
	payload := gleann.GraphInjectionPayload{Nodes: make([]gleann.MemoryGraphNode, n)}
	for i := 0; i < n; i++ {
		payload.Nodes[i] = gleann.MemoryGraphNode{
			ID:      fmt.Sprintf("e%04d", i),
			Type:    "concept",
			Content: fmt.Sprintf("entity number %d about topic %d", i, i%97),
		}
	}

	ctx := context.Background()
	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}
	meta := memoryIndexMeta(t, dir, name)
	if meta.VectorGeneration != 1 {
		t.Fatalf("injecting %d entities in one request caused %d vector publications (vector_generation = %d), want 1",
			n, meta.VectorGeneration, meta.VectorGeneration)
	}
	if meta.NumPassages != n {
		t.Fatalf("passages after inject = %d, want %d", meta.NumPassages, n)
	}

	// A repeated request replaces the entities instead of duplicating them,
	// and still costs exactly one publication.
	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("InjectEntities (second round): %v", err)
	}
	meta = memoryIndexMeta(t, dir, name)
	if meta.VectorGeneration != 2 {
		t.Fatalf("two inject requests caused %d vector publications (vector_generation = %d), want 2",
			meta.VectorGeneration, meta.VectorGeneration)
	}
	if meta.NumPassages != n {
		t.Fatalf("passages after re-inject = %d, want %d (duplicated passages)", meta.NumPassages, n)
	}
}

// Searches running while several goroutines inject disjoint batches must keep
// returning each passage's own text (RFC 005 G4: a hit never resolves to the
// wrong passage), every injected entity must stay searchable, and each inject
// request must cost one publication.
func TestInjectEntitiesConcurrentSearchStaysCorrect(t *testing.T) {
	emb := hashEmbedder{dim: 8}
	const name = "concurrent_memory_vectors"
	svc, dir := newInjectedMemoryService(t, "concurrent", name, emb)
	ctx := context.Background()

	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	cfg.Backend = "hnsw"

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{Nodes: []gleann.MemoryGraphNode{
		{ID: "seed", Type: "concept", Content: "seed entity text"},
	}}); err != nil {
		t.Fatalf("seed inject: %v", err)
	}

	newLoadedSearcher := func() *gleann.LeannSearcher {
		t.Helper()
		s := gleann.NewSearcher(cfg, emb)
		if err := s.Load(ctx, name); err != nil {
			t.Fatalf("Load: %v", err)
		}
		return s
	}

	const writers, perWriter = 4, 25
	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			nodes := make([]gleann.MemoryGraphNode, perWriter)
			for i := 0; i < perWriter; i++ {
				id := fmt.Sprintf("w%d-%02d", w, i)
				nodes[i] = gleann.MemoryGraphNode{ID: id, Type: "concept", Content: "entity " + id + " text"}
			}
			if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{Nodes: nodes}); err != nil {
				errCh <- err
			}
		}(w)
	}

	// Search while the injections run. A fresh searcher is opened the first
	// time and again whenever the main vector file changed under it.
	s := newLoadedSearcher()
	searched := 0
	for i := 0; i < 200; i++ {
		results, err := s.Search(ctx, "entity text", gleann.WithTopK(20))
		if errors.Is(err, gleann.ErrIndexChanged) {
			s.Close()
			s = newLoadedSearcher()
			continue
		}
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		searched++
		for _, r := range results {
			id, _ := r.Metadata["memory_node_id"].(string)
			if id == "" || id == "seed" {
				continue
			}
			if want := "entity " + id + " text"; r.Text != want {
				t.Fatalf("hit for %q carries text %q, want %q", id, r.Text, want)
			}
		}
	}
	s.Close()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent InjectEntities: %v", err)
	}
	if searched == 0 {
		t.Fatal("no search completed while injections were running")
	}

	// Every inject request is one publication: four batches plus the seed.
	meta := memoryIndexMeta(t, dir, name)
	if want := writers + 1; meta.VectorGeneration != want {
		t.Fatalf("%d inject requests caused %d vector publications (vector_generation = %d), want %d",
			writers+1, meta.VectorGeneration, meta.VectorGeneration, want)
	}
	if want := writers*perWriter + 1; meta.NumPassages != want {
		t.Fatalf("passages = %d, want %d", meta.NumPassages, want)
	}

	// And every stored passage is exactly the entity it belongs to.
	rows := passagesOf(t, dir, name)
	if len(rows) != writers*perWriter+1 {
		t.Fatalf("stored passages = %d, want %d", len(rows), writers*perWriter+1)
	}
	for _, p := range rows {
		id := strings.TrimPrefix(p.Source, "memory_engine:")
		if id == p.Source {
			t.Fatalf("passage source %q is not a memory entity source", p.Source)
		}
		if id == "seed" {
			if p.Text != "seed entity text" {
				t.Fatalf("seed passage text = %q", p.Text)
			}
			continue
		}
		if want := "entity " + id + " text"; p.Text != want {
			t.Fatalf("passage for %q has text %q, want %q", id, p.Text, want)
		}
	}
}

// memoryIndexes lists every index under dir; a batch that publishes nothing
// must not create one.
func memoryIndexes(t *testing.T, dir string) []gleann.IndexMeta {
	t.Helper()
	metas, err := gleann.ListIndexes(dir)
	if err != nil {
		t.Fatalf("ListIndexes(%q): %v", dir, err)
	}
	return metas
}

// A duplicate cleared within one batch keeps its last content in the database,
// so the syncer must publish nothing for it rather than the earlier text.
// A direct AddContents call carries the same last-wins contract as
// InjectEntities, because the database upsert it mirrors is last-wins.
func TestLeannSyncerAddContentsLastWinsOnClearedEntity(t *testing.T) {
	ctx := context.Background()
	const name = "cleared_direct_memory_vectors"
	s, dir := newSyncer(t, hashEmbedder{dim: 8}, name)

	if err := s.AddContents(ctx, []memsync.Content{
		{NodeID: "x", Content: "v1"},
		{NodeID: "x", Content: ""},
	}); err != nil {
		t.Fatalf("AddContents: %v", err)
	}

	if metas := memoryIndexes(t, dir); len(metas) != 0 {
		t.Fatalf("a cleared-only batch published %d indexes (%+v), want 0", len(metas), metas)
	}
}

// The cleared duplicate must not suppress the other entities of the batch.
func TestLeannSyncerAddContentsLastWinsKeepsOtherEntities(t *testing.T) {
	ctx := context.Background()
	const name = "cleared_mixed_memory_vectors"
	s, dir := newSyncer(t, hashEmbedder{dim: 8}, name)

	if err := s.AddContents(ctx, []memsync.Content{
		{NodeID: "x", Content: "v1"},
		{NodeID: "x", Content: ""},
		{NodeID: "y", Content: "ok"},
	}); err != nil {
		t.Fatalf("AddContents: %v", err)
	}

	meta := memoryIndexMeta(t, dir, name)
	if meta.VectorGeneration != 1 || meta.NumPassages != 1 {
		t.Fatalf("vector_generation = %d, passages = %d; want 1, 1",
			meta.VectorGeneration, meta.NumPassages)
	}
	rows := passagesOf(t, dir, name)
	if len(rows) != 1 || rows[0].Source != "memory_engine:y" || rows[0].Text != "ok" {
		t.Fatalf("passages = %+v, want only memory_engine:y = %q", rows, "ok")
	}
}

// Last occurrence wins in either direction: empty first, content last.
func TestLeannSyncerAddContentsLaterContentWins(t *testing.T) {
	ctx := context.Background()
	const name = "later_direct_memory_vectors"
	s, dir := newSyncer(t, hashEmbedder{dim: 8}, name)

	if err := s.AddContents(ctx, []memsync.Content{
		{NodeID: "x", Content: ""},
		{NodeID: "x", Content: "v1"},
	}); err != nil {
		t.Fatalf("AddContents: %v", err)
	}

	rows := passagesOf(t, dir, name)
	if len(rows) != 1 || rows[0].Source != "memory_engine:x" || rows[0].Text != "v1" {
		t.Fatalf("passages = %+v, want only memory_engine:x = %q", rows, "v1")
	}
}

// The same contract end to end through InjectEntities with a real syncer: the
// committed entity has empty content, so nothing is published -- no index at
// all, hence vector_generation 0.
func TestInjectEntitiesLastWinsOnClearedEntityPublishesNothing(t *testing.T) {
	emb := hashEmbedder{dim: 8}
	const name = "cleared_inject_memory_vectors"
	svc, dir := newInjectedMemoryService(t, "cleared", name, emb)

	if err := svc.InjectEntities(context.Background(), gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "x", Type: "concept", Content: "v1"},
			{ID: "x", Type: "concept", Content: ""},
		},
	}); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	if metas := memoryIndexes(t, dir); len(metas) != 0 {
		t.Fatalf("a cleared-only inject published %d indexes (%+v), want 0", len(metas), metas)
	}
}

// A cleared duplicate must not suppress or corrupt the other entities of the
// same inject request.
func TestInjectEntitiesLastWinsKeepsOtherEntities(t *testing.T) {
	emb := hashEmbedder{dim: 8}
	const name = "cleared_mixed_inject_memory_vectors"
	svc, dir := newInjectedMemoryService(t, "cleared-mixed", name, emb)

	if err := svc.InjectEntities(context.Background(), gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "x", Type: "concept", Content: "v1"},
			{ID: "x", Type: "concept", Content: ""},
			{ID: "y", Type: "concept", Content: "ok"},
		},
	}); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	meta := memoryIndexMeta(t, dir, name)
	if meta.VectorGeneration != 1 || meta.NumPassages != 1 {
		t.Fatalf("vector_generation = %d, passages = %d; want 1, 1",
			meta.VectorGeneration, meta.NumPassages)
	}
	rows := passagesOf(t, dir, name)
	if len(rows) != 1 || rows[0].Source != "memory_engine:y" || rows[0].Text != "ok" {
		t.Fatalf("passages = %+v, want only memory_engine:y = %q", rows, "ok")
	}
}

// Empty first, content last: the later text is what the database committed,
// so it is what the vector index must receive.
func TestInjectEntitiesLaterContentWins(t *testing.T) {
	emb := hashEmbedder{dim: 8}
	const name = "later_inject_memory_vectors"
	svc, dir := newInjectedMemoryService(t, "later", name, emb)

	if err := svc.InjectEntities(context.Background(), gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "x", Type: "concept", Content: ""},
			{ID: "x", Type: "concept", Content: "v1"},
		},
	}); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	meta := memoryIndexMeta(t, dir, name)
	if meta.VectorGeneration != 1 || meta.NumPassages != 1 {
		t.Fatalf("vector_generation = %d, passages = %d; want 1, 1",
			meta.VectorGeneration, meta.NumPassages)
	}
	rows := passagesOf(t, dir, name)
	if len(rows) != 1 || rows[0].Source != "memory_engine:x" || rows[0].Text != "v1" {
		t.Fatalf("passages = %+v, want only memory_engine:x = %q", rows, "v1")
	}
}
