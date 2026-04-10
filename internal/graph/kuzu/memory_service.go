//go:build treesitter

// Package kuzu — Memory Engine service.
//
// MemoryService provides the four core operations of gleann's generic
// Knowledge Graph API: bulk injection (upsert), entity deletion, edge
// deletion, and sub-graph traversal.  It wraps a KuzuDB [DB] and an
// optional [VectorSyncer] so that every Entity node with non-empty
// content is automatically reflected in the HNSW+BM25 vector index.
package kuzu

import (
	"context"
	"fmt"
	"sync"
	"time"

	gokuzu "github.com/kuzudb/go-kuzu"
	"github.com/tevfik/gleann/pkg/gleann"
)

// VectorSyncer abstracts vector-index write operations for the Memory Engine.
// Implementations bridge KuzuDB graph writes to the HNSW+BM25 vector store.
// Pass nil to [NewMemoryService] to skip vector synchronisation entirely.
type VectorSyncer interface {
	// AddContent indexes node content in the vector store.
	// nodeID is stored as passage metadata so the vector ID can be mapped
	// back to the Entity primary key during retrieval.
	AddContent(ctx context.Context, nodeID, content string, attrs map[string]any) error

	// DeleteContent removes the passage associated with nodeID from the
	// vector store (soft-delete / tombstone as appropriate for the backend).
	DeleteContent(ctx context.Context, nodeID string) error
}

// MemoryService orchestrates transactional Entity / RELATES_TO operations on
// KuzuDB with optional synchronisation to the HNSW+BM25 vector index.
//
// All write methods serialise through an internal mutex because KuzuDB does
// not support concurrent write transactions on the same database handle.
type MemoryService struct {
	db     *DB
	syncer VectorSyncer // nil == disabled
	mu     sync.Mutex   // serialises all KuzuDB write operations
}

// NewMemoryService constructs a MemoryService backed by db.
// syncer may be nil when vector synchronisation is not required.
func NewMemoryService(db *DB, syncer VectorSyncer) *MemoryService {
	return &MemoryService{db: db, syncer: syncer}
}

// ── Core write operations ─────────────────────────────────────────────────────

// InjectEntities persists nodes and edges from payload into KuzuDB within a
// single atomic transaction.  Node upserts use MERGE and are therefore
// idempotent — calling InjectEntities twice with the same payload is safe.
//
// If a node carries non-empty Content, the content is synchronised to the
// HNSW+BM25 vector index via the optional VectorSyncer after the transaction
// commits.  Because the vector sync happens outside the DB transaction, the
// caller should retry on failure — the graph write is already durable and the
// node MERGE is idempotent.
//
// Edge creation uses MATCH + MERGE so the operation silently skips edges
// whose endpoints do not (yet) exist in the graph.
func (m *MemoryService) InjectEntities(ctx context.Context, payload gleann.GraphInjectionPayload) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	conn, err := m.db.NewConn()
	if err != nil {
		return fmt.Errorf("inject entities: open connection: %w", err)
	}
	defer conn.Close()

	queries := make([]string, 0, len(payload.Nodes)+len(payload.Edges))

	// ── Node upserts ─────────────────────────────────────────────────────────
	for i := range payload.Nodes {
		n := &payload.Nodes[i]
		attrsJSON, err := gleann.AttributesToJSON(n.Attributes)
		if err != nil {
			return fmt.Errorf("inject entities: node %q attributes: %w", n.ID, err)
		}
		queries = append(queries, entityMergeQuery(n.ID, n.Type, n.Content, attrsJSON))
	}

	// ── Edge upserts ─────────────────────────────────────────────────────────
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range payload.Edges {
		e := &payload.Edges[i]
		// Auto-inject temporal attributes.
		if e.Attributes == nil {
			e.Attributes = make(map[string]any)
		}
		if _, ok := e.Attributes["created_at"]; !ok {
			e.Attributes["created_at"] = now
		}
		e.Attributes["updated_at"] = now
		attrsJSON, err := gleann.AttributesToJSON(e.Attributes)
		if err != nil {
			return fmt.Errorf("inject entities: edge %q→%q attributes: %w", e.From, e.To, err)
		}
		queries = append(queries, relMergeQuery(e.From, e.To, e.RelationType, e.Weight, attrsJSON))
	}

	// Execute everything atomically — any failure triggers ROLLBACK.
	if err := ExecTxOn(conn, queries); err != nil {
		return fmt.Errorf("inject entities: transaction: %w", err)
	}

	// ── Optional vector synchronisation (runs after the DB commit) ────────────
	if m.syncer != nil {
		for i := range payload.Nodes {
			n := &payload.Nodes[i]
			if n.Content == "" {
				continue
			}
			if sErr := m.syncer.AddContent(ctx, n.ID, n.Content, n.Attributes); sErr != nil {
				return fmt.Errorf("inject entities: vector sync node %q: %w", n.ID, sErr)
			}
		}
	}

	return nil
}

// DeleteEntity removes an Entity node and all of its incident RELATES_TO edges
// from KuzuDB in a single transaction (DETACH DELETE).  If a VectorSyncer is
// configured the corresponding content vector is also removed.
func (m *MemoryService) DeleteEntity(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	conn, err := m.db.NewConn()
	if err != nil {
		return fmt.Errorf("delete entity %q: open connection: %w", id, err)
	}
	defer conn.Close()

	// DETACH DELETE removes the node together with all incoming and outgoing edges.
	q := fmt.Sprintf(`MATCH (e:Entity {id: %q}) DETACH DELETE e`, id)
	if err := ExecTxOn(conn, []string{q}); err != nil {
		return fmt.Errorf("delete entity %q: %w", id, err)
	}

	if m.syncer != nil {
		if sErr := m.syncer.DeleteContent(ctx, id); sErr != nil {
			return fmt.Errorf("delete entity %q: vector sync: %w", id, sErr)
		}
	}

	return nil
}

// DeleteEdge removes the specific RELATES_TO relationship identified by
// (fromID, toID, relationType).  Other edges between the same node pair under
// different relation types are not affected.
func (m *MemoryService) DeleteEdge(_ context.Context, fromID, toID, relationType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	conn, err := m.db.NewConn()
	if err != nil {
		return fmt.Errorf("delete edge %q→%q: open connection: %w", fromID, toID, err)
	}
	defer conn.Close()

	q := fmt.Sprintf(
		`MATCH (a:Entity {id: %q})-[r:RELATES_TO]->(b:Entity {id: %q}) WHERE r.relation_type = %q DELETE r`,
		fromID, toID, relationType,
	)
	if err := ExecTxOn(conn, []string{q}); err != nil {
		return fmt.Errorf("delete edge %q→%q (%s): %w", fromID, toID, relationType, err)
	}

	return nil
}

// Traverse performs a breadth-first sub-graph walk from startID following
// RELATES_TO edges and returns all reachable nodes and the edges that connect
// them, up to depth hops away.
//
//   - depth == 0: returns only the start node with an empty edge list.
//   - depth >= 1: returns reachable nodes and all intra-subgraph edges.
//   - depth is capped at 10 to prevent runaway traversals.
func (m *MemoryService) Traverse(_ context.Context, startID string, depth int) ([]gleann.MemoryGraphNode, []gleann.MemoryGraphEdge, error) {
	if depth < 0 {
		depth = 0
	}
	if depth > 10 {
		depth = 10
	}

	conn, err := m.db.NewConn()
	if err != nil {
		return nil, nil, fmt.Errorf("traverse %q: open connection: %w", startID, err)
	}
	defer conn.Close()

	// ── Depth 0: return just the start node ───────────────────────────────────
	if depth == 0 {
		nodeCypher := fmt.Sprintf(
			`MATCH (e:Entity {id: %q}) RETURN e.id AS id, e.type AS type, e.content AS content, e.attributes AS attributes`,
			startID,
		)
		res, err := conn.Query(nodeCypher)
		if err != nil {
			return nil, nil, fmt.Errorf("traverse %q (depth=0): %w", startID, err)
		}
		defer res.Close()
		nodes, err := consumeEntityNodes(res)
		if err != nil {
			return nil, nil, fmt.Errorf("traverse %q (depth=0): %w", startID, err)
		}
		return nodes, nil, nil
	}

	// ── Collect all reachable nodes (inclusive of start) ─────────────────────
	// KuzuDB variable-length path: -[:REL*0..N]-> includes the start node (0 hops).
	nodeCypher := fmt.Sprintf(`
		MATCH (start:Entity {id: %q})-[:RELATES_TO*0..%d]->(n:Entity)
		RETURN DISTINCT n.id AS id, n.type AS type, n.content AS content, n.attributes AS attributes
	`, startID, depth)

	nodeRes, err := conn.Query(nodeCypher)
	if err != nil {
		return nil, nil, fmt.Errorf("traverse %q nodes: %w", startID, err)
	}
	defer nodeRes.Close()

	nodes, err := consumeEntityNodes(nodeRes)
	if err != nil {
		return nil, nil, fmt.Errorf("traverse %q nodes: %w", startID, err)
	}

	if len(nodes) == 0 {
		return nil, nil, nil
	}

	// ── Collect all edges that lie within the sub-graph ───────────────────────
	// Re-run the reachability query inside a WITH clause and match edges whose
	// both endpoints are in the reachable set.
	edgeCypher := fmt.Sprintf(`
		MATCH (start:Entity {id: %q})-[:RELATES_TO*0..%d]->(n:Entity)
		WITH collect(DISTINCT n.id) AS nodeIDs
		MATCH (a:Entity)-[r:RELATES_TO]->(b:Entity)
		WHERE a.id IN nodeIDs AND b.id IN nodeIDs
		RETURN a.id AS from_id, b.id AS to_id,
		       r.relation_type AS relation_type,
		       r.weight       AS weight,
		       r.attributes   AS attributes
	`, startID, depth)

	edgeRes, err := conn.Query(edgeCypher)
	if err != nil {
		return nil, nil, fmt.Errorf("traverse %q edges: %w", startID, err)
	}
	defer edgeRes.Close()

	edges, err := consumeEntityEdges(edgeRes)
	if err != nil {
		return nil, nil, fmt.Errorf("traverse %q edges: %w", startID, err)
	}

	return nodes, edges, nil
}

// ── Cypher query builder helpers ─────────────────────────────────────────────

// entityMergeQuery returns an idempotent Cypher MERGE statement for an Entity.
//
// String values are embedded using Go's %q verb which produces properly
// escaped, double-quoted string literals compatible with KuzuDB's Cypher
// dialect, providing the same injection safety as parameterised queries.
func entityMergeQuery(id, nodeType, content, attrsJSON string) string {
	return fmt.Sprintf(
		`MERGE (e:Entity {id: %q}) SET e.type = %q, e.content = %q, e.attributes = %q`,
		id, nodeType, content, attrsJSON,
	)
}

// relMergeQuery returns an idempotent Cypher MERGE statement for a RELATES_TO
// edge.  The relation_type is part of the merge key so that multiple distinct
// semantic edges between the same pair of nodes are supported.
func relMergeQuery(fromID, toID, relationType string, weight float64, attrsJSON string) string {
	return fmt.Sprintf(
		`MATCH (a:Entity {id: %q}), (b:Entity {id: %q})`+
			` MERGE (a)-[r:RELATES_TO {relation_type: %q}]->(b)`+
			` SET r.weight = %f, r.attributes = %q`,
		fromID, toID, relationType, weight, attrsJSON,
	)
}

// ── QueryResult consumers ─────────────────────────────────────────────────────

// consumeEntityNodes reads Entity rows from res and returns them as
// []gleann.MemoryGraphNode. The caller retains ownership of res (defer Close).
func consumeEntityNodes(res *gokuzu.QueryResult) ([]gleann.MemoryGraphNode, error) {
	var out []gleann.MemoryGraphNode
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			return nil, fmt.Errorf("read entity row: %w", err)
		}
		m, err := row.GetAsMap()
		if err != nil {
			return nil, fmt.Errorf("entity row to map: %w", err)
		}
		attrs, _ := gleann.JSONToAttributes(strVal(m["attributes"]))
		out = append(out, gleann.MemoryGraphNode{
			ID:         strVal(m["id"]),
			Type:       strVal(m["type"]),
			Content:    strVal(m["content"]),
			Attributes: attrs,
		})
	}
	return out, nil
}

// consumeEntityEdges reads RELATES_TO rows from res and returns them as
// []gleann.MemoryGraphEdge. Columns expected: from_id, to_id, relation_type,
// weight, attributes.
func consumeEntityEdges(res *gokuzu.QueryResult) ([]gleann.MemoryGraphEdge, error) {
	var out []gleann.MemoryGraphEdge
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			return nil, fmt.Errorf("read edge row: %w", err)
		}
		m, err := row.GetAsMap()
		if err != nil {
			return nil, fmt.Errorf("edge row to map: %w", err)
		}
		attrs, _ := gleann.JSONToAttributes(strVal(m["attributes"]))
		weight := 1.0
		if v, ok := m["weight"].(float64); ok {
			weight = v
		}
		out = append(out, gleann.MemoryGraphEdge{
			From:         strVal(m["from_id"]),
			To:           strVal(m["to_id"]),
			RelationType: strVal(m["relation_type"]),
			Weight:       weight,
			Attributes:   attrs,
		})
	}
	return out, nil
}
