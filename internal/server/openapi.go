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
//
// The paths and schemas are assembled from domain-specific builders
// defined in openapi_paths.go and openapi_schemas.go.
func (s *Server) openAPISpec() map[string]any {
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Gleann API",
			"description": "Unified vector + graph search engine. Index, search, and query code and documents with DiskANN, HNSW, FAISS, BM25, and KuzuDB graph.",
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
			{"name": "unified-memory", "description": "Unified Memory API — single interface orchestrating blocks, graph, and vector search"},
			{"name": "blocks", "description": "Memory Blocks — hierarchical BBolt storage (short/medium/long tiers) providing infinite persistent memory for LLMs"},
			{"name": "a2a", "description": "A2A Protocol — Google Agent-to-Agent discovery and communication"},
			{"name": "tasks", "description": "Background task management"},
			{"name": "webhooks", "description": "Webhook notification management"},
			{"name": "metrics", "description": "Prometheus-compatible metrics"},
			{"name": "proxy", "description": "OpenAI-compatible RAG proxy (model: \"gleann/<index>\")"},
			{"name": "packs", "description": "Knowledge Packs — domain-specific readonly datasets (crops, pests, varieties, …) served as versioned YAML bundles"},
		},
		"paths":      s.openAPIPaths(),
		"components": map[string]any{"schemas": openAPISchemas()},
	}
}

// openAPIPaths merges all domain-specific path groups into a single paths map.
func (s *Server) openAPIPaths() map[string]any {
	paths := make(map[string]any)
	groups := []map[string]any{
		openAPIPathsHealth(),
		openAPIPathsIndexes(),
		openAPIPathsSearch(),
		openAPIPathsGraph(),
		openAPIPathsMemory(),
		openAPIPathsBlocks(),
		openAPIPathsWebhooks(),
		openAPIPathsMetrics(),
		openAPIPathsConversations(),
		openAPIPathsProxy(),
		openAPIPathsA2A(),
		openAPIPathsUnifiedMemory(),
		openAPIPathsTasks(),
		openAPIPathsPacks(),
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
