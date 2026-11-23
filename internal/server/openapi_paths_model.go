package server

// openAPIPathsModels returns the model-catalog and model-download endpoints
// (GET/POST /api/models/catalog, GET /api/models/local, POST
// /api/models/download) that had no OpenAPI entry.
func openAPIPathsModels() map[string]any {
	return map[string]any{
		"/api/models/catalog": map[string]any{
			"get": map[string]any{
				"tags":        []string{"models"},
				"summary":     "List the model catalog with state",
				"description": "Returns each catalog model merged with its runtime state (installed / available / downloading / failed / blocked) and any in-flight progress.",
				"operationId": "getModelCatalog",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Models with state",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"models": map[string]any{"type": "array", "items": map[string]any{
											"type": "object",
											"properties": map[string]any{
												"id":        map[string]any{"type": "string"},
												"name":      map[string]any{"type": "string"},
												"group":     map[string]any{"type": "string", "description": "Optional grouping label"},
												"size":      map[string]any{"type": "string", "description": "Human-readable size, e.g. \"466 MB\""},
												"note":      map[string]any{"type": "string"},
												"value":     map[string]any{"type": "string", "description": "What a setting stores to use this model"},
												"installed": map[string]any{"type": "boolean"},
												"blocked":   map[string]any{"type": "string", "description": "Why the model cannot be downloaded yet, if blocked"},
												"state":     map[string]any{"type": "string", "enum": []string{"installed", "available", "downloading", "failed", "blocked"}},
												"progress":  map[string]any{"type": "object", "properties": map[string]any{"done": map[string]any{"type": "integer", "description": "Bytes so far"}, "total": map[string]any{"type": "integer", "description": "Bytes expected; 0 = unknown"}, "message": map[string]any{"type": "string"}}},
												"error":     map[string]any{"type": "string", "description": "Error of the last failed download, if retried"},
											},
										}},
									},
								},
							},
						},
					},
					"401": map[string]any{"description": "Authorization required"},
				},
			},
			"post": map[string]any{
				"tags":        []string{"models"},
				"summary":     "Download a catalog model",
				"description": "Starts a catalog download by id. Answers 202 with `state: downloading`; a blocked or already-downloading model answers 409; an unknown id answers 404; a non-JSON body answers 415; a bad id answers 400; another method answers 405.",
				"operationId": "downloadCatalogModel",
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"type": "object", "required": []string{"id"}, "properties": map[string]any{"id": map[string]any{"type": "string", "description": "Catalog model id"}}},
						},
					},
				},
				"responses": map[string]any{
					"202": map[string]any{"description": "Download started", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}, "state": map[string]any{"type": "string", "example": "downloading"}}}}}},
					"400": map[string]any{"description": "Invalid or empty id"},
					"404": map[string]any{"description": "Unknown model id"},
					"405": map[string]any{"description": "Method not allowed"},
					"409": map[string]any{"description": "Model is blocked or already downloading"},
					"415": map[string]any{"description": "Content-Type is not application/json"},
				},
			},
		},
		"/api/models/local": map[string]any{
			"get": map[string]any{
				"tags":        []string{"models"},
				"summary":     "List local GGUF model files",
				"description": "Returns the filenames of the .gguf files in the model catalogue directory, sorted.",
				"operationId": "listLocalModels",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Local model filenames",
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
				},
			},
		},
		"/api/models/download": map[string]any{
			"post": map[string]any{
				"tags":        []string{"models"},
				"summary":     "Download a model from a URL",
				"description": "Fetches a model over HTTPS (or the loopback HTTP allowlist) into the model directory, streaming to a temp file and renaming into place, as a background task. Both `url` and `filename` are required; the filename must be safe.",
				"operationId": "downloadModel",
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"type": "object", "required": []string{"url", "filename"}, "properties": map[string]any{"url": map[string]any{"type": "string", "description": "HTTPS model URL (loopback HTTP allowlist also accepted)"}, "filename": map[string]any{"type": "string", "description": "Safe target filename (.gguf, .eifm, .bin, ...)"}}},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Download started", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}, "status": map[string]any{"type": "string", "example": "started"}}}}}},
					"400": map[string]any{"description": "Bad body, missing url or filename, or an invalid scheme/filename"},
					"405": map[string]any{"description": "Method not allowed"},
					"500": map[string]any{"description": "Failed to start the download"},
				},
			},
		},
	}
}

// openAPIPathsBlockCompact returns POST /api/blocks/compact, the memory
// maintenance endpoint that had no OpenAPI entry.
func openAPIPathsBlockCompact() map[string]any {
	return map[string]any{
		"/api/blocks/compact": map[string]any{
			"post": map[string]any{
				"tags":        []string{"blocks"},
				"summary":     "Prune decayed memory blocks",
				"description": "Deletes expired blocks and blocks whose validity score has fallen below `min_validity` (query param, default 0.2; non-positive or unparsable values fall back to the default). Returns how many blocks were pruned.",
				"operationId": "compactBlocks",
				"parameters": []map[string]any{
					{"name": "min_validity", "in": "query", "required": false, "schema": map[string]any{"type": "number", "default": 0.2}, "description": "Validity threshold; blocks below it are pruned"},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Compaction result",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"pruned":       map[string]any{"type": "integer"},
										"min_validity": map[string]any{"type": "number"},
									},
								},
							},
						},
					},
					"500": map[string]any{"description": "Failed to open the store or compact it"},
				},
			},
		},
	}
}
