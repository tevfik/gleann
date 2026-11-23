package server

// openAPIPathsPlugins returns the plugin catalog and plugin-runtime endpoints
// (list, install, uninstall, proxy settings and models) that had no OpenAPI
// entry.
func openAPIPathsPlugins() map[string]any {
	nameParam := paramName()
	pluginProxyResponses := func() map[string]any {
		return map[string]any{
			"200": map[string]any{"description": "Passthrough of the plugin's JSON response"},
			"400": map[string]any{"description": "Invalid plugin name"},
			"404": map[string]any{"description": "Plugin not installed"},
			"500": map[string]any{"description": "Registry or manager lookup failed"},
			"501": map[string]any{"description": "The plugin exposes no such web endpoint"},
			"502": map[string]any{"description": "The plugin is not running, or the request to it failed"},
		}
	}
	return map[string]any{
		"/api/plugins": map[string]any{
			"get": map[string]any{
				"tags":        []string{"plugins"},
				"summary":     "List the plugin catalog",
				"description": "Returns the catalog entries (from plugin_catalog.json) annotated with each plugin's install/run status.",
				"operationId": "listPlugins",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Catalog entries with status",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"plugins": map[string]any{"type": "array", "items": map[string]any{
											"type": "object",
											"properties": map[string]any{
												"name":                map[string]any{"type": "string"},
												"icon":                map[string]any{"type": "string"},
												"description":         map[string]any{"type": "string"},
												"repo_url":            map[string]any{"type": "string"},
												"language":            map[string]any{"type": "string"},
												"extensions":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
												"has_settings":        map[string]any{"type": "boolean"},
												"settings_cmd":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
												"requires_markitdown": map[string]any{"type": "boolean"},
												"version":             map[string]any{"type": "string", "description": "Latest version available"},
												"status":              map[string]any{"type": "string", "enum": []string{"not_installed", "installed", "running"}},
											},
										}},
									},
								},
							},
						},
					},
				},
			},
		},
		"/api/plugins/{name}/install": map[string]any{
			"post": map[string]any{
				"tags":        []string{"plugins"},
				"summary":     "Install a plugin (background task)",
				"description": "Queues a background `gleann plugin install <name>` task and returns immediately.",
				"operationId": "installPlugin",
				"parameters":  []map[string]any{nameParam},
				"responses": map[string]any{
					"202": map[string]any{"description": "Install queued", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}}}}}},
					"400": map[string]any{"description": "Invalid plugin name"},
					"404": map[string]any{"description": "Plugin not found in the catalog"},
				},
			},
		},
		"/api/plugins/{name}": map[string]any{
			"delete": map[string]any{
				"tags":        []string{"plugins"},
				"summary":     "Uninstall a plugin",
				"description": "Stops the plugin's sidecar (if running) and removes it.",
				"operationId": "uninstallPlugin",
				"parameters":  []map[string]any{nameParam},
				"responses": map[string]any{
					"200": map[string]any{"description": "Uninstalled", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}, "result": map[string]any{"type": "string"}}}}}},
					"400": map[string]any{"description": "Invalid plugin name"},
					"500": map[string]any{"description": "Uninstall failed"},
				},
			},
		},
		"/api/plugins/{name}/settings": map[string]any{
			"get": map[string]any{
				"tags":        []string{"plugins"},
				"summary":     "Get a plugin's settings",
				"description": "Proxies GET /settings to the plugin's sidecar. Answers 501 when the plugin has settings but they are not web-editable, and 502 when the sidecar is not running or the call fails.",
				"operationId": "getPluginSettings",
				"parameters":  []map[string]any{nameParam},
				"responses":   pluginProxyResponses(),
			},
			"put": map[string]any{
				"tags":        []string{"plugins"},
				"summary":     "Save a plugin's settings",
				"description": "Forwards the JSON body (as-is, including a `values` envelope if the client sends one) to the plugin's sidecar PUT /settings.",
				"operationId": "putPluginSettings",
				"parameters":  []map[string]any{nameParam},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"type": "object", "additionalProperties": true},
						},
					},
				},
				"responses": pluginProxyResponses(),
			},
		},
		"/api/plugins/{name}/models": map[string]any{
			"get": map[string]any{
				"tags":        []string{"plugins"},
				"summary":     "List a plugin's models",
				"description": "Proxies GET /models to the plugin's sidecar. Answers 501 when the plugin offers no model downloads.",
				"operationId": "getPluginModels",
				"parameters":  []map[string]any{nameParam},
				"responses":   pluginProxyResponses(),
			},
			"post": map[string]any{
				"tags":        []string{"plugins"},
				"summary":     "Request a plugin model download",
				"description": "Forwards the JSON body (the model id) to the plugin's sidecar POST /models. The plugin's own status codes are passed through (404 for an unknown id, 400 for a bad body).",
				"operationId": "postPluginModels",
				"parameters":  []map[string]any{nameParam},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"type": "object", "additionalProperties": true},
						},
					},
				},
				"responses": pluginProxyResponses(),
			},
		},
	}
}
