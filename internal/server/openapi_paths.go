package server

// openAPIPathsHealth returns the /health endpoint spec.
func openAPIPathsHealth() map[string]any {
	return map[string]any{
		"/health": map[string]any{
			"get": map[string]any{
				"tags":        []string{"health"},
				"summary":     "Health check",
				"operationId": "healthCheck",
				// Public: security.go serves /health without a token.
				"security": []map[string]any{},
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
		"/api/storage": map[string]any{
			"get": map[string]any{
				"tags":        []string{"indexes"},
				"summary":     "Disk space of the indexes (admin)",
				"operationId": "getStorage",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Bytes of all files under the index directory, index and passage counts, and the file system's size and free space",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"index_dir":        map[string]any{"type": "string"},
										"indexes":          map[string]any{"type": "integer"},
										"passages":         map[string]any{"type": "integer"},
										"bytes":            map[string]any{"type": "integer"},
										"disk_total_bytes": map[string]any{"type": "integer"},
										"disk_free_bytes":  map[string]any{"type": "integer"},
										"data_dir":         map[string]any{"type": "string", "description": "The gleann data folder (GLEANN_CONFIG_DIR, default ~/.gleann)"},
										"data_bytes":       map[string]any{"type": "integer", "description": "Bytes of all regular files under data_dir (measurement cached for 60 s)"},
										"data_folders":     map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}, "description": "Bytes per top-level entry of data_dir"},
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
					"400": map[string]any{"description": "Invalid index name"},
					"403": map[string]any{"description": "Access denied (client target or tag isolation). With GLEANN_TAGS set, an index that does not exist answers 403 as well, so a restricted client cannot probe index names"},
					"404": map[string]any{"description": "Index not found (unrestricted clients)"},
					"409": map[string]any{"description": "Index is in the old store format; run `gleann index migrate`, or was built with another embedding model than the server uses (model mismatch)"},
					"500": map[string]any{"description": "Index could not be loaded (corrupt or unreadable)"},
					"503": map[string]any{"description": "Index kept changing while loading; retry (Retry-After)"},
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
			"patch": map[string]any{
				"tags":        []string{"indexes"},
				"summary":     "Update an index (metadata only)",
				"description": "Updates the metadata of an existing index. Only the fields present in the request body are changed (tags, description, mcp_exposed); fields left out keep their value. A missing index answers 404 to an unrestricted client; a client restricted by target or tags gets 403, also for an index that does not exist (so index names cannot be probed).",
				"operationId": "patchIndex",
				"parameters":  []map[string]any{paramName()},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":        "object",
								"description": "Index metadata fields to update",
								"properties": map[string]any{
									"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Replaces the index's tag list"},
									"description": map[string]any{"type": "string", "description": "Description of the index contents"},
									"mcp_exposed": map[string]any{"type": "boolean", "description": "Whether the index is exposed to MCP clients"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Index updated; returns the updated index metadata"},
					"400": map[string]any{"description": "Invalid index name or JSON body"},
					"403": map[string]any{"description": "Access denied (client target or tag isolation; also an index that does not exist when GLEANN_TAGS is set)"},
					"404": map[string]any{"description": "Index not found (unrestricted clients)"},
					"500": map[string]any{"description": "Failed to update or read the index metadata"},
				},
			},
		},
		"/api/indexes/{name}/passages": map[string]any{
			"get": map[string]any{
				"tags":        []string{"indexes"},
				"summary":     "List stored passages",
				"description": "Lists the passages of a format-2 index in internal ID order, one SQLite page per request (ORDER BY id LIMIT/OFFSET). It reads the record store only: no vector file or embedder is loaded. `id` is the internal passage ID (the sort key); `vec_slot` is the ID a search result carries and is null for a lexical-only passage.",
				"operationId": "listIndexPassages",
				"parameters":  []map[string]any{paramName(), paramListLimit(), paramListOffset()},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "One page of stored passages",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("PassageListResponse"),
							},
						},
					},
					"400": map[string]any{"description": "Invalid index name, limit or offset"},
					"403": map[string]any{"description": "Access denied (client target or tag isolation)"},
					"404": map[string]any{"description": "Index not found"},
					"409": map[string]any{"description": "Index uses the old store format; run gleann index migrate, or was built with another embedding model than the server uses (model mismatch)"},
				},
			},
		},
		"/api/indexes/{name}/sections": map[string]any{
			"get": map[string]any{
				"tags":        []string{"indexes"},
				"summary":     "List document sections",
				"description": "Groups the stored passages of a format-2 index into document sections from their heading metadata (section_id, section_title, section_path, section_summary; falling back to heading/title), with the passage count and the chunk and passage-ID ranges of each section. Passages without section metadata are not listed.",
				"operationId": "listIndexSections",
				"parameters":  []map[string]any{paramName(), paramListLimit(), paramListOffset()},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "One page of sections",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("SectionListResponse"),
							},
						},
					},
					"400": map[string]any{"description": "Invalid index name, limit or offset"},
					"403": map[string]any{"description": "Access denied (client target or tag isolation)"},
					"404": map[string]any{"description": "Index not found"},
					"409": map[string]any{"description": "Index uses the old store format; run gleann index migrate, or was built with another embedding model than the server uses (model mismatch)"},
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
					"403": map[string]any{"description": "Access denied (client target or tag isolation; also an index that does not exist when GLEANN_TAGS is set)"},
					"404": map[string]any{"description": "Index not found (unrestricted clients)"},
					"409": map[string]any{"description": "Index is in the old store format; run `gleann index migrate`, or was built with another embedding model than the server uses (model mismatch)"},
					"500": map[string]any{"description": "Index could not be loaded (corrupt or unreadable)"},
					"503": map[string]any{"description": "Index kept changing while loading; retry (Retry-After)"},
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
					"403": map[string]any{"description": "Access denied (client target or tag isolation; also an index that does not exist when GLEANN_TAGS is set)"},
					"404": map[string]any{"description": "Index not found (unrestricted clients)"},
					"409": map[string]any{"description": "Index is in the old store format; run `gleann index migrate`, or was built with another embedding model than the server uses (model mismatch)"},
					"500": map[string]any{"description": "Index could not be loaded (corrupt or unreadable)"},
					"503": map[string]any{"description": "Index kept changing while loading; retry (Retry-After)"},
				},
			},
		},
		"/api/search": map[string]any{
			"post": map[string]any{
				"tags":        []string{"search"},
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
		"/api/graph/{name}/toc": map[string]any{
			"get": map[string]any{
				"tags":        []string{"graph"},
				"summary":     "Get hierarchical document Table of Contents (TOC)",
				"description": "Returns hierarchical heading outline for a document, or all documents if path is omitted.",
				"operationId": "graphDocumentTOC",
				"parameters": []map[string]any{
					paramName(),
					{
						"name":        "path",
						"in":          "query",
						"description": "Document virtual or relative path (e.g. docs/guide.md)",
						"required":    false,
						"schema":      map[string]any{"type": "string"},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Document TOC or document list"},
					"404": map[string]any{"description": "Document not found"},
				},
			},
		},
		"/api/graph/{name}/documents": map[string]any{
			"get": map[string]any{
				"tags":        []string{"graph"},
				"summary":     "List all indexed documents",
				"description": "Returns all indexed documents with folder info, summaries, and heading counts.",
				"operationId": "graphListDocuments",
				"parameters":  []map[string]any{paramName()},
				"responses": map[string]any{
					"200": map[string]any{"description": "List of indexed documents"},
				},
			},
		},
		"/api/graph/{name}/query": map[string]any{
			"post": map[string]any{
				"tags":        []string{"graph"},
				"summary":     "Query the code graph",
				"description": "Run predefined queries (callees, callers, symbols_in_file, impact, toc) against the code graph: the SQLite graph of a format-2 index, in every build. Raw Cypher is rejected with 400; toc of a path that is not an indexed document is 404.",
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
				"description": "Indexes a source directory into the code graph (the SQLite tables of a format-2 index, in every build; a format-1 index has no code graph and answers 409).",
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
					"409": map[string]any{"description": "The index uses the old store format, which has no code graph; migrate it first"},
					"503": map[string]any{"description": "Graph database not available"},
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
				"description": "Returns all persisted memory blocks. Use `tier` to filter by storage tier and `scope`/`target`/`X-Gleann-Target` to filter by scope (global blocks are always included).",
				"operationId": "listBlocks",
				"parameters": []map[string]any{
					{
						"name":        "tier",
						"in":          "query",
						"required":    false,
						"description": "Filter by memory tier",
						"schema":      map[string]any{"type": "string", "enum": []string{"short", "medium", "long"}},
					},
					{
						"name":        "scope",
						"in":          "query",
						"required":    false,
						"description": "Scope filter (alias: target; also X-Gleann-Target), normalized. When unrestricted, 'all'/'*' selects all scopes — identically in the query string and the header; against a restricted target the sentinel is rejected (403). Omitted lists every block.",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "target",
						"in":          "query",
						"required":    false,
						"description": "Alias of scope (used when scope is absent).",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "X-Gleann-Target",
						"in":          "header",
						"required":    false,
						"description": "Target scope header; must agree with the query scope (403 on mismatch). A wildcard ('all'/'*') header is unrestricted.",
						"schema":      map[string]any{"type": "string"},
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
					"403": map[string]any{"description": "Target scope mismatch between header and query, or an 'all'/'*' sentinel against a restricted target"},
				},
			},
			"post": map[string]any{
				"tags":        []string{"blocks"},
				"summary":     "Add a memory block",
				"description": "Stores a new memory block in the specified tier. Short-term blocks are in-memory (session-scoped), medium and long-term are persisted. Scopes are normalized on write; a body scope of 'all'/'*' stores a global block when unrestricted and is rejected (403) against a restricted X-Gleann-Target header.",
				"operationId": "addBlock",
				"parameters": []map[string]any{
					{
						"name":        "X-Gleann-Target",
						"in":          "header",
						"required":    false,
						"description": "Target scope header; must agree with the body scope/target after normalization (403 on mismatch). A wildcard ('all'/'*') header is unrestricted.",
						"schema":      map[string]any{"type": "string"},
					},
				},
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
					"403": map[string]any{"description": "Target scope mismatch between header and body, or an 'all'/'*' body sentinel against a restricted target"},
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
				"description": "Full-text search across all memory tiers. Matches content, label, and tags. Scope resolves like listing: a sentinel in the query string or the header alike selects all scopes when unrestricted.",
				"operationId": "searchBlocks",
				"parameters": []map[string]any{
					{
						"name":        "q",
						"in":          "query",
						"required":    true,
						"description": "Search query",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "scope",
						"in":          "query",
						"required":    false,
						"description": "Scope filter (alias: target; also X-Gleann-Target), normalized. When unrestricted, 'all'/'*' selects all scopes — identically in the query string and the header; against a restricted target the sentinel is rejected (403).",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "target",
						"in":          "query",
						"required":    false,
						"description": "Alias of scope (used when scope is absent).",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "X-Gleann-Target",
						"in":          "header",
						"required":    false,
						"description": "Target scope header; must agree with the query scope (403 on mismatch). A wildcard ('all'/'*') header is unrestricted.",
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
					"403": map[string]any{"description": "Target scope mismatch between header and query, or an 'all'/'*' sentinel against a restricted target"},
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
					{
						"name":        "scope",
						"in":          "query",
						"required":    false,
						"description": "Scope to build the context for (alias: target; also X-Gleann-Target). Empty uses the token/header target, or the global context when omitted. When unrestricted, 'all'/'*' explicitly selects all scopes; against a restricted target the sentinel is rejected (403).",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "target",
						"in":          "query",
						"required":    false,
						"description": "Alias of scope (used when scope is absent).",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "X-Gleann-Target",
						"in":          "header",
						"required":    false,
						"description": "Target scope header; must agree with the query scope (403 on mismatch).",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "command",
						"in":          "query",
						"required":    false,
						"description": "The command the task is about to run; surfaces matching recorded failures (normalized prefix match in either direction)",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "paths",
						"in":          "query",
						"required":    false,
						"description": "Comma-separated file paths to match against failure provenance",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "path",
						"in":          "query",
						"required":    false,
						"description": "Repeatable singular alias of paths",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "symbols",
						"in":          "query",
						"required":    false,
						"description": "Comma-separated symbol names/FQNs to match against failure provenance",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "symbol",
						"in":          "query",
						"required":    false,
						"description": "Repeatable singular alias of symbols",
						"schema":      map[string]any{"type": "string"},
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
					"403": map[string]any{"description": "Target scope mismatch between header and query, or an 'all'/'*' sentinel against a restricted target"},
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
		"/api/blocks/failures": map[string]any{
			"post": map[string]any{
				"tags":        []string{"blocks"},
				"summary":     "Record a command failure and its fix",
				"description": "Stores a failure memory block with the command, error, fix, and related file paths/symbols so future sessions surface it instead of repeating the mistake. Recording the same failure again reinforces the existing block (Confirms++). Admin scope required. The target scope resolves from ?scope= / ?target= / X-Gleann-Target and must agree with any body scope after normalization (403 on mismatch). Body scope 'all'/'*' records a global block when unrestricted, and is rejected (403) against a restricted target.",
				"operationId": "addFailure",
				"parameters": []map[string]any{
					{
						"name":        "scope",
						"in":          "query",
						"required":    false,
						"description": "Target scope (alias: target). Must match the body scope and X-Gleann-Target if present, after normalization. 'all'/'*' means global when unrestricted; against a restricted target it is rejected (403).",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "X-Gleann-Target",
						"in":          "header",
						"required":    false,
						"description": "Target scope header; must agree with the query/body scope (403 on mismatch).",
						"schema":      map[string]any{"type": "string"},
					},
				},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": refSchema("FailureAddRequest"),
						},
					},
				},
				"responses": map[string]any{
					"201": map[string]any{
						"description": "Failure recorded (first observation); returns the saved failure block.",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("Failure"),
							},
						},
					},
					"200": map[string]any{
						"description": "Failure confirmed (repeat recording, Confirms > 1); returns the reinforced failure block.",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("Failure"),
							},
						},
					},
					"400": map[string]any{"description": "Missing command or error, or invalid body"},
					"403": map[string]any{"description": "Target scope mismatch between header/query and body"},
				},
			},
			"get": map[string]any{
				"tags":        []string{"blocks"},
				"summary":     "List recorded command failures",
				"description": "Returns the failure blocks visible to a scope (global + the scope and its ancestors). With no command/paths/symbols filter it lists every failure in the scope; otherwise it returns only the failures matching the task query. Requires query auth scope (`ScopeQuery`). Target scope filter is optional (defaults to token/header target, or lists all scopes when omitted; `?scope=all` / `*` explicitly selects all scopes when unrestricted).",
				"operationId": "listFailures",
				"parameters": []map[string]any{
					{
						"name":        "scope",
						"in":          "query",
						"required":    false,
						"description": "Scope to list (alias: target; also X-Gleann-Target). Empty defaults to the token/header target, or lists all scopes when unrestricted. 'all'/'*' explicitly selects all scopes when unrestricted; against a restricted target the sentinel is rejected (403).",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "target",
						"in":          "query",
						"required":    false,
						"description": "Alias of scope (used when scope is absent).",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "X-Gleann-Target",
						"in":          "header",
						"required":    false,
						"description": "Target scope header; must agree with the query scope (403 on mismatch).",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "command",
						"in":          "query",
						"required":    false,
						"description": "Filter by command (normalized prefix match in either direction)",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "paths",
						"in":          "query",
						"required":    false,
						"description": "Comma-separated file paths to match against failure provenance",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "path",
						"in":          "query",
						"required":    false,
						"description": "Repeatable singular alias of paths",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "symbols",
						"in":          "query",
						"required":    false,
						"description": "Comma-separated symbol names/FQNs to match against failure provenance",
						"schema":      map[string]any{"type": "string"},
					},
					{
						"name":        "symbol",
						"in":          "query",
						"required":    false,
						"description": "Repeatable singular alias of symbols",
						"schema":      map[string]any{"type": "string"},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "List of failures",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": refSchema("FailureListResponse"),
							},
						},
					},
					"403": map[string]any{"description": "Target scope mismatch between header and query, or an 'all'/'*' sentinel against a restricted target"},
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
			"delete": map[string]any{
				"tags":        []string{"conversations"},
				"summary":     "Clear all conversations",
				"description": "Permanently deletes all saved conversations.",
				"operationId": "clearConversations",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "All conversations cleared",
						"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"status": map[string]any{"type": "string", "example": "deleted"},
								"count":  map[string]any{"type": "integer", "description": "Number of conversations deleted"},
							},
						}}},
					},
					"500": map[string]any{"description": "Failed to list the conversations"},
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
					"200": map[string]any{"description": "Full conversation with messages", "content": map[string]any{"application/json": map[string]any{"schema": refSchema("Conversation")}}},
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
			"put": map[string]any{
				"tags":        []string{"conversations"},
				"summary":     "Update a conversation title",
				"description": "Updates the title of a saved conversation.",
				"operationId": "updateConversation",
				"parameters": []map[string]any{
					{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Conversation ID or prefix"},
				},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":     "object",
								"required": []string{"title"},
								"properties": map[string]any{
									"title": map[string]any{"type": "string", "description": "New conversation title"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Conversation updated; returns the full conversation", "content": map[string]any{"application/json": map[string]any{"schema": refSchema("Conversation")}}},
					"400": map[string]any{"description": "Missing conversation ID or invalid JSON body"},
					"404": map[string]any{"description": "Conversation not found"},
					"500": map[string]any{"description": "Failed to save the conversation"},
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
				"description": "Drop-in for OpenAI chat completions. Set model to gleann/<index> to enable RAG. Multi-index: gleann/a,b. Pure LLM: gleann/.\n\nOptional override headers:\n- X-Gleann-Top-K: number of RAG results (default: config top_k)\n- X-Gleann-Min-Score: minimum similarity score\n- X-Gleann-Hybrid-Alpha: vector vs lexical weight, 0 to 1 (0 = lexical only; invalid values are ignored)\n- X-Gleann-Cover: true (pass-through mode, several indexes) keeps the best passage of every index whose best passage is close to the overall best; X-Gleann-Cover-Margin / -Share / -Floor set what close means",
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
				// Public: security.go serves the agent card without a token.
				"security": []map[string]any{},
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
