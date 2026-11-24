// Package server — OpenAPI 3.0 specification for the gleann REST API.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tevfik/gleann/pkg/gleann"
)

// openAPISpec returns the OpenAPI 3.0 specification as a Go map.
// Keeping it as a map rather than a raw string allows programmatic
// manipulation (e.g. injecting the actual server version at runtime).
//
// The paths and schemas are assembled from domain-specific builders
// defined in openapi_paths.go and openapi_schemas.go.
func (s *Server) openAPISpec() map[string]any {
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Gleann API",
			"description": "Unified vector + graph search engine. Index, search, and query code and documents with DiskANN, HNSW, FAISS, BM25, and a SQLite code graph.",
			"version":     s.version,
			"license": map[string]any{
				"name": "MIT",
				"url":  "https://github.com/tevfik/gleann/blob/main/LICENSE",
			},
		},
		"servers": []map[string]any{
			{"url": gleann.AdvertisedBaseURL(s.addr), "description": "Local development server"},
		},
		"tags": []map[string]any{
			{"name": "health", "description": "Health check"},
			{"name": "indexes", "description": "Index management"},
			{"name": "search", "description": "Semantic and hybrid search"},
			{"name": "graph", "description": "Code graph queries (the SQLite graph of format-2 indexes, every build)"},
			{"name": "memory", "description": "Memory Engine — generic Entity/RELATES_TO knowledge graph for external AI agents"},
			{"name": "unified-memory", "description": "Unified Memory API — single interface orchestrating blocks, graph, and vector search"},
			{"name": "blocks", "description": "Memory Blocks — hierarchical BBolt storage (short/medium/long tiers) providing infinite persistent memory for LLMs"},
			{"name": "a2a", "description": "A2A Protocol — Google Agent-to-Agent discovery and communication"},
			{"name": "tasks", "description": "Background task management"},
			{"name": "webhooks", "description": "Webhook notification management"},
			{"name": "metrics", "description": "Prometheus-compatible metrics"},
			{"name": "proxy", "description": "OpenAI-compatible RAG proxy (model: \"gleann/<index>\")"},
			{"name": "conversations", "description": "Saved chat conversations (list, get, update title, delete)"},
			{"name": "config", "description": "Server configuration (redacted GET) and the editable settings registry (GET/PUT /api/settings)"},
			{"name": "plugins", "description": "Language-plugin catalog: list, install, uninstall, settings, and plugin-provided models"},
			{"name": "models", "description": "Local GGUF models and the model catalog (list, download, catalog state)"},
			{"name": "system", "description": "Server control and observability: logs, proxy stop, and proxy provider model discovery"},
			{"name": "packs", "description": "Knowledge Packs — domain-specific readonly datasets (crops, pests, varieties, …) served as versioned YAML bundles"},
		},
		"paths": withAccessResponses(s.openAPIPaths()),
		// Bearer auth is declared once here so importing /api/openapi.json into
		// Postman surfaces a single security scheme that every operation
		// inherits. The four public routes (health, the agent card, the Swagger
		// UI and the spec itself) override it with an empty list;
		// TestSpecSecurityMatchesPublicRoutes checks the overrides against the
		// routes security.go serves without a token.
		"security": []map[string]any{{"bearerAuth": []string{}}},
		"components": map[string]any{
			"schemas": openAPISchemas(),
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{
					"type":   "http",
					"scheme": "bearer",
				},
			},
		},
	}
}

// openAPIPaths merges all domain-specific path groups into a single paths map.
func (s *Server) openAPIPaths() map[string]any {
	paths := make(map[string]any)
	groups := []map[string]any{
		openAPIPathsHealth(),
		openAPIPathsIndexes(),
		openAPIPathsSearch(),
		openAPIPathsMemory(),
		openAPIPathsGraph(),
		openAPIPathsConversations(),
		openAPIPathsBlocks(),
		openAPIPathsBlockCompact(),
		openAPIPathsWebhooks(),
		openAPIPathsMetrics(),
		openAPIPathsProxy(),
		openAPIPathsA2A(),
		openAPIPathsUnifiedMemory(),
		openAPIPathsTasks(),
		openAPIPathsPacks(),
		openAPIPathsSystem(),
		openAPIPathsConfig(),
		openAPIPathsPlugins(),
		openAPIPathsModels(),
	}
	for _, g := range groups {
		for k, v := range g {
			paths[k] = v
		}
	}
	return paths
}

// openAPISchemas merges all domain-specific schema groups into a single schemas map.
func openAPISchemas() map[string]any {
	schemas := make(map[string]any)
	groups := []map[string]any{
		openAPISchemasSearch(),
		openAPISchemasIndex(),
		openAPISchemasGraph(),
		openAPISchemasMemory(),
		openAPISchemasBlocks(),
		openAPISchemasConversations(),
		openAPISchemasWebhooks(),
		openAPISchemasProxy(),
		openAPISchemasErrors(),
		openAPISchemasA2A(),
		openAPISchemasUnifiedMemory(),
		openAPISchemasTasks(),
		openAPISchemasPacks(),
	}
	for _, g := range groups {
		for k, v := range g {
			schemas[k] = v
		}
	}
	return schemas
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

// paramListLimit returns the ?limit= query parameter of the passage/section
// listing routes (default gleann.DefaultPassageListLimit, at most
// gleann.MaxPassageListLimit), so the spec cannot drift from the validation.
func paramListLimit() map[string]any {
	return map[string]any{
		"name":        "limit",
		"in":          "query",
		"required":    false,
		"description": fmt.Sprintf("Page size (default %d, maximum %d)", gleann.DefaultPassageListLimit, gleann.MaxPassageListLimit),
		"schema": map[string]any{
			"type":    "integer",
			"minimum": 1,
			"maximum": gleann.MaxPassageListLimit,
			"default": gleann.DefaultPassageListLimit,
		},
	}
}

// paramListOffset returns the ?offset= query parameter of the listing routes.
func paramListOffset() map[string]any {
	return map[string]any{
		"name":        "offset",
		"in":          "query",
		"required":    false,
		"description": "Rows to skip before the page (default 0)",
		"schema":      map[string]any{"type": "integer", "minimum": 0, "default": 0},
	}
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

// accessRestrictedOps are the operations that answer 403 for a client the
// GLEANN_TAGS isolation or the X-Gleann-Target pin keeps away from an index.
// GLEANN_TAGS restricts every index route, reads and writes; a missing index
// looks like a forbidden one to a restricted client.
var accessRestrictedOps = map[string][]string{
	"/api/indexes/{name}":            {"get", "delete", "patch"},
	"/api/indexes/{name}/passages":   {"get"},
	"/api/indexes/{name}/sections":   {"get"},
	"/api/indexes/{name}/search":     {"post"},
	"/api/indexes/{name}/ask":        {"post"},
	"/api/indexes/{name}/build":      {"post"},
	"/api/indexes/{name}/update":     {"post"},
	"/api/indexes/{name}/index-path": {"post"},
	"/api/indexes/{name}/watch":      {"post"},
	"/api/indexes/{name}/upload":     {"post"},
	"/api/indexes/{name}/storage":    {"post"},
	"/api/graph/{name}":              {"get"},
	"/api/graph/{name}/toc":          {"get"},
	"/api/graph/{name}/documents":    {"get"},
	"/api/graph/{name}/query":        {"post"},
	"/api/graph/{name}/index":        {"post"},
	"/api/memory/{name}/inject":      {"post"},
	"/api/memory/{name}/nodes/{id}":  {"delete"},
	"/api/memory/{name}/edges":       {"delete"},
	"/api/memory/{name}/traverse":    {"post"},
	"/api/search":                    {"post"},
	"/v1/chat/completions":           {"post"},
}

const accessDeniedDescription = "Access denied: client target or GLEANN_TAGS isolation. With GLEANN_TAGS set, an index that does not exist (or whose metadata cannot be read) answers 403 as well, so a restricted client cannot probe index names"

// withAccessResponses documents the 403 of every access-restricted operation
// (and the 404 /api/search gives an unrestricted client for a missing index).
func withAccessResponses(paths map[string]any) map[string]any {
	for path, methods := range accessRestrictedOps {
		item, _ := paths[path].(map[string]any)
		for _, m := range methods {
			op, _ := item[m].(map[string]any)
			resp, _ := op["responses"].(map[string]any)
			if resp == nil {
				continue
			}
			if _, ok := resp["403"]; !ok {
				resp["403"] = map[string]any{"description": accessDeniedDescription}
			}
		}
	}
	if item, _ := paths["/api/search"].(map[string]any); item != nil {
		if op, _ := item["post"].(map[string]any); op != nil {
			if resp, _ := op["responses"].(map[string]any); resp != nil {
				if _, ok := resp["404"]; !ok {
					resp["404"] = map[string]any{"description": "A named index does not exist (unrestricted clients)"}
				}
			}
		}
	}
	return paths
}
