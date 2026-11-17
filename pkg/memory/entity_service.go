package memory

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// EntityService implements gleann.MemoryService backed by the SQLite memory database
// tables entities, relations, and entity_archive (RFC 003 section 3.5).
//
// All write operations execute in a single BEGIN IMMEDIATE transaction.
type EntityService struct {
	store  *Store
	ns     string
	syncer gleann.VectorSyncer
	// mu serialises each write together with its vector sync, as the Kùzu
	// service did: two injects of one entity must reach the vector index in
	// commit order, or it keeps the older content.
	mu sync.Mutex
}

var _ gleann.MemoryService = (*EntityService)(nil)

// NewEntityService creates a new EntityService for the given namespace.
// syncer may be nil when vector synchronisation is not required.
func NewEntityService(store *Store, ns string, syncer gleann.VectorSyncer) *EntityService {
	return &EntityService{
		store:  store,
		ns:     ns,
		syncer: syncer,
	}
}

// EntityService returns an EntityService for the specified namespace.
func (s *Store) EntityService(ns string, syncer gleann.VectorSyncer) *EntityService {
	return NewEntityService(s, ns, syncer)
}

// InjectEntities persists nodes and edges from payload into the SQLite memory tables
// within a single atomic BEGIN IMMEDIATE transaction. Node upserts archive previous
// versions into entity_archive before updating, matching Kùzu MemoryService semantics.
//
// Nodes with non-empty Content are synchronised to the vector index via VectorSyncer
// after the database transaction commits.
// Edge creation silently skips edges whose endpoints do not exist in the entities table.
func (es *EntityService) InjectEntities(ctx context.Context, payload gleann.GraphInjectionPayload) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)

	err := es.store.writeTx(func(tx *sql.Tx) error {
		// 1. Temporal Archiving:
		// For each node in payload.Nodes, check if an existing version is present in entities.
		// If so, archive the previous version into entity_archive.
		for i, n := range payload.Nodes {
			var oldType, oldContent, oldAttrs string
			err := tx.QueryRow(`SELECT type, content, attributes FROM entities WHERE ns = ? AND id = ?`, es.ns, n.ID).
				Scan(&oldType, &oldContent, &oldAttrs)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("checking entity %q for archive: %w", n.ID, err)
			}
			versionID := fmt.Sprintf("%d_%d", time.Now().UnixNano(), i)
			// archive_id is the table's key across every namespace and
			// process; a clock reading alone repeats (Windows ticks are
			// ~1 ms), so the namespace and a random suffix make it unique.
			archiveID := es.ns + "/" + n.ID + "_" + versionID + "_" + randomSuffix()
			_, err = tx.Exec(`INSERT INTO entity_archive (archive_id, ns, id, version_id, type, content, attributes, valid_to)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, archiveID, es.ns, n.ID, versionID, oldType, oldContent, oldAttrs, now)
			if err != nil {
				return fmt.Errorf("archiving entity %q: %w", n.ID, err)
			}
		}

		// 2. Standard Upserts for Nodes
		for i := range payload.Nodes {
			n := &payload.Nodes[i]
			attrsJSON, err := gleann.AttributesToJSON(n.Attributes)
			if err != nil {
				return fmt.Errorf("encoding node %q attributes: %w", n.ID, err)
			}
			_, err = tx.Exec(`INSERT INTO entities (ns, id, type, content, attributes)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(ns, id) DO UPDATE SET
  type = excluded.type,
  content = excluded.content,
  attributes = excluded.attributes`, es.ns, n.ID, n.Type, n.Content, attrsJSON)
			if err != nil {
				return fmt.Errorf("upserting entity %q: %w", n.ID, err)
			}
		}

		// 3. Upsert Edges: only if both endpoints exist in entities for this namespace
		for i := range payload.Edges {
			e := &payload.Edges[i]
			if e.Attributes == nil {
				e.Attributes = make(map[string]any)
			}
			if _, ok := e.Attributes["created_at"]; !ok {
				e.Attributes["created_at"] = now
			}
			e.Attributes["updated_at"] = now
			attrsJSON, err := gleann.AttributesToJSON(e.Attributes)
			if err != nil {
				return fmt.Errorf("encoding edge %q->%q attributes: %w", e.From, e.To, err)
			}
			// Stored as given, like the Kùzu service: an explicit 0 stays 0.
			weight := e.Weight

			// Atomic conditional insert: only if both From and To exist in entities for this ns
			_, err = tx.Exec(`INSERT INTO relations (ns, src, dst, relation_type, weight, attributes)
SELECT ?, ?, ?, ?, ?, ?
WHERE EXISTS (SELECT 1 FROM entities WHERE ns = ? AND id = ?)
  AND EXISTS (SELECT 1 FROM entities WHERE ns = ? AND id = ?)
ON CONFLICT(ns, src, dst, relation_type) DO UPDATE SET
  weight = excluded.weight,
  attributes = excluded.attributes`,
				es.ns, e.From, e.To, e.RelationType, weight, attrsJSON,
				es.ns, e.From,
				es.ns, e.To)
			if err != nil {
				return fmt.Errorf("upserting relation %q->%q (%s): %w", e.From, e.To, e.RelationType, err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("inject entities in namespace %q: %w", es.ns, err)
	}

	// 4. VectorSyncer hook
	if es.syncer != nil {
		for i := range payload.Nodes {
			n := &payload.Nodes[i]
			if n.Content == "" {
				continue
			}
			if sErr := es.syncer.AddContent(ctx, n.ID, n.Content, n.Attributes); sErr != nil {
				return fmt.Errorf("vector sync node %q: %w", n.ID, sErr)
			}
		}
	}

	return nil
}

// DeleteEntity removes an Entity node and all of its incident RELATES_TO relations
// from the namespace in a single atomic BEGIN IMMEDIATE transaction.
// If a VectorSyncer is configured, the corresponding content vector is also removed.
func (es *EntityService) DeleteEntity(ctx context.Context, id string) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	err := es.store.writeTx(func(tx *sql.Tx) error {
		// Detach incident relations
		if _, err := tx.Exec(`DELETE FROM relations WHERE ns = ? AND (src = ? OR dst = ?)`, es.ns, id, id); err != nil {
			return fmt.Errorf("deleting incident relations for entity %q: %w", id, err)
		}
		// Delete entity
		if _, err := tx.Exec(`DELETE FROM entities WHERE ns = ? AND id = ?`, es.ns, id); err != nil {
			return fmt.Errorf("deleting entity %q: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete entity %q in namespace %q: %w", id, es.ns, err)
	}

	if es.syncer != nil {
		if sErr := es.syncer.DeleteContent(ctx, id); sErr != nil {
			return fmt.Errorf("delete entity %q: vector sync: %w", id, sErr)
		}
	}
	return nil
}

// DeleteEdge removes a specific directed relation between fromID and toID with relationType.
func (es *EntityService) DeleteEdge(_ context.Context, fromID, toID, relationType string) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	err := es.store.writeTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM relations WHERE ns = ? AND src = ? AND dst = ? AND relation_type = ?`,
			es.ns, fromID, toID, relationType)
		if err != nil {
			return fmt.Errorf("deleting relation %q->%q (%s): %w", fromID, toID, relationType, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete edge %q->%q (%s) in namespace %q: %w", fromID, toID, relationType, es.ns, err)
	}
	return nil
}

// Traverse performs a breadth-first sub-graph walk from startID following directed relations
// and returns all reachable nodes and the edges that connect them up to depth hops away.
//
//   - depth <= 0: returns only the start node with an empty edge list.
//   - depth >= 1: returns reachable nodes and all intra-subgraph edges.
//   - depth is capped at 10 to prevent runaway traversals.
//   - uses a recursive CTE with cycle guard and depth cap.
func (es *EntityService) Traverse(ctx context.Context, startID string, depth int) ([]gleann.MemoryGraphNode, []gleann.MemoryGraphEdge, error) {
	if depth < 0 {
		depth = 0
	}
	if depth > 10 {
		depth = 10
	}

	// 1. Verify start node exists in namespace
	var startNode gleann.MemoryGraphNode
	var attrsJSON string
	err := es.store.reader.QueryRowContext(ctx,
		`SELECT id, type, content, attributes FROM entities WHERE ns = ? AND id = ?`,
		es.ns, startID,
	).Scan(&startNode.ID, &startNode.Type, &startNode.Content, &attrsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("traverse start node %q: %w", startID, err)
	}
	startNode.Attributes, _ = gleann.JSONToAttributes(attrsJSON)

	// Depth 0: return start node only, no edges
	if depth == 0 {
		return []gleann.MemoryGraphNode{startNode}, nil, nil
	}

	// 2. Depth >= 1: Recursive CTE to find all reachable nodes.
	// UNION keeps one row per (id, depth), so cycles and dense graphs cost
	// at most entities x depth rows; no per-path bookkeeping is needed.
	const traverseNodesSQL = `
WITH RECURSIVE reachable(id, depth) AS (
  SELECT ?, 0
  UNION
  SELECT r.dst, w.depth + 1
  FROM relations r
  JOIN reachable w ON r.src = w.id
  WHERE r.ns = ? AND w.depth < ?
)
SELECT DISTINCT e.id, e.type, e.content, e.attributes, min(w.depth) AS min_depth
FROM reachable w
JOIN entities e ON e.id = w.id
WHERE e.ns = ?
GROUP BY e.id, e.type, e.content, e.attributes
ORDER BY min_depth, e.id`

	rows, err := es.store.reader.QueryContext(ctx, traverseNodesSQL, startID, es.ns, depth, es.ns)
	if err != nil {
		return nil, nil, fmt.Errorf("traverse nodes from %q: %w", startID, err)
	}
	defer rows.Close()

	var nodes []gleann.MemoryGraphNode
	for rows.Next() {
		var n gleann.MemoryGraphNode
		var attrs string
		var minDepth int
		if err := rows.Scan(&n.ID, &n.Type, &n.Content, &attrs, &minDepth); err != nil {
			return nil, nil, fmt.Errorf("scan traverse node: %w", err)
		}
		n.Attributes, _ = gleann.JSONToAttributes(attrs)
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterating traverse nodes: %w", err)
	}

	if len(nodes) == 0 {
		return nil, nil, nil
	}

	// Collect all intra-subgraph relations whose endpoints both belong to the reachable set.
	const traverseEdgesSQL = `
WITH RECURSIVE reachable(id, depth) AS (
  SELECT ?, 0
  UNION
  SELECT r.dst, w.depth + 1
  FROM relations r
  JOIN reachable w ON r.src = w.id
  WHERE r.ns = ? AND w.depth < ?
),
node_ids AS (
  SELECT DISTINCT w.id FROM reachable w JOIN entities e ON e.id = w.id WHERE e.ns = ?
)
SELECT r.src, r.dst, r.relation_type, r.weight, r.attributes
FROM relations r
JOIN node_ids a ON r.src = a.id
JOIN node_ids b ON r.dst = b.id
WHERE r.ns = ?
ORDER BY r.src, r.dst, r.relation_type`

	edgeRows, err := es.store.reader.QueryContext(ctx, traverseEdgesSQL, startID, es.ns, depth, es.ns, es.ns)
	if err != nil {
		return nil, nil, fmt.Errorf("traverse edges from %q: %w", startID, err)
	}
	defer edgeRows.Close()

	var edges []gleann.MemoryGraphEdge
	for edgeRows.Next() {
		var e gleann.MemoryGraphEdge
		var attrs string
		if err := edgeRows.Scan(&e.From, &e.To, &e.RelationType, &e.Weight, &attrs); err != nil {
			return nil, nil, fmt.Errorf("scan traverse edge: %w", err)
		}
		e.Attributes, _ = gleann.JSONToAttributes(attrs)
		edges = append(edges, e)
	}
	if err := edgeRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterating traverse edges: %w", err)
	}

	return nodes, edges, nil
}

// randomSuffix returns 8 random hex characters for archive IDs.
func randomSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}
