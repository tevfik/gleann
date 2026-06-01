package server

// openAPISchemasSearch returns search-related component schemas.
func openAPISchemasSearch() map[string]any {
	return map[string]any{
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
	}
}

// openAPISchemasIndex returns index-building component schemas.
func openAPISchemasIndex() map[string]any {
	return map[string]any{
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
				"backend": map[string]any{
					"type":        "string",
					"enum":        []string{"diskann", "hnsw", "faiss", "faiss-hybrid"},
					"description": "Vector search backend (default: diskann)",
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
	}
}

// openAPISchemasGraph returns graph-related component schemas.
func openAPISchemasGraph() map[string]any {
	return map[string]any{
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
	}
}

// openAPISchemasMemory returns Memory Engine component schemas.
func openAPISchemasMemory() map[string]any {
	return map[string]any{
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
	}
}

// openAPISchemasBlocks returns Memory Block component schemas.
func openAPISchemasBlocks() map[string]any {
	return map[string]any{
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
	}
}

// openAPISchemasConversations returns conversation-related component schemas.
func openAPISchemasConversations() map[string]any {
	return map[string]any{
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
	}
}

// openAPISchemasWebhooks returns webhook-related component schemas.
func openAPISchemasWebhooks() map[string]any {
	return map[string]any{
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
	}
}

// openAPISchemasProxy returns OpenAI-compatible proxy component schemas.
func openAPISchemasProxy() map[string]any {
	return map[string]any{
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
	}
}

// openAPISchemasErrors returns error-related component schemas.
func openAPISchemasErrors() map[string]any {
	return map[string]any{
		"Error": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"error": map[string]any{"type": "string"},
			},
		},
		"ErrorResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"error": map[string]any{"type": "string", "description": "Human-readable error message"},
			},
		},
		"RateLimitError": map[string]any{
			"type":        "object",
			"description": "Returned when a client exceeds the per-IP rate limit (token bucket: default 60 req/s sustained, 120 burst). Configure via GLEANN_RATE_LIMIT / GLEANN_RATE_BURST env vars. The Retry-After header indicates how many seconds to wait.",
			"properties": map[string]any{
				"error": map[string]any{"type": "string", "example": "rate limit exceeded — slow down"},
			},
		},
		"TimeoutError": map[string]any{
			"type":        "object",
			"description": "Returned when a request exceeds the per-endpoint context deadline. Configure via GLEANN_TIMEOUT_ASK_S, GLEANN_TIMEOUT_SEARCH_S, GLEANN_TIMEOUT_BUILD_S, GLEANN_TIMEOUT_DEFAULT_S env vars. SSE streams bypass the timeout.",
			"properties": map[string]any{
				"error": map[string]any{"type": "string", "example": "request timed out — try a shorter query or increase GLEANN_TIMEOUT_*_S"},
			},
		},
	}
}

// openAPISchemasA2A returns A2A Protocol component schemas.
func openAPISchemasA2A() map[string]any {
	return map[string]any{
		"AgentCard": map[string]any{
			"type":        "object",
			"description": "A2A Agent Card — self-describing manifest published at /.well-known/agent-card.json for agent discovery.",
			"properties": map[string]any{
				"name":                map[string]any{"type": "string"},
				"description":         map[string]any{"type": "string"},
				"version":             map[string]any{"type": "string"},
				"documentationUrl":    map[string]any{"type": "string"},
				"iconUrl":             map[string]any{"type": "string"},
				"defaultInputModes":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"defaultOutputModes":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"supportedInterfaces": map[string]any{"type": "array", "items": refSchema("AgentInterface")},
				"capabilities":        refSchema("AgentCapabilities"),
				"skills":              map[string]any{"type": "array", "items": refSchema("AgentSkill")},
			},
		},
		"AgentInterface": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":             map[string]any{"type": "string"},
				"protocolBinding": map[string]any{"type": "string"},
				"protocolVersion": map[string]any{"type": "string"},
			},
		},
		"AgentCapabilities": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"streaming":         map[string]any{"type": "boolean"},
				"pushNotifications": map[string]any{"type": "boolean"},
			},
		},
		"AgentSkill": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          map[string]any{"type": "string"},
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"examples":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"inputModes":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"outputModes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
		"A2ASendMessageRequest": map[string]any{
			"type":     "object",
			"required": []string{"message"},
			"properties": map[string]any{
				"message":  refSchema("A2AMessage"),
				"metadata": map[string]any{"type": "object", "additionalProperties": true, "description": "Optional metadata. Set 'skill' key to target a specific skill by ID."},
			},
		},
		"A2AMessage": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"messageId": map[string]any{"type": "string"},
				"role":      map[string]any{"type": "string", "enum": []string{"ROLE_USER", "ROLE_AGENT"}},
				"parts":     map[string]any{"type": "array", "items": refSchema("A2APart")},
			},
		},
		"A2APart": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":      map[string]any{"type": "string"},
				"mediaType": map[string]any{"type": "string"},
			},
		},
		"A2ATask": map[string]any{
			"type":        "object",
			"description": "A2A Task — the core unit of work in the A2A protocol.",
			"properties": map[string]any{
				"id":        map[string]any{"type": "string"},
				"contextId": map[string]any{"type": "string"},
				"status": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"state":     map[string]any{"type": "string", "enum": []string{"TASK_STATE_SUBMITTED", "TASK_STATE_WORKING", "TASK_STATE_COMPLETED", "TASK_STATE_FAILED", "TASK_STATE_CANCELED", "TASK_STATE_INPUT_REQUIRED"}},
						"message":   refSchema("A2AMessage"),
						"timestamp": map[string]any{"type": "string"},
					},
				},
				"artifacts": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"artifactId": map[string]any{"type": "string"},
							"name":       map[string]any{"type": "string"},
							"parts":      map[string]any{"type": "array", "items": refSchema("A2APart")},
						},
					},
				},
				"history": map[string]any{"type": "array", "items": refSchema("A2AMessage")},
			},
		},
	}
}

// openAPISchemasUnifiedMemory returns Unified Memory component schemas.
func openAPISchemasUnifiedMemory() map[string]any {
	return map[string]any{
		"UnifiedIngestRequest": map[string]any{
			"type":        "object",
			"description": "Ingest facts and relationships into unified memory.",
			"properties": map[string]any{
				"facts": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"content"},
						"properties": map[string]any{
							"content":    map[string]any{"type": "string", "description": "The knowledge to store"},
							"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							"label":      map[string]any{"type": "string", "description": "Short label (auto-truncated from content if omitted)"},
							"tier":       map[string]any{"type": "string", "enum": []string{"short", "medium", "long"}, "default": "short"},
							"metadata":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Arbitrary key-value metadata"},
							"expires_in": map[string]any{"type": "string", "description": "TTL as Go duration (e.g. \"24h\", \"7d\")"},
							"char_limit": map[string]any{"type": "integer", "description": "Per-block character limit (0 = unlimited)"},
						},
					},
				},
				"relationships": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"from", "to", "relation"},
						"properties": map[string]any{
							"from":       map[string]any{"type": "string", "description": "Source entity"},
							"to":         map[string]any{"type": "string", "description": "Target entity"},
							"relation":   map[string]any{"type": "string", "description": "Edge type (e.g. DEPENDS_ON, IMPLEMENTS)"},
							"weight":     map[string]any{"type": "number"},
							"index":      map[string]any{"type": "string", "description": "Target index (default: first available)"},
							"attributes": map[string]any{"type": "object", "additionalProperties": true, "description": "Edge metadata"},
						},
					},
				},
				"scope":   map[string]any{"type": "string", "description": "Isolate facts to a conversation/agent scope (default: global)"},
				"project": map[string]any{"type": "string", "description": "Project shorthand: sets scope to 'project:{name}' and defaults relationship index to this name"},
			},
		},
		"UnifiedIngestResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"facts_stored":  map[string]any{"type": "integer"},
				"edges_created": map[string]any{"type": "integer"},
				"fact_ids":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"errors":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
		"UnifiedRecallRequest": map[string]any{
			"type":     "object",
			"required": []string{"query"},
			"properties": map[string]any{
				"query":     map[string]any{"type": "string", "description": "Natural-language recall query"},
				"scope":     map[string]any{"type": "string", "description": "Restrict block search to scope"},
				"index":     map[string]any{"type": "string", "description": "Index for vector + graph search"},
				"layers":    map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"blocks", "graph", "vector"}}, "description": "Memory layers to query (default: all)"},
				"top_k":     map[string]any{"type": "integer", "default": 5, "description": "Max results per layer"},
				"depth":     map[string]any{"type": "integer", "default": 2, "description": "Graph traversal depth"},
				"format":    map[string]any{"type": "string", "enum": []string{"json", "context"}, "default": "json", "description": "Output format (context = pre-formatted for LLM injection)"},
				"tier":      map[string]any{"type": "string", "enum": []string{"short", "medium", "long"}, "description": "Filter blocks by tier"},
				"tags":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter blocks by tags (AND logic)"},
				"after":     map[string]any{"type": "string", "description": "Filter blocks created after (RFC3339 or Go duration like \"24h\")"},
				"before":    map[string]any{"type": "string", "description": "Filter blocks created before (RFC3339 or Go duration like \"7d\")"},
				"relations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter graph edges by relation types"},
				"project":   map[string]any{"type": "string", "description": "Project shorthand: sets scope to 'project:{name}' and index to matching name"},
			},
		},
		"UnifiedRecallResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":   map[string]any{"type": "string"},
				"blocks":  map[string]any{"type": "array", "items": refSchema("RecallBlock")},
				"graph":   refSchema("RecallGraph"),
				"vector":  map[string]any{"type": "array", "items": refSchema("RecallHit")},
				"context": map[string]any{"type": "string", "description": "Pre-formatted context for LLM injection (when format=context)"},
			},
		},
		"RecallBlock": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":         map[string]any{"type": "string"},
				"tier":       map[string]any{"type": "string"},
				"label":      map[string]any{"type": "string"},
				"content":    map[string]any{"type": "string"},
				"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"scope":      map[string]any{"type": "string"},
				"metadata":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"created_at": map[string]any{"type": "string", "format": "date-time"},
				"source":     map[string]any{"type": "string"},
			},
		},
		"RecallGraph": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"nodes": map[string]any{"type": "array", "items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":      map[string]any{"type": "string"},
						"type":    map[string]any{"type": "string"},
						"content": map[string]any{"type": "string"},
					},
				}},
				"edges": map[string]any{"type": "array", "items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"from":     map[string]any{"type": "string"},
						"to":       map[string]any{"type": "string"},
						"relation": map[string]any{"type": "string"},
						"weight":   map[string]any{"type": "number"},
					},
				}},
			},
		},
		"RecallHit": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content":  map[string]any{"type": "string"},
				"source":   map[string]any{"type": "string"},
				"score":    map[string]any{"type": "number"},
				"chunk_id": map[string]any{"type": "integer"},
			},
		},
	}
}

// openAPISchemasTasks returns background task component schemas.
func openAPISchemasTasks() map[string]any {
	return map[string]any{
		"BackgroundTask": map[string]any{
			"type":        "object",
			"description": "Background task status and metadata.",
			"properties": map[string]any{
				"id":         map[string]any{"type": "string"},
				"type":       map[string]any{"type": "string", "enum": []string{"SleepTimeCompute", "AutoIndex", "MemoryConsolidate", "HealthCheck", "ReIndex", "Custom"}},
				"status":     map[string]any{"type": "string", "enum": []string{"queued", "running", "completed", "failed", "cancelled"}},
				"progress":   map[string]any{"type": "number", "description": "Completion percentage 0.0 – 1.0"},
				"message":    map[string]any{"type": "string", "description": "Human-readable status message"},
				"error":      map[string]any{"type": "string"},
				"created_at": map[string]any{"type": "string", "format": "date-time"},
				"started_at": map[string]any{"type": "string", "format": "date-time"},
				"ended_at":   map[string]any{"type": "string", "format": "date-time"},
			},
		},
	}
}

// openAPISchemasPacks returns knowledge pack component schemas.
func openAPISchemasPacks() map[string]any {
	return map[string]any{
		"PackManifest": map[string]any{
			"type":        "object",
			"description": "Knowledge pack manifest as declared in pack.yaml.",
			"properties": map[string]any{
				"id":             map[string]any{"type": "string", "description": "Unique pack identifier (e.g. `crops-tr`)"},
				"version":        map[string]any{"type": "string", "description": "Semantic version string"},
				"schema_version": map[string]any{"type": "integer", "description": "Pack schema version (bumped on breaking manifest changes)"},
				"locale":         map[string]any{"type": "string", "description": "BCP 47 language tag (e.g. `tr`)"},
				"title":          map[string]any{"type": "string", "description": "Human-readable display name"},
				"description":    map[string]any{"type": "string"},
				"tier":           map[string]any{"type": "string", "enum": []string{"free", "premium"}, "description": "Access tier"},
				"content_files":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "YAML data file names relative to pack directory"},
				"etag":           map[string]any{"type": "string", "description": "SHA-256[:12] of manifest + all content bytes — changes whenever the pack changes"},
				"search": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"fields":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Item fields used for substring search"},
						"semantic": map[string]any{"type": "boolean", "description": "Whether semantic (vector) search is available for this pack"},
					},
				},
				"app_hints": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"app":       map[string]any{"type": "string"},
							"required":  map[string]any{"type": "boolean"},
							"auto_load": map[string]any{"type": "boolean"},
						},
					},
					"description": "Per-app metadata hints (required, auto_load, etc.)",
				},
			},
		},
	}
}
