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
			{"name": "webhooks", "description": "Webhook notification management"},
			{"name": "metrics", "description": "Prometheus-compatible metrics"},
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
			"/api/search": map[string]any{
				"post": map[string]any{
					"tags":        []string{"search"},
					"summary":     "Multi-index search",
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
						"question":     map[string]any{"type": "string", "description": "Question to answer using RAG"},
						"top_k":        map[string]any{"type": "integer", "default": 10},
						"llm_model":    map[string]any{"type": "string", "description": "LLM model name for answer generation"},
						"llm_provider": map[string]any{"type": "string", "description": "LLM provider (ollama, openai, anthropic)"},
						"stream":       map[string]any{"type": "boolean", "default": false, "description": "Enable SSE streaming. When true, response is text/event-stream with `data: {\"token\": \"...\"}` events, ending with `data: [DONE]`"},
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
				"Error": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"error": map[string]any{"type": "string"},
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
