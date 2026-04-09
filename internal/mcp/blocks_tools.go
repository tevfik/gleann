// Package mcp — Memory Block MCP tool definitions.
//
// These tools expose gleann's hierarchical BBolt memory (pkg/memory) to
// external AI agents via the Model Context Protocol.  Unlike the KuzuDB
// Knowledge Graph tools, these operate on simple text blocks organized into
// short / medium / long tiers — providing infinite persistent memory for LLMs.
//
// Tools registered (no build tag — pure Go):
//
//   - memory_remember  — store a fact in long-term memory
//   - memory_forget    — remove a block by ID or content match
//   - memory_search    — full-text search across all tiers
//   - memory_list      — list blocks with optional tier filter
//   - memory_context   — get the compiled <memory_context> window for LLM injection
package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/memory"
)

// ── Shared memory manager ─────────────────────────────────────────────────────

// blockMemPool is a lazy, process-scoped cache for the BBolt Manager.
// BBolt allows exactly one open handle per file; this ensures we reuse it.
type blockMemPool struct {
	mu  sync.Mutex
	mgr *memory.Manager
}

func (p *blockMemPool) get() (*memory.Manager, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.mgr != nil {
		return p.mgr, nil
	}

	mgr, err := memory.DefaultManager()
	if err != nil {
		return nil, fmt.Errorf("open block memory: %w", err)
	}
	p.mgr = mgr
	return mgr, nil
}

func (p *blockMemPool) close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.mgr != nil {
		_ = p.mgr.Close()
		p.mgr = nil
	}
}

// ── Tool: memory_remember ─────────────────────────────────────────────────────

func (s *Server) buildMemoryRememberTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name: "memory_remember",
		Description: "Store an important fact, preference, or piece of knowledge in gleann's " +
			"long-term persistent memory. Stored facts survive across sessions and are " +
			"automatically injected into future LLM context windows. Use this to give the " +
			"LLM infinite, persistent memory.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"content": map[string]interface{}{
					"type":        "string",
					"description": "The fact, preference, or knowledge to remember",
				},
				"tier": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"short", "medium", "long"},
					"default":     "long",
					"description": "Memory tier: short (session), medium (days), long (permanent)",
				},
				"label": map[string]interface{}{
					"type":        "string",
					"description": "Semantic label for the memory (e.g. 'user_preference', 'project_fact')",
				},
				"tags": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Optional tags for categorization and search",
				},
				"char_limit": map[string]interface{}{
					"type":        "integer",
					"description": "Max characters for this block's content (0 = unlimited, default uses server setting)",
				},
				"scope": map[string]interface{}{
					"type":        "string",
					"description": "Isolate this block to a specific scope (e.g. conversation ID). Empty = global.",
				},
			},
			Required: []string{"content"},
		},
	}
}

func (s *Server) handleMemoryRemember(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcpsdk.NewToolResultError("invalid arguments"), nil
	}

	content, _ := args["content"].(string)
	if content == "" {
		return mcpsdk.NewToolResultError("content is required"), nil
	}

	tierStr, _ := args["tier"].(string)
	if tierStr == "" {
		tierStr = "long"
	}
	tier, err := memory.ParseTier(tierStr)
	if err != nil {
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	label, _ := args["label"].(string)
	if label == "" {
		label = "agent_memory"
	}

	var tags []string
	if raw, ok := args["tags"]; ok && raw != nil {
		if rawSlice, ok := raw.([]interface{}); ok {
			for _, t := range rawSlice {
				if str, ok := t.(string); ok {
					tags = append(tags, str)
				}
			}
		}
	}

	mgr, err := s.blockMem.get()
	if err != nil {
		return mcpsdk.NewToolResultError("open memory store: " + err.Error()), nil
	}

	charLimit := 0
	if raw, ok := args["char_limit"]; ok && raw != nil {
		if v, ok := raw.(float64); ok {
			charLimit = int(v)
		}
	}

	scope, _ := args["scope"].(string)

	block := &memory.Block{
		Tier:      tier,
		Label:     label,
		Content:   content,
		Source:    "mcp_agent",
		Tags:      tags,
		CharLimit: charLimit,
		Scope:     scope,
	}
	if err := mgr.Store().Add(block); err != nil {
		return mcpsdk.NewToolResultError("remember failed: " + err.Error()), nil
	}

	return mcpsdk.NewToolResultText(fmt.Sprintf(
		"Remembered (ID: %s, tier: %s): %s", block.ID, tier, content,
	)), nil
}

// ── Tool: memory_forget ───────────────────────────────────────────────────────

func (s *Server) buildMemoryForgetTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name:        "memory_forget",
		Description: "Remove a memory block by its ID, or delete all blocks whose content matches the given query string.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"id_or_query": map[string]interface{}{
					"type":        "string",
					"description": "Block ID to delete exactly, or a content snippet to match against (first exact ID match wins)",
				},
			},
			Required: []string{"id_or_query"},
		},
	}
}

func (s *Server) handleMemoryForget(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcpsdk.NewToolResultError("invalid arguments"), nil
	}

	idOrQuery, _ := args["id_or_query"].(string)
	if idOrQuery == "" {
		return mcpsdk.NewToolResultError("id_or_query is required"), nil
	}

	mgr, err := s.blockMem.get()
	if err != nil {
		return mcpsdk.NewToolResultError("open memory store: " + err.Error()), nil
	}

	n, err := mgr.Forget(idOrQuery)
	if err != nil {
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	return mcpsdk.NewToolResultText(fmt.Sprintf("Forgot %d block(s) matching %q.", n, idOrQuery)), nil
}

// ── Tool: memory_search ───────────────────────────────────────────────────────

func (s *Server) buildMemorySearchTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name:        "memory_search",
		Description: "Full-text search across all memory tiers. Returns matching blocks with their IDs, tiers, and content. Use this to check what gleann currently remembers about a topic.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query — matches against content, label, and tags",
				},
			},
			Required: []string{"query"},
		},
	}
}

func (s *Server) handleMemorySearch(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcpsdk.NewToolResultError("invalid arguments"), nil
	}

	query, _ := args["query"].(string)
	if query == "" {
		return mcpsdk.NewToolResultError("query is required"), nil
	}

	mgr, err := s.blockMem.get()
	if err != nil {
		return mcpsdk.NewToolResultError("open memory store: " + err.Error()), nil
	}

	blocks, err := mgr.Search(query)
	if err != nil {
		return mcpsdk.NewToolResultError("search failed: " + err.Error()), nil
	}

	if len(blocks) == 0 {
		return mcpsdk.NewToolResultText(fmt.Sprintf("No memories found for %q.", query)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d memory block(s) for %q:\n\n", len(blocks), query)
	for i, b := range blocks {
		fmt.Fprintf(&sb, "%d. [%s] %s (ID: %s)\n", i+1, b.Tier, b.Content, b.ID)
		if len(b.Tags) > 0 {
			fmt.Fprintf(&sb, "   Tags: %s\n", strings.Join(b.Tags, ", "))
		}
	}
	return mcpsdk.NewToolResultText(sb.String()), nil
}

// ── Tool: memory_list ─────────────────────────────────────────────────────────

func (s *Server) buildMemoryListTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name:        "memory_list",
		Description: "List all memory blocks, optionally filtered by tier. Returns structured data about each block including ID, tier, label, content, tags, and timestamps.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"tier": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"short", "medium", "long", ""},
					"description": "Filter by tier (omit to list all tiers)",
				},
			},
		},
	}
}

func (s *Server) handleMemoryList(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)

	var tier memory.Tier
	if args != nil {
		if tierStr, _ := args["tier"].(string); tierStr != "" {
			t, err := memory.ParseTier(tierStr)
			if err != nil {
				return mcpsdk.NewToolResultError(err.Error()), nil
			}
			tier = t
		}
	}

	mgr, err := s.blockMem.get()
	if err != nil {
		return mcpsdk.NewToolResultError("open memory store: " + err.Error()), nil
	}

	blocks, err := mgr.List(tier)
	if err != nil {
		return mcpsdk.NewToolResultError("list failed: " + err.Error()), nil
	}

	if len(blocks) == 0 {
		label := "all tiers"
		if tier != "" {
			label = string(tier) + "-term"
		}
		return mcpsdk.NewToolResultText("No memory blocks found in " + label + "."), nil
	}

	var sb strings.Builder
	label := "all tiers"
	if tier != "" {
		label = string(tier) + "-term tier"
	}
	fmt.Fprintf(&sb, "%d memory block(s) in %s:\n\n", len(blocks), label)
	for i, b := range blocks {
		fmt.Fprintf(&sb, "%d. [%s/%s] %s\n   ID: %s\n", i+1, b.Tier, b.Label, b.Content, b.ID)
		if len(b.Tags) > 0 {
			fmt.Fprintf(&sb, "   Tags: %s\n", strings.Join(b.Tags, ", "))
		}
	}
	return mcpsdk.NewToolResultText(sb.String()), nil
}

// ── Tool: memory_context ──────────────────────────────────────────────────────

func (s *Server) buildMemoryContextTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name: "memory_context",
		Description: "Build and return the compiled <memory_context> window — the exact string " +
			"that gleann injects into LLM system prompts. Use this to inspect what the AI " +
			"currently 'knows' from persistent memory before generating a response.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type:       "object",
			Properties: map[string]interface{}{},
		},
	}
}

func (s *Server) handleMemoryContext(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	mgr, err := s.blockMem.get()
	if err != nil {
		return mcpsdk.NewToolResultError("open memory store: " + err.Error()), nil
	}

	cw, err := mgr.BuildContext()
	if err != nil {
		return mcpsdk.NewToolResultError("build context: " + err.Error()), nil
	}

	rendered := cw.Render()
	if rendered == "" {
		return mcpsdk.NewToolResultText("Memory is empty — no blocks stored yet."), nil
	}
	return mcpsdk.NewToolResultText(rendered), nil
}
