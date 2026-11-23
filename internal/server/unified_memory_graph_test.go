package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// injectGraph seeds nodes and edges directly through the pooled memory service,
// bypassing the HTTP ingest endpoint so tests can control node type/content and
// relation types precisely.
func injectGraph(t *testing.T, s *Server, index string, nodes []gleann.MemoryGraphNode, edges []gleann.MemoryGraphEdge) {
	t.Helper()
	svc, err := s.memoryPool.get(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.InjectEntities(context.Background(), gleann.GraphInjectionPayload{
		Nodes: nodes,
		Edges: edges,
	}); err != nil {
		t.Fatal(err)
	}
}

// decodeRecall decodes a unified recall response, failing the test on bad JSON.
func decodeRecall(t *testing.T, w *httptest.ResponseRecorder) UnifiedRecallResponse {
	t.Helper()
	var resp UnifiedRecallResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return resp
}

// seedGraphRelationship ingests one relationship through the unified ingest
// endpoint (injectGraphRelationship path) into the given project index.
func seedGraphRelationship(t *testing.T, s *Server, project, from, to, relation string, weight float64) {
	t.Helper()
	body := `{"project":"` + project + `","relationships":[{"from":"` + from + `","to":"` + to +
		`","relation":"` + relation + `","weight":` + jsonNumber(weight) + `}]}`
	w := unifiedPost(s, s.handleUnifiedIngest, "/api/memory/ingest", body, nil)
	if resp := decodeIngest(t, w); w.Code != http.StatusOK || resp.EdgesCreated != 1 {
		t.Fatalf("seed relationship: %d %s", w.Code, w.Body.String())
	}
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func graphHasNode(g *RecallGraph, id string) bool {
	if g == nil {
		return false
	}
	for _, n := range g.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

func graphFindEdge(g *RecallGraph, from, to, relation string) *RecallGraphEdge {
	if g == nil {
		return nil
	}
	for i := range g.Edges {
		e := &g.Edges[i]
		if e.From == from && e.To == to && e.Relation == relation {
			return e
		}
	}
	return nil
}

// assertGraphWellFormed checks bounding invariants: no duplicate nodes or
// edges, and every edge endpoints present in the node set.
func assertGraphWellFormed(t *testing.T, g *RecallGraph, nodeCap, edgeCap int) {
	t.Helper()
	if g == nil {
		return
	}
	if len(g.Nodes) > nodeCap {
		t.Errorf("nodes = %d, want <= %d", len(g.Nodes), nodeCap)
	}
	if len(g.Edges) > edgeCap {
		t.Errorf("edges = %d, want <= %d", len(g.Edges), edgeCap)
	}
	seenNode := make(map[string]bool)
	for _, n := range g.Nodes {
		if seenNode[n.ID] {
			t.Errorf("duplicate node %q", n.ID)
		}
		seenNode[n.ID] = true
	}
	seenEdge := make(map[string]bool)
	for _, e := range g.Edges {
		key := e.From + "\x00" + e.To + "\x00" + e.Relation
		if seenEdge[key] {
			t.Errorf("duplicate edge %s -[%s]-> %s", e.From, e.Relation, e.To)
		}
		seenEdge[key] = true
		if !seenNode[e.From] || !seenNode[e.To] {
			t.Errorf("edge %s -[%s]-> %s references missing node", e.From, e.Relation, e.To)
		}
	}
}

// TestRecallGraph_ReturnsIngestedRelationship is the roadmap measure: ingest a
// relationship, recall with layers=["graph"] and a matching query returns it.
func TestRecallGraph_ReturnsIngestedRelationship(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
	seedGraphRelationship(t, s, "proj", "AuthService", "JWTValidator", "DEPENDS_ON", 0.9)

	// Enrich one endpoint with a rich type/content via Service.InjectEntities,
	// as /api/memory/{name}/inject does.
	svc, err := s.memoryPool.get("proj")
	if err != nil {
		t.Fatal(err)
	}
	err = svc.InjectEntities(context.Background(), gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "JWTValidator", Type: "component", Content: "Validates RS256 JWT signatures"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("match by entity ID", func(t *testing.T) {
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
			`{"query":"auth","index":"proj","layers":["graph"]}`, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		resp := decodeRecall(t, w)
		if resp.Graph == nil {
			t.Fatalf("graph layer is nil, want the ingested relationship: %s", w.Body.String())
		}
		if !graphHasNode(resp.Graph, "AuthService") || !graphHasNode(resp.Graph, "JWTValidator") {
			t.Errorf("nodes = %+v, want AuthService and JWTValidator", resp.Graph.Nodes)
		}
		e := graphFindEdge(resp.Graph, "AuthService", "JWTValidator", "DEPENDS_ON")
		if e == nil {
			t.Fatalf("edges = %+v, want AuthService -[DEPENDS_ON]-> JWTValidator", resp.Graph.Edges)
		}
		if e.Weight != 0.9 {
			t.Errorf("weight = %v, want 0.9", e.Weight)
		}
		assertGraphWellFormed(t, resp.Graph, 5, 10)
	})

	t.Run("match by content case-insensitive", func(t *testing.T) {
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
			`{"query":"rs256 signatures","index":"proj","layers":["graph"]}`, nil)
		resp := decodeRecall(t, w)
		if w.Code != http.StatusOK || resp.Graph == nil || !graphHasNode(resp.Graph, "JWTValidator") {
			t.Fatalf("content match: %d %s", w.Code, w.Body.String())
		}
		// Incoming relation of the matched entity must come along.
		if graphFindEdge(resp.Graph, "AuthService", "JWTValidator", "DEPENDS_ON") == nil {
			t.Errorf("incoming edge missing: %+v", resp.Graph.Edges)
		}
		if !graphHasNode(resp.Graph, "AuthService") {
			t.Errorf("edge endpoint node missing: %+v", resp.Graph.Nodes)
		}
	})

	t.Run("match by type", func(t *testing.T) {
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
			`{"query":"COMPONENT","index":"proj","layers":["graph"]}`, nil)
		resp := decodeRecall(t, w)
		if w.Code != http.StatusOK || resp.Graph == nil || !graphHasNode(resp.Graph, "JWTValidator") {
			t.Fatalf("type match: %d %s", w.Code, w.Body.String())
		}
	})

	t.Run("match by relation type", func(t *testing.T) {
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
			`{"query":"depends_on","index":"proj","layers":["graph"]}`, nil)
		resp := decodeRecall(t, w)
		if w.Code != http.StatusOK || resp.Graph == nil ||
			graphFindEdge(resp.Graph, "AuthService", "JWTValidator", "DEPENDS_ON") == nil {
			t.Fatalf("relation match: %d %s", w.Code, w.Body.String())
		}
	})

	t.Run("project shorthand and default index fallback", func(t *testing.T) {
		for _, body := range []string{
			`{"query":"auth","project":"proj","layers":["graph"]}`,
			`{"query":"auth","target":"proj","layers":["graph"]}`,
			`{"query":"auth","layers":["graph"]}`, // first accessible index = proj
		} {
			w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall", body, nil)
			resp := decodeRecall(t, w)
			if w.Code != http.StatusOK || resp.Graph == nil ||
				graphFindEdge(resp.Graph, "AuthService", "JWTValidator", "DEPENDS_ON") == nil {
				t.Errorf("%s: %d %s", body, w.Code, w.Body.String())
			}
		}
	})

	t.Run("context format renders relationships", func(t *testing.T) {
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
			`{"query":"auth","index":"proj","layers":["graph"],"format":"context"}`, nil)
		resp := decodeRecall(t, w)
		if w.Code != http.StatusOK || !strings.Contains(resp.Context, "AuthService -[DEPENDS_ON]-> JWTValidator") {
			t.Fatalf("context format: %d %s", w.Code, w.Body.String())
		}
	})
}

// TestRecallGraph_NonMatchingQuery: a query that matches nothing returns an
// empty/nil graph cleanly with 200 OK.
func TestRecallGraph_NonMatchingQuery(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
	seedGraphRelationship(t, s, "proj", "AuthService", "JWTValidator", "DEPENDS_ON", 1.0)

	w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
		`{"query":"zzz-no-such-entity","index":"proj","layers":["graph"]}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	resp := decodeRecall(t, w)
	if resp.Graph != nil && (len(resp.Graph.Nodes) != 0 || len(resp.Graph.Edges) != 0) {
		t.Errorf("non-matching query returned graph data: %+v", resp.Graph)
	}
}

// TestRecallGraph_AccessScoping: inaccessible, private, or non-existent indexes
// never leak entities, and multi-layer recall still returns 200.
func TestRecallGraph_AccessScoping(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
	createMockIndexMeta(t, s.config.IndexDir, "secret", false, nil)

	// Put data into the private namespace directly through the pool (the pool
	// itself does not enforce HTTP access rules; recall must).
	svc, err := s.memoryPool.get("secret")
	if err != nil {
		t.Fatal(err)
	}
	err = svc.InjectEntities(context.Background(), gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "Secretive", Type: "entity", Content: "secretive payload"},
			{ID: "Vault", Type: "entity", Content: "vault"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "Secretive", To: "Vault", RelationType: "HIDES_IN", Weight: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{
		`{"query":"secretive","index":"secret","layers":["graph"]}`,                   // private index
		`{"query":"secretive","project":"secret","layers":["graph"]}`,                 // private via project
		`{"query":"secretive","index":"ghost","layers":["graph"]}`,                    // non-existent index
		`{"query":"secretive","layers":["graph"]}`,                                    // fallback -> proj (empty ns): no cross-index leak
		`{"query":"secretive","index":"proj","layers":["graph"]}`,                     // accessible but empty namespace
		`{"query":"secretive","index":"secret","layers":["blocks","graph","vector"]}`, // multi-layer stays 200
	} {
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall", body, nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status = %d: %s", body, w.Code, w.Body.String())
			continue
		}
		resp := decodeRecall(t, w)
		if resp.Graph != nil && (len(resp.Graph.Nodes) != 0 || len(resp.Graph.Edges) != 0) {
			t.Errorf("%s: leaked graph data: %+v", body, resp.Graph)
		}
	}
}

// TestRecallGraph_MultiLayerBounding: layers=["blocks","graph"] with top_k caps
// both layers and keeps the graph well-formed on a hub with many leaves.
func TestRecallGraph_MultiLayerBounding(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)

	var facts []string
	for i := 0; i < 6; i++ {
		facts = append(facts, `{"content":"alpha fact number `+jsonNumber(float64(i))+`"}`)
	}
	w := unifiedPost(s, s.handleUnifiedIngest, "/api/memory/ingest",
		`{"project":"proj","facts":[`+strings.Join(facts, ",")+`]}`, nil)
	if decodeIngest(t, w).FactsStored != 6 {
		t.Fatalf("seed facts: %d %s", w.Code, w.Body.String())
	}
	for i := 0; i < 12; i++ {
		leaf := "AlphaLeaf" + jsonNumber(float64(i))
		seedGraphRelationship(t, s, "proj", "AlphaHub", leaf, "LINKS", 1.0)
	}

	w = unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
		`{"query":"alpha","index":"proj","layers":["blocks","graph"],"top_k":3}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	resp := decodeRecall(t, w)
	if len(resp.Blocks) == 0 || len(resp.Blocks) > 3 {
		t.Errorf("blocks = %d, want 1..3", len(resp.Blocks))
	}
	if resp.Graph == nil || len(resp.Graph.Nodes) == 0 {
		t.Fatalf("graph empty despite matching hub: %s", w.Body.String())
	}
	assertGraphWellFormed(t, resp.Graph, 3, 6)
	if !graphHasNode(resp.Graph, "AlphaHub") {
		t.Errorf("seed entity AlphaHub missing: %+v", resp.Graph.Nodes)
	}

	// top_k=1 still answers cleanly within budget.
	w = unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
		`{"query":"alpha","index":"proj","layers":["blocks","graph"],"top_k":1}`, nil)
	resp = decodeRecall(t, w)
	if w.Code != http.StatusOK {
		t.Fatalf("top_k=1 status = %d: %s", w.Code, w.Body.String())
	}
	assertGraphWellFormed(t, resp.Graph, 1, 2)
}

// TestRecallGraph_DepthAndRelationsFilter checks depth-bounded expansion and
// the relations edge-type filter, including incoming edges.
func TestRecallGraph_DepthAndRelationsFilter(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
	seedGraphRelationship(t, s, "proj", "Alpha", "Beta", "CALLS", 1.0)
	seedGraphRelationship(t, s, "proj", "Beta", "Gamma", "USES", 1.0)

	t.Run("depth 1 stops at direct neighbors", func(t *testing.T) {
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
			`{"query":"alpha","index":"proj","layers":["graph"],"depth":1}`, nil)
		resp := decodeRecall(t, w)
		if w.Code != http.StatusOK || resp.Graph == nil {
			t.Fatalf("depth 1: %d %s", w.Code, w.Body.String())
		}
		if !graphHasNode(resp.Graph, "Beta") || graphHasNode(resp.Graph, "Gamma") {
			t.Errorf("depth 1 nodes = %+v, want Alpha+Beta without Gamma", resp.Graph.Nodes)
		}
	})

	t.Run("depth 2 reaches two hops", func(t *testing.T) {
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
			`{"query":"alpha","index":"proj","layers":["graph"],"depth":2}`, nil)
		resp := decodeRecall(t, w)
		if w.Code != http.StatusOK || resp.Graph == nil || !graphHasNode(resp.Graph, "Gamma") {
			t.Fatalf("depth 2: %d %s", w.Code, w.Body.String())
		}
		if graphFindEdge(resp.Graph, "Beta", "Gamma", "USES") == nil {
			t.Errorf("two-hop edge missing: %+v", resp.Graph.Edges)
		}
	})

	t.Run("relations filter keeps only requested types", func(t *testing.T) {
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
			`{"query":"beta","index":"proj","layers":["graph"],"relations":["uses"]}`, nil)
		resp := decodeRecall(t, w)
		if w.Code != http.StatusOK || resp.Graph == nil {
			t.Fatalf("filter: %d %s", w.Code, w.Body.String())
		}
		if graphFindEdge(resp.Graph, "Beta", "Gamma", "USES") == nil {
			t.Errorf("USES edge missing: %+v", resp.Graph.Edges)
		}
		for _, e := range resp.Graph.Edges {
			if !strings.EqualFold(e.Relation, "USES") {
				t.Errorf("unrequested relation %q leaked through filter", e.Relation)
			}
		}
	})

	t.Run("incoming edge of matched entity", func(t *testing.T) {
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
			`{"query":"beta","index":"proj","layers":["graph"],"relations":["CALLS"]}`, nil)
		resp := decodeRecall(t, w)
		if w.Code != http.StatusOK || resp.Graph == nil ||
			graphFindEdge(resp.Graph, "Alpha", "Beta", "CALLS") == nil || !graphHasNode(resp.Graph, "Alpha") {
			t.Fatalf("incoming: %d %s", w.Code, w.Body.String())
		}
	})
}

// TestRecallGraph_NoDuplicateEdges: repeated ingest (idempotent upsert) and a
// diamond graph must not produce duplicate nodes or edges.
func TestRecallGraph_NoDuplicateEdges(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
	seedGraphRelationship(t, s, "proj", "Dia", "Left", "R", 1.0)
	seedGraphRelationship(t, s, "proj", "Dia", "Left", "R", 1.0) // duplicate ingest
	seedGraphRelationship(t, s, "proj", "Dia", "Right", "R", 1.0)
	seedGraphRelationship(t, s, "proj", "Left", "Sink", "R", 1.0)
	seedGraphRelationship(t, s, "proj", "Right", "Sink", "R", 1.0)

	w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
		`{"query":"dia","index":"proj","layers":["graph"],"depth":3,"top_k":20}`, nil)
	resp := decodeRecall(t, w)
	if w.Code != http.StatusOK || resp.Graph == nil {
		t.Fatalf("diamond: %d %s", w.Code, w.Body.String())
	}
	assertGraphWellFormed(t, resp.Graph, 20, 40)
	if len(resp.Graph.Nodes) != 4 || len(resp.Graph.Edges) != 4 {
		t.Errorf("diamond nodes=%d edges=%d, want 4/4: %+v", len(resp.Graph.Nodes), len(resp.Graph.Edges), resp.Graph)
	}
}

// TestRecallGraph_GracefulDegradation covers the nil-without-error paths that
// keep multi-layer recall healthy.
func TestRecallGraph_GracefulDegradation(t *testing.T) {
	t.Run("empty request yields nil", func(t *testing.T) {
		s := unifiedServer(t)
		if got := s.recallGraph(t.Context(), UnifiedRecallRequest{}); got != nil {
			t.Errorf("empty request: %+v", got)
		}
	})

	t.Run("empty query yields nil", func(t *testing.T) {
		s := unifiedServer(t)
		createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
		if got := s.recallGraph(t.Context(), UnifiedRecallRequest{Index: "proj", Query: "  "}); got != nil {
			t.Errorf("blank query: %+v", got)
		}
	})

	t.Run("service without graph query capability yields nil", func(t *testing.T) {
		s := unifiedServer(t)
		createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
		s.memoryPool.handles["proj"] = failingMemoryService{}
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
			`{"query":"anything","index":"proj","layers":["graph"]}`, nil)
		resp := decodeRecall(t, w)
		if w.Code != http.StatusOK || resp.Graph != nil {
			t.Errorf("failing service: %d %s", w.Code, w.Body.String())
		}
	})

	t.Run("unopenable memory store yields nil", func(t *testing.T) {
		s := unifiedServer(t)
		createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
		blockedMemoryDir(t)
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
			`{"query":"anything","index":"proj","layers":["graph"]}`, nil)
		resp := decodeRecall(t, w)
		if w.Code != http.StatusOK || resp.Graph != nil {
			t.Errorf("blocked store: %d %s", w.Code, w.Body.String())
		}
	})

	t.Run("accessible empty index yields nil", func(t *testing.T) {
		s := unifiedServer(t)
		createMockIndexMeta(t, s.config.IndexDir, "empty", true, nil)
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
			`{"query":"anything","index":"empty","layers":["graph"]}`, nil)
		resp := decodeRecall(t, w)
		if w.Code != http.StatusOK || resp.Graph != nil {
			t.Errorf("empty index: %d %s", w.Code, w.Body.String())
		}
	})
}

// TestRecallGraph_SeedRelationsNotStarved is finding 1: global relation-type
// matches must never consume the edge budget before the matched seed's own
// (incoming/outgoing) relations are fetched.
func TestRecallGraph_SeedRelationsNotStarved(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)

	seedGraphRelationship(t, s, "proj", "SigmaSeed", "Upstream", "CALLS", 1.0)
	for i := 0; i < 10; i++ {
		seedGraphRelationship(t, s, "proj",
			"Z"+twoDigits(i), "Z"+twoDigits(i+1), "SIGMA_LINK", 1.0)
	}
	// Make only SigmaSeed match the query text; the Z edges match through their
	// relation type ("SIGMA_LINK").
	injectGraph(t, s, "proj", []gleann.MemoryGraphNode{
		{ID: "SigmaSeed", Type: "service", Content: "sigma seed service"},
	}, nil)

	w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
		`{"query":"sigma","index":"proj","layers":["graph"]}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	resp := decodeRecall(t, w)
	if resp.Graph == nil {
		t.Fatalf("graph layer is nil: %s", w.Body.String())
	}
	if graphFindEdge(resp.Graph, "SigmaSeed", "Upstream", "CALLS") == nil {
		t.Fatalf("seed's own CALLS relation missing (starved by relation matches): %+v", resp.Graph)
	}
	assertGraphWellFormed(t, resp.Graph, 5, 10)
}

func twoDigits(i int) string {
	if i < 10 {
		return "0" + strconv.Itoa(i)
	}
	return strconv.Itoa(i)
}

// TestRecallGraph_SeedsReserveNeighbourSlots is finding 2: a query that fills
// top_k with seeds must still return some relations; seeds reserve part of the
// node budget for their neighbours.
func TestRecallGraph_SeedsReserveNeighbourSlots(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)

	var seeds, leaves []gleann.MemoryGraphNode
	var edges []gleann.MemoryGraphEdge
	for i := 0; i < 5; i++ {
		seed := "S" + twoDigits(i)
		leaf := "L" + twoDigits(i)
		seeds = append(seeds, gleann.MemoryGraphNode{ID: seed, Type: "entity", Content: "alpha seed"})
		leaves = append(leaves, gleann.MemoryGraphNode{ID: leaf, Type: "entity", Content: "leaf"})
		edges = append(edges, gleann.MemoryGraphEdge{From: seed, To: leaf, RelationType: "LINKS", Weight: 1})
	}
	injectGraph(t, s, "proj", append(seeds, leaves...), edges)

	w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
		`{"query":"alpha","index":"proj","layers":["graph"],"top_k":5}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	resp := decodeRecall(t, w)
	if resp.Graph == nil {
		t.Fatalf("graph layer is nil: %s", w.Body.String())
	}
	if len(resp.Graph.Nodes) > 5 {
		t.Errorf("nodes = %d, want <= 5", len(resp.Graph.Nodes))
	}
	if len(resp.Graph.Edges) == 0 {
		t.Fatalf("zero edges although every seed has a relation: %+v", resp.Graph)
	}
	assertGraphWellFormed(t, resp.Graph, 5, 10)

	// Isolated seeds: all five must still be returned when nothing consumes the
	// reserved neighbour slots.
	s2 := unifiedServer(t)
	createMockIndexMeta(t, s2.config.IndexDir, "proj", true, nil)
	var isolated []gleann.MemoryGraphNode
	for i := 0; i < 5; i++ {
		isolated = append(isolated, gleann.MemoryGraphNode{
			ID: "Iso" + twoDigits(i), Type: "entity", Content: "alpha isolated",
		})
	}
	injectGraph(t, s2, "proj", isolated, nil)

	w = unifiedPost(s2, s2.handleUnifiedRecall, "/api/memory/recall",
		`{"query":"alpha","index":"proj","layers":["graph"],"top_k":5}`, nil)
	resp = decodeRecall(t, w)
	if resp.Graph == nil || len(resp.Graph.Nodes) != 5 {
		t.Fatalf("isolated seeds: %d %s", w.Code, w.Body.String())
	}
	assertGraphWellFormed(t, resp.Graph, 5, 10)
}

// TestRecallGraph_NonASCIIMatching is finding 3 at the HTTP layer: stored
// upper-case Turkish/German text must match a lower-case query and an exact
// case-insensitive relation filter.
func TestRecallGraph_NonASCIIMatching(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
	injectGraph(t, s, "proj", []gleann.MemoryGraphNode{
		{ID: "Alpha", Type: "entity", Content: "alpha service"},
		{ID: "Beta", Type: "entity", Content: "beta service"},
		{ID: "ÇAĞRI", Type: "entity", Content: "çağrı merkezi"},
		{ID: "ÄRGER", Type: "entity", Content: "ÜBER ALLES"},
	}, []gleann.MemoryGraphEdge{
		{From: "Alpha", To: "Beta", RelationType: "ÇAĞRI", Weight: 1},
		{From: "ÇAĞRI", To: "Beta", RelationType: "LINKS", Weight: 1},
	})

	cases := []struct {
		name  string
		body  string
		check func(*testing.T, *RecallGraph)
	}{
		{
			name: "ASCII query still sees non-ASCII relation",
			body: `{"query":"alpha","index":"proj","layers":["graph"]}`,
			check: func(t *testing.T, g *RecallGraph) {
				if graphFindEdge(g, "Alpha", "Beta", "ÇAĞRI") == nil {
					t.Errorf("ÇAĞRI edge missing: %+v", g.Edges)
				}
			},
		},
		{
			name: "exact stored relation filter",
			body: `{"query":"alpha","index":"proj","layers":["graph"],"relations":["ÇAĞRI"]}`,
			check: func(t *testing.T, g *RecallGraph) {
				if graphFindEdge(g, "Alpha", "Beta", "ÇAĞRI") == nil {
					t.Errorf("filter by stored relation type dropped the edge: %+v", g.Edges)
				}
			},
		},
		{
			name: "lower-case relation filter",
			body: `{"query":"alpha","index":"proj","layers":["graph"],"relations":["çağrı"]}`,
			check: func(t *testing.T, g *RecallGraph) {
				if graphFindEdge(g, "Alpha", "Beta", "ÇAĞRI") == nil {
					t.Errorf("case-insensitive filter dropped the edge: %+v", g.Edges)
				}
			},
		},
		{
			name: "lower-case query matches stored upper-case ID",
			body: `{"query":"çağrı","index":"proj","layers":["graph"]}`,
			check: func(t *testing.T, g *RecallGraph) {
				if !graphHasNode(g, "ÇAĞRI") {
					t.Errorf("node ÇAĞRI missing: %+v", g.Nodes)
				}
			},
		},
		{
			name: "exact stored ID matches itself",
			body: `{"query":"ÇAĞRI","index":"proj","layers":["graph"]}`,
			check: func(t *testing.T, g *RecallGraph) {
				if !graphHasNode(g, "ÇAĞRI") {
					t.Errorf("node ÇAĞRI missing: %+v", g.Nodes)
				}
			},
		},
		{
			name: "lower-case query matches German umlaut",
			body: `{"query":"ärger","index":"proj","layers":["graph"]}`,
			check: func(t *testing.T, g *RecallGraph) {
				if !graphHasNode(g, "ÄRGER") {
					t.Errorf("node ÄRGER missing: %+v", g.Nodes)
				}
			},
		},
		{
			name: "upper-case query matches German umlaut content",
			body: `{"query":"ÜBER","index":"proj","layers":["graph"]}`,
			check: func(t *testing.T, g *RecallGraph) {
				if !graphHasNode(g, "ÄRGER") {
					t.Errorf("node ÄRGER missing: %+v", g.Nodes)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall", tc.body, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			resp := decodeRecall(t, w)
			if resp.Graph == nil {
				t.Fatalf("graph layer is nil: %s", w.Body.String())
			}
			tc.check(t, resp.Graph)
		})
	}
}

// TestRecallGraph_ContextNoEmptyRelationships is finding 4: node-only graph
// results must not emit an empty <relationships> block; they render entities.
func TestRecallGraph_ContextNoEmptyRelationships(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
	injectGraph(t, s, "proj", []gleann.MemoryGraphNode{
		{ID: "Lonely", Type: "entity", Content: "alpha loner"},
	}, nil)

	w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
		`{"query":"alpha","index":"proj","layers":["graph"],"format":"context"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	resp := decodeRecall(t, w)
	if strings.Contains(resp.Context, "<relationships>\n</relationships>") {
		t.Errorf("empty <relationships> block emitted: %q", resp.Context)
	}
	if !strings.Contains(resp.Context, "<entities>") || !strings.Contains(resp.Context, "Lonely") {
		t.Errorf("matched node not rendered: %q", resp.Context)
	}

	// With edges the relationship block must still be rendered.
	injectGraph(t, s, "proj", []gleann.MemoryGraphNode{
		{ID: "Neighbour", Type: "entity", Content: "alpha neighbour"},
	}, []gleann.MemoryGraphEdge{
		{From: "Lonely", To: "Neighbour", RelationType: "CALLS", Weight: 1},
	})
	w = unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
		`{"query":"alpha","index":"proj","layers":["graph"],"format":"context"}`, nil)
	resp = decodeRecall(t, w)
	if !strings.Contains(resp.Context, "Lonely -[CALLS]-> Neighbour") {
		t.Errorf("relationship line missing: %q", resp.Context)
	}
}

// TestRecallGraph_BoundsClientInput is finding 5: huge token lists and huge
// relation filters must not fail silently behind a 200 with no graph.
func TestRecallGraph_BoundsClientInput(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
	injectGraph(t, s, "proj", []gleann.MemoryGraphNode{
		{ID: "Alpha", Type: "entity", Content: "alpha service"},
	}, nil)

	t.Run("many query tokens", func(t *testing.T) {
		query := strings.TrimSpace(strings.Repeat("alpha ", 1500))
		body, _ := json.Marshal(map[string]any{
			"query": query, "index": "proj", "layers": []string{"graph"},
		})
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall", string(body), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		resp := decodeRecall(t, w)
		if resp.Graph == nil || !graphHasNode(resp.Graph, "Alpha") {
			t.Fatalf("graph missing after token bounding: %s", w.Body.String())
		}
	})

	t.Run("many relation filter entries", func(t *testing.T) {
		relations := make([]string, 40000)
		for i := range relations {
			relations[i] = "rel" + strconv.Itoa(i)
		}
		body, _ := json.Marshal(map[string]any{
			"query": "alpha", "index": "proj", "layers": []string{"graph"},
			"relations": relations,
		})
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall", string(body), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		resp := decodeRecall(t, w)
		if resp.Graph == nil || !graphHasNode(resp.Graph, "Alpha") {
			t.Fatalf("graph missing after relation-filter bounding: %s", w.Body.String())
		}
	})
}

// TestRecallGraph_EmptyRelationsFilterIgnored is finding 7: empty or
// whitespace-only filter entries must behave like an omitted filter instead of
// silently disabling every relation.
func TestRecallGraph_EmptyRelationsFilterIgnored(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)
	injectGraph(t, s, "proj", []gleann.MemoryGraphNode{
		{ID: "Alpha", Type: "entity", Content: "alpha service"},
		{ID: "Beta", Type: "entity", Content: "beta service"},
	}, []gleann.MemoryGraphEdge{
		{From: "Alpha", To: "Beta", RelationType: "CALLS", Weight: 1},
	})

	for _, body := range []string{
		`{"query":"alpha","index":"proj","layers":["graph"],"relations":[""]}`,
		`{"query":"alpha","index":"proj","layers":["graph"],"relations":["   "]}`,
		`{"query":"alpha","index":"proj","layers":["graph"],"relations":["","  "]}`,
		`{"query":"alpha","index":"proj","layers":["graph"],"relations":["","calls"]}`,
	} {
		w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall", body, nil)
		resp := decodeRecall(t, w)
		if w.Code != http.StatusOK || resp.Graph == nil ||
			graphFindEdge(resp.Graph, "Alpha", "Beta", "CALLS") == nil {
			t.Errorf("%s: edge lost: %d %s", body, w.Code, w.Body.String())
		}
	}
}

// TestRecallGraph_BackfillSeedsExpandRelations is v3 finding 2: seeds pushed
// past the reserved quota ("backfill") are admitted to the node set but their
// incident relations were never fetched, so an edge between two backfilled
// seeds (S03 -[LINKS]-> S04) was silently dropped while edge budget remained.
func TestRecallGraph_BackfillSeedsExpandRelations(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)

	var nodes []gleann.MemoryGraphNode
	for i := 0; i < 5; i++ {
		nodes = append(nodes, gleann.MemoryGraphNode{
			ID: "S" + twoDigits(i), Type: "entity", Content: "match seed",
		})
	}
	// S00..S02 are isolated; S03 and S04 are connected by a relation type that
	// does NOT match the query, so only seed expansion can discover the edge.
	injectGraph(t, s, "proj", nodes, []gleann.MemoryGraphEdge{
		{From: "S03", To: "S04", RelationType: "LINKS", Weight: 1},
	})

	w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
		`{"query":"match","index":"proj","layers":["graph"],"top_k":5,"depth":2}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	resp := decodeRecall(t, w)
	if resp.Graph == nil {
		t.Fatalf("graph layer is nil: %s", w.Body.String())
	}
	if len(resp.Graph.Nodes) != 5 {
		t.Errorf("nodes = %d, want 5: %+v", len(resp.Graph.Nodes), resp.Graph.Nodes)
	}
	if graphFindEdge(resp.Graph, "S03", "S04", "LINKS") == nil {
		t.Fatalf("backfilled seed edge S03 -[LINKS]-> S04 missing: %+v", resp.Graph.Edges)
	}
	assertGraphWellFormed(t, resp.Graph, 5, 10)
}

// TestRecallGraph_BackfillSeedExpandsToNewNeighbour is v3 finding 2 with spare
// node budget: a single backfilled seed must expand to a neighbour that is not
// itself a query seed, within the node cap.
func TestRecallGraph_BackfillSeedExpandsToNewNeighbour(t *testing.T) {
	s := unifiedServer(t)
	createMockIndexMeta(t, s.config.IndexDir, "proj", true, nil)

	nodes := []gleann.MemoryGraphNode{
		{ID: "S00", Type: "entity", Content: "match seed"},
		{ID: "S01", Type: "entity", Content: "match seed"},
		{ID: "S02", Type: "entity", Content: "match seed"},
		{ID: "S03", Type: "entity", Content: "match seed"},
		{ID: "ExtraNode", Type: "entity", Content: "neighbour"},
	}
	injectGraph(t, s, "proj", nodes, []gleann.MemoryGraphEdge{
		{From: "S03", To: "ExtraNode", RelationType: "LINKS", Weight: 1},
	})

	w := unifiedPost(s, s.handleUnifiedRecall, "/api/memory/recall",
		`{"query":"match","index":"proj","layers":["graph"],"top_k":5,"depth":2}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	resp := decodeRecall(t, w)
	if resp.Graph == nil {
		t.Fatalf("graph layer is nil: %s", w.Body.String())
	}
	if !graphHasNode(resp.Graph, "ExtraNode") {
		t.Errorf("backfilled seed S03 did not expand to ExtraNode: %+v", resp.Graph.Nodes)
	}
	if len(resp.Graph.Nodes) != 5 {
		t.Errorf("nodes = %d, want 5: %+v", len(resp.Graph.Nodes), resp.Graph.Nodes)
	}
	if graphFindEdge(resp.Graph, "S03", "ExtraNode", "LINKS") == nil {
		t.Fatalf("expanded edge S03 -[LINKS]-> ExtraNode missing: %+v", resp.Graph.Edges)
	}
	assertGraphWellFormed(t, resp.Graph, 5, 10)
}
