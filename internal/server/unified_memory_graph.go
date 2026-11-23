package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// Hard safety caps so a huge top_k on a dense graph can never exhaust memory.
const (
	maxRecallGraphNodes = 256
	maxRecallGraphEdges = 512
	maxRecallGraphDepth = 10
)

// graphRecallService is the read-side capability of the memory graph used by
// the unified recall graph layer. *memory.EntityService implements it; services
// without it (e.g. minimal stubs) degrade to an empty graph layer instead of
// breaking multi-layer recall.
type graphRecallService interface {
	SearchEntities(ctx context.Context, query string, limit int) ([]gleann.MemoryGraphNode, error)
	SearchRelations(ctx context.Context, query string, limit int) ([]gleann.MemoryGraphEdge, error)
	EntityRelations(ctx context.Context, ids []string, relationTypes []string, limit int) ([]gleann.MemoryGraphEdge, error)
	EntitiesByIDs(ctx context.Context, ids []string) ([]gleann.MemoryGraphNode, error)
}

// recallGraphImpl queries the knowledge graph of the resolved index namespace
// for entities matching the request query (ID, type, content, or relation
// type; case-insensitive) and returns them with their incoming and outgoing
// relations, bounded by top_k and depth. Every failure path returns nil so a
// missing, private, or empty index never errors out multi-layer recall and
// never leaks entities across indexes.
func (s *Server) recallGraphImpl(ctx context.Context, req UnifiedRecallRequest) *RecallGraph {
	if strings.TrimSpace(req.Query) == "" {
		return nil
	}

	// Index selection mirrors recallVector: an explicit index must be
	// accessible; the first-accessible fallback only applies when neither
	// project nor target pins the request.
	indexName := req.Index
	if indexName != "" {
		if !s.isIndexAccessible(indexName) {
			return nil
		}
	} else if req.Project == "" && req.Target == "" {
		indexName = s.firstAccessibleIndexName()
	}
	if indexName == "" {
		return nil
	}

	svc, err := s.memoryPool.get(indexName)
	if err != nil {
		return nil
	}
	es, ok := svc.(graphRecallService)
	if !ok {
		return nil
	}

	graph, err := buildRecallGraph(ctx, es, req)
	if err != nil {
		return nil
	}
	return graph
}

// buildRecallGraph assembles the bounded recall subgraph. Seed entities are
// inserted first, but part of the node budget is reserved for neighbours and
// the seeds' own relations are expanded before the global relation-type search
// runs: a matched seed must never lose its incident edge to a broad
// relation-type match or to a seed list that fills top_k.
func buildRecallGraph(ctx context.Context, es graphRecallService, req UnifiedRecallRequest) (*RecallGraph, error) {
	nodeCap := req.TopK
	if nodeCap <= 0 {
		nodeCap = 5
	}
	if nodeCap > maxRecallGraphNodes {
		nodeCap = maxRecallGraphNodes
	}
	edgeCap := 2 * nodeCap
	depth := req.Depth
	if depth <= 0 {
		depth = 2
	}
	if depth > maxRecallGraphDepth {
		depth = maxRecallGraphDepth
	}

	// Normalize once: empty/whitespace entries are ignored and duplicates are
	// collapsed, so relations:[""] behaves exactly like an omitted filter.
	relations := memory.NormalizeRelationTypes(req.Relations)

	acc := newRecallGraphAccumulator(nodeCap, edgeCap)

	seeds, err := es.SearchEntities(ctx, req.Query, nodeCap)
	if err != nil {
		return nil, fmt.Errorf("recall graph seeds: %w", err)
	}

	// Reserve roughly half the node budget for neighbours when expansion is
	// possible; the rest of the seeds are queued for backfill.
	seedQuota := len(seeds)
	if depth > 0 && nodeCap >= 2 {
		seedQuota = (nodeCap + 1) / 2
		if seedQuota < 1 {
			seedQuota = 1
		}
		if seedQuota > len(seeds) {
			seedQuota = len(seeds)
		}
	}

	frontier := make([]string, 0, seedQuota)
	for _, n := range seeds[:seedQuota] {
		if acc.addNode(n) {
			frontier = append(frontier, n.ID)
		}
	}
	backfill := seeds[seedQuota:]

	// Expand the matched seeds' incident relations before consulting
	// SearchRelations so their own edges always get first claim on the budget.
	if depth > 0 && len(frontier) > 0 {
		if err := acc.expand(ctx, es, frontier, depth, relations); err != nil {
			return nil, err
		}
	}

	// Return reserved slots to seeds that did not get expanded when the
	// neighbourhood left room. Their incident relations have not been fetched
	// yet, so expand them too while depth and the node/edge budget allow: an
	// edge between two backfilled seeds must not be dropped just because the
	// first quota had no relations.
	var backfillIDs []string
	for _, n := range backfill {
		if acc.addNode(n) {
			backfillIDs = append(backfillIDs, n.ID)
		}
	}
	if depth > 0 && len(backfillIDs) > 0 && len(acc.edges) < edgeCap {
		if err := acc.expand(ctx, es, backfillIDs, depth, relations); err != nil {
			return nil, err
		}
	}

	// Relation-type matches fill only the remaining budget, and an edge whose
	// endpoints cannot fit the node cap is never admitted: result() would drop
	// it, so spending edge budget on it would starve a real edge.
	if len(acc.edges) < edgeCap {
		matched, err := es.SearchRelations(ctx, req.Query, edgeCap)
		if err != nil {
			return nil, fmt.Errorf("recall graph relation match: %w", err)
		}
		added, err := acc.applyEdges(ctx, es, matched, relations)
		if err != nil {
			return nil, err
		}
		if depth > 0 && len(added) > 0 {
			if err := acc.expand(ctx, es, added, depth, relations); err != nil {
				return nil, err
			}
		}
	}

	return acc.result(), nil
}

// expand walks incoming and outgoing relations from frontier up to depth hops,
// admitting only edges whose endpoints survive the node budget.
func (a *recallGraphAccumulator) expand(ctx context.Context, es graphRecallService, frontier []string, depth int, relations []string) error {
	for hop := 0; hop < depth && len(frontier) > 0; hop++ {
		if len(a.edges) >= a.edgeCap {
			break
		}
		// Fetch up to the global edge cap (not merely the remaining room) so
		// duplicates or unfittable endpoints cannot hide a valid edge.
		edges, err := es.EntityRelations(ctx, frontier, relations, a.edgeCap)
		if err != nil {
			return fmt.Errorf("recall graph hop %d: %w", hop+1, err)
		}
		if len(edges) == 0 {
			break
		}
		added, err := a.applyEdges(ctx, es, edges, relations)
		if err != nil {
			return fmt.Errorf("recall graph hop %d: %w", hop+1, err)
		}
		if len(added) == 0 {
			break
		}
		frontier = added
	}
	return nil
}

// recallGraphAccumulator collects recall nodes and edges with deduplication
// and hard budgets. Edges whose endpoints do not fit the node budget are never
// admitted, so result() never has to discard a valid edge.
type recallGraphAccumulator struct {
	nodes   map[string]RecallGraphNode
	edges   map[string]RecallGraphEdge
	nodeCap int
	edgeCap int
}

func newRecallGraphAccumulator(nodeCap, edgeCap int) *recallGraphAccumulator {
	return &recallGraphAccumulator{
		nodes:   make(map[string]RecallGraphNode),
		edges:   make(map[string]RecallGraphEdge),
		nodeCap: nodeCap,
		edgeCap: edgeCap,
	}
}

// addNode stores n unless it is already present or the node budget is spent.
// It reports whether the node was newly added.
func (a *recallGraphAccumulator) addNode(n gleann.MemoryGraphNode) bool {
	if _, ok := a.nodes[n.ID]; ok {
		return false
	}
	if len(a.nodes) >= a.nodeCap {
		return false
	}
	a.nodes[n.ID] = RecallGraphNode{ID: n.ID, Type: n.Type, Content: n.Content}
	return true
}

// addEdge stores e unless a duplicate, filtered out by the allowed relation
// types, or over the edge budget.
func (a *recallGraphAccumulator) addEdge(e gleann.MemoryGraphEdge, allowed []string) {
	if !recallRelationAllowed(e.RelationType, allowed) {
		return
	}
	key := e.From + "\x00" + e.To + "\x00" + e.RelationType
	if _, ok := a.edges[key]; ok {
		return
	}
	if len(a.edges) >= a.edgeCap {
		return
	}
	a.edges[key] = RecallGraphEdge{From: e.From, To: e.To, Relation: e.RelationType, Weight: e.Weight}
}

// applyEdges selects the relations whose endpoints fit the remaining node
// budget, fetches those endpoints, stores the nodes, then stores the edges.
// It returns the IDs of nodes newly added by this batch (the next BFS
// frontier). Edges pointing at entities that do not exist are dropped without
// consuming edge budget.
func (a *recallGraphAccumulator) applyEdges(ctx context.Context, es graphRecallService, edges []gleann.MemoryGraphEdge, allowed []string) ([]string, error) {
	if len(edges) == 0 {
		return nil, nil
	}

	predicted := make(map[string]bool, len(a.nodes)+len(edges))
	for id := range a.nodes {
		predicted[id] = true
	}
	predictedCount := len(a.nodes)
	selected := make(map[string]bool, len(edges))
	candidates := make([]gleann.MemoryGraphEdge, 0, len(edges))

	for _, e := range edges {
		if !recallRelationAllowed(e.RelationType, allowed) {
			continue
		}
		key := e.From + "\x00" + e.To + "\x00" + e.RelationType
		_, stored := a.edges[key]
		isNew := !stored && !selected[key]
		if isNew && len(a.edges)+len(candidates) >= a.edgeCap {
			break
		}

		var needed []string
		seenNeed := make(map[string]bool, 2)
		for _, id := range []string{e.From, e.To} {
			if !predicted[id] && !seenNeed[id] {
				seenNeed[id] = true
				needed = append(needed, id)
			}
		}
		if predictedCount+len(needed) > a.nodeCap {
			// result() would drop this edge; skip it without spending budget.
			continue
		}
		for _, id := range needed {
			predicted[id] = true
		}
		predictedCount += len(needed)

		if isNew {
			selected[key] = true
			candidates = append(candidates, e)
		}
	}

	// Fetch the predicted endpoints that are not already present. Dangling
	// relation rows simply stay absent.
	fetch := make([]string, 0, len(predicted))
	for id := range predicted {
		if _, ok := a.nodes[id]; !ok {
			fetch = append(fetch, id)
		}
	}
	sort.Strings(fetch)
	var added []string
	if len(fetch) > 0 {
		nodes, err := es.EntitiesByIDs(ctx, fetch)
		if err != nil {
			return nil, fmt.Errorf("recall graph endpoints: %w", err)
		}
		for _, n := range nodes {
			if a.addNode(n) {
				added = append(added, n.ID)
			}
		}
	}

	for _, e := range candidates {
		if _, ok := a.nodes[e.From]; !ok {
			continue
		}
		if _, ok := a.nodes[e.To]; !ok {
			continue
		}
		a.addEdge(e, allowed)
	}
	return added, nil
}

// result returns the deduplicated, deterministically ordered subgraph, keeping
// only edges whose endpoints both fit the node budget. No nodes → nil.
func (a *recallGraphAccumulator) result() *RecallGraph {
	if len(a.nodes) == 0 {
		return nil
	}
	nodes := make([]RecallGraphNode, 0, len(a.nodes))
	for _, n := range a.nodes {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	edges := make([]RecallGraphEdge, 0, len(a.edges))
	for _, e := range a.edges {
		if _, ok := a.nodes[e.From]; !ok {
			continue
		}
		if _, ok := a.nodes[e.To]; !ok {
			continue
		}
		edges = append(edges, e)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Relation < edges[j].Relation
	})
	return &RecallGraph{Nodes: nodes, Edges: edges}
}

// recallRelationAllowed reports whether rel passes the optional relation-type
// filter (case-insensitive exact match; empty filter allows everything).
func recallRelationAllowed(rel string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	folded := memory.FoldCase(rel)
	for _, a := range allowed {
		if memory.FoldCase(a) == folded {
			return true
		}
	}
	return false
}

// injectGraphRelationship stores a relationship in the knowledge graph.
func (s *Server) injectGraphRelationship(r *http.Request, indexName string, rel IngestRelationship) error {
	svc, err := s.memoryPool.get(indexName)
	if err != nil {
		return err
	}

	weight := rel.Weight
	if weight == 0 {
		weight = 1.0
	}

	// Merge user-provided attributes with auto-generated metadata.
	attrs := rel.Attributes
	if attrs == nil {
		attrs = make(map[string]any)
	}

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: rel.From, Type: "entity", Content: rel.From},
			{ID: rel.To, Type: "entity", Content: rel.To},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: rel.From, To: rel.To, RelationType: rel.Relation, Weight: weight, Attributes: attrs},
		},
	}

	return svc.InjectEntities(context.Background(), payload)
}
