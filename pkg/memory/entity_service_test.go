package memory_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// newTestEntityService creates an isolated test store in t.TempDir() and returns an EntityService.
func newTestEntityService(t *testing.T, ns string, syncer gleann.VectorSyncer) (*memory.EntityService, *memory.Store) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	store, err := memory.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store.EntityService(ns, syncer), store
}

// ── InjectEntities ─────────────────────────────────────────────────────────────

func TestEntityService_InjectEntities_Nodes(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "req-001", Type: "requirement", Content: "User can log in"},
			{ID: "sym-001", Type: "code_symbol", Attributes: map[string]any{"file": "auth.go"}},
		},
	}

	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	nodes, _, err := svc.Traverse(ctx, "req-001", 0)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != "req-001" || nodes[0].Content != "User can log in" {
		t.Fatalf("unexpected traverse result: %+v", nodes)
	}
}

func TestEntityService_InjectEntities_NodesAndEdges(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

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

	nodes, edges, err := svc.Traverse(ctx, "a", 1)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
	if len(edges) != 1 || edges[0].From != "a" || edges[0].To != "b" {
		t.Errorf("expected 1 edge a->b, got %+v", edges)
	}
}

func TestEntityService_InjectEntities_Empty(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{}); err != nil {
		t.Fatalf("InjectEntities with empty payload: %v", err)
	}
}

func TestEntityService_InjectEntities_Idempotent(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "x", Type: "item", Content: "same content"},
		},
	}

	for i := 0; i < 3; i++ {
		if err := svc.InjectEntities(ctx, payload); err != nil {
			t.Fatalf("InjectEntities call %d: %v", i+1, err)
		}
	}

	nodes, _, err := svc.Traverse(ctx, "x", 0)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
}

func TestEntityService_InjectEntities_AttributesPersisted(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	attrs := map[string]any{"priority": float64(1), "tag": "auth"}
	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "n-attrs", Type: "req", Content: "some text", Attributes: attrs},
		},
	}

	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

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

func TestEntityService_InjectEntities_EdgeMissingNode_NoError(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	// An edge whose endpoints don't exist should be silently skipped.
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

func TestEntityService_DeleteEntity_Existing(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "del-1", Type: "item"}},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	if err := svc.DeleteEntity(ctx, "del-1"); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	nodes, _, err := svc.Traverse(ctx, "del-1", 0)
	if err != nil {
		t.Fatalf("Traverse after delete: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes after delete, got %d", len(nodes))
	}
}

func TestEntityService_DeleteEntity_CascadesEdges(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

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

	if err := svc.DeleteEntity(ctx, "parent"); err != nil {
		t.Fatalf("DeleteEntity parent: %v", err)
	}

	nodes, edges, err := svc.Traverse(ctx, "child", 1)
	if err != nil {
		t.Fatalf("Traverse child: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != "child" {
		t.Errorf("child node should survive parent delete, got: %+v", nodes)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges after parent deleted, got %d", len(edges))
	}
}

func TestEntityService_DeleteEntity_NonExistent_NoError(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	if err := svc.DeleteEntity(ctx, "does-not-exist"); err != nil {
		t.Fatalf("DeleteEntity non-existent: %v", err)
	}
}

// ── DeleteEdge ────────────────────────────────────────────────────────────────

func TestEntityService_DeleteEdge_Existing(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

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

	nodes, edges, err := svc.Traverse(ctx, "e1", 1)
	if err != nil {
		t.Fatalf("Traverse after DeleteEdge: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}

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

func TestEntityService_DeleteEdge_NonExistent_NoError(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	if err := svc.DeleteEdge(ctx, "x", "y", "PHANTOM"); err != nil {
		t.Fatalf("DeleteEdge non-existent: %v", err)
	}
}

// ── Traverse ──────────────────────────────────────────────────────────────────

func TestEntityService_Traverse_Depth0_StartNode(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

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

func TestEntityService_Traverse_Depth1(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

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
	if len(nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(nodes))
	}
	if len(edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(edges))
	}
}

func TestEntityService_Traverse_Depth2_MultiHop(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	// r → a → b → c (3 hops)
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

	nodes2, _, err := svc.Traverse(ctx, "r", 2)
	if err != nil {
		t.Fatalf("Traverse depth 2: %v", err)
	}
	for _, n := range nodes2 {
		if n.ID == "c" {
			t.Error("depth 2 traversal should not reach node 'c' (3 hops away)")
		}
	}

	nodes3, _, err := svc.Traverse(ctx, "r", 3)
	if err != nil {
		t.Fatalf("Traverse depth 3: %v", err)
	}
	if len(nodes3) != 4 {
		t.Errorf("depth 3 traversal should reach 4 nodes, got %d", len(nodes3))
	}
}

func TestEntityService_Traverse_DepthNegative_TreatedAsZero(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "solo", Type: "item"}},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	nodes, _, err := svc.Traverse(ctx, "solo", -5)
	if err != nil {
		t.Fatalf("Traverse negative depth: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("negative depth should return start node only, got %d nodes", len(nodes))
	}
}

func TestEntityService_Traverse_DepthCapAt10(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "cap", Type: "item"}},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	nodes, _, err := svc.Traverse(ctx, "cap", 99)
	if err != nil {
		t.Fatalf("Traverse depth 99: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(nodes))
	}
}

func TestEntityService_Traverse_NonExistentStartNode(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	nodes, edges, err := svc.Traverse(ctx, "ghost", 3)
	if err != nil {
		t.Fatalf("Traverse non-existent start: %v", err)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Errorf("expected 0 nodes and 0 edges for non-existent start, got %d / %d", len(nodes), len(edges))
	}
}

func TestEntityService_Traverse_Cycles(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	// A -> B -> A (2-node cycle) and B -> C -> B (another cycle)
	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "A", Type: "node"},
			{ID: "B", Type: "node"},
			{ID: "C", Type: "node"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "A", To: "B", RelationType: "LINK"},
			{From: "B", To: "A", RelationType: "LINK"},
			{From: "B", To: "C", RelationType: "LINK"},
			{From: "C", To: "B", RelationType: "LINK"},
		},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}

	// Must terminate cleanly without looping infinitely
	nodes, edges, err := svc.Traverse(ctx, "A", 5)
	if err != nil {
		t.Fatalf("Traverse cycle: %v", err)
	}
	if len(nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(nodes))
	}
	if len(edges) != 4 {
		t.Errorf("expected 4 edges, got %d", len(edges))
	}
}

// A dense entity graph must not make the traversal enumerate paths: with
// 14 fully connected entities and depth 10 a per-path walk visits billions of
// paths, while the (id, depth) walk visits at most 14 x 10 rows.
func TestEntityService_Traverse_DenseGraphIsBounded(t *testing.T) {
	svc, _ := newTestEntityService(t, "dense", nil)
	const n = 14
	var payload gleann.GraphInjectionPayload
	for i := 0; i < n; i++ {
		payload.Nodes = append(payload.Nodes, gleann.MemoryGraphNode{ID: fmt.Sprintf("e%d", i), Type: "node"})
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j {
				payload.Edges = append(payload.Edges, gleann.MemoryGraphEdge{From: fmt.Sprintf("e%d", i), To: fmt.Sprintf("e%d", j), RelationType: "LINK"})
			}
		}
	}
	if err := svc.InjectEntities(context.Background(), payload); err != nil {
		t.Fatalf("inject: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nodes, edges, err := svc.Traverse(ctx, "e0", 10)
	if err != nil {
		t.Fatalf("Traverse on a dense graph: %v", err)
	}
	if len(nodes) != n || len(edges) != n*(n-1) {
		t.Fatalf("got %d nodes, %d edges; want %d, %d", len(nodes), len(edges), n, n*(n-1))
	}
}

func TestEntityService_EdgeWeight(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

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

func TestEntityService_EdgeAttributes(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

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

func TestEntityService_TemporalEdgeAttributes(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "test", nil)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "tp1", Type: "node"},
			{ID: "tp2", Type: "node"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "tp1", To: "tp2", RelationType: "PRESERVE_CREATED", Weight: 1.0},
		},
	}

	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("first inject: %v", err)
	}

	_, edges1, err := svc.Traverse(ctx, "tp1", 1)
	if err != nil {
		t.Fatalf("Traverse 1: %v", err)
	}
	var firstCreatedAt string
	for _, e := range edges1 {
		if e.RelationType == "PRESERVE_CREATED" {
			firstCreatedAt, _ = e.Attributes["created_at"].(string)
		}
	}
	if firstCreatedAt == "" {
		t.Fatal("created_at must be set after first injection")
	}

	// User supplies explicit created_at on second inject
	payload.Edges[0].Attributes = map[string]any{"created_at": "user-supplied"}
	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("second inject: %v", err)
	}

	_, edges2, err := svc.Traverse(ctx, "tp1", 1)
	if err != nil {
		t.Fatalf("Traverse 2: %v", err)
	}
	for _, e := range edges2 {
		if e.RelationType == "PRESERVE_CREATED" {
			if e.Attributes["created_at"] != "user-supplied" {
				t.Errorf("user-supplied created_at should be preserved, got %v", e.Attributes["created_at"])
			}
			if _, ok := e.Attributes["updated_at"]; !ok {
				t.Error("updated_at should be present")
			}
			return
		}
	}
	t.Error("PRESERVE_CREATED edge not found on second traversal")
}

// ── Archive Versions ──────────────────────────────────────────────────────────

func TestEntityService_ArchiveVersions(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestEntityService(t, "test_ns", nil)

	// First injection
	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "arch-1", Type: "concept", Content: "Version 1", Attributes: map[string]any{"v": float64(1)}},
		},
	}); err != nil {
		t.Fatalf("first inject: %v", err)
	}

	// Verify nothing in entity_archive yet
	var count int
	err := store.ReaderForTest().QueryRow(`SELECT count(*) FROM entity_archive WHERE ns = ? AND id = ?`, "test_ns", "arch-1").Scan(&count)
	if err != nil {
		t.Fatalf("count entity_archive: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 archived rows, got %d", count)
	}

	// Second injection with updated content
	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "arch-1", Type: "concept", Content: "Version 2", Attributes: map[string]any{"v": float64(2)}},
		},
	}); err != nil {
		t.Fatalf("second inject: %v", err)
	}

	// Verify previous version is archived in entity_archive
	var archType, archContent, archAttrs, validTo string
	err = store.ReaderForTest().QueryRow(`SELECT type, content, attributes, valid_to FROM entity_archive WHERE ns = ? AND id = ?`, "test_ns", "arch-1").
		Scan(&archType, &archContent, &archAttrs, &validTo)
	if err != nil {
		t.Fatalf("reading entity_archive: %v", err)
	}
	if archContent != "Version 1" {
		t.Errorf("archived content = %q, want 'Version 1'", archContent)
	}
	if validTo == "" {
		t.Error("valid_to must not be empty in entity_archive")
	}

	// Verify active table has Version 2
	nodes, _, err := svc.Traverse(ctx, "arch-1", 0)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Content != "Version 2" {
		t.Fatalf("active entity = %+v, want Version 2", nodes)
	}
}

// ── Two Namespaces Isolated ───────────────────────────────────────────────────

func TestEntityService_NamespaceIsolation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	store, err := memory.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	svc1 := store.EntityService("ns1", nil)
	svc2 := store.EntityService("ns2", nil)

	// Inject same entity ID "common" into ns1 and ns2 with different content
	if err := svc1.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "common", Type: "ns1_type", Content: "Content in NS1"},
			{ID: "ns1_only", Type: "ns1_type"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "common", To: "ns1_only", RelationType: "NS1_EDGE"},
		},
	}); err != nil {
		t.Fatalf("inject ns1: %v", err)
	}

	if err := svc2.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "common", Type: "ns2_type", Content: "Content in NS2"},
			{ID: "ns2_only", Type: "ns2_type"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "common", To: "ns2_only", RelationType: "NS2_EDGE"},
		},
	}); err != nil {
		t.Fatalf("inject ns2: %v", err)
	}

	// Traverse ns1
	nodes1, edges1, err := svc1.Traverse(ctx, "common", 1)
	if err != nil {
		t.Fatalf("traverse ns1: %v", err)
	}
	if len(nodes1) != 2 || len(edges1) != 1 {
		t.Fatalf("expected 2 nodes, 1 edge in ns1, got %d / %d", len(nodes1), len(edges1))
	}
	if nodes1[0].Content != "Content in NS1" && nodes1[1].Content != "Content in NS1" {
		t.Errorf("ns1 node content wrong: %+v", nodes1)
	}
	if edges1[0].RelationType != "NS1_EDGE" {
		t.Errorf("ns1 edge wrong: %+v", edges1)
	}

	// Traverse ns2
	nodes2, edges2, err := svc2.Traverse(ctx, "common", 1)
	if err != nil {
		t.Fatalf("traverse ns2: %v", err)
	}
	if len(nodes2) != 2 || len(edges2) != 1 {
		t.Fatalf("expected 2 nodes, 1 edge in ns2, got %d / %d", len(nodes2), len(edges2))
	}
	if nodes2[0].Content != "Content in NS2" && nodes2[1].Content != "Content in NS2" {
		t.Errorf("ns2 node content wrong: %+v", nodes2)
	}
	if edges2[0].RelationType != "NS2_EDGE" {
		t.Errorf("ns2 edge wrong: %+v", edges2)
	}

	// Delete from ns1 should not affect ns2
	if err := svc1.DeleteEntity(ctx, "common"); err != nil {
		t.Fatalf("delete from ns1: %v", err)
	}

	// Check ns1 is gone
	delNodes1, _, err := svc1.Traverse(ctx, "common", 0)
	if err != nil {
		t.Fatalf("traverse deleted ns1: %v", err)
	}
	if len(delNodes1) != 0 {
		t.Errorf("expected 0 nodes in ns1 after delete, got %d", len(delNodes1))
	}

	// Check ns2 is still intact
	survNodes2, _, err := svc2.Traverse(ctx, "common", 0)
	if err != nil {
		t.Fatalf("traverse surviving ns2: %v", err)
	}
	if len(survNodes2) != 1 || survNodes2[0].Content != "Content in NS2" {
		t.Errorf("ns2 entity was affected by ns1 delete: %+v", survNodes2)
	}
}

// ── Concurrent Writers ────────────────────────────────────────────────────────

func TestEntityService_ConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	store, err := memory.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	// Goroutine 1 writes into ns_worker1
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc := store.EntityService("ns_worker1", nil)
		for i := 0; i < 20; i++ {
			p := gleann.GraphInjectionPayload{
				Nodes: []gleann.MemoryGraphNode{
					{ID: fmt.Sprintf("w1-n%d", i), Type: "worker1", Content: fmt.Sprintf("worker 1 item %d", i)},
				},
			}
			if err := svc.InjectEntities(ctx, p); err != nil {
				errCh <- fmt.Errorf("worker 1 inject %d: %w", i, err)
				return
			}
		}
	}()

	// Goroutine 2 writes into ns_worker2
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc := store.EntityService("ns_worker2", nil)
		for i := 0; i < 20; i++ {
			p := gleann.GraphInjectionPayload{
				Nodes: []gleann.MemoryGraphNode{
					{ID: fmt.Sprintf("w2-n%d", i), Type: "worker2", Content: fmt.Sprintf("worker 2 item %d", i)},
				},
			}
			if err := svc.InjectEntities(ctx, p); err != nil {
				errCh <- fmt.Errorf("worker 2 inject %d: %w", i, err)
				return
			}
		}
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent writer error: %v", err)
	}

	// Verify counts in both namespaces
	var count1, count2 int
	_ = store.ReaderForTest().QueryRow(`SELECT count(*) FROM entities WHERE ns = 'ns_worker1'`).Scan(&count1)
	_ = store.ReaderForTest().QueryRow(`SELECT count(*) FROM entities WHERE ns = 'ns_worker2'`).Scan(&count2)
	if count1 != 20 || count2 != 20 {
		t.Errorf("expected 20 items per worker, got w1=%d, w2=%d", count1, count2)
	}
}

// ── VectorSyncer Integration ──────────────────────────────────────────────────

type mockVectorSyncer struct {
	mu      sync.Mutex
	added   []string
	deleted []string
}

func (m *mockVectorSyncer) AddContent(_ context.Context, nodeID, _ string, _ map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.added = append(m.added, nodeID)
	return nil
}

func (m *mockVectorSyncer) DeleteContent(_ context.Context, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, nodeID)
	return nil
}

func TestEntityService_VectorSync(t *testing.T) {
	ctx := context.Background()
	syncer := &mockVectorSyncer{}
	svc, _ := newTestEntityService(t, "test", syncer)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "vs1", Type: "doc", Content: "Some content to vectorize"},
			{ID: "vs2", Type: "tag"},                          // empty content -> no sync
			{ID: "vs3", Type: "doc", Content: "More content"}, // should sync
		},
	}

	if err := svc.InjectEntities(ctx, payload); err != nil {
		t.Fatalf("InjectEntities: %v", err)
	}

	if len(syncer.added) != 2 {
		t.Fatalf("AddContent called %d times, want 2", len(syncer.added))
	}
	if syncer.added[0] != "vs1" || syncer.added[1] != "vs3" {
		t.Errorf("AddContent called for %v, want [vs1, vs3]", syncer.added)
	}

	if err := svc.DeleteEntity(ctx, "vs1"); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}
	if len(syncer.deleted) != 1 || syncer.deleted[0] != "vs1" {
		t.Errorf("DeleteContent called for %v, want [vs1]", syncer.deleted)
	}
}

// An explicit edge weight of 0 is stored as given, as the Kùzu service did.
func TestEntityService_ZeroWeightKept(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestEntityService(t, "zw", nil)
	if err := svc.InjectEntities(ctx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "a", Type: "n"}, {ID: "b", Type: "n"}},
		Edges: []gleann.MemoryGraphEdge{{From: "a", To: "b", RelationType: "R", Weight: 0}},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}
	_, edges, err := svc.Traverse(ctx, "a", 1)
	if err != nil || len(edges) != 1 || edges[0].Weight != 0 {
		t.Fatalf("edges = %+v (%v); want one edge with weight 0", edges, err)
	}
}

// gatedSyncer blocks the sync of content "A" until released, so a second
// inject can commit meanwhile; it records the content order the vector index
// saw.
type gatedSyncer struct {
	mu    sync.Mutex
	order []string
	gate  chan struct{}
}

func (g *gatedSyncer) AddContent(_ context.Context, _ string, content string, _ map[string]any) error {
	if content == "A" {
		<-g.gate
	}
	g.mu.Lock()
	g.order = append(g.order, content)
	g.mu.Unlock()
	return nil
}

func (g *gatedSyncer) DeleteContent(context.Context, string) error { return nil }

// Two injects of one entity must reach the vector index in commit order: the
// last committed content is the last synced one (the Kùzu service held its
// mutex across the sync).
func TestEntityService_VectorSyncFollowsCommitOrder(t *testing.T) {
	ctx := context.Background()
	g := &gatedSyncer{gate: make(chan struct{})}
	svc, store := newTestEntityService(t, "order", g)
	inject := func(content string) error {
		return svc.InjectEntities(ctx, gleann.GraphInjectionPayload{Nodes: []gleann.MemoryGraphNode{{ID: "x", Type: "n", Content: content}}})
	}
	done := make(chan error, 2)
	go func() { done <- inject("A") }()
	time.Sleep(200 * time.Millisecond) // A has committed and waits in its sync
	go func() { done <- inject("B") }()
	time.Sleep(200 * time.Millisecond)
	close(g.gate)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("inject: %v", err)
		}
	}
	var stored string
	if err := store.ReaderForTest().QueryRow(`SELECT content FROM entities WHERE ns = 'order' AND id = 'x'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if last := g.order[len(g.order)-1]; last != stored {
		t.Fatalf("vector index last saw %q, entities holds %q (sync order %v)", last, stored, g.order)
	}
}
