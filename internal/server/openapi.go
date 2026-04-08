// Package server — OpenAPI 3.0 specification for the gleann REST API.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// openAPISpec returns the OpenAPI 3.0 specification as a Go map.
// Keeping it as a map rather than a raw string allows programmatic
// manipulation (e.g. injecting the actual server version at runtime).
func (s *Server) openAPISpec() map[string]any {
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Gleann API",
			"description": "Unified vector + graph search engine. Index, search, and query code and documents with HNSW, BM25, and KuzuDB graph.",
			"version":     s.version,
			"license": map[string]any{
				"name": "MIT",
				"url":  "https://github.com/tevfik/gleann/blob/main/LICENSE",
			},
		},
		"servers": []map[string]any{
			{"url": fmt.Sprintf("http://localhost%s", s.addr), "description": "Local development server"},
		},
		"tags": []map[string]any{
			{"name": "health", "description": "Health check"},
			{"name": "indexes", "description": "Index management"},
			{"name": "search", "description": "Semantic and hybrid search"},
			{"name": "graph", "description": "KuzuDB code graph queries"},
			{"name": "memory", "description": "Memory Engine — generic Entity/RELATES_TO knowledge graph for external AI agents"},
			{"name": "blocks", "description": "Memory Blocks — hierarchical BBolt storage (short/medium/long tiers) providing infinite persistent memory for LLMs"},
			{"name": "webhooks", "description": "Webhook notification management"},
			{"name": "metrics", "description": "Prometheus-compatible metrics"},
			{"name": "proxy", "description": "OpenAI-compatible RAG proxy (model: \"gleann/<index>\")"},
		},
		"paths": map[string]any{
			"/health": map[string]any{
				"get": map[string]any{
					"tags":        []string{"health"},
					"summary":     "Health check",
					"operationId": "healthCheck",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Server is healthy",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"status":  map[string]any{"type": "string", "example": "ok"},
											"version": map[string]any{"type": "string", "example": "1.0.0"},
											"engine":  map[string]any{"type": "string", "example": "gleann-go"},
										},
									},
								},
							},
						},
					},
				},
			},
			"/api/indexes": map[string]any{
				"get": map[string]any{
					"tags":        []string{"indexes"},
					"summary":     "List all indexes",
					"operationId": "listIndexes",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "List of available indexes",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"indexes": map[string]any{
												"type":  "array",
												"items": map[string]any{"type": "string"},
											},
											"count": map[string]any{"type": "integer"},
										},
									},
								},
							},
						},
					},
				},
			},
			"/api/indexes/{name}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"indexes"},
					"summary":     "Get index metadata",
					"operationId": "getIndex",
					"parameters":  []map[string]any{paramName()},
					"responses": map[string]any{
						"200": map[string]any{"description": "Index metadata"},
						"404": map[string]any{"description": "Index not found"},
					},
				},
				"delete": map[string]any{
					"tags":        []string{"indexes"},
					"summary":     "Delete an index",
					"operationId": "deleteIndex",
					"parameters":  []map[string]any{paramName()},
					"responses": map[string]any{
						"200": map[string]any{"description": "Index deleted"},
						"404": map[string]any{"description": "Index not found"},
					},
				},
			},
			"/api/indexes/{name}/search": map[string]any{
				"post": map[string]any{
					"tags":        []string{"search"},
					"summary":     "Search an index",
					"operationId": "searchIndex",
					"parameters":  []map[string]any{paramName()},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("SearchRequest"),
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Search results",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": refSchema("SearchResponse"),
								},
							},
						},
						"400": map[string]any{"description": "Invalid request"},
						"404": map[string]any{"description": "Index not found"},
					},
				},
			},
			"/api/indexes/{name}/ask": map[string]any{
				"post": map[string]any{
					"tags":        []string{"search"},
					"summary":     "Ask a question (RAG)",
					"description": "Searches the index and uses an LLM to answer the question based on retrieved context. Set `stream: true` (or query param `?stream=true`) to receive tokens via Server-Sent Events (SSE).",
					"operationId": "askQuestion",
					"parameters": []map[string]any{
						paramName(),
						{
							"name":        "stream",
							"in":          "query",
							"required":    false,
							"description": "Enable SSE streaming (alternative to setting stream in body)",
							"schema":      map[string]any{"type": "boolean", "default": false},
						},
					},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("AskRequest"),
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Answer with sources",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": refSchema("AskResponse"),
								},
							},
						},
						"400": map[string]any{"description": "Invalid request"},
						"404": map[string]any{"description": "Index not found"},
					},
				},
			},
			"/api/indexes/{name}/build": map[string]any{
				"post": map[string]any{
					"tags":        []string{"indexes"},
					"summary":     "Build an index from texts or items",
					"operationId": "buildIndex",
					"parameters":  []map[string]any{paramName()},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("BuildRequest"),
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Build result",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"status":  map[string]any{"type": "string", "example": "ok"},
											"name":    map[string]any{"type": "string"},
											"count":   map[string]any{"type": "integer"},
											"buildMs": map[string]any{"type": "integer", "format": "int64"},
										},
									},
								},
							},
						},
						"400": map[string]any{"description": "Invalid request"},
					},
				},
			},
			"/api/graph/{name}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"graph"},
					"summary":     "Get graph statistics",
					"operationId": "graphStats",
					"parameters":  []map[string]any{paramName()},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Graph statistics",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": refSchema("GraphStatsResponse"),
								},
							},
						},
					},
				},
			},
			"/api/graph/{name}/query": map[string]any{
				"post": map[string]any{
					"tags":        []string{"graph"},
					"summary":     "Query the code graph",
					"description": "Run predefined queries (callees, callers, symbols_in_file, impact) or raw Cypher against the KuzuDB code graph.",
					"operationId": "graphQuery",
					"parameters":  []map[string]any{paramName()},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("GraphQueryRequest"),
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Query results",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": refSchema("GraphQueryResponse"),
								},
							},
						},
						"400": map[string]any{"description": "Invalid request"},
						"404": map[string]any{"description": "Graph index not found"},
					},
				},
			},
			"/api/graph/{name}/index": map[string]any{
				"post": map[string]any{
					"tags":        []string{"graph"},
					"summary":     "Trigger AST graph indexing",
					"description": "Indexes a source directory into the KuzuDB code graph using tree-sitter AST parsing.",
					"operationId": "graphIndex",
					"parameters":  []map[string]any{paramName()},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("GraphIndexRequest"),
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Indexing result",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"status":   map[string]any{"type": "string", "example": "ok"},
											"name":     map[string]any{"type": "string"},
											"docs_dir": map[string]any{"type": "string"},
											"buildMs":  map[string]any{"type": "integer", "format": "int64"},
										},
									},
								},
							},
						},
						"400": map[string]any{"description": "Invalid request"},
						"503": map[string]any{"description": "Graph database not available (requires treesitter build tag)"},
					},
				},
			},

			// ── Memory Engine endpoints ──────────────────────────────────────────
			"/api/memory/{name}/inject": map[string]any{
				"post": map[string]any{
					"tags":        []string{"memory"},
					"summary":     "Inject nodes and edges (bulk upsert)",
					"description": "Atomically upserts a batch of Entity nodes and RELATES_TO edges into the knowledge graph. The operation is idempotent — re-submitting the same payload is safe. Nodes with non-empty content are also indexed in the HNSW vector store.",
					"operationId": "memoryInject",
					"parameters":  []map[string]any{paramName()},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("GraphInjectionPayload"),
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Injection successful",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"ok":         map[string]any{"type": "boolean"},
											"nodes_sent": map[string]any{"type": "integer"},
											"edges_sent": map[string]any{"type": "integer"},
										},
									},
								},
							},
						},
						"400": map[string]any{"description": "Invalid request body"},
						"500": map[string]any{"description": "Internal server error"},
					},
				},
			},
			"/api/memory/{name}/nodes/{id}": map[string]any{
				"delete": map[string]any{
					"tags":        []string{"memory"},
					"summary":     "Delete an entity node",
					"description": "Removes the Entity identified by id from the knowledge graph together with all of its incident RELATES_TO edges. If a vector syncer is configured, the corresponding embedding is also deleted.",
					"operationId": "memoryDeleteNode",
					"parameters": []map[string]any{
						paramName(),
						{
							"name":        "id",
							"in":          "path",
							"required":    true,
							"description": "Entity node ID",
							"schema":      map[string]any{"type": "string"},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Entity deleted",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"ok":         map[string]any{"type": "boolean"},
											"deleted_id": map[string]any{"type": "string"},
										},
									},
								},
							},
						},
						"400": map[string]any{"description": "Missing id"},
						"500": map[string]any{"description": "Internal server error"},
					},
				},
			},
			"/api/memory/{name}/edges": map[string]any{
				"delete": map[string]any{
					"tags":        []string{"memory"},
					"summary":     "Delete a specific edge",
					"description": "Removes the single RELATES_TO relationship identified by (from, to, relation_type). Other edges between the same pair with different relation types are not affected.",
					"operationId": "memoryDeleteEdge",
					"parameters":  []map[string]any{paramName()},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("DeleteEdgeRequest"),
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Edge deleted",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type":       "object",
										"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
									},
								},
							},
						},
						"400": map[string]any{"description": "Missing required fields"},
						"500": map[string]any{"description": "Internal server error"},
					},
				},
			},
			"/api/memory/{name}/traverse": map[string]any{
				"post": map[string]any{
					"tags":        []string{"memory"},
					"summary":     "Traverse the knowledge graph",
					"description": "Walks RELATES_TO edges starting from start_id up to depth hops. Returns all reachable Entity nodes and the edges that connect them within the sub-graph. Useful for agents exploring requirement chains, dependency graphs, or semantic concept clusters.",
					"operationId": "memoryTraverse",
					"parameters":  []map[string]any{paramName()},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("TraverseRequest"),
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Sub-graph result",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": refSchema("TraverseResponse"),
								},
							},
						},
						"400": map[string]any{"description": "Invalid request"},
						"500": map[string]any{"description": "Internal server error"},
					},
				},
			},

			// ── Memory Block endpoints (BBolt hierarchical memory) ───────────────────────
			"/api/blocks": map[string]any{
				"get": map[string]any{
					"tags":        []string{"blocks"},
					"summary":     "List memory blocks",
					"description": "Returns all persisted memory blocks. Use `tier` to filter by storage tier.",
					"operationId": "listBlocks",
					"parameters": []map[string]any{
						{
							"name":        "tier",
							"in":          "query",
							"required":    false,
							"description": "Filter by memory tier",
							"schema":      map[string]any{"type": "string", "enum": []string{"short", "medium", "long"}},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "List of memory blocks",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"blocks": map[string]any{"type": "array", "items": refSchema("MemoryBlock")},
											"count":  map[string]any{"type": "integer"},
										},
									},
								},
							},
						},
					},
				},
				"post": map[string]any{
					"tags":        []string{"blocks"},
					"summary":     "Add a memory block",
					"description": "Stores a new memory block in the specified tier. Short-term blocks are in-memory (session-scoped), medium and long-term are persisted to BBolt.",
					"operationId": "addBlock",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("BlockAddRequest"),
							},
						},
					},
					"responses": map[string]any{
						"201": map[string]any{
							"description": "Block created",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": refSchema("MemoryBlock"),
								},
							},
						},
						"400": map[string]any{"description": "Invalid request"},
						"500": map[string]any{"description": "Internal server error"},
					},
				},
				"delete": map[string]any{
					"tags":        []string{"blocks"},
					"summary":     "Clear memory blocks",
					"description": "Removes all blocks from a specific tier, or all tiers if `tier` is omitted.",
					"operationId": "clearBlocks",
					"parameters": []map[string]any{
						{
							"name":        "tier",
							"in":          "query",
							"required":    false,
							"description": "Tier to clear (omit to clear all)",
							"schema":      map[string]any{"type": "string", "enum": []string{"short", "medium", "long"}},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Blocks cleared",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type":       "object",
										"properties": map[string]any{"ok": map[string]any{"type": "boolean"}, "cleared": map[string]any{"type": "integer"}},
									},
								},
							},
						},
						"400": map[string]any{"description": "Invalid tier value"},
						"500": map[string]any{"description": "Internal server error"},
					},
				},
			},
			"/api/blocks/{id}": map[string]any{
				"delete": map[string]any{
					"tags":        []string{"blocks"},
					"summary":     "Forget a memory block",
					"description": "Removes a specific memory block by ID. Also accepts a content query — all blocks matching the query will be deleted.",
					"operationId": "deleteBlock",
					"parameters": []map[string]any{
						{
							"name":        "id",
							"in":          "path",
							"required":    true,
							"description": "Block ID",
							"schema":      map[string]any{"type": "string"},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Block deleted",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type":       "object",
										"properties": map[string]any{"ok": map[string]any{"type": "boolean"}, "deleted": map[string]any{"type": "integer"}},
									},
								},
							},
						},
						"404": map[string]any{"description": "Block not found"},
						"500": map[string]any{"description": "Internal server error"},
					},
				},
			},
			"/api/blocks/search": map[string]any{
				"get": map[string]any{
					"tags":        []string{"blocks"},
					"summary":     "Search memory blocks",
					"description": "Full-text search across all memory tiers. Matches content, label, and tags.",
					"operationId": "searchBlocks",
					"parameters": []map[string]any{
						{
							"name":        "q",
							"in":          "query",
							"required":    true,
							"description": "Search query",
							"schema":      map[string]any{"type": "string"},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Matching memory blocks",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"blocks": map[string]any{"type": "array", "items": refSchema("MemoryBlock")},
											"count":  map[string]any{"type": "integer"},
											"query":  map[string]any{"type": "string"},
										},
									},
								},
							},
						},
						"400": map[string]any{"description": "Missing q parameter"},
					},
				},
			},
			"/api/blocks/context": map[string]any{
				"get": map[string]any{
					"tags":        []string{"blocks"},
					"summary":     "Get compiled memory context",
					"description": "Returns the full compiled memory context window — the exact text that gleann injects into LLM system prompts. Use `?format=xml` to get raw XML instead of JSON.",
					"operationId": "blockContext",
					"parameters": []map[string]any{
						{
							"name":        "format",
							"in":          "query",
							"required":    false,
							"description": "Response format: json (default) or xml",
							"schema":      map[string]any{"type": "string", "enum": []string{"json", "xml"}},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Memory context window",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"context":  refSchema("ContextWindow"),
											"rendered": map[string]any{"type": "string", "description": "LLM-injectable <memory_context> XML string"},
										},
									},
								},
								"text/xml": map[string]any{"schema": map[string]any{"type": "string"}},
							},
						},
					},
				},
			},
			"/api/blocks/stats": map[string]any{
				"get": map[string]any{
					"tags":        []string{"blocks"},
					"summary":     "Memory storage statistics",
					"description": "Returns block counts per tier and total disk usage of the BBolt memory store.",
					"operationId": "blockStats",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Memory statistics",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": refSchema("MemoryStats"),
								},
							},
						},
					},
				},
			},
			"/api/search": map[string]any{
				"post": map[string]any{
					"description": "Search across multiple indexes simultaneously. Results are merged by score, each tagged with the source index. Omit 'indexes' to search all available indexes.",
					"operationId": "multiIndexSearch",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("MultiSearchRequest"),
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Merged search results",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": refSchema("MultiSearchResponse"),
								},
							},
						},
						"400": map[string]any{"description": "Invalid request"},
					},
				},
			},
			"/api/webhooks": map[string]any{
				"get": map[string]any{
					"tags":        []string{"webhooks"},
					"summary":     "List registered webhooks",
					"operationId": "listWebhooks",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "List of webhooks",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"webhooks": map[string]any{
												"type":  "array",
												"items": refSchema("Webhook"),
											},
											"count": map[string]any{"type": "integer"},
										},
									},
								},
							},
						},
					},
				},
				"post": map[string]any{
					"tags":        []string{"webhooks"},
					"summary":     "Register a webhook",
					"description": "Register a URL to receive POST notifications for specified events (build_complete, index_deleted). If a secret is provided, payloads include an X-Gleann-Signature HMAC-SHA256 header.",
					"operationId": "registerWebhook",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("WebhookRegisterRequest"),
							},
						},
					},
					"responses": map[string]any{
						"201": map[string]any{"description": "Webhook registered"},
						"400": map[string]any{"description": "Invalid request"},
					},
				},
				"delete": map[string]any{
					"tags":        []string{"webhooks"},
					"summary":     "Delete a webhook by URL",
					"operationId": "deleteWebhook",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type":     "object",
									"required": []string{"url"},
									"properties": map[string]any{
										"url": map[string]any{"type": "string"},
									},
								},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{"description": "Webhook deleted"},
						"404": map[string]any{"description": "Webhook not found"},
					},
				},
			},
			"/metrics": map[string]any{
				"get": map[string]any{
					"tags":        []string{"metrics"},
					"summary":     "Prometheus-compatible metrics",
					"description": "Returns server metrics in Prometheus text exposition format. Compatible with Prometheus, Grafana, and OpenTelemetry collectors.",
					"operationId": "getMetrics",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Metrics in Prometheus text format",
							"content": map[string]any{
								"text/plain": map[string]any{
									"schema": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			},
			"/api/conversations": map[string]any{
				"get": map[string]any{
					"tags":        []string{"conversations"},
					"summary":     "List saved conversations",
					"description": "Returns summaries of all saved conversations, sorted by most recently updated.",
					"operationId": "listConversations",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "List of conversation summaries",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"conversations": map[string]any{
												"type":  "array",
												"items": refSchema("ConversationSummary"),
											},
											"count": map[string]any{"type": "integer"},
										},
									},
								},
							},
						},
					},
				},
			},
			"/api/conversations/{id}": map[string]any{
				"get": map[string]any{
					"tags":        []string{"conversations"},
					"summary":     "Get conversation by ID",
					"description": "Returns the full conversation including all messages.",
					"operationId": "getConversation",
					"parameters": []map[string]any{
						{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Conversation ID or prefix"},
					},
					"responses": map[string]any{
						"200": map[string]any{"description": "Full conversation with messages"},
						"404": map[string]any{"description": "Conversation not found"},
					},
				},
				"delete": map[string]any{
					"tags":        []string{"conversations"},
					"summary":     "Delete a conversation",
					"description": "Permanently deletes a saved conversation.",
					"operationId": "deleteConversation",
					"parameters": []map[string]any{
						{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Conversation ID or prefix"},
					},
					"responses": map[string]any{
						"200": map[string]any{"description": "Conversation deleted"},
						"404": map[string]any{"description": "Conversation not found"},
					},
				},
			},
			"/v1/models": map[string]any{
				"get": map[string]any{
					"tags":        []string{"proxy"},
					"summary":     "List available models (indexes)",
					"description": "Returns gleann indexes as OpenAI-compatible model objects. Use gleann/<index> as the model in chat completions.",
					"operationId": "listModels",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Model list",
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": refSchema("ModelList"),
								},
							},
						},
					},
				},
			},
			"/v1/chat/completions": map[string]any{
				"post": map[string]any{
					"tags":        []string{"proxy"},
					"summary":     "OpenAI-compatible RAG chat completions",
					"description": "Drop-in for OpenAI chat completions. Set model to gleann/<index> to enable RAG. Multi-index: gleann/a,b. Pure LLM: gleann/.\n\nOptional override headers:\n- X-Gleann-Top-K: number of RAG results (default: config top_k)\n- X-Gleann-Min-Score: minimum similarity score",
					"operationId": "chatCompletions",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("ChatCompletionRequest"),
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Chat completion (or SSE stream when stream=true)",
							"content": map[string]any{
								"application/json":  map[string]any{"schema": refSchema("ChatCompletionResponse")},
								"text/event-stream": map[string]any{"schema": map[string]any{"type": "string"}},
							},
						},
						"400": map[string]any{"description": "Invalid request"},
						"500": map[string]any{"description": "LLM or RAG error"},
					},
				},
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"SearchRequest": map[string]any{
					"type":     "object",
					"required": []string{"query"},
					"properties": map[string]any{
						"query":                map[string]any{"type": "string", "description": "Search query text"},
						"top_k":                map[string]any{"type": "integer", "default": 10, "description": "Number of results to return"},
						"hybrid_alpha":         map[string]any{"type": "number", "format": "float", "default": 0.7, "description": "Weight for vector vs BM25 (1.0 = pure vector, 0.0 = pure BM25)"},
						"min_score":            map[string]any{"type": "number", "format": "float", "description": "Minimum score threshold"},
						"ef_search":            map[string]any{"type": "integer", "description": "HNSW ef_search parameter (higher = more accurate, slower)"},
						"recompute_embeddings": map[string]any{"type": "boolean", "default": false},
						"rerank":               map[string]any{"type": "boolean", "default": false, "description": "Enable reranking of results"},
						"rerank_model":         map[string]any{"type": "string", "description": "Reranker model name (default: bge-reranker-v2-m3)"},
						"graph_context":        map[string]any{"type": "boolean", "default": false, "description": "Include code graph context (callers/callees) in results"},
						"metadata_filters": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"field": map[string]any{"type": "string"},
									"op":    map[string]any{"type": "string", "enum": []string{"eq", "ne", "gt", "gte", "lt", "lte", "in", "contains"}},
									"value": map[string]any{},
								},
							},
							"description": "Metadata filters for narrowing results",
						},
						"filter_logic": map[string]any{"type": "string", "enum": []string{"and", "or"}, "default": "and"},
					},
				},
				"SearchResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"results": map[string]any{
							"type":  "array",
							"items": refSchema("SearchResult"),
						},
						"count":    map[string]any{"type": "integer"},
						"query_ms": map[string]any{"type": "integer", "format": "int64"},
					},
				},
				"SearchResult": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text":          map[string]any{"type": "string"},
						"score":         map[string]any{"type": "number", "format": "float"},
						"metadata":      map[string]any{"type": "object", "additionalProperties": true},
						"graph_context": map[string]any{"type": "object", "additionalProperties": true},
					},
				},
				"AskRequest": map[string]any{
					"type":     "object",
					"required": []string{"question"},
					"properties": map[string]any{
						"question":        map[string]any{"type": "string", "description": "Question to answer using RAG"},
						"top_k":           map[string]any{"type": "integer", "default": 10},
						"llm_model":       map[string]any{"type": "string", "description": "LLM model name for answer generation"},
						"llm_provider":    map[string]any{"type": "string", "description": "LLM provider (ollama, openai, anthropic)"},
						"system_prompt":   map[string]any{"type": "string", "description": "Custom system prompt for the LLM (overrides default). Use to set a role or behavior."},
						"role":            map[string]any{"type": "string", "description": "Named role (e.g. 'code', 'shell', 'explain'). Resolves to a system prompt from the role registry."},
						"conversation_id": map[string]any{"type": "string", "description": "Continue an existing conversation by ID. Restores message history."},
						"stream":          map[string]any{"type": "boolean", "default": false, "description": "Enable SSE streaming. When true, response is text/event-stream with `data: {\"token\": \"...\"}` events, ending with `data: [DONE]`"},
					},
				},
				"AskResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"answer": map[string]any{"type": "string"},
						"sources": map[string]any{
							"type":  "array",
							"items": refSchema("SearchResult"),
						},
						"query_ms": map[string]any{"type": "integer", "format": "int64"},
					},
				},
				"BuildRequest": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"texts": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Raw text strings to index",
						},
						"items": map[string]any{
							"type":        "array",
							"items":       refSchema("BuildItem"),
							"description": "Structured items with text and metadata",
						},
						"metadata": map[string]any{
							"type":                 "object",
							"additionalProperties": true,
							"description":          "Default metadata for all items",
						},
					},
				},
				"BuildItem": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text":     map[string]any{"type": "string"},
						"metadata": map[string]any{"type": "object", "additionalProperties": true},
					},
				},
				"GraphQueryRequest": map[string]any{
					"type":     "object",
					"required": []string{"query"},
					"properties": map[string]any{
						"query":     map[string]any{"type": "string", "enum": []string{"callees", "callers", "symbols_in_file", "impact", "cypher"}, "description": "Query type"},
						"symbol":    map[string]any{"type": "string", "description": "Fully qualified symbol name (for callees/callers/impact)"},
						"file":      map[string]any{"type": "string", "description": "File path (for symbols_in_file)"},
						"cypher":    map[string]any{"type": "string", "description": "Raw Cypher query (for cypher type)"},
						"max_depth": map[string]any{"type": "integer", "default": 5, "description": "Max traversal depth for impact analysis"},
					},
				},
				"GraphQueryResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"results": map[string]any{
							"type":  "array",
							"items": refSchema("GraphNode"),
						},
						"count":    map[string]any{"type": "integer"},
						"query_ms": map[string]any{"type": "integer", "format": "int64"},
					},
				},
				"GraphNode": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"fqn":  map[string]any{"type": "string", "description": "Fully qualified name"},
						"name": map[string]any{"type": "string", "description": "Short name"},
						"kind": map[string]any{"type": "string", "description": "Symbol kind (function, method, type, struct, etc.)"},
					},
				},
				"GraphStatsResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":           map[string]any{"type": "string"},
						"db_path":        map[string]any{"type": "string"},
						"available":      map[string]any{"type": "boolean"},
						"file_count":     map[string]any{"type": "integer"},
						"symbol_count":   map[string]any{"type": "integer"},
						"calls_count":    map[string]any{"type": "integer"},
						"declares_count": map[string]any{"type": "integer"},
					},
				},
				"GraphIndexRequest": map[string]any{
					"type":     "object",
					"required": []string{"docs_dir"},
					"properties": map[string]any{
						"docs_dir": map[string]any{"type": "string", "description": "Directory path to index"},
						"module":   map[string]any{"type": "string", "description": "Go module name (auto-detected from go.mod if omitted)"},
					},
				},

				// ── Memory Engine schemas ─────────────────────────────────────────
				"MemoryGraphNode": map[string]any{
					"type":     "object",
					"required": []string{"id", "type"},
					"properties": map[string]any{
						"id":         map[string]any{"type": "string", "description": "Globally unique, stable node identifier (e.g. UUID or slug)"},
						"type":       map[string]any{"type": "string", "description": "Semantic class of the node (e.g. requirement, concept, code_symbol)"},
						"content":    map[string]any{"type": "string", "description": "Natural-language text used to generate the vector embedding. Omit if no vector search is needed."},
						"attributes": map[string]any{"type": "object", "additionalProperties": true, "description": "Arbitrary key-value metadata persisted as JSON"},
					},
				},
				"MemoryGraphEdge": map[string]any{
					"type":     "object",
					"required": []string{"from", "to", "relation_type"},
					"properties": map[string]any{
						"from":          map[string]any{"type": "string", "description": "Source node ID"},
						"to":            map[string]any{"type": "string", "description": "Destination node ID"},
						"relation_type": map[string]any{"type": "string", "description": "Semantic edge label (e.g. DEPENDS_ON, IMPLEMENTS, RELATED_TO)"},
						"weight":        map[string]any{"type": "number", "format": "double", "default": 1.0, "description": "Optional edge strength"},
						"attributes":    map[string]any{"type": "object", "additionalProperties": true, "description": "Arbitrary edge metadata"},
					},
				},
				"GraphInjectionPayload": map[string]any{
					"type":        "object",
					"description": "Bulk upsert payload for the Memory Engine. Nodes and edges are processed inside a single KuzuDB transaction.",
					"properties": map[string]any{
						"nodes": map[string]any{
							"type":  "array",
							"items": refSchema("MemoryGraphNode"),
						},
						"edges": map[string]any{
							"type":  "array",
							"items": refSchema("MemoryGraphEdge"),
						},
					},
				},
				"DeleteEdgeRequest": map[string]any{
					"type":     "object",
					"required": []string{"from", "to", "relation_type"},
					"properties": map[string]any{
						"from":          map[string]any{"type": "string", "description": "Source node ID"},
						"to":            map[string]any{"type": "string", "description": "Destination node ID"},
						"relation_type": map[string]any{"type": "string", "description": "Edge label to remove"},
					},
				},
				"TraverseRequest": map[string]any{
					"type":     "object",
					"required": []string{"start_id"},
					"properties": map[string]any{
						"start_id": map[string]any{"type": "string", "description": "ID of the starting Entity node"},
						"depth":    map[string]any{"type": "integer", "default": 1, "minimum": 0, "maximum": 10, "description": "Maximum traversal depth (hops). Capped at 10."},
					},
				},
				"TraverseResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"nodes": map[string]any{
							"type":  "array",
							"items": refSchema("MemoryGraphNode"),
						},
						"edges": map[string]any{
							"type":  "array",
							"items": refSchema("MemoryGraphEdge"),
						},
						"count": map[string]any{"type": "integer", "description": "Number of nodes returned"},
					},
				},
				"Error": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"error": map[string]any{"type": "string"},
					},
				},

				// ── Memory Block schemas ───────────────────────────────────────────────────────────────────
				"MemoryBlock": map[string]any{
					"type":        "object",
					"description": "A single persisted memory entry in the hierarchical BBolt store.",
					"properties": map[string]any{
						"id":         map[string]any{"type": "string", "description": "Unique block identifier (content-derived hash)"},
						"tier":       map[string]any{"type": "string", "enum": []string{"short", "medium", "long"}, "description": "Storage tier"},
						"label":      map[string]any{"type": "string", "description": "Semantic label (e.g. user_preference, project_fact)"},
						"content":    map[string]any{"type": "string", "description": "The memory content"},
						"source":     map[string]any{"type": "string", "description": "Origin: user, api, mcp_agent, auto_summary, system"},
						"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Searchable tags"},
						"metadata":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Arbitrary key-value metadata"},
						"created_at": map[string]any{"type": "string", "format": "date-time"},
						"updated_at": map[string]any{"type": "string", "format": "date-time"},
						"expires_at": map[string]any{"type": "string", "format": "date-time", "nullable": true, "description": "Expiration time (null = never)"},
					},
				},
				"BlockAddRequest": map[string]any{
					"type":     "object",
					"required": []string{"content"},
					"properties": map[string]any{
						"content":    map[string]any{"type": "string", "description": "The fact or knowledge to store"},
						"tier":       map[string]any{"type": "string", "enum": []string{"short", "medium", "long"}, "default": "long", "description": "Storage tier"},
						"label":      map[string]any{"type": "string", "description": "Semantic label for search/grouping"},
						"source":     map[string]any{"type": "string", "description": "Origin tag (default: api)"},
						"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"metadata":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
						"expires_in": map[string]any{"type": "string", "example": "24h", "description": "Go duration string (e.g. 24h, 7d). Omit for no expiry."},
					},
				},
				"MemoryStats": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"short_term_count":  map[string]any{"type": "integer", "description": "Number of in-memory short-term blocks"},
						"medium_term_count": map[string]any{"type": "integer", "description": "Number of persisted medium-term blocks"},
						"long_term_count":   map[string]any{"type": "integer", "description": "Number of persisted long-term blocks"},
						"total_count":       map[string]any{"type": "integer"},
						"disk_size_bytes":   map[string]any{"type": "integer", "format": "int64", "description": "BBolt file size in bytes"},
					},
				},
				"ContextWindow": map[string]any{
					"type":        "object",
					"description": "Compiled memory context ready for LLM injection.",
					"properties": map[string]any{
						"short_term":  map[string]any{"type": "array", "items": refSchema("MemoryBlock")},
						"medium_term": map[string]any{"type": "array", "items": refSchema("MemoryBlock")},
						"long_term":   map[string]any{"type": "array", "items": refSchema("MemoryBlock")},
						"summaries": map[string]any{
							"type":  "array",
							"items": refSchema("MemorySummary"),
						},
					},
				},
				"MemorySummary": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"conversation_id": map[string]any{"type": "string"},
						"title":           map[string]any{"type": "string"},
						"content":         map[string]any{"type": "string"},
						"message_count":   map[string]any{"type": "integer"},
						"index_names":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"model":           map[string]any{"type": "string"},
						"created_at":      map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"ConversationSummary": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":            map[string]any{"type": "string", "description": "Full conversation ID (SHA-1)"},
						"short_id":      map[string]any{"type": "string", "description": "First 8 chars of the ID"},
						"title":         map[string]any{"type": "string", "description": "Conversation title"},
						"model":         map[string]any{"type": "string", "description": "LLM model used"},
						"indexes":       map[string]any{"type": "string", "description": "Comma-separated index names"},
						"message_count": map[string]any{"type": "integer", "description": "Total number of messages"},
						"created_at":    map[string]any{"type": "string", "format": "date-time"},
						"updated_at":    map[string]any{"type": "string", "format": "date-time"},
					},
				},
				"MultiSearchRequest": map[string]any{
					"type":     "object",
					"required": []string{"query"},
					"properties": map[string]any{
						"query":            map[string]any{"type": "string", "description": "Search query text"},
						"indexes":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Index names to search (omit for all)"},
						"top_k":            map[string]any{"type": "integer", "default": 10},
						"hybrid_alpha":     map[string]any{"type": "number", "format": "float", "default": 0.7},
						"min_score":        map[string]any{"type": "number", "format": "float"},
						"rerank":           map[string]any{"type": "boolean", "default": false},
						"rerank_model":     map[string]any{"type": "string"},
						"metadata_filters": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
						"filter_logic":     map[string]any{"type": "string", "enum": []string{"and", "or"}, "default": "and"},
					},
				},
				"MultiSearchResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"results": map[string]any{
							"type":  "array",
							"items": refSchema("MultiSearchResult"),
						},
						"count":    map[string]any{"type": "integer"},
						"query_ms": map[string]any{"type": "integer", "format": "int64"},
					},
				},
				"MultiSearchResult": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"index":         map[string]any{"type": "string", "description": "Source index name"},
						"text":          map[string]any{"type": "string"},
						"score":         map[string]any{"type": "number", "format": "float"},
						"metadata":      map[string]any{"type": "object", "additionalProperties": true},
						"graph_context": map[string]any{"type": "object", "additionalProperties": true},
					},
				},
				"Webhook": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"url":    map[string]any{"type": "string"},
						"events": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Event types: build_complete, index_deleted, *"},
						"secret": map[string]any{"type": "string", "description": "HMAC-SHA256 secret for payload signing"},
					},
				},
				"WebhookRegisterRequest": map[string]any{
					"type":     "object",
					"required": []string{"url", "events"},
					"properties": map[string]any{
						"url":    map[string]any{"type": "string", "description": "Endpoint URL to receive POST notifications"},
						"events": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Event types to subscribe to: build_complete, index_deleted, *"},
						"secret": map[string]any{"type": "string", "description": "Optional secret for HMAC-SHA256 payload signing (X-Gleann-Signature header)"},
					},
				},
				"ChatCompletionRequest": map[string]any{
					"type":     "object",
					"required": []string{"model", "messages"},
					"properties": map[string]any{
						"model":       map[string]any{"type": "string", "example": "gleann/my-docs", "description": "gleann/<index> for RAG, gleann/ for pure LLM"},
						"messages":    map[string]any{"type": "array", "items": refSchema("ChatMessage"), "description": "Conversation history + latest user message"},
						"stream":      map[string]any{"type": "boolean", "default": false, "description": "Enable SSE streaming"},
						"temperature": map[string]any{"type": "number", "format": "float"},
						"max_tokens":  map[string]any{"type": "integer"},
					},
				},
				"ChatMessage": map[string]any{
					"type":     "object",
					"required": []string{"role", "content"},
					"properties": map[string]any{
						"role":    map[string]any{"type": "string", "enum": []string{"system", "user", "assistant"}},
						"content": map[string]any{"type": "string"},
					},
				},
				"ChatCompletionResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":      map[string]any{"type": "string"},
						"object":  map[string]any{"type": "string", "example": "chat.completion"},
						"created": map[string]any{"type": "integer"},
						"model":   map[string]any{"type": "string"},
						"choices": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"index":         map[string]any{"type": "integer"},
									"message":       refSchema("ChatMessage"),
									"finish_reason": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"ModelObject": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":       map[string]any{"type": "string", "example": "gleann/my-docs"},
						"object":   map[string]any{"type": "string", "example": "model"},
						"created":  map[string]any{"type": "integer"},
						"owned_by": map[string]any{"type": "string", "example": "gleann"},
					},
				},
				"ModelList": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"object": map[string]any{"type": "string", "example": "list"},
						"data":   map[string]any{"type": "array", "items": refSchema("ModelObject")},
					},
				},
			},
		},
	}
}

// paramName returns the common {name} path parameter definition.
func paramName() map[string]any {
	return map[string]any{
		"name":        "name",
		"in":          "path",
		"required":    true,
		"description": "Index name",
		"schema":      map[string]any{"type": "string"},
	}
}

// refSchema returns a $ref to a component schema.
func refSchema(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

// handleOpenAPISpec serves the OpenAPI 3.0 JSON specification.
func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	spec := s.openAPISpec()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(spec)
}

// swaggerUIHTML is a minimal single-page Swagger UI that loads the spec from /api/openapi.json.
const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Gleann API Documentation</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>
    body { margin: 0; padding: 0; }
    #swagger-ui { max-width: 1200px; margin: 0 auto; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: '/api/openapi.json',
      dom_id: '#swagger-ui',
      presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
      layout: 'BaseLayout',
      deepLinking: true,
    });
  </script>
</body>
</html>`

// handleSwaggerUI serves the Swagger UI HTML page.
func (s *Server) handleSwaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, swaggerUIHTML)
}
