// Package main implements an MCP (Model Context Protocol) server for gleann.
// This enables Claude Code, VS Code Copilot, and other MCP clients to search
// gleann indexes via JSON-RPC over stdio.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"

	// Register HNSW backend.
	_ "github.com/tevfik/gleann/internal/backend/hnsw"
)

const mcpVersion = "2024-11-05"

// JSON-RPC types
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP types
type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Capabilities    mcpCaps    `json:"capabilities"`
	ServerInfo      serverInfo `json:"serverInfo"`
}

type mcpCaps struct {
	Tools *toolsCap `json:"tools,omitempty"`
}

type toolsCap struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type toolDef struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	InputSchema jsonSchema `json:"inputSchema"`
}

type jsonSchema struct {
	Type       string                `json:"type"`
	Properties map[string]jsonProp   `json:"properties,omitempty"`
	Required   []string              `json:"required,omitempty"`
}

type jsonProp struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callToolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// MCPServer handles MCP protocol over stdio.
type MCPServer struct {
	config    gleann.Config
	embedder  gleann.EmbeddingComputer
	searchers map[string]*gleann.LeannSearcher
}

func main() {
	log.SetOutput(os.Stderr)
	log.Println("gleann-mcp server starting...")

	homeDir, _ := os.UserHomeDir()
	config := gleann.DefaultConfig()
	config.IndexDir = filepath.Join(homeDir, ".gleann", "indexes")

	// Override from env.
	if dir := os.Getenv("GLEANN_INDEX_DIR"); dir != "" {
		config.IndexDir = dir
	}
	if model := os.Getenv("GLEANN_MODEL"); model != "" {
		config.EmbeddingModel = model
	}
	if provider := os.Getenv("GLEANN_PROVIDER"); provider != "" {
		config.EmbeddingProvider = provider
	}

	embedder := embedding.NewComputer(embedding.Options{
		Provider: embedding.Provider(config.EmbeddingProvider),
		Model:    config.EmbeddingModel,
		BaseURL:  config.OllamaHost,
	})

	server := &MCPServer{
		config:    config,
		embedder:  embedder,
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	server.run()
}

func (s *MCPServer) run() {
	reader := bufio.NewReader(os.Stdin)
	writer := os.Stdout

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Printf("read error: %v", err)
			return
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			log.Printf("parse error: %v", err)
			continue
		}

		resp := s.handleRequest(req)
		if resp != nil {
			data, _ := json.Marshal(resp)
			data = append(data, '\n')
			writer.Write(data)
		}
	}
}

func (s *MCPServer) handleRequest(req jsonRPCRequest) *jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: initializeResult{
				ProtocolVersion: mcpVersion,
				Capabilities: mcpCaps{
					Tools: &toolsCap{},
				},
				ServerInfo: serverInfo{
					Name:    "gleann-mcp",
					Version: "1.0.0",
				},
			},
		}

	case "notifications/initialized":
		return nil // No response for notifications.

	case "tools/list":
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": s.listTools(),
			},
		}

	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.errorResponse(req.ID, -32602, "invalid params: "+err.Error())
		}
		result := s.callTool(params)
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}

	default:
		return s.errorResponse(req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *MCPServer) listTools() []toolDef {
	return []toolDef{
		{
			Name:        "leann_search",
			Description: "Search a gleann index for relevant text passages using semantic search. Returns scored results with source metadata.",
			InputSchema: jsonSchema{
				Type: "object",
				Properties: map[string]jsonProp{
					"index": {Type: "string", Description: "Name of the index to search"},
					"query": {Type: "string", Description: "Search query text"},
					"top_k": {Type: "number", Description: "Number of results to return (default: 5)"},
				},
				Required: []string{"index", "query"},
			},
		},
		{
			Name:        "leann_list",
			Description: "List all available gleann indexes with their metadata (name, backend, model, passage count).",
			InputSchema: jsonSchema{
				Type:       "object",
				Properties: map[string]jsonProp{},
			},
		},
		{
			Name:        "leann_ask",
			Description: "Ask a question about indexed data using RAG (Retrieval-Augmented Generation). Retrieves relevant context and generates an answer.",
			InputSchema: jsonSchema{
				Type: "object",
				Properties: map[string]jsonProp{
					"index":    {Type: "string", Description: "Name of the index to query"},
					"question": {Type: "string", Description: "Question to ask"},
				},
				Required: []string{"index", "question"},
			},
		},
	}
}

func (s *MCPServer) callTool(params toolCallParams) callToolResult {
	switch params.Name {
	case "leann_search":
		return s.toolSearch(params.Arguments)
	case "leann_list":
		return s.toolList()
	case "leann_ask":
		return s.toolAsk(params.Arguments)
	default:
		return callToolResult{
			Content: []contentItem{{Type: "text", Text: "Unknown tool: " + params.Name}},
			IsError: true,
		}
	}
}

func (s *MCPServer) toolSearch(args map[string]any) callToolResult {
	indexName, _ := args["index"].(string)
	query, _ := args["query"].(string)
	topK := 5
	if k, ok := args["top_k"].(float64); ok {
		topK = int(k)
	}

	if indexName == "" || query == "" {
		return callToolResult{
			Content: []contentItem{{Type: "text", Text: "index and query are required"}},
			IsError: true,
		}
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return callToolResult{
			Content: []contentItem{{Type: "text", Text: fmt.Sprintf("Error loading index %q: %v", indexName, err)}},
			IsError: true,
		}
	}

	ctx := context.Background()
	results, err := searcher.Search(ctx, query, gleann.WithTopK(topK))
	if err != nil {
		return callToolResult{
			Content: []contentItem{{Type: "text", Text: fmt.Sprintf("Search error: %v", err)}},
			IsError: true,
		}
	}

	// Format results.
	var text string
	if len(results) == 0 {
		text = "No results found."
	} else {
		for i, r := range results {
			source := ""
			if s, ok := r.Metadata["source"]; ok {
				source = fmt.Sprintf(" [%v]", s)
			}
			text += fmt.Sprintf("[%d]%s (score: %.4f)\n%s\n\n", i+1, source, r.Score, r.Text)
		}
	}

	return callToolResult{
		Content: []contentItem{{Type: "text", Text: text}},
	}
}

func (s *MCPServer) toolList() callToolResult {
	indexes, err := gleann.ListIndexes(s.config.IndexDir)
	if err != nil {
		return callToolResult{
			Content: []contentItem{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}

	if len(indexes) == 0 {
		return callToolResult{
			Content: []contentItem{{Type: "text", Text: "No indexes found."}},
		}
	}

	var text string
	for _, idx := range indexes {
		text += fmt.Sprintf("- %s: %d passages, backend=%s, model=%s\n", idx.Name, idx.NumPassages, idx.Backend, idx.EmbeddingModel)
	}

	return callToolResult{
		Content: []contentItem{{Type: "text", Text: text}},
	}
}

func (s *MCPServer) toolAsk(args map[string]any) callToolResult {
	indexName, _ := args["index"].(string)
	question, _ := args["question"].(string)

	if indexName == "" || question == "" {
		return callToolResult{
			Content: []contentItem{{Type: "text", Text: "index and question are required"}},
			IsError: true,
		}
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return callToolResult{
			Content: []contentItem{{Type: "text", Text: fmt.Sprintf("Error loading index %q: %v", indexName, err)}},
			IsError: true,
		}
	}

	chatConfig := gleann.DefaultChatConfig()
	chat := gleann.NewChat(searcher, chatConfig)

	ctx := context.Background()
	answer, err := chat.Ask(ctx, question)
	if err != nil {
		return callToolResult{
			Content: []contentItem{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
			IsError: true,
		}
	}

	return callToolResult{
		Content: []contentItem{{Type: "text", Text: answer}},
	}
}

func (s *MCPServer) getSearcher(name string) (*gleann.LeannSearcher, error) {
	if searcher, ok := s.searchers[name]; ok {
		return searcher, nil
	}

	searcher := gleann.NewSearcher(s.config, s.embedder)
	ctx := context.Background()
	if err := searcher.Load(ctx, name); err != nil {
		return nil, err
	}

	s.searchers[name] = searcher
	return searcher, nil
}

func (s *MCPServer) errorResponse(id any, code int, msg string) *jsonRPCResponse {
	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: msg},
	}
}
