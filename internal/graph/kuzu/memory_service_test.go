//go:build treesitter && !windows

package kuzu_test

import (
	"context"
	"testing"

	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/pkg/gleann"
)

// newTestMemoryService opens an in-memory KuzuDB and returns a MemoryService
// ready for use. The caller does not need to close it (the DB is in-memory).
func newTestMemoryService(t *testing.T) *kgraph.MemoryService {
	t.Helper()
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("Open in-memory kuzu: %v", err)
	}
	t.Cleanup(db.Close)
	return kgraph.NewMemoryService(db, nil)
}

// ── InjectEntities ─────────────────────────────────────────────────────────────

func TestMemoryService_InjectEntities_Nodes(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "req-001", Type: "requirement", Content: "User can log in"},
			{ID: "sym-001", Type: "code_symbol", Attributes: map[string]any{"file": "auth.go"}},
		},
	}

	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}
}

func TestMemoryService_InjectEntities_NodesAndEdges(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "a", Type: "concept"},
			{ID: "b", Type: "concept"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "a", To: "b", RelationType: "RELATED_TO", Weight: 1.0},
		},
	}

	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("InjectEntities with edges: %v", err)
	}
}

func TestMemoryService_InjectEntities_Empty(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	// An empty payload must not error.
	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{}); err != nil {
		t.Fatalf("InjectEntities with empty payload: %v", err)
	}
}

func TestMemoryService_InjectEntities_Idempotent(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "x", Type: "item", Content: "same content"},
		},
	}

	// Injecting the same node twice must not fail (MERGE semantics).
	for i := 0; i < 3; i++ {
		if err := svc.InjectEntities(ctx, payload); err != nil {
			t.Fatalf("InjectEntities call %d: %v", i+1, err)
		}
	}
}

func TestMemoryService_InjectEntities_AttributesPersisted(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	attrs := map[string]any{"priority": float64(1), "tag": "auth"}
	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "n-attrs", Type: "req", Content: "some text", Attributes: attrs},
		},
	}

	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	// Traverse depth 0 to retrieve the node and inspect its attributes.
	nodes, _, err := svc.Traverse(ctx, "n-attrs", 0)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Attributes == nil {
		t.Fatal("attributes should not be nil after persistence")
	}
	if nodes[0].Attributes["tag"] != "auth" {
		t.Errorf("attribute tag = %v, want auth", nodes[0].Attributes["tag"])
	}
}

func TestMemoryService_InjectEntities_EdgeMissingNode_NoError(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	// An edge whose endpoints don't exist should be silently skipped (MATCH returns nothing).
	payload := gleann.GraphInjectionPayload{
		Edges: []gleann.MemoryGraphEdge{
			{From: "ghost-a", To: "ghost-b", RelationType: "CALLS"},
		},
	}

	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("edge with missing nodes should not error, got: %v", err)
	}
}

// ── DeleteEntity ──────────────────────────────────────────────────────────────

func TestMemoryService_DeleteEntity_Existing(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "del-1", Type: "item"}},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if err := svc.DeleteEntity(ctx, "del-1"); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	// Node should be gone after deletion — Traverse returns empty.
	nodes, _, err := svc.Traverse(ctx, "del-1", 0)
	if err != nil {
		t.Fatalf("Traverse after delete: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes after delete, got %d", len(nodes))
	}
}

func TestMemoryService_DeleteEntity_CascadesEdges(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "parent", Type: "concept"},
			{ID: "child", Type: "concept"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "parent", To: "child", RelationType: "HAS_CHILD", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	// Delete the parent — the HAS_CHILD edge should be removed too (DETACH DELETE).
	if err := svc.DeleteEntity(ctx, "parent"); err != nil {
		t.Fatalf("DeleteEntity parent: %v", err)
	}

	// child should still exist but have no inbound edges reachable from parent.
	nodes, _, err := svc.Traverse(ctx, "child", 1)
	if err != nil {
		t.Fatalf("Traverse child: %v", err)
	}
	// child itself should still be there.
	if len(nodes) == 0 {
		t.Error("child node should still exist after parent deletion")
	}
}

func TestMemoryService_DeleteEntity_NonExistent_NoError(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	// Deleting a node that doesn't exist should not error — DETACH DELETE on
	// an empty MATCH simply does nothing.
	if err := svc.DeleteEntity(ctx, "does-not-exist"); err != nil {
		t.Fatalf("DeleteEntity non-existent: %v", err)
	}
}

// ── DeleteEdge ────────────────────────────────────────────────────────────────

func TestMemoryService_DeleteEdge_Existing(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "e1", Type: "node"},
			{ID: "e2", Type: "node"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "e1", To: "e2", RelationType: "LINKED"},
			{From: "e1", To: "e2", RelationType: "ALSO_LINKED"},
		},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if err := svc.DeleteEdge(ctx, "e1", "e2", "LINKED"); err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}

	// e1→e2 sub-graph should still have e1 and e2 (nodes remain).
	nodes, edges, err := svc.Traverse(ctx, "e1", 2)
	if err != nil {
		t.Fatalf("Traverse after DeleteEdge: %v", err)
	}
	_ = nodes

	// LINKED edge must be gone; ALSO_LINKED must survive.
	linkedRemains := false
	alsoLinkedRemains := false
	for _, edge := range edges {
		if edge.RelationType == "LINKED" {
			linkedRemains = true
		}
		if edge.RelationType == "ALSO_LINKED" {
			alsoLinkedRemains = true
		}
	}
	if linkedRemains {
		t.Error("LINKED edge should have been deleted")
	}
	if !alsoLinkedRemains {
		t.Error("ALSO_LINKED edge should still exist")
	}
}

func TestMemoryService_DeleteEdge_NonExistent_NoError(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	if err := svc.DeleteEdge(ctx, "x", "y", "PHANTOM"); err != nil {
		t.Fatalf("DeleteEdge non-existent: %v", err)
	}
}

// ── Traverse ──────────────────────────────────────────────────────────────────

func TestMemoryService_Traverse_Depth0_StartNode(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "root", Type: "root"}},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	nodes, edges, err := svc.Traverse(ctx, "root", 0)
	if err != nil {
		t.Fatalf("Traverse depth 0: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].ID != "root" {
		t.Errorf("node ID = %q, want root", nodes[0].ID)
	}
	if len(edges) != 0 {
		t.Errorf("depth 0 should have no edges, got %d", len(edges))
	}
}

func TestMemoryService_Traverse_Depth1(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "r", Type: "root"},
			{ID: "c1", Type: "child"},
			{ID: "c2", Type: "child"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "r", To: "c1", RelationType: "CHILD", Weight: 1},
			{From: "r", To: "c2", RelationType: "CHILD", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	nodes, edges, err := svc.Traverse(ctx, "r", 1)
	if err != nil {
		t.Fatalf("Traverse depth 1: %v", err)
	}

	// Should include root + 2 children.
	if len(nodes) < 3 {
		t.Errorf("expected at least 3 nodes (root + 2 children), got %d", len(nodes))
	}
	if len(edges) < 2 {
		t.Errorf("expected at least 2 edges, got %d", len(edges))
	}
}

func TestMemoryService_Traverse_Depth2_MultiHop(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	// r → a → b → c  (3 hops)
	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "r", Type: "root"},
			{ID: "a", Type: "mid"},
			{ID: "b", Type: "mid"},
			{ID: "c", Type: "leaf"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "r", To: "a", RelationType: "STEP", Weight: 1},
			{From: "a", To: "b", RelationType: "STEP", Weight: 1},
			{From: "b", To: "c", RelationType: "STEP", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	// depth 2 should reach r, a, b but NOT c (3 hops away).
	nodes2, _, err := svc.Traverse(ctx, "r", 2)
	if err != nil {
		t.Fatalf("Traverse depth 2: %v", err)
	}
	seenC := false
	for _, n := range nodes2 {
		if n.ID == "c" {
			seenC = true
		}
	}
	if seenC {
		t.Error("depth 2 traversal should not reach node 'c' (3 hops away)")
	}

	// depth 3 should reach all 4 nodes.
	nodes3, _, err := svc.Traverse(ctx, "r", 3)
	if err != nil {
		t.Fatalf("Traverse depth 3: %v", err)
	}
	if len(nodes3) < 4 {
		t.Errorf("depth 3 traversal should reach 4 nodes, got %d", len(nodes3))
	}
}

func TestMemoryService_Traverse_DepthNegative_TreatedAsZero(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "solo", Type: "item"}},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	// Negative depth should not panic and should behave like depth 0.
	nodes, _, err := svc.Traverse(ctx, "solo", -5)
	if err != nil {
		t.Fatalf("Traverse negative depth: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("negative depth should return start node only, got %d nodes", len(nodes))
	}
}

func TestMemoryService_Traverse_DepthCapAt10(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	// Just verify depth > 10 doesn't panic.
	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "cap", Type: "item"}},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if _, _, err := svc.Traverse(ctx, "cap", 99); err != nil {
		t.Fatalf("Traverse depth 99: %v", err)
	}
}

func TestMemoryService_Traverse_NonExistentStartNode(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	// Traversal from a nonexistent start node must not error — just returns empty.
	nodes, edges, err := svc.Traverse(ctx, "ghost", 3)
	if err != nil {
		t.Fatalf("Traverse non-existent start: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes for non-existent start, got %d", len(nodes))
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges for non-existent start, got %d", len(edges))
	}
}

// ── Edge attributes and weight ────────────────────────────────────────────────

func TestMemoryService_EdgeWeight(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "w1", Type: "node"},
			{ID: "w2", Type: "node"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "w1", To: "w2", RelationType: "WEIGHTED", Weight: 0.75},
		},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	_, edges, err := svc.Traverse(ctx, "w1", 1)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	var found bool
	for _, e := range edges {
		if e.RelationType == "WEIGHTED" {
			found = true
			if e.Weight != 0.75 {
				t.Errorf("weight = %v, want 0.75", e.Weight)
			}
		}
	}
	if !found {
		t.Error("WEIGHTED edge not found in traversal result")
	}
}

func TestMemoryService_EdgeAttributes(t *testing.T) {
	ctx := context.Background()
	svc := newTestMemoryService(t)

	edgeAttrs := map[string]any{"confidence": float64(0.9), "source": "manual"}
	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "ea1", Type: "node"},
			{ID: "ea2", Type: "node"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "ea1", To: "ea2", RelationType: "ANNOTATED", Weight: 1, Attributes: edgeAttrs},
		},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	_, edges, err := svc.Traverse(ctx, "ea1", 1)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	for _, e := range edges {
		if e.RelationType == "ANNOTATED" {
			if e.Attributes == nil {
				t.Fatal("edge attributes should not be nil")
			}
			if e.Attributes["source"] != "manual" {
				t.Errorf("edge attribute source = %v, want manual", e.Attributes["source"])
			}
			return
		}
	}
	t.Error("ANNOTATED edge not found")
}

// ── VectorSyncer integration ───────────────────────────────────────────────────

// mockVectorSyncer records calls for inspection in tests.
type mockVectorSyncer struct {
	added   []string
	deleted []string
}

func (m *mockVectorSyncer) AddContent(_ context.Context, nodeID, _ string, _ map[string]any) error {
	m.added = append(m.added, nodeID)
	return nil
}

func (m *mockVectorSyncer) DeleteContent(_ context.Context, nodeID string) error {
	m.deleted = append(m.deleted, nodeID)
	return nil
}

func TestMemoryService_VectorSync_AddCalledForContent(t *testing.T) {
	ctx := context.Background()
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	syncer := &mockVectorSyncer{}
	svc := kgraph.NewMemoryService(db, syncer)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "vs1", Type: "doc", Content: "Some content to vectorize"},
			{ID: "vs2", Type: "tag"},                          // no content → no sync
			{ID: "vs3", Type: "doc", Content: "More content"}, // should sync
		},
	}

	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	// Only nodes with non-empty Content should trigger AddContent.
	if len(syncer.added) != 2 {
		t.Errorf("AddContent called %d times, want 2", len(syncer.added))
	}

	addedSet := map[string]bool{}
	for _, id := range syncer.added {
		addedSet[id] = true
	}
	if !addedSet["vs1"] || !addedSet["vs3"] {
		t.Errorf("AddContent called for %v, want [vs1, vs3]", syncer.added)
	}
}

func TestMemoryService_VectorSync_DeleteCalledOnEntityDelete(t *testing.T) {
	ctx := context.Background()
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	syncer := &mockVectorSyncer{}
	svc := kgraph.NewMemoryService(db, syncer)

	_ = svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "del-sync", Type: "doc", Content: "hi"}},
	})

	if err := svc.DeleteEntity(ctx, "del-sync"); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	if len(syncer.deleted) != 1 || syncer.deleted[0] != "del-sync" {
		t.Errorf("DeleteContent called with %v, want [del-sync]", syncer.deleted)
	}
}

// ── Schema co-existence: existing tables not corrupted ────────────────────────

func TestMemoryService_CoexistsWithCodeGraph(t *testing.T) {
	// The Entity/RELATES_TO schema should live alongside CodeFile/Symbol tables.
	db, err := kgraph.Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Standard code graph operations must still work.
	if err := db.UpsertFile("cmd/main.go", "go"); err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	svc := kgraph.NewMemoryService(db, nil)
	ctx := context.Background()

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "coexist-1", Type: "requirement"},
		},
	}); err != nil {
		t.Fatalf("InjectEntities alongside code graph: %v", err)
	}
}
