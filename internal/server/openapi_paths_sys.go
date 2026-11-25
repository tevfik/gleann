package server

// openAPIPathsSystem returns the system, proxy-control and index-mutation
// endpoints that are registered directly in routes() but had no OpenAPI
// entry, plus the public documentation endpoints (/api/docs and
// /api/openapi.json) so the contract covers them too.
func openAPIPathsSystem() map[string]any {
	nameParam := paramName()
	return map[string]any{
		"/api/logs": map[string]any{
			"get": map[string]any{
				"tags":        []string{"system"},
				"summary":     "Recent server log lines",
				"description": "Returns the last lines captured in the in-memory log ring buffer, oldest first.",
				"operationId": "getLogs",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Recent log lines",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"logs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
									},
								},
							},
						},
					},
				},
			},
		},
		"/api/proxy/models": map[string]any{
			"get": map[string]any{
				"tags":        []string{"proxy"},
				"summary":     "List a provider's models (proxy)",
				"description": "Queries the provider (ollama, openai, ...) for its model list so the UI can fill the model dropdown. When `apikey` is omitted or the redacted placeholder, the stored key is used.",
				"operationId": "proxyModels",
				"parameters": []map[string]any{
					{"name": "provider", "in": "query", "required": false, "schema": map[string]any{"type": "string", "example": "ollama"}, "description": "Provider to query: ollama or openai. Any other value answers 200 with an empty list."},
					{"name": "host", "in": "query", "required": false, "schema": map[string]any{"type": "string"}, "description": "Provider base URL; defaults to http://localhost:11434 for ollama and https://api.openai.com/v1 for openai"},
					{"name": "apikey", "in": "query", "required": false, "schema": map[string]any{"type": "string"}, "description": "Override the provider key; empty or redacted placeholder uses the stored key"},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Provider models",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"models": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
									},
								},
							},
						},
					},
					"400": map[string]any{"description": "Invalid provider host"},
					"500": map[string]any{"description": "Provider request or JSON decode failed"},
				},
			},
		},
		"/stop": map[string]any{
			"post": map[string]any{
				"tags":        []string{"proxy", "system"},
				"summary":     "Stop the llama.cpp backend",
				"description": "In proxy_mode this endpoint is a pass-through: the request is forwarded to the upstream LLM server's /stop, and its status and body are returned verbatim. Outside proxy_mode it answers 404. When the upstream is configured but unreachable it answers 503.",
				"operationId": "proxyStop",
				"responses": map[string]any{
					"200": map[string]any{"description": "Upstream response forwarded verbatim"},
					"404": map[string]any{"description": "This endpoint is only registered in proxy_mode pass-through"},
					"503": map[string]any{"description": "The LLM server is not running"},
					"502": map[string]any{"description": "Could not reach the upstream /stop"},
				},
			},
		},
		"/api/indexes/{name}/update": map[string]any{
			"post": map[string]any{
				"tags":        []string{"indexes"},
				"summary":     "Update (sync) an index with new or changed sources",
				"description": "Re-embeds changed or added sources into an existing index; unchanged sources are dropped from the store. `sources` are read from disk under `docs_root` when `items` is empty. A never-built index answers 404; an embedding-model mismatch answers 400.",
				"operationId": "updateIndex",
				"parameters":  []map[string]any{nameParam},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":        "object",
								"description": "At least one of `sources` or `items` is required",
								"properties": map[string]any{
									"sources": map[string]any{
										"type":        "array",
										"items":       map[string]any{"type": "string"},
										"description": "Files to re-index, relative to `docs_root` and inside it",
									},
									"docs_root": map[string]any{"type": "string", "description": "Base directory the `sources` paths are resolved against"},
									"items": map[string]any{
										"type":        "array",
										"description": "Passages to add directly, when the caller has the text in hand",
										"items": map[string]any{
											"type": "object",
											"properties": map[string]any{
												"text":     map[string]any{"type": "string"},
												"metadata": map[string]any{"type": "object", "additionalProperties": true},
											},
										},
									},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Update result",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"status":          map[string]any{"type": "string", "example": "ok"},
										"index":           map[string]any{"type": "string"},
										"updated_sources": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
										"items_count":     map[string]any{"type": "integer"},
										"duration_ms":     map[string]any{"type": "integer", "format": "int64"},
										"mode":            map[string]any{"type": "string", "description": "How the update was applied (incremental or compact)"},
										"passages":        map[string]any{"type": "integer"},
										"removed":         map[string]any{"type": "integer"},
										"embedded_texts":  map[string]any{"type": "integer"},
										"graph_stale":     map[string]any{"type": "boolean", "description": "Present and true when the code graph may be stale"},
										"warning":         map[string]any{"type": "string", "description": "Warning shown with graph_stale"},
									},
								},
							},
						},
					},
					"400": map[string]any{"description": "Invalid name/body, no sources or items, a source path escaping docs_root, or a model mismatch"},
					"403": map[string]any{"description": "A source resolves outside the allowed roots"},
					"404": map[string]any{"description": "Index not found; build it before updating"},
					"500": map[string]any{"description": "Update failed"},
				},
			},
		},
		"/api/indexes/{name}/index-path": map[string]any{
			"post": map[string]any{
				"tags":        []string{"indexes"},
				"summary":     "Index a local directory (background task)",
				"description": "Starts a background indexing of a server-side directory into this index and returns immediately (the build runs as a background task, pollable via the task endpoints). The path must be inside the server's allowed roots.",
				"operationId": "indexPath",
				"parameters":  []map[string]any{nameParam},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":     "object",
								"required": []string{"path"},
								"properties": map[string]any{
									"path":        map[string]any{"type": "string", "description": "Server-side directory or file to index; must be inside the server's allowed roots"},
									"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Tags to stamp on the resulting passages"},
									"description": map[string]any{"type": "string", "description": "Description stored with the index"},
									"mcp_exposed": map[string]any{"type": "boolean", "description": "Expose the index through the MCP endpoint"},
									"code_model":  map[string]any{"type": "string", "description": "Embedding model for code files"},
									"doc_model":   map[string]any{"type": "string", "description": "Embedding model for document files"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Indexing started",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"status": map[string]any{"type": "string", "example": "indexing_started"},
										"path":   map[string]any{"type": "string"},
										"index":  map[string]any{"type": "string"},
									},
								},
							},
						},
					},
					"400": map[string]any{"description": "Invalid name/body or no path"},
					"403": map[string]any{"description": "Path resolves outside the allowed roots"},
				},
			},
		},
		"/api/indexes/{name}/storage": map[string]any{
			"post": map[string]any{
				"tags":        []string{"indexes"},
				"summary":     "Switch the index storage mode",
				"description": "Switches an index between keeping vectors on disk (`store`) and re-embedding on demand (`recompute`). A conversion re-indexes from the stored passages and runs as a background task; an index that is already in the requested mode answers 200 with `unchanged`.",
				"operationId": "setStorage",
				"parameters":  []map[string]any{nameParam},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":       "object",
								"required":   []string{"mode"},
								"properties": map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"store", "recompute"}}},
							},
						},
					},
				},
				"responses": map[string]any{
					"202": map[string]any{"description": "Conversion queued as a background task", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}, "mode": map[string]any{"type": "string"}}}}}},
					"200": map[string]any{"description": "Already in the requested mode; nothing to do", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"mode": map[string]any{"type": "string"}, "unchanged": map[string]any{"type": "boolean", "example": true}}}}}},
					"400": map[string]any{"description": "Invalid mode or index name"},
					"404": map[string]any{"description": "Index not found"},
					"409": map[string]any{"description": "Index cannot be converted to the requested mode"},
					"501": map[string]any{"description": "This server was built without a storage converter"},
				},
			},
		},
		"/api/indexes/{name}/upload": map[string]any{
			"post": map[string]any{
				"tags":        []string{"indexes"},
				"summary":     "Upload a file into a new index",
				"description": "Receives a multipart/form-data file (field name `file`) and builds the index from it. An index that already has passages answers 409.",
				"operationId": "uploadIndex",
				"parameters":  []map[string]any{nameParam},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"multipart/form-data": map[string]any{
							"schema": map[string]any{
								"type":       "object",
								"properties": map[string]any{"file": map[string]any{"type": "string", "format": "binary"}},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "File indexed", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"status": map[string]any{"type": "string", "example": "indexed"}, "file": map[string]any{"type": "string"}}}}}},
					"400": map[string]any{"description": "Invalid name, no file, or bad filename"},
					"409": map[string]any{"description": "Index already has passages; upload targets empty indexes"},
					"500": map[string]any{"description": "Save or build error"},
				},
			},
		},
		"/api/indexes/{name}/watch": map[string]any{
			"post": map[string]any{
				"tags":        []string{"indexes"},
				"summary":     "Start or stop watching the index source",
				"description": "Turns the server's auto-indexer on or off for this index. With `enable` true the directory watched is `dir` when given, otherwise the directory recorded in the index metadata; a client-supplied `dir` must be inside the server's allowed roots. With `enable` false the index is unwatched.",
				"operationId": "watchIndex",
				"parameters":  []map[string]any{nameParam},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type":     "object",
								"required": []string{"enable"},
								"properties": map[string]any{
									"enable": map[string]any{"type": "boolean", "description": "True starts watching, false stops it"},
									"dir":    map[string]any{"type": "string", "description": "Directory to watch; required when enabling if the index has no recorded source directory"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Watch updated", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"status": map[string]any{"type": "string", "example": "ok"}, "watch": map[string]any{"type": "boolean"}}}}}},
					"400": map[string]any{"description": "Invalid name or body, missing `enable`, or no dir when enabling"},
					"403": map[string]any{"description": "Dir resolves outside the allowed roots"},
					"404": map[string]any{"description": "Index not found"},
					"500": map[string]any{"description": "Read, watch, or auto-indexer error"},
				},
			},
		},

		// The interactive documentation endpoints are public, and the spec is
		// the same document a Postman import reads, so they are described here
		// too rather than left as gaps between the router and the contract.
		"/api/docs": map[string]any{
			"get": map[string]any{
				"tags":        []string{"system"},
				"summary":     "Swagger UI (public)",
				"description": "Single-page Swagger UI that loads /api/openapi.json. Public, like the specification itself.",
				"operationId": "swaggerUI",
				"security":    []map[string]any{},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "HTML page",
						"content":     map[string]any{"text/html": map[string]any{"schema": map[string]any{"type": "string"}}},
					},
				},
			},
		},
		"/api/openapi.json": map[string]any{
			"get": map[string]any{
				"tags":        []string{"system"},
				"summary":     "OpenAPI 3.0 specification (public)",
				"description": "The machine-readable contract served to Swagger UI and to API clients such as Postman. Sent with `Cache-Control: public, max-age=3600`.",
				"operationId": "openAPISpec",
				"security":    []map[string]any{},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "OpenAPI 3.0 document",
						"content":     map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}},
					},
				},
			},
		},
	}
}
