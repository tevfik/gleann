package memory_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// batchRecordingSyncer records both the batch and the per-entity calls, so a
// test can tell which path EntityService took.
type batchRecordingSyncer struct {
	mu       sync.Mutex
	added    []string
	batches  [][]string
	payloads [][]gleann.VectorContent
	fail     error
}

func (b *batchRecordingSyncer) AddContent(_ context.Context, nodeID, _ string, _ map[string]any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.added = append(b.added, nodeID)
	return nil
}

func (b *batchRecordingSyncer) DeleteContent(context.Context, string) error { return nil }

func (b *batchRecordingSyncer) AddContents(_ context.Context, contents []gleann.VectorContent) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail != nil {
		return b.fail
	}
	ids := make([]string, 0, len(contents))
	for _, c := range contents {
		ids = append(ids, c.NodeID)
	}
	b.batches = append(b.batches, ids)
	b.payloads = append(b.payloads, append([]gleann.VectorContent(nil), contents...))
	return nil
}

// vectorOnlySyncer implements gleann.VectorSyncer but not the batch extension,
// so EntityService takes the per-entity path.
type vectorOnlySyncer struct {
	mu       sync.Mutex
	added    []string
	contents []string
}

func (v *vectorOnlySyncer) AddContent(_ context.Context, nodeID, content string, _ map[string]any) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.added = append(v.added, nodeID)
	v.contents = append(v.contents, content)
	return nil
}

func (v *vectorOnlySyncer) DeleteContent(context.Context, string) error { return nil }

// A syncer that can batch gets ONE call for the whole inject request instead
// of one per entity (RFC 005: every publication bumps vector_generation).
func TestEntityService_InjectEntitiesBatchesVectorSync(t *testing.T) {
	ctx := context.Background()
	syncer := &batchRecordingSyncer{}
	svc, _ := newTestEntityService(t, "batch", syncer)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "vs1", Type: "doc", Content: "Some content to vectorize"},
			{ID: "vs2", Type: "tag"}, // empty content -> skipped
			{ID: "vs3", Type: "doc", Content: "More content"},
		},
	}
	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	if len(syncer.batches) != 1 {
		t.Fatalf("AddContents called %d times, want 1", len(syncer.batches))
	}
	if got := syncer.batches[0]; len(got) != 2 || got[0] != "vs1" || got[1] != "vs3" {
		t.Fatalf("AddContents got %v, want [vs1 vs3]", got)
	}
	if len(syncer.added) != 0 {
		t.Fatalf("AddContent called for %v although the syncer batches", syncer.added)
	}
}

// An inject without any content must not publish anything at all.
func TestEntityService_InjectEntitiesBatchSkipsEmptyContent(t *testing.T) {
	ctx := context.Background()
	syncer := &batchRecordingSyncer{}
	svc, _ := newTestEntityService(t, "batch-empty", syncer)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{Nodes: []gleann.MemoryGraphNode{
		{ID: "a", Type: "tag"},
		{ID: "b", Type: "tag"},
	}}); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}
	if len(syncer.batches) != 0 || len(syncer.added) != 0 {
		t.Fatalf("a content-less inject published: batches=%v added=%v", syncer.batches, syncer.added)
	}
}

// A failed batch is reported after the commit (inject is idempotent, so the
// caller can retry); it is not dropped silently.
func TestEntityService_InjectEntitiesBatchErrorIsReported(t *testing.T) {
	ctx := context.Background()
	syncer := &batchRecordingSyncer{fail: errors.New("embedding server down")}
	svc, _ := newTestEntityService(t, "batch-fail", syncer)

	err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{Nodes: []gleann.MemoryGraphNode{
		{ID: "n1", Type: "doc", Content: "one"},
		{ID: "n2", Type: "doc", Content: "two"},
	}})
	if err == nil || !strings.Contains(err.Error(), "vector sync 2 entities") {
		t.Fatalf("err = %v, want the batch vector sync error", err)
	}
	nodes, _, terr := svc.Traverse(ctx, "n1", 0)
	if terr != nil || len(nodes) != 1 || nodes[0].ID != "n1" {
		t.Fatalf("the rows must be committed before the vector sync: nodes=%+v err=%v", nodes, terr)
	}
}

// One inject request may name the same entity more than once. The SQLite
// upsert is last-wins, so the vector publish must use the last occurrence too:
// clearing an entity's content in the same request publishes nothing for it,
// never the earlier text it just replaced.
func TestEntityService_InjectEntitiesBatchLastWinsOnClearedEntity(t *testing.T) {
	ctx := context.Background()
	syncer := &batchRecordingSyncer{}
	svc, store := newTestEntityService(t, "batch-clear", syncer)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{Nodes: []gleann.MemoryGraphNode{
		{ID: "x", Type: "concept", Content: "v1"},
		{ID: "x", Type: "concept", Content: ""},
	}}); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	var stored string
	if err := store.ReaderForTest().QueryRow(
		`SELECT content FROM entities WHERE ns = 'batch-clear' AND id = 'x'`).Scan(&stored); err != nil {
		t.Fatalf("read committed entity: %v", err)
	}
	if stored != "" {
		t.Fatalf("committed content = %q, want empty (last-wins)", stored)
	}
	if len(syncer.payloads) != 0 {
		t.Fatalf("published %v although the committed entity has no content", syncer.payloads)
	}
}

// A cleared duplicate must not suppress the other entities of the same request.
func TestEntityService_InjectEntitiesBatchLastWinsKeepsOtherEntities(t *testing.T) {
	ctx := context.Background()
	syncer := &batchRecordingSyncer{}
	svc, _ := newTestEntityService(t, "batch-clear-other", syncer)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{Nodes: []gleann.MemoryGraphNode{
		{ID: "x", Type: "concept", Content: "v1"},
		{ID: "x", Type: "concept", Content: ""},
		{ID: "y", Type: "concept", Content: "ok"},
	}}); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	if len(syncer.payloads) != 1 {
		t.Fatalf("AddContents called %d times, want 1", len(syncer.payloads))
	}
	got := syncer.payloads[0]
	if len(got) != 1 || got[0].NodeID != "y" || got[0].Content != "ok" {
		t.Fatalf("AddContents got %+v, want only {y ok}", got)
	}
}

// The last occurrence wins in either direction: empty first, content last.
func TestEntityService_InjectEntitiesBatchLaterContentWins(t *testing.T) {
	ctx := context.Background()
	syncer := &batchRecordingSyncer{}
	svc, _ := newTestEntityService(t, "batch-later", syncer)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{Nodes: []gleann.MemoryGraphNode{
		{ID: "x", Type: "concept", Content: ""},
		{ID: "x", Type: "concept", Content: "v1"},
	}}); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	if len(syncer.payloads) != 1 {
		t.Fatalf("AddContents called %d times, want 1", len(syncer.payloads))
	}
	got := syncer.payloads[0]
	if len(got) != 1 || got[0].NodeID != "x" || got[0].Content != "v1" {
		t.Fatalf("AddContents got %+v, want only {x v1}", got)
	}
}

// The per-entity fallback must keep the same last-wins contract as the batch
// path: a duplicate cleared in the request is not synced at all.
func TestEntityService_InjectEntitiesPerEntityLastWins(t *testing.T) {
	ctx := context.Background()
	syncer := &vectorOnlySyncer{}
	svc, _ := newTestEntityService(t, "per-entity", syncer)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{Nodes: []gleann.MemoryGraphNode{
		{ID: "x", Type: "concept", Content: "v1"},
		{ID: "x", Type: "concept", Content: ""},
		{ID: "y", Type: "concept", Content: "ok"},
	}}); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	if len(syncer.added) != 1 || syncer.added[0] != "y" || syncer.contents[0] != "ok" {
		t.Fatalf("AddContent called for %v with %v, want only {y ok}", syncer.added, syncer.contents)
	}
}
