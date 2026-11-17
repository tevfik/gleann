// Package mcp — Memory Block MCP tool definitions.
//
// These tools expose gleann's hierarchical BBolt memory (pkg/memory) to
// external AI agents via the Model Context Protocol.  Unlike the KuzuDB
// Knowledge Graph tools, these operate on simple text blocks organized into
// short / medium / long tiers — providing infinite persistent memory for LLMs.
//
// Tools registered (no build tag — pure Go):
//
//   - memory_remember     — store a fact in long-term memory
//   - memory_forget       — remove a block by ID or content match
//   - memory_search       — full-text search across all tiers
//   - memory_list         — list blocks with optional tier filter
//   - memory_context      — get the compiled <memory_context> window for LLM injection
//   - memory_failure_add  — record a command failure and its fix
//   - memory_failure_list — list recorded failures
package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		_ = p.mgr.EndSession()
		_ = p.mgr.Close()
		p.mgr = nil
	}
}

// remoteMemoryClient probes for a running gleann REST server and returns
// a client if reachable. This prevents bbolt lock contention when gleann serve
// is running in the background.
func remoteMemoryClient() *memory.RemoteClient {
	return memory.Remote()
}

// detectRepoScope returns the normalized git remote origin or root directory name as the memory scope.
// E.g. "github.com/tevfik/gleann" or working dir basename.
func detectRepoScope() string {
	// 1. Try git remote get-url origin
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	if out, err := cmd.Output(); err == nil {
		raw := strings.TrimSpace(string(out))
		if raw != "" {
			return normalizeGitRemote(raw)
		}
	}
	// 2. Try git rev-parse --show-toplevel
	cmd = exec.Command("git", "rev-parse", "--show-toplevel")
	if out, err := cmd.Output(); err == nil {
		top := strings.TrimSpace(string(out))
		if top != "" {
			return filepath.Base(top)
		}
	}
	// 3. Fallback to current working directory basename
	if wd, err := os.Getwd(); err == nil {
		return filepath.Base(wd)
	}
	return "global"
}

func normalizeGitRemote(remote string) string {
	s := remote
	s = strings.TrimPrefix(s, "git@")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "ssh://")
	s = strings.Replace(s, ":", "/", 1)
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	return s
}

func detectGitCommit() string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	if out, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
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
					"description": "Isolate this block to a specific scope (e.g. conversation ID). Empty = auto repo scope.",
				},
				"repo": map[string]interface{}{
					"type":        "string",
					"description": "Repository identifier (e.g. github.com/tevfik/gleann). Defaults to current repo.",
				},
				"paths": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Relative file paths related to this memory (e.g. ['pkg/memory/block.go'])",
				},
				"symbols": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "FQN or symbol names related to this memory (e.g. ['Block', 'pkg/memory.OpenStore'])",
				},
				"commit": map[string]interface{}{
					"type":        "string",
					"description": "Git commit hash when memory was recorded (auto-detected from git HEAD if omitted)",
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

	charLimit := 0
	if raw, ok := args["char_limit"]; ok && raw != nil {
		if v, ok := raw.(float64); ok {
			charLimit = int(v)
		}
	}

	scope, _ := args["scope"].(string)
	repo, _ := args["repo"].(string)
	if scope == "" {
		if repo != "" {
			scope = repo
		} else {
			scope = detectRepoScope()
		}
	}
	if repo == "" {
		repo = scope
	}

	var paths []string
	if raw, ok := args["paths"]; ok && raw != nil {
		if rawSlice, ok := raw.([]interface{}); ok {
			for _, p := range rawSlice {
				if str, ok := p.(string); ok && str != "" {
					paths = append(paths, str)
				}
			}
		}
	}

	var symbols []string
	if raw, ok := args["symbols"]; ok && raw != nil {
		if rawSlice, ok := raw.([]interface{}); ok {
			for _, sym := range rawSlice {
				if str, ok := sym.(string); ok && str != "" {
					symbols = append(symbols, str)
				}
			}
		}
	}

	commit, _ := args["commit"].(string)
	if commit == "" {
		commit = detectGitCommit()
	}

	block := &memory.Block{
		Tier:      tier,
		Label:     label,
		Content:   content,
		Source:    "mcp_agent",
		Tags:      tags,
		CharLimit: charLimit,
		Scope:     scope,
		Repo:      repo,
		Paths:     paths,
		Symbols:   symbols,
		Commit:    commit,
	}

	prov := ""
	if len(symbols) > 0 {
		prov += fmt.Sprintf(" [symbols: %s]", strings.Join(symbols, ", "))
	}
	if len(paths) > 0 {
		prov += fmt.Sprintf(" [files: %s]", strings.Join(paths, ", "))
	}

	if rc := remoteMemoryClient(); rc != nil {
		created, err := rc.AddBlock(block)
		if err == nil {
			dedupNotice := ""
			if created.Confirms > 0 {
				dedupNotice = fmt.Sprintf(" (reinforced %dx)", created.Confirms+1)
			}
			return mcpsdk.NewToolResultText(fmt.Sprintf(
				"Remembered (ID: %s, tier: %s%s): %s%s", created.ID, created.Tier, dedupNotice, created.Content, prov,
			)), nil
		}
		// Remote call failed (e.g. server restarted or 404); fallback to local store below
	}

	mgr, err := s.blockMem.get()
	if err != nil {
		return mcpsdk.NewToolResultError("open memory store: " + err.Error()), nil
	}

	saved, err := mgr.RememberBlock(block)
	if err != nil {
		return mcpsdk.NewToolResultError("remember failed: " + err.Error()), nil
	}

	dedupNotice := ""
	if saved.Confirms > 0 {
		dedupNotice = fmt.Sprintf(" (reinforced %dx)", saved.Confirms+1)
	}

	return mcpsdk.NewToolResultText(fmt.Sprintf(
		"Remembered (ID: %s, tier: %s%s): %s%s", saved.ID, saved.Tier, dedupNotice, saved.Content, prov,
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

	if rc := remoteMemoryClient(); rc != nil {
		n, err := rc.Forget(idOrQuery)
		if err == nil {
			return mcpsdk.NewToolResultText(fmt.Sprintf("Forgot %d block(s) matching %q.", n, idOrQuery)), nil
		}
		// Remote call failed; fallback to local store below
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
		Description: "Full-text search across memory blocks. Matches against content, label, and tags. Scoped by default to the current repository plus global memories.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query — matches against content, label, and tags",
				},
				"scope": map[string]interface{}{
					"type":        "string",
					"description": "Filter memories by scope (default: current git repository + global). Pass 'all' or '*' to search across all scopes.",
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

	scope, _ := args["scope"].(string)
	if scope == "" {
		scope = detectRepoScope()
	} else if scope == "all" || scope == "*" {
		scope = ""
	}

	var blocks []memory.Block
	var remoteDone bool
	if rc := remoteMemoryClient(); rc != nil {
		var b []memory.Block
		var err error
		if scope != "" {
			b, err = rc.SearchScoped(scope, query)
		} else {
			b, err = rc.Search(query)
		}
		if err == nil {
			blocks = b
			remoteDone = true
		}
	}
	if !remoteDone {
		mgr, err := s.blockMem.get()
		if err != nil {
			return mcpsdk.NewToolResultError("open memory store: " + err.Error()), nil
		}

		var searchErr error
		if scope != "" {
			blocks, searchErr = mgr.SearchScoped(scope, query)
		} else {
			blocks, searchErr = mgr.Search(query)
		}
		if searchErr != nil {
			return mcpsdk.NewToolResultError("search failed: " + searchErr.Error()), nil
		}
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
		Description: "List memory blocks, optionally filtered by tier and scope. Scoped by default to the current repository plus global memories.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"tier": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"short", "medium", "long", ""},
					"description": "Filter by tier (omit to list all tiers)",
				},
				"scope": map[string]interface{}{
					"type":        "string",
					"description": "Filter memories by scope (default: current git repository + global). Pass 'all' or '*' to list all scopes.",
				},
			},
		},
	}
}

func (s *Server) handleMemoryList(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)

	var tier memory.Tier
	scope := ""
	if args != nil {
		if tierStr, _ := args["tier"].(string); tierStr != "" {
			t, err := memory.ParseTier(tierStr)
			if err != nil {
				return mcpsdk.NewToolResultError(err.Error()), nil
			}
			tier = t
		}
		scope, _ = args["scope"].(string)
	}

	if scope == "" {
		scope = detectRepoScope()
	} else if scope == "all" || scope == "*" {
		scope = ""
	}

	var blocks []memory.Block
	var remoteDone bool
	if rc := remoteMemoryClient(); rc != nil {
		var b []memory.Block
		var err error
		if scope != "" {
			b, err = rc.ListScoped(scope, tier)
		} else {
			b, err = rc.List(tier)
		}
		if err == nil {
			blocks = b
			remoteDone = true
		}
	}
	if !remoteDone {
		mgr, err := s.blockMem.get()
		if err != nil {
			return mcpsdk.NewToolResultError("open memory store: " + err.Error()), nil
		}

		var listErr error
		if scope != "" {
			blocks, listErr = mgr.ListScoped(scope, tier)
		} else {
			blocks, listErr = mgr.List(tier)
		}
		if listErr != nil {
			return mcpsdk.NewToolResultError("list failed: " + listErr.Error()), nil
		}
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
			"that gleann injects into LLM system prompts. Scoped by default to the current repository plus global memories.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"scope": map[string]interface{}{
					"type":        "string",
					"description": "Scope for memory context (default: current git repository + global). Pass 'all' or '*' for all scopes.",
				},
				"verbose": map[string]interface{}{
					"type":        "boolean",
					"description": "Append the injection budget report (what was injected and what the token cap dropped).",
				},
				"command": map[string]interface{}{
					"type":        "string",
					"description": "The command the current task is about to run; surfaces matching recorded failures.",
				},
				"paths": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Files the current task touches; surfaces recorded failures overlapping these paths.",
				},
			},
		},
	}
}

func (s *Server) handleMemoryContext(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	scope := ""
	verbose := false
	var command string
	var paths []string
	if args != nil {
		scope, _ = args["scope"].(string)
		verbose, _ = args["verbose"].(bool)
		command, _ = args["command"].(string)
		paths = stringSliceArg(args, "paths")
	}

	if scope == "" {
		scope = detectRepoScope()
	} else if scope == "all" || scope == "*" {
		scope = ""
	}

	var rendered string
	var budget *memory.ContextBudgetReport
	var remoteDone bool
	// Failures are looked up locally (the running server cannot pass its
	// manager), so a task-scoped context always builds against the local store.
	if command == "" && len(paths) == 0 {
		if rc := remoteMemoryClient(); rc != nil {
			r, err := rc.Context(scope)
			if err == nil {
				rendered = r
				remoteDone = true
			}
		}
	}
	if !remoteDone {
		mgr, err := s.blockMem.get()
		if err != nil {
			return mcpsdk.NewToolResultError("open memory store: " + err.Error()), nil
		}

		s.checkStaleBlocks(mgr, scope)
		var cw *memory.ContextWindow
		if command != "" || len(paths) > 0 {
			cw, err = mgr.BuildTaskContext(scope, command, paths)
		} else {
			cw, err = mgr.BuildScopedContext(scope)
		}
		if err != nil {
			return mcpsdk.NewToolResultError("build context: " + err.Error()), nil
		}
		rendered = cw.Render()
		budget = cw.Budget
	}

	if rendered == "" {
		return mcpsdk.NewToolResultText("Memory is empty — no blocks stored yet for scope."), nil
	}
	if verbose {
		if budget != nil {
			return mcpsdk.NewToolResultText(rendered + "\n\n" + budget.Summary()), nil
		}
		rendered += "\n\nmemory budget: not reported by a running server"
	}
	return mcpsdk.NewToolResultText(rendered), nil
}

func (s *Server) checkStaleBlocks(mgr *memory.Manager, scope string) {
	blocks, err := mgr.ListScoped(scope, "")
	if err != nil {
		return
	}
	var modifiedFiles []string
	for _, b := range blocks {
		if b.Suspect || len(b.Paths) == 0 {
			continue
		}
		for _, p := range b.Paths {
			fi, err := os.Stat(p)
			if err != nil {
				continue
			}
			if fi.ModTime().After(b.UpdatedAt) {
				modifiedFiles = append(modifiedFiles, p)
			}
		}
	}
	if len(modifiedFiles) > 0 {
		_, _ = mgr.MarkSuspect(modifiedFiles, nil)
	}
}

// ── Tool: memory_failure_add ────────────────────────────────────────────────

func (s *Server) buildMemoryFailureAddTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name: "memory_failure_add",
		Description: "Record a command failure and the fix that resolved it, so future " +
			"sessions surface it (via memory_context) instead of repeating the mistake. " +
			"Recording the same failure again reinforces the existing block.",
		InputSchema: mcpsdk.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "The command that failed (e.g. 'go test ./pkg/memory/')",
				},
				"error": map[string]interface{}{
					"type":        "string",
					"description": "The error text produced by the failing command",
				},
				"fix": map[string]interface{}{
					"type":        "string",
					"description": "The fix that resolved the failure",
				},
				"paths": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "File paths involved in the failure",
				},
				"symbols": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Symbol names involved in the failure",
				},
			},
			Required: []string{"command", "error"},
		},
	}
}

func (s *Server) handleMemoryFailureAdd(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcpsdk.NewToolResultError("invalid arguments"), nil
	}

	command, _ := args["command"].(string)
	errText, _ := args["error"].(string)
	if command == "" || errText == "" {
		return mcpsdk.NewToolResultError("command and error are required"), nil
	}

	fix, _ := args["fix"].(string)
	paths := stringSliceArg(args, "paths")
	symbols := stringSliceArg(args, "symbols")

	mgr, err := s.blockMem.get()
	if err != nil {
		return mcpsdk.NewToolResultError("open memory store: " + err.Error()), nil
	}

	saved, err := mgr.RecordFailure(memory.Failure{
		Command: command,
		Error:   errText,
		Fix:     fix,
		Paths:   paths,
		Symbols: symbols,
	})
	if err != nil {
		return mcpsdk.NewToolResultError("record failure: " + err.Error()), nil
	}

	if saved.Confirms > 1 {
		return mcpsdk.NewToolResultText(fmt.Sprintf(
			"Failure confirmed (%dx) for %q: %s", saved.Confirms, command, saved.Content,
		)), nil
	}
	return mcpsdk.NewToolResultText(fmt.Sprintf(
		"Recorded failure (ID: %s) for %q: %s", saved.ID, command, saved.Content,
	)), nil
}

// ── Tool: memory_failure_list ───────────────────────────────────────────────

func (s *Server) buildMemoryFailureListTool() mcpsdk.Tool {
	return mcpsdk.Tool{
		Name:        "memory_failure_list",
		Description: "List recorded command failures (command, error and fix).",
		InputSchema: mcpsdk.ToolInputSchema{
			Type:       "object",
			Properties: map[string]interface{}{},
		},
	}
}

func (s *Server) handleMemoryFailureList(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	mgr, err := s.blockMem.get()
	if err != nil {
		return mcpsdk.NewToolResultError("open memory store: " + err.Error()), nil
	}

	failures, err := mgr.ListFailures()
	if err != nil {
		return mcpsdk.NewToolResultError("list failures: " + err.Error()), nil
	}

	if len(failures) == 0 {
		return mcpsdk.NewToolResultText("No failures recorded."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d recorded failure(s):\n\n", len(failures))
	for i, b := range failures {
		fmt.Fprintf(&sb, "%d. command: %s\n   error: %s\n", i+1, b.Metadata[memory.FailureMetaCommand], b.Content)
		if fix := b.Metadata[memory.FailureMetaFix]; fix != "" {
			fmt.Fprintf(&sb, "   fix: %s\n", fix)
		}
		if len(b.Paths) > 0 {
			fmt.Fprintf(&sb, "   files: %s\n", strings.Join(b.Paths, ", "))
		}
	}
	return mcpsdk.NewToolResultText(sb.String()), nil
}

// stringSliceArg reads a string-array argument (marshalled back through JSON
// so both []string and []interface{} arrive consistently).
func stringSliceArg(args map[string]any, key string) []string {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil
	}
	if slice, ok := raw.([]interface{}); ok {
		out := make([]string, 0, len(slice))
		for _, v := range slice {
			if str, ok := v.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		return out
	}
	if slice, ok := raw.([]string); ok {
		out := make([]string, 0, len(slice))
		for _, str := range slice {
			if str != "" {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}
