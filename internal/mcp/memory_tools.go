//go:build treesitter

// Package mcp — Memory Engine MCP tool definitions.
//
// These three tools allow autonomous AI agents that speak the Model Context
// Protocol (MCP) to manipulate gleann's generic Knowledge Graph directly,
// bypassing the RAG pipeline entirely.  Useful for:
//
//   - Injecting structured knowledge captured during a chat session.
//   - Linking requirements, code symbols, and concepts in a graph.
//   - Exploring multi-hop relationships (e.g. "which code is affected by req-42?").
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── Memory service pool ───────────────────────────────────────────────────────

// mcpMemoryPool caches MemoryService instances per index name within an MCP
// server session.  Each database is stored at <indexDir>/<name>_memory.
type mcpMemoryPool struct {
	mu       sync.RWMutex
	services map[string]*kgraph.MemoryService
	dbs      map[string]*kgraph.DB
	dir      string
}

func newMCPMemoryPool(indexDir string) *mcpMemoryPool {
	return &mcpMemoryPool{
		services: make(map[string]*kgraph.MemoryService),
		dbs:      make(map[string]*kgraph.DB),
		dir:      indexDir,
	}
}

// get returns a cached MemoryService for name, opening it lazily on first access.
func (p *mcpMemoryPool) get(name string) (*kgraph.MemoryService, error) {
	p.mu.RLock()
	svc, ok := p.services[name]
	p.mu.RUnlock()
	if ok {
		return svc, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if svc, ok := p.services[name]; ok {
		return svc, nil // won the race
	}

	dbPath := filepath.Join(p.dir, name+"_memory")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open memory db %q: %w", dbPath, err)
	}

	svc = kgraph.NewMemoryService(db, nil /* no vector syncer */)
	p.dbs[name] = db
	p.services[name] = svc
	return svc, nil
}

// closeAll releases all open database handles.
func (p *mcpMemoryPool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, db := range p.dbs {
		db.Close()
	}
	p.dbs = make(map[string]*kgraph.DB)
	p.services = make(map[string]*kgraph.MemoryService)
}

// ── Tool: inject_knowledge_graph ─────────────────────────────────────────────

func (s *Server) buildInjectKGTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name: "inject_knowledge_graph",
		Description: "Inject Entity nodes and RELATES_TO edges into gleann's generic " +
			"Knowledge Graph Memory Engine. The operation is idempotent (MERGE semantics). " +
			"Nodes with non-empty 'content' are also indexed in the HNSW vector store.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the gleann index / memory store to write into",
				},
				"nodes": map[string]interface{}{
					"type":        "array",
					"description": "List of entity nodes to upsert",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"id":         map[string]interface{}{"type": "string"},
							"type":       map[string]interface{}{"type": "string"},
							"content":    map[string]interface{}{"type": "string"},
							"attributes": map[string]interface{}{"type": "object"},
						},
						"required": []string{"id", "type"},
					},
				},
				"edges": map[string]interface{}{
					"type":        "array",
					"description": "List of directed relationships to upsert",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"from":          map[string]interface{}{"type": "string"},
							"to":            map[string]interface{}{"type": "string"},
							"relation_type": map[string]interface{}{"type": "string"},
							"weight":        map[string]interface{}{"type": "number"},
							"attributes":    map[string]interface{}{"type": "object"},
						},
						"required": []string{"from", "to", "relation_type"},
					},
				},
			},
			Required: []string{"index"},
		},
	}
}

func (s *Server) handleInjectKG(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcpsdk.NewToolResultError("invalid arguments"), nil
	}

	indexName, _ := args["index"].(string)
	if indexName == "" {
		return mcpsdk.NewToolResultError("index is required"), nil
	}

	// Decode nodes.
	var nodes []gleann.MemoryGraphNode
	if raw, ok := args["nodes"]; ok && raw != nil {
		b, err := json.Marshal(raw)
		if err != nil {
			return mcpsdk.NewToolResultError("invalid nodes: " + err.Error()), nil
		}
		if err := json.Unmarshal(b, &nodes); err != nil {
			return mcpsdk.NewToolResultError("invalid nodes: " + err.Error()), nil
		}
	}

	// Decode edges.
	var edges []gleann.MemoryGraphEdge
	if raw, ok := args["edges"]; ok && raw != nil {
		b, err := json.Marshal(raw)
		if err != nil {
			return mcpsdk.NewToolResultError("invalid edges: " + err.Error()), nil
		}
		if err := json.Unmarshal(b, &edges); err != nil {
			return mcpsdk.NewToolResultError("invalid edges: " + err.Error()), nil
		}
	}

	svc, err := s.memPool.get(indexName)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("open memory store %q: %v", indexName, err)), nil
	}

	payload := gleann.GraphInjectionPayload{Nodes: nodes, Edges: edges}
	if err := svc.InjectEntities(ctx, payload); err != nil {
		return mcpsdk.NewToolResultError("inject failed: " + err.Error()), nil
	}

	return mcpsdk.NewToolResultText(fmt.Sprintf(
		"OK — injected %d nodes and %d edges into memory store %q.",
		len(nodes), len(edges), indexName,
	)), nil
}

// ── Tool: delete_graph_entity ─────────────────────────────────────────────────

func (s *Server) buildDeleteEntityTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name:        "delete_graph_entity",
		Description: "Remove an Entity node and all of its incident RELATES_TO edges from the Knowledge Graph Memory Engine.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the gleann index / memory store",
				},
				"id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the entity node to delete",
				},
			},
			Required: []string{"index", "id"},
		},
	}
}

func (s *Server) handleDeleteEntity(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcpsdk.NewToolResultError("invalid arguments"), nil
	}

	indexName, _ := args["index"].(string)
	entityID, _ := args["id"].(string)
	if indexName == "" || entityID == "" {
		return mcpsdk.NewToolResultError("index and id are required"), nil
	}

	svc, err := s.memPool.get(indexName)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("open memory store %q: %v", indexName, err)), nil
	}

	if err := svc.DeleteEntity(ctx, entityID); err != nil {
		return mcpsdk.NewToolResultError("delete entity: " + err.Error()), nil
	}

	return mcpsdk.NewToolResultText(fmt.Sprintf("OK — entity %q deleted from memory store %q.", entityID, indexName)), nil
}

// ── Tool: traverse_knowledge_graph ────────────────────────────────────────────

func (s *Server) buildTraverseKGTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name: "traverse_knowledge_graph",
		Description: "Walk the Knowledge Graph Memory Engine starting from a given entity and " +
			"return all reachable nodes and the edges connecting them up to the requested depth. " +
			"Use this to explore requirement chains, dependency graphs, or semantic concept clusters. " +
			"Example: 'Which code symbols are linked to requirement req-42?' → start_id=req-42, depth=3.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"index": map[string]interface{}{
					"type":        "string",
					"description": "Name of the gleann index / memory store",
				},
				"start_id": map[string]interface{}{
					"type":        "string",
					"description": "ID of the starting Entity node",
				},
				"depth": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum traversal depth (hops, 1–10, default 2)",
					"default":     2,
					"minimum":     1,
					"maximum":     10,
				},
			},
			Required: []string{"index", "start_id"},
		},
	}
}

func (s *Server) handleTraverseKG(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcpsdk.NewToolResultError("invalid arguments"), nil
	}

	indexName, _ := args["index"].(string)
	startID, _ := args["start_id"].(string)
	if indexName == "" || startID == "" {
		return mcpsdk.NewToolResultError("index and start_id are required"), nil
	}

	depth := 2
	if d, ok := args["depth"].(float64); ok && d >= 1 {
		depth = int(d)
	}

	svc, err := s.memPool.get(indexName)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("open memory store %q: %v", indexName, err)), nil
	}

	nodes, edges, err := svc.Traverse(ctx, startID, depth)
	if err != nil {
		return mcpsdk.NewToolResultError("traverse: " + err.Error()), nil
	}

	// Format output as readable text for agent consumption.
	var sb fmt.Stringer
	_ = sb // silence linter; we use fmt.Fprintf below
	out := &stringWriter{}

	fmt.Fprintf(out, "Traversal from %q (depth %d) — %d nodes, %d edges\n\n", startID, depth, len(nodes), len(edges))

	fmt.Fprintln(out, "=== Nodes ===")
	for _, n := range nodes {
		fmt.Fprintf(out, "  [%s] id=%q type=%q", markStart(n.ID, startID), n.ID, n.Type)
		if n.Content != "" {
			preview := n.Content
			if len(preview) > 120 {
				preview = preview[:120] + "…"
			}
			fmt.Fprintf(out, " content=%q", preview)
		}
		if len(n.Attributes) > 0 {
			b, _ := json.Marshal(n.Attributes)
			fmt.Fprintf(out, " attrs=%s", b)
		}
		fmt.Fprintln(out)
	}

	if len(edges) > 0 {
		fmt.Fprintln(out, "\n=== Edges ===")
		for _, e := range edges {
			fmt.Fprintf(out, "  %q -[%s]-> %q (weight=%.2f)\n", e.From, e.RelationType, e.To, e.Weight)
		}
	}

	return mcpsdk.NewToolResultText(out.String()), nil
}

// markStart returns "*" for the start node and " " for others, to visually
// highlight the traversal origin in the text output.
func markStart(id, startID string) string {
	if id == startID {
		return "*"
	}
	return " "
}

// stringWriter is a simple fmt.Fprintf target that accumulates a string.
type stringWriter struct {
	buf []byte
}

func (w *stringWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *stringWriter) String() string { return string(w.buf) }
