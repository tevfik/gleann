package server

// openAPIPathsConfig returns the runtime configuration and the settings-
// registry endpoints (GET /api/config, POST /api/config, GET /api/settings,
// PUT /api/settings) that had no OpenAPI entry.
func openAPIPathsConfig() map[string]any {
	return map[string]any{
		"/api/config": map[string]any{
			"get": map[string]any{
				"tags":        []string{"config"},
				"summary":     "Get the running configuration",
				"description": "Returns the live configuration as JSON, with `openai_api_key` and `ollama_api_key` redacted. Keys follow the configuration structure the CLI writes to config.json. When serve was started with flags that pin certain keys, a `runtime_overrides` object carries the on-disk value of each of those keys (secrets among them redacted too).",
				"operationId": "getConfig",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Redacted running configuration",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"type": "object", "additionalProperties": true},
							},
						},
					},
					"500": map[string]any{"description": "Failed to encode the configuration"},
				},
			},
			"post": map[string]any{
				"tags":        []string{"config"},
				"summary":     "Update the running configuration",
				"description": "Merges the supplied fields over the running config (fields left out keep their value), persists the result to the config file, and applies it to the live server. The redaction sentinel that GET returns means \"keep the current secret\"; an empty string clears it. A vision-model or storage-mode change is validated and answers 400 on bad input; a JSON-decode error answers 400.",
				"operationId": "updateConfig",
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"type": "object", "additionalProperties": true},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Configuration updated and persisted", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean", "example": true}}}}}},
					"400": map[string]any{"description": "Invalid JSON or a rejected field (storage mode, vision model)"},
					"403": map[string]any{"description": "The body changes allowed_roots, index_dir, auth or cors_origins, which are read-only over the API (edit config.json or use gleann setup, then restart)"},
					"500": map[string]any{"description": "Failed to encode or persist the change"},
				},
			},
		},
		"/api/settings": map[string]any{
			"get": map[string]any{
				"tags":        []string{"config"},
				"summary":     "Describe the settings registry",
				"description": "Returns the descriptor (fields, current values, metadata) of the registered settings. Mounted only when the server is wired with a settings handler.",
				"operationId": "getSettings",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Settings descriptor",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"type": "object", "additionalProperties": true},
							},
						},
					},
					"401": map[string]any{"description": "Authorization required"},
					"500": map[string]any{"description": "Failed to load settings"},
				},
			},
			"put": map[string]any{
				"tags":        []string{"config"},
				"summary":     "Save settings",
				"description": "Persists a set of setting values. Sends an optional `revision` for optimistic concurrency: a mismatch answers 409. Invalid values answer 422; a non-JSON body answers 415; empty values or malformed JSON answer 400.",
				"operationId": "updateSettings",
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"values":   map[string]any{"type": "object", "additionalProperties": true},
									"revision": map[string]any{"type": "string", "description": "Optional base revision for optimistic concurrency"},
								},
							},
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Saved", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"saved": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "restart_required": map[string]any{"type": "boolean"}, "revision": map[string]any{"type": "string"}}}}}},
					"400": map[string]any{"description": "Empty values or malformed JSON"},
					"401": map[string]any{"description": "Authorization required"},
					"409": map[string]any{"description": "Revision conflict"},
					"415": map[string]any{"description": "Content-Type is not application/json"},
					"422": map[string]any{"description": "One or more settings are invalid"},
					"500": map[string]any{"description": "Failed to load or save settings"},
				},
			},
		},
	}
}
