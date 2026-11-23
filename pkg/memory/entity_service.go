package memory

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
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

	// 4. VectorSyncer hook. One index publication per inject request when the
	// syncer batches (RFC 005: every publication bumps vector_generation and
	// makes cached searchers reload the delta), one per entity otherwise. A
	// failure is reported, never dropped silently; the rows are already
	// committed, and inject is idempotent, so the caller can retry.
	if es.syncer != nil {
		// Deduplicate by node ID BEFORE filtering empty content: the database
		// upsert is last-wins, so only the last occurrence of an entity is the
		// content that was committed. Filtering first would publish an earlier
		// text that the same transaction already replaced (e.g. a cleared
		// duplicate whose stale content is still in the payload).
		effective := make([]int, 0, len(payload.Nodes))
		seen := make(map[string]int, len(payload.Nodes))
		for i := range payload.Nodes {
			if at, ok := seen[payload.Nodes[i].ID]; ok {
				effective[at] = i
				continue
			}
			seen[payload.Nodes[i].ID] = len(effective)
			effective = append(effective, i)
		}

		if batch, ok := es.syncer.(gleann.VectorBatchSyncer); ok {
			contents := make([]gleann.VectorContent, 0, len(effective))
			for _, i := range effective {
				n := &payload.Nodes[i]
				if n.Content == "" {
					continue
				}
				contents = append(contents, gleann.VectorContent{NodeID: n.ID, Content: n.Content, Attrs: n.Attributes})
			}
			if len(contents) > 0 {
				if sErr := batch.AddContents(ctx, contents); sErr != nil {
					return fmt.Errorf("vector sync %d entities: %w", len(contents), sErr)
				}
			}
		} else {
			for _, i := range effective {
				n := &payload.Nodes[i]
				if n.Content == "" {
					continue
				}
				if sErr := es.syncer.AddContent(ctx, n.ID, n.Content, n.Attributes); sErr != nil {
					return fmt.Errorf("vector sync node %q: %w", n.ID, sErr)
				}
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

// ── Read-side queries (unified memory recall, graph layer) ──────────────────

const (
	// maxRecallQueryTokens bounds the number of query terms turned into SQL
	// predicates, keeping the expression tree well below SQLite's depth limit.
	maxRecallQueryTokens = 32
	// maxRecallRelationTypes bounds the relation-type filter so its IN list
	// stays well below SQLite's bind-parameter limit.
	maxRecallRelationTypes = 256
	// recallScanBatch is the page size used by the FoldCase Go-side table
	// scans that every entity and relation search now performs.
	recallScanBatch = 512
)

// SearchEntities returns up to limit entities of the namespace whose ID, type,
// or content matches the query: every whitespace-separated token must occur as
// a case-insensitive substring in at least one of those fields. Results are
// ordered by ID for determinism. An empty query or non-positive limit yields
// no results.
//
// Matching uses FoldCase in Go, so stored Unicode text matches an ASCII query
// regardless of direction: "İstanbul" matches "istanbul", "Diyarbakır" matches
// "DIYARBAKIR", and "ÄRGER" matches "ärger". SQLite's LOWER() only folds ASCII
// and therefore cannot be used for stored rows that may hold non-ASCII text.
func (es *EntityService) SearchEntities(ctx context.Context, query string, limit int) ([]gleann.MemoryGraphNode, error) {
	tokens := recallQueryTokens(query)
	if len(tokens) == 0 || limit <= 0 {
		return nil, nil
	}
	return es.scanEntities(ctx, tokens, limit)
}

// scanEntities pages the namespace in ID order and applies token matching in Go
// with Unicode-aware lower-casing, stopping once limit matches were found.
func (es *EntityService) scanEntities(ctx context.Context, tokens []string, limit int) ([]gleann.MemoryGraphNode, error) {
	var out []gleann.MemoryGraphNode
	for offset := 0; ; offset += recallScanBatch {
		rows, err := es.store.reader.QueryContext(ctx,
			`SELECT id, type, content, attributes FROM entities WHERE ns = ? ORDER BY id LIMIT ? OFFSET ?`,
			es.ns, recallScanBatch, offset)
		if err != nil {
			return nil, fmt.Errorf("search entities in namespace %q: %w", es.ns, err)
		}
		nodes, err := scanGraphNodes(rows)
		if err != nil {
			return nil, fmt.Errorf("search entities in namespace %q: %w", es.ns, err)
		}
		for _, n := range nodes {
			if entityMatchesTokens(n, tokens) {
				out = append(out, n)
				if len(out) >= limit {
					return out, nil
				}
			}
		}
		if len(nodes) < recallScanBatch {
			break
		}
	}
	return out, nil
}

// SearchRelations returns up to limit relations of the namespace whose
// relation type matches every query token (case-insensitive substring).
// Results are ordered by (src, dst, relation_type). An empty query or
// non-positive limit yields no results. Matching uses the same FoldCase Go-side
// scan as SearchEntities so stored Unicode relation types match ASCII queries.
func (es *EntityService) SearchRelations(ctx context.Context, query string, limit int) ([]gleann.MemoryGraphEdge, error) {
	tokens := recallQueryTokens(query)
	if len(tokens) == 0 || limit <= 0 {
		return nil, nil
	}
	return es.scanRelations(ctx, tokens, limit)
}

// scanRelations pages relations in deterministic order and applies token
// matching in Go with Unicode-aware lower-casing.
func (es *EntityService) scanRelations(ctx context.Context, tokens []string, limit int) ([]gleann.MemoryGraphEdge, error) {
	var out []gleann.MemoryGraphEdge
	for offset := 0; ; offset += recallScanBatch {
		rows, err := es.store.reader.QueryContext(ctx,
			`SELECT src, dst, relation_type, weight, attributes FROM relations WHERE ns = ? ORDER BY src, dst, relation_type LIMIT ? OFFSET ?`,
			es.ns, recallScanBatch, offset)
		if err != nil {
			return nil, fmt.Errorf("search relations in namespace %q: %w", es.ns, err)
		}
		edges, err := scanGraphEdges(rows)
		if err != nil {
			return nil, fmt.Errorf("search relations in namespace %q: %w", es.ns, err)
		}
		for _, e := range edges {
			if relationMatchesTokens(e.RelationType, tokens) {
				out = append(out, e)
				if len(out) >= limit {
					return out, nil
				}
			}
		}
		if len(edges) < recallScanBatch {
			break
		}
	}
	return out, nil
}

// EntityRelations returns up to limit relations incident to any of ids, as
// source OR destination (outgoing and incoming), optionally restricted to the
// given relation types (case-insensitive exact match). The (ns, src, dst,
// relation_type) primary key guarantees duplicate-free results, ordered by
// (src, dst, relation_type). Empty ids or a non-positive limit yield no
// results. Empty/whitespace filter entries are ignored and duplicate filters
// are collapsed; filter entries are compared in Go with FoldCase, so an ASCII
// filter such as "istanbul" matches a stored "İstanbul" relation type.
func (es *EntityService) EntityRelations(ctx context.Context, ids []string, relationTypes []string, limit int) ([]gleann.MemoryGraphEdge, error) {
	ids = uniqueNonEmpty(ids)
	if len(ids) == 0 || limit <= 0 {
		return nil, nil
	}
	types := NormalizeRelationTypes(relationTypes)

	ph := sqlPlaceholders(len(ids))
	q := `SELECT src, dst, relation_type, weight, attributes FROM relations
WHERE ns = ? AND (src IN (` + ph + `) OR dst IN (` + ph + `))`
	args := make([]any, 0, 2*len(ids)+len(types)+2)
	args = append(args, es.ns)
	for _, id := range ids {
		args = append(args, id)
	}
	for _, id := range ids {
		args = append(args, id)
	}

	if len(types) == 0 {
		q += ` ORDER BY src, dst, relation_type LIMIT ?`
		args = append(args, limit)
		return es.queryRelations(ctx, q, args...)
	}
	// SQLite LOWER() folds ASCII only, so an ASCII filter would never match a
	// stored "İstanbul" or "Diyarbakır" relation type. Page the ordered rows and
	// compare every entry with FoldCase in Go.
	q += ` ORDER BY src, dst, relation_type LIMIT ? OFFSET ?`
	return es.scanEntityRelations(ctx, q, args, types, limit)
}

func (es *EntityService) queryRelations(ctx context.Context, q string, args ...any) ([]gleann.MemoryGraphEdge, error) {
	rows, err := es.store.reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("entity relations in namespace %q: %w", es.ns, err)
	}
	edges, err := scanGraphEdges(rows)
	if err != nil {
		return nil, fmt.Errorf("entity relations in namespace %q: %w", es.ns, err)
	}
	return edges, nil
}

func (es *EntityService) scanEntityRelations(ctx context.Context, q string, args []any, types []string, limit int) ([]gleann.MemoryGraphEdge, error) {
	var out []gleann.MemoryGraphEdge
	for offset := 0; ; offset += recallScanBatch {
		full := make([]any, 0, len(args)+2)
		full = append(full, args...)
		full = append(full, recallScanBatch, offset)
		rows, err := es.store.reader.QueryContext(ctx, q, full...)
		if err != nil {
			return nil, fmt.Errorf("entity relations in namespace %q: %w", es.ns, err)
		}
		edges, err := scanGraphEdges(rows)
		if err != nil {
			return nil, fmt.Errorf("entity relations in namespace %q: %w", es.ns, err)
		}
		for _, e := range edges {
			if relationTypeAllowed(e.RelationType, types) {
				out = append(out, e)
				if len(out) >= limit {
					return out, nil
				}
			}
		}
		if len(edges) < recallScanBatch {
			break
		}
	}
	return out, nil
}

// EntitiesByIDs returns the entities of the namespace with the given IDs,
// ordered by ID. Missing IDs are skipped; empty ids yield no results.
func (es *EntityService) EntitiesByIDs(ctx context.Context, ids []string) ([]gleann.MemoryGraphNode, error) {
	ids = uniqueNonEmpty(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	q := `SELECT id, type, content, attributes FROM entities WHERE ns = ? AND id IN (` +
		sqlPlaceholders(len(ids)) + `) ORDER BY id`
	args := make([]any, 0, len(ids)+1)
	args = append(args, es.ns)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := es.store.reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("entities by id in namespace %q: %w", es.ns, err)
	}
	nodes, err := scanGraphNodes(rows)
	if err != nil {
		return nil, fmt.Errorf("entities by id in namespace %q: %w", es.ns, err)
	}
	return nodes, nil
}

// NormalizeRelationTypes trims, drops empty entries, deduplicates
// case-insensitively, and caps an optional relation-type filter. It is shared
// by the store queries and the server-side recall graph so that an empty filter
// entry can never silently disable every relation.
func NormalizeRelationTypes(types []string) []string {
	if len(types) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(types))
	out := make([]string, 0, min(len(types), maxRecallRelationTypes))
	for _, rt := range types {
		rt = strings.TrimSpace(rt)
		if rt == "" {
			continue
		}
		key := FoldCase(rt)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, rt)
		if len(out) == maxRecallRelationTypes {
			break
		}
	}
	return out
}

// recallQueryTokens splits, case-folds, deduplicates, and caps the query
// tokens. Bounding the count keeps the generated predicate tree below SQLite's
// expression-depth limit for arbitrarily large client queries.
func recallQueryTokens(query string) []string {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, min(len(fields), maxRecallQueryTokens))
	for _, tok := range fields {
		tok = FoldCase(tok)
		if tok == "" || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
		if len(out) == maxRecallQueryTokens {
			break
		}
	}
	return out
}

// uniqueNonEmpty removes empty and duplicate values while preserving order.
func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// entityMatchesTokens applies the AND-of-ORs token rule in Go.
func entityMatchesTokens(n gleann.MemoryGraphNode, tokens []string) bool {
	id := FoldCase(n.ID)
	typ := FoldCase(n.Type)
	content := FoldCase(n.Content)
	for _, tok := range tokens {
		if !strings.Contains(id, tok) && !strings.Contains(typ, tok) && !strings.Contains(content, tok) {
			return false
		}
	}
	return true
}

// relationMatchesTokens reports whether every token occurs in the folded
// relation type.
func relationMatchesTokens(relation string, tokens []string) bool {
	rel := FoldCase(relation)
	for _, tok := range tokens {
		if !strings.Contains(rel, tok) {
			return false
		}
	}
	return true
}

// FoldCase normalizes a string for locale-independent, Unicode-aware
// case-insensitive comparison. Lower-casing alone misses the Turkish
// dotless/dotted I pairs (ı/I and İ/i), so the string is upper-cased first.
func FoldCase(s string) string {
	return strings.ToLower(strings.ToUpper(s))
}

// relationTypeAllowed reports whether rel passes the normalized relation-type
// filter (case-insensitive exact match; empty filter allows everything).
func relationTypeAllowed(rel string, types []string) bool {
	if len(types) == 0 {
		return true
	}
	folded := FoldCase(rel)
	for _, t := range types {
		if FoldCase(t) == folded {
			return true
		}
	}
	return false
}

// likeContainsPattern wraps a token in % wildcards, escaping LIKE
// metacharacters so they match literally (paired with ESCAPE '\').
func likeContainsPattern(token string) string {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + esc.Replace(strings.ToLower(token)) + "%"
}

// sqlPlaceholders returns "?, ?, ..." with n bind markers.
func sqlPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return "?" + strings.Repeat(", ?", n-1)
}

// scanGraphNodes drains entity rows (id, type, content, attributes) into
// MemoryGraphNodes and closes the rows.
func scanGraphNodes(rows *sql.Rows) ([]gleann.MemoryGraphNode, error) {
	defer rows.Close()
	var nodes []gleann.MemoryGraphNode
	for rows.Next() {
		var n gleann.MemoryGraphNode
		var attrs string
		if err := rows.Scan(&n.ID, &n.Type, &n.Content, &attrs); err != nil {
			return nil, fmt.Errorf("scan entity row: %w", err)
		}
		n.Attributes, _ = gleann.JSONToAttributes(attrs)
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entity rows: %w", err)
	}
	return nodes, nil
}

// scanGraphEdges drains relation rows (src, dst, relation_type, weight,
// attributes) into MemoryGraphEdges and closes the rows.
func scanGraphEdges(rows *sql.Rows) ([]gleann.MemoryGraphEdge, error) {
	defer rows.Close()
	var edges []gleann.MemoryGraphEdge
	for rows.Next() {
		var e gleann.MemoryGraphEdge
		var attrs string
		if err := rows.Scan(&e.From, &e.To, &e.RelationType, &e.Weight, &attrs); err != nil {
			return nil, fmt.Errorf("scan relation row: %w", err)
		}
		e.Attributes, _ = gleann.JSONToAttributes(attrs)
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate relation rows: %w", err)
	}
	return edges, nil
}
