package server

// openAPIPathsHealth returns the /health endpoint spec.
func openAPIPathsHealth() map[string]any {
	return map[string]any{
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
	}
}

// openAPIPathsIndexes returns the /api/indexes/* endpoint specs.
func openAPIPathsIndexes() map[string]any {
	return map[string]any{
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
	}
}

// openAPIPathsSearch returns the search-related endpoint specs.
func openAPIPathsSearch() map[string]any {
	return map[string]any{
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
	}
}

// openAPIPathsGraph returns the /api/graph/* endpoint specs.
func openAPIPathsGraph() map[string]any {
	return map[string]any{
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
	}
}

// openAPIPathsMemory returns the /api/memory/* (Memory Engine) endpoint specs.
func openAPIPathsMemory() map[string]any {
	return map[string]any{
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
	}
}

// openAPIPathsBlocks returns the /api/blocks/* endpoint specs.
func openAPIPathsBlocks() map[string]any {
	return map[string]any{
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
	}
}

// openAPIPathsWebhooks returns the /api/webhooks endpoint specs.
func openAPIPathsWebhooks() map[string]any {
	return map[string]any{
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
	}
}

// openAPIPathsMetrics returns the /metrics endpoint spec.
func openAPIPathsMetrics() map[string]any {
	return map[string]any{
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
	}
}

// openAPIPathsConversations returns the /api/conversations/* endpoint specs.
func openAPIPathsConversations() map[string]any {
	return map[string]any{
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
	}
}

// openAPIPathsProxy returns the OpenAI-compatible proxy endpoint specs.
func openAPIPathsProxy() map[string]any {
	return map[string]any{
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
	}
}

// openAPIPathsA2A returns the A2A Protocol endpoint specs.
func openAPIPathsA2A() map[string]any {
	return map[string]any{
		"/.well-known/agent-card.json": map[string]any{
			"get": map[string]any{
				"tags":        []string{"a2a"},
				"summary":     "A2A Agent Card (discovery)",
				"description": "Returns the A2A Agent Card describing gleann's capabilities and skills. Used by other agents and orchestrators to discover this agent.",
				"operationId": "getAgentCard",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Agent Card",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("AgentCard"),
							},
						},
					},
				},
			},
		},
		"/a2a/v1/message:send": map[string]any{
			"post": map[string]any{
				"tags":        []string{"a2a"},
				"summary":     "Send a message to an A2A skill",
				"description": "Routes the message to the best matching skill (semantic-search, ask-rag, code-analysis, memory-management) based on content keywords. Set metadata.skill to target a specific skill.",
				"operationId": "a2aSendMessage",
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": refSchema("A2ASendMessageRequest"),
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Task result",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("A2ATask"),
							},
						},
					},
					"400": map[string]any{"description": "Invalid request"},
				},
			},
		},
		"/a2a/v1/tasks/{id}": map[string]any{
			"get": map[string]any{
				"tags":        []string{"a2a"},
				"summary":     "Get A2A task status",
				"operationId": "a2aGetTask",
				"parameters": []map[string]any{
					{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Task ID"},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Task details",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("A2ATask"),
							},
						},
					},
					"404": map[string]any{"description": "Task not found"},
				},
			},
		},
	}
}

// openAPIPathsUnifiedMemory returns the /api/memory/* (unified memory) endpoint specs.
func openAPIPathsUnifiedMemory() map[string]any {
	return map[string]any{
		"/api/memory/ingest": map[string]any{
			"post": map[string]any{
				"tags":        []string{"unified-memory"},
				"summary":     "Ingest facts and relationships",
				"description": "Store facts (block memory) and relationships (knowledge graph) in a single call. Supports metadata, TTL, scoping, and edge attributes.",
				"operationId": "unifiedMemoryIngest",
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": refSchema("UnifiedIngestRequest"),
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Ingest result",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("UnifiedIngestResponse"),
							},
						},
					},
					"400": map[string]any{"description": "Invalid request"},
					"422": map[string]any{"description": "Nothing was stored (all facts/relationships failed)"},
				},
			},
		},
		"/api/memory/recall": map[string]any{
			"post": map[string]any{
				"tags":        []string{"unified-memory"},
				"summary":     "Recall from all memory layers",
				"description": "Queries blocks, knowledge graph, and vector search in parallel. Supports date-range filtering, tag filtering, tier filtering, relation filtering, and LLM-ready context output.",
				"operationId": "unifiedMemoryRecall",
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": refSchema("UnifiedRecallRequest"),
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Merged recall results",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("UnifiedRecallResponse"),
							},
						},
					},
					"400": map[string]any{"description": "Invalid request (empty query)"},
				},
			},
		},
	}
}

// openAPIPathsTasks returns the /api/tasks/* endpoint specs.
func openAPIPathsTasks() map[string]any {
	return map[string]any{
		"/api/tasks": map[string]any{
			"get": map[string]any{
				"tags":        []string{"tasks"},
				"summary":     "List background tasks",
				"description": "Returns all background tasks. Use status query param to filter.",
				"operationId": "listTasks",
				"parameters": []map[string]any{
					{"name": "status", "in": "query", "required": false, "schema": map[string]any{"type": "string", "enum": []string{"queued", "running", "completed", "failed", "cancelled"}}, "description": "Filter by task status"},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Task list",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"tasks": map[string]any{"type": "array", "items": refSchema("BackgroundTask")},
										"count": map[string]any{"type": "integer"},
									},
								},
							},
						},
					},
				},
			},
			"delete": map[string]any{
				"tags":        []string{"tasks"},
				"summary":     "Cleanup old tasks",
				"description": "Removes completed and failed tasks older than 1 hour.",
				"operationId": "cleanupTasks",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Cleanup result",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type":       "object",
									"properties": map[string]any{"removed": map[string]any{"type": "integer"}},
								},
							},
						},
					},
				},
			},
		},
		"/api/tasks/{id}": map[string]any{
			"get": map[string]any{
				"tags":        []string{"tasks"},
				"summary":     "Get task by ID",
				"operationId": "getTask",
				"parameters": []map[string]any{
					{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Background task ID"},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Task details",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("BackgroundTask"),
							},
						},
					},
					"404": map[string]any{"description": "Task not found"},
				},
			},
		},
	}
}

// openAPIPathsPacks returns the /api/packs/* endpoint specs.
func openAPIPathsPacks() map[string]any {
	return map[string]any{
		"/api/packs": map[string]any{
			"get": map[string]any{
				"tags":        []string{"packs"},
				"summary":     "List knowledge packs",
				"operationId": "listPacks",
				"parameters": []map[string]any{
					{
						"name": "app", "in": "query", "required": false,
						"schema":      map[string]any{"type": "string"},
						"description": "Filter packs by app_hint (e.g. `ekiyo`).",
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Pack list",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"packs": map[string]any{"type": "array", "items": refSchema("PackManifest")},
										"count": map[string]any{"type": "integer"},
									},
								},
							},
						},
					},
				},
			},
		},
		"/api/packs/{id}": map[string]any{
			"get": map[string]any{
				"tags":        []string{"packs"},
				"summary":     "Get pack manifest",
				"operationId": "getPack",
				"parameters": []map[string]any{
					{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Pack ID (e.g. `crops-tr`)"},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Pack manifest",
						"content": map[string]any{
							"application/json": map[string]any{"schema": refSchema("PackManifest")},
						},
					},
					"304": map[string]any{"description": "Not modified (ETag matched)"},
					"404": map[string]any{"description": "Pack not found"},
				},
			},
		},
		"/api/packs/{id}/data": map[string]any{
			"get": map[string]any{
				"tags":        []string{"packs"},
				"summary":     "Get full pack contents",
				"description": "Returns the manifest and the full items array. Clients can use `If-None-Match` with the previously received `ETag` to avoid re-downloading unchanged packs.",
				"operationId": "getPackData",
				"parameters": []map[string]any{
					{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Pack ID"},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Manifest + items",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"manifest": refSchema("PackManifest"),
										"items": map[string]any{
											"type":  "array",
											"items": map[string]any{"type": "object", "additionalProperties": true},
										},
									},
								},
							},
						},
					},
					"304": map[string]any{"description": "Not modified (ETag matched)"},
					"404": map[string]any{"description": "Pack not found"},
				},
			},
		},
		"/api/packs/{id}/items/{slug}": map[string]any{
			"get": map[string]any{
				"tags":        []string{"packs"},
				"summary":     "Get single item by slug",
				"operationId": "getPackItem",
				"parameters": []map[string]any{
					{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Pack ID"},
					{"name": "slug", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Item slug (e.g. `pepper_capia`)"},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Item object",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"type": "object", "additionalProperties": true},
							},
						},
					},
					"304": map[string]any{"description": "Not modified (ETag matched)"},
					"404": map[string]any{"description": "Pack or item not found"},
				},
			},
		},
		"/api/packs/{id}/search": map[string]any{
			"get": map[string]any{
				"tags":        []string{"packs"},
				"summary":     "Search pack items",
				"description": "Case-insensitive substring search across the fields listed in `pack.yaml search.fields`.",
				"operationId": "searchPackItems",
				"parameters": []map[string]any{
					{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Pack ID"},
					{"name": "q", "in": "query", "required": true, "schema": map[string]any{"type": "string"}, "description": "Search query"},
					{"name": "n", "in": "query", "required": false, "schema": map[string]any{"type": "integer", "default": 20}, "description": "Max results"},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Search results",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"items": map[string]any{
											"type":  "array",
											"items": map[string]any{"type": "object", "additionalProperties": true},
										},
										"count": map[string]any{"type": "integer"},
										"query": map[string]any{"type": "string"},
									},
								},
							},
						},
					},
					"404": map[string]any{"description": "Pack not found"},
				},
			},
		},
		"/api/packs/reload": map[string]any{
			"post": map[string]any{
				"tags":        []string{"packs"},
				"summary":     "Reload all packs from disk",
				"description": "Rescans `GLEANN_PACKS_DIR` and replaces the in-memory registry. No downtime — existing requests continue to completion before the registry is swapped.",
				"operationId": "reloadPacks",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Reload succeeded",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"reloaded": map[string]any{"type": "boolean", "description": "True if reload succeeded"},
										"count":    map[string]any{"type": "integer", "description": "Number of packs now in registry"},
									},
								},
							},
						},
					},
					"500": map[string]any{"description": "Reload failed — see server logs"},
				},
			},
		},
	}
}
