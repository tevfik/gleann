package mcp

import (
	"sort"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// Tool annotations are the hints an MCP client uses to decide whether a call
// is safe to run automatically, needs confirmation, or can be retried. The
// zero value means "unknown", so every registered tool gets all four hints
// explicitly (TestMCP06 in mcp06_contract_test.go enforces this).
//
// The classification is deliberately conservative:
//
//   - ReadOnlyHint: the tool only reads index/files/memory content and leaves
//     stored state untouched. Pure searches and reads qualify even though they
//     may record internal telemetry (token gain, session log); tools that
//     change stored state (memory_context flags stale blocks as suspect,
//     gleann_gain {reset:true} zeroes the counters) do not.
//   - DestructiveHint: tools that delete or overwrite stored knowledge
//     (memory_forget, delete_graph_entity) and gleann_session_end, whose
//     EndSession prunes expired short-term blocks.
//   - IdempotentHint: repeating the same call has no additional effect.
//     memory_remember/memory_failure_add reinforce confidence (Confirms++) and
//     refresh UpdatedAt, so they are not idempotent; inject_knowledge_graph
//     archives the previous node versions and re-embeds node content on every
//     call, so it is not idempotent either.
//   - OpenWorldHint: the tool may reach a configured network service. Fifteen
//     tools qualify, in two groups.
//
//     Eight reach the configured embedder or LLM over HTTP: the LLM-backed ask
//     tools, any search that accepts rerank/expand (a configured cross-encoder
//     or LLM endpoint), and every tool that computes embeddings through the
//     configured embedder: gleann_search, gleann_search_multi,
//     gleann_search_ids, gleann_def (its passage-search fallback), gleann_sync
//     (it embeds source contents) and inject_knowledge_graph (entity vector
//     sync). With the default Ollama provider these are HTTP calls to
//     http://localhost:11434/api/embed, and a saved config can point them at a
//     remote host.
//
//     Seven route memory/session I/O through pkg/memory.Remote() (wrapped by
//     remoteMemoryClient in blocks_tools.go / session_tools.go):
//     memory_search, memory_list, memory_context, memory_remember,
//     memory_forget, gleann_session_start and gleann_session_end. When a
//     healthy gleann server answers they POST/GET/DELETE /api/blocks*. The
//     address is GLEANN_REMOTE_ADDR when set, otherwise a saved server_addr in
//     ~/.gleann/config.json, the addr in ~/.gleann/server.pid, or the default
//     http://localhost:8081; GLEANN_REMOTE_ADDR=off disables that path.
//
//     The remaining 22 tools — including memory_failure_add,
//     memory_failure_list, gleann_session_status, the code/doc graph tools,
//     gleann_fetch/get/list, gleann_read/shell/gain and delete_graph_entity —
//     work against the local index, files and SQLite stores only.

func boolHint(v bool) *bool { return &v }

func annotate(title string, readOnly, destructive, idempotent, openWorld bool) mcpsdk.ToolAnnotation {
	return mcpsdk.ToolAnnotation{
		Title:           title,
		ReadOnlyHint:    boolHint(readOnly),
		DestructiveHint: boolHint(destructive),
		IdempotentHint:  boolHint(idempotent),
		OpenWorldHint:   boolHint(openWorld),
	}
}

// toolAnnotations is the advertised hint set of every tool NewServer can
// register. Every registered tool must have an entry, and every entry must be
// a registrable tool; TestMCP06 fails in both directions.
var toolAnnotations = map[string]mcpsdk.ToolAnnotation{
	// ── Read-only search and retrieval ────────────────────────────────
	// gleann_search embeds every query and may also call a configured
	// cross-encoder or LLM endpoint for rerank/expand.
	"gleann_search": annotate("Search Index", true, false, true, true),
	// The query is embedded through the configured embedder on every call.
	"gleann_search_multi": annotate("Search Multiple Indexes", true, false, true, true),
	// Embeds the query and may call a configured cross-encoder for rerank.
	"gleann_search_ids": annotate("Search Passage IDs", true, false, true, true),
	"gleann_fetch":      annotate("Fetch Passages", true, false, true, false),
	"gleann_get":        annotate("Resolve Citation", true, false, true, false),
	// Falls back to passage search (and therefore the embedder) when the code
	// graph has no declaration for the symbol.
	"gleann_def":       annotate("Go to Definition", true, false, true, true),
	"gleann_list":      annotate("List Indexes", true, false, true, false),
	"gleann_ask":       annotate("Ask Indexed Data", true, false, true, true),
	"gleann_batch_ask": annotate("Batch Ask", true, false, true, true),

	// ── Read-only code graph and document queries ──────────────────────
	"gleann_graph_neighbors":    annotate("Callers and Callees", true, false, true, false),
	"gleann_impact":             annotate("Blast Radius", true, false, true, false),
	"gleann_graph_stats":        annotate("Graph Statistics", true, false, true, false),
	"gleann_symbols_in_file":    annotate("Symbols in File", true, false, true, false),
	"gleann_communities":        annotate("Community Detection", true, false, true, false),
	"gleann_risk_analysis":      annotate("Change Risk Analysis", true, false, true, false),
	"gleann_repo_map":           annotate("Repository Map", true, false, true, false),
	"gleann_navigate_symbol":    annotate("Navigate Symbol", true, false, true, false),
	"gleann_document_links":     annotate("Document Links", true, false, true, false),
	"gleann_read_full_document": annotate("Read Document", true, false, true, false),
	"gleann_document_toc":       annotate("Document Table of Contents", true, false, true, false),
	"traverse_knowledge_graph":  annotate("Traverse Knowledge Graph", true, false, true, false),

	// ── Read-only filesystem helpers ──────────────────────────────────
	"gleann_read":  annotate("Read File", true, false, true, false),
	"gleann_shell": annotate("Compress Shell Output", true, false, true, false),

	// ── Read-only memory queries ──────────────────────────────────────
	// memory_search forwards GET /api/blocks/search?q=… to a healthy gleann
	// REST server when remoteMemoryClient() (pkg/memory.Remote) finds one.
	"memory_search": annotate("Search Memory", true, false, true, true),
	// memory_list forwards GET /api/blocks?scope=… to that server.
	"memory_list": annotate("List Memory", true, false, true, true),
	// Both of these read the local SQLite store only.
	"memory_failure_list":   annotate("List Command Failures", true, false, true, false),
	"gleann_session_status": annotate("Session Status", true, false, true, false),

	// ── Additive or idempotent writes ─────────────────────────────────
	// Flags stale blocks as suspect (Suspect, StaleReason, UpdatedAt) in
	// SQLite, which is a write; already-suspect blocks are skipped, so a
	// repeated call is idempotent. Also forwards GET /api/blocks/context to a
	// healthy gleann REST server unless a command/path filter forces the
	// local store.
	"memory_context": annotate("Memory Context", false, false, true, true),
	// {reset:true} zeroes the accumulated counters, so this is a write
	// (non-destructive, and idempotent when resetting).
	"gleann_gain": annotate("Token Gain Report", false, false, true, false),
	// Repeated identical calls reinforce confidence (Confirms++) and
	// refresh UpdatedAt. Also POSTs the fact to /api/blocks when a healthy
	// gleann REST server answers; memory_failure_add stays local.
	"memory_remember":    annotate("Remember in Memory", false, false, false, true),
	"memory_failure_add": annotate("Record Command Failure", false, false, false, false),
	// Each call archives the previous node versions and re-embeds node
	// content for the vector store, so identical repeats still have effects.
	"inject_knowledge_graph": annotate("Inject Knowledge Graph", false, false, false, true),
	// Both session tools POST their note to /api/blocks through
	// pkg/memory.Remote() when a server is healthy.
	"gleann_session_start": annotate("Start Session", false, false, false, true),
	// Also prunes expired short-term blocks (store.PruneExpired).
	"gleann_session_end": annotate("End Session", false, true, false, true),
	// Builds/syncs by embedding the source contents through the configured
	// embedder.
	"gleann_sync": annotate("Sync Index", false, false, true, true),

	// ── Destructive writes ────────────────────────────────────────────
	// Deletes locally, or forwards DELETE /api/blocks/{id} to a healthy
	// gleann REST server.
	"memory_forget":       annotate("Forget Memory", false, true, true, true),
	"delete_graph_entity": annotate("Delete Graph Entity", false, true, true, false),
}

// allToolNames returns every canonical tool name the server can register, in
// deterministic order. serverInstructionsFor uses it to name every tool of a
// custom profile.
func allToolNames() []string {
	names := make([]string, 0, len(toolAnnotations))
	for name := range toolAnnotations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// toolAnnotationFor returns the MCP annotations of a registered tool. Every
// tool name registered by NewServer must have an entry; TestMCP06 fails when
// one is missing.
func toolAnnotationFor(name string) (mcpsdk.ToolAnnotation, bool) {
	a, ok := toolAnnotations[name]
	return a, ok
}

// annotateTool attaches the hint set of a registered tool. The name is the
// canonical gleann_/memory_ name, before --clean-names strips a prefix.
func annotateTool(tool *mcpsdk.Tool) {
	if a, ok := toolAnnotationFor(tool.Name); ok {
		tool.Annotations = a
	}
}
