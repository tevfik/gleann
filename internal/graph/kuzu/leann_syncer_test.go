//go:build treesitter && !windows

package kuzu_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── Mock Builder for LeannSyncer tests ────────────────────────────────────────

type mockLeannBuilder struct {
	mu          sync.Mutex
	addedItems  []gleann.Item
	addedIndex  string
	removeSrcs  []string
	removeIndex string
	addErr      error
	updateErr   error
}

func (m *mockLeannBuilder) recordAdd(name string, items []gleann.Item) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addedIndex = name
	m.addedItems = append(m.addedItems, items...)
}

func (m *mockLeannBuilder) recordRemove(name string, sources []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeIndex = name
	m.removeSrcs = append(m.removeSrcs, sources...)
}

// mockEmbedder implements gleann.EmbeddingComputer for tests.
type mockEmbedder struct {
	dim int
}

func (e *mockEmbedder) Compute(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, e.dim)
		for j := range out[i] {
			out[i][j] = float32(i+1) * 0.1
		}
	}
	return out, nil
}

func (e *mockEmbedder) ComputeSingle(_ context.Context, _ string) ([]float32, error) {
	v := make([]float32, e.dim)
	v[0] = 1.0
	return v, nil
}

func (e *mockEmbedder) Dimensions() int   { return e.dim }
func (e *mockEmbedder) ModelName() string { return "mock-embed" }

// ── Thin wrapper to intercept LeannBuilder calls ──────────────────────────────

// testableBuilder wraps a real LeannBuilder but intercepts calls for verification.
// For unit tests we don't need real disk I/O — we just verify the syncer routes
// correctly by using the mock tracker above and an erroring builder.
type testableLeannSyncer struct {
	tracker *mockLeannBuilder
}

func (t *testableLeannSyncer) AddContent(ctx context.Context, nodeID, content string, attrs map[string]any) error {
	if t.tracker.addErr != nil {
		return t.tracker.addErr
	}
	meta := map[string]any{
		"memory_node_id": nodeID,
		"source":         "memory_engine:" + nodeID,
	}
	for k, v := range attrs {
		if k != "memory_node_id" && k != "source" {
			meta[k] = v
		}
	}
	t.tracker.recordAdd("test-index", []gleann.Item{{Text: content, Metadata: meta}})
	return nil
}

func (t *testableLeannSyncer) DeleteContent(ctx context.Context, nodeID string) error {
	if t.tracker.updateErr != nil {
		return t.tracker.updateErr
	}
	t.tracker.recordRemove("test-index", []string{"memory_engine:" + nodeID})
	return nil
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestLeannSyncer_NilBuilder_NoOp(t *testing.T) {
	syncer := kgraph.NewLeannSyncer(nil, "test", nil)

	ctx := context.Background()
	if err := syncer.AddContent(ctx, "n1", "hello", nil); err != nil {
		t.Fatalf("AddContent with nil builder should not error: %v", err)
	}
	if err := syncer.DeleteContent(ctx, "n1"); err != nil {
		t.Fatalf("DeleteContent with nil builder should not error: %v", err)
	}
}

func TestLeannSyncer_EmptyContent_NoOp(t *testing.T) {
	syncer := kgraph.NewLeannSyncer(nil, "test", nil)

	ctx := context.Background()
	if err := syncer.AddContent(ctx, "n2", "", nil); err != nil {
		t.Fatalf("AddContent with empty content should not error: %v", err)
	}
}

func TestLeannSyncer_IntegrationWithMemoryService(t *testing.T) {
	// This test verifies that MemoryService correctly calls the syncer
	// when entities with content are injected.
	ctx := context.Background()

	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	tracker := &mockLeannBuilder{}
	syncer := &testableLeannSyncer{tracker: tracker}

	svc := kgraph.NewMemoryService(db, syncer)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "entity-1", Type: "concept", Content: "Machine learning is a subset of AI"},
			{ID: "entity-2", Type: "tag"}, // no content → should not trigger syncer
			{ID: "entity-3", Type: "doc", Content: "Neural networks perform pattern recognition"},
		},
	}

	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	// Verify syncer was called exactly for content-bearing nodes.
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	if len(tracker.addedItems) != 2 {
		t.Fatalf("expected 2 AddContent calls, got %d", len(tracker.addedItems))
	}

	// Verify metadata contains node IDs.
	for _, item := range tracker.addedItems {
		nodeID, ok := item.Metadata["memory_node_id"]
		if !ok {
			t.Error("metadata missing memory_node_id")
		}
		src, _ := item.Metadata["source"].(string)
		if src == "" {
			t.Errorf("metadata source empty for node %v", nodeID)
		}
	}
}

func TestLeannSyncer_DeleteIntegration(t *testing.T) {
	ctx := context.Background()

	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	tracker := &mockLeannBuilder{}
	syncer := &testableLeannSyncer{tracker: tracker}

	svc := kgraph.NewMemoryService(db, syncer)

	// First inject an entity.
	_ = svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "del-entity", Type: "doc", Content: "content to delete"},
		},
	})

	// Now delete it.
	if err := svc.DeleteEntity(ctx, "del-entity"); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	if len(tracker.removeSrcs) != 1 {
		t.Fatalf("expected 1 DeleteContent call, got %d", len(tracker.removeSrcs))
	}
	if tracker.removeSrcs[0] != "memory_engine:del-entity" {
		t.Errorf("unexpected source: %s", tracker.removeSrcs[0])
	}
}

func TestLeannSyncer_SyncerError_PropagatesFromInject(t *testing.T) {
	ctx := context.Background()

	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	tracker := &mockLeannBuilder{addErr: fmt.Errorf("embedding service down")}
	syncer := &testableLeannSyncer{tracker: tracker}

	svc := kgraph.NewMemoryService(db, syncer)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "fail-node", Type: "doc", Content: "will fail"},
		},
	}

	err = svc.InjectEntities(ctx, payload)
	if err == nil {
		t.Fatal("expected error when syncer fails, got nil")
	}
}

func TestLeannSyncer_ConcurrentAddContent(t *testing.T) {
	ctx := context.Background()

	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	tracker := &mockLeannBuilder{}
	syncer := &testableLeannSyncer{tracker: tracker}

	svc := kgraph.NewMemoryService(db, syncer)

	// Launch concurrent injects.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			p := gleann.GraphInjectionPayload{
				Nodes: []gleann.MemoryGraphNode{
					{ID: fmt.Sprintf("conc-%d", n), Type: "item", Content: fmt.Sprintf("content %d", n)},
				},
			}
			_ = svc.InjectEntities(ctx, p)
		}(i)
	}
	wg.Wait()

	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	if len(tracker.addedItems) != 10 {
		t.Errorf("expected 10 items added concurrently, got %d", len(tracker.addedItems))
	}
}
