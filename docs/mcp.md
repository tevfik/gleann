# MCP Server Guide & Platform Install

Gleann includes a built-in [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server that exposes your indexed knowledge base to AI editors like Cursor, Windsurf, Claude Desktop, and others.

## Zero-Friction MCP Installer (`gleann mcp install`)

To immediately configure gleann's MCP server for AI agents and editors without editing configuration files manually, run:

```bash
# Auto-configure all detected targets (Claude Code, Cursor, Gemini/Antigravity, VS Code)
gleann mcp install --target all

# Or configure a specific client:
gleann mcp install --target claude-code   # Merges into ~/.claude.json
gleann mcp install --target cursor        # Merges into ~/.cursor/mcp.json
gleann mcp install --target gemini        # Merges into ~/.gemini/config/mcp_config.json
gleann mcp install --target vscode        # Merges into .vscode/mcp.json
```

The installer parses existing JSON configuration files, cleanly merges `gleann` under `mcpServers`, preserves all other configured tools and settings, and automatically backs up corrupt files to `.bak`.

## One-command Platform Setup (`gleann install`)

The `gleann install` command auto-detects your AI coding platform and writes all required integration files (AGENTS.md, MCP config, platform-specific plugins) in one step.

```bash
# Auto-detect and install for all platforms found in the current directory
gleann install

# Install for a specific platform (the name can also be given positionally:
# `gleann install claude` is the same as `gleann install --platform claude`)
gleann install --platform opencode
gleann install --platform claude
gleann install --platform cursor
gleann install --platform codex
gleann install --platform gemini
gleann install --platform claw      # OpenClaw
gleann install --platform aider
gleann install --platform copilot   # GitHub Copilot CLI
gleann install --platform windsurf  # Windsurf IDE
gleann install --platform cline     # Cline / Roo Code
gleann install --platform amp       # Amp
gleann install --platform kiro      # Kiro IDE
gleann install --platform amazonq   # Amazon Q Developer
gleann install --platform continue  # Continue
gleann install --platform zed       # Zed IDE
gleann install --platform neovim    # Neovim (avante/codecompanion)
gleann install --platform jetbrains # JetBrains IDEs (.junie)

# List all supported platforms and detection status
gleann install --list

# Remove integration files
gleann install uninstall
gleann install uninstall --platform cursor
```

### User-level vs project-level files

`claude` and `gemini` install **user-level** by default, so the instructions apply
to every project and nothing is written into the directory you ran the command in:
`~/.claude/CLAUDE.md` + `~/.claude/settings.json`, and `~/.gemini/GEMINI.md` +
`~/.gemini/settings.json`. Pass `--project` (or an explicit `--dir <path>`) to write
`CLAUDE.md` / `GEMINI.md` / `.gemini/` into the current project instead. `uninstall`
follows the same rule. All other platforms are project-level, as listed below.

### What gets written per platform

| Platform | Files written | Integration type |
|----------|--------------|-----------------|
| **opencode** | `AGENTS.md` · `.opencode/plugins/gleann.js` · `.opencode/mcp.json` · `opencode.json` | `experimental.session.compacting` plugin hook |
| **claude** | `~/.claude/CLAUDE.md` (or `CLAUDE.md` with `--project`) · `~/.claude/settings.json` | `SessionStart` hook (once per session: memory context, bounded to 2000 bytes; hint when a `GRAPH_REPORT.md` exists). Older `PreToolUse` gleann hooks are removed on install |
| **cursor** | `.cursor/rules/gleann.mdc` · `.cursor/mcp.json` | `alwaysApply: true` rules file |
| **codex** | `AGENTS.md` · `.codex/hooks.json` | `PreToolUse` on Bash |
| **gemini** | `~/.gemini/GEMINI.md` + `settings.json` (or `GEMINI.md` · `.gemini/settings.json` with `--project`) | `BeforeTool` hook |
| **claw** | `AGENTS.md` · `~/.openclaw/skills/gleann/SKILL.md` | Always-on skill |
| **aider** | `AGENTS.md` | Always-on AGENTS.md |
| **copilot** | `~/.copilot/skills/gleann/SKILL.md` | CLI skill |
| **windsurf** | `.windsurf/rules/gleann.md` · `.windsurf/mcp.json` | Rules + MCP config |
| **cline** | `.clinerules` · `.cline/mcp.json` | Rules + MCP config |
| **amp** | `AGENTS.md` · `.amp/mcp.json` | MCP config |
| **kiro** | `.kiro/rules/gleann.md` · `.kiro/mcp.json` | Rules + MCP config |
| **amazonq** | `AGENTS.md` · `.amazonq/mcp.json` | MCP config |
| **continue** | `AGENTS.md` · `.continue/config.json` | MCP config block |
| **zed** | `~/.config/zed/settings.json` | MCP config |
| **neovim** | `AGENTS.md` | MCP instructions |
| **jetbrains** | `AGENTS.md` · `.junie/guidelines.md` | Guidelines + MCP |

The `claude` and `gemini` sections are delimited by `<!-- gleann ... -->` / `<!-- /gleann -->`; `gleann install` replaces an older variant of the section instead of adding a second one. The user-level text applies only to projects that have a gleann index; a project-level install names the index. Hooks written by gleann carry the `[gleann]` marker in their command, and install and uninstall remove every gleann-owned hook from `PreToolUse` and `SessionStart` (foreign hooks stay).

### OpenCode in depth

OpenCode is the only platform that receives a JavaScript plugin (`.opencode/plugins/gleann.js`) which fires when a session is compacted. It injects the first 2 000 characters of `GRAPH_REPORT.md` (if one exists in the project root) and the memory blocks into the context window so the AI sees god nodes and community structure before it starts searching source files.

To generate a report (optional; `gleann graph map --index <name>` prints a compact repo map to stdout instead):

```bash
gleann graph report --index <name>   # writes GRAPH_REPORT.md
```

Register the MCP server in OpenCode for inline tool access:

```json5
// .opencode/mcp.json
{
  "mcpServers": {
    "gleann": { "type": "stdio", "command": "gleann", "args": ["mcp"] }
  }
}
```

`gleann install --platform opencode` writes both files automatically.

---

## Manual MCP Configuration

If you prefer to configure the MCP server manually (without `gleann install`):

## Editor Configuration

### Cursor / Windsurf

Add to your MCP settings (`.cursor/mcp.json` or similar):

```json
{
  "mcpServers": {
    "gleann": {
      "command": "gleann",
      "args": ["mcp"]
    }
  }
}
```

### Claude Desktop

Add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "gleann": {
      "command": "gleann",
      "args": ["mcp"]
    }
  }
}
```

## Server Instructions & Routing (gleann vs grep)

The `initialize` response includes server instructions that clients show to the
model before the first tool call. They describe the intended routing:

| Question | Use | Not |
|----------|-----|-----|
| "How does auth work?", "where are retries handled?" | `gleann_search` (or `gleann_search_ids` → `gleann_fetch`) | keyword grep |
| Exact literal, regex or identifier occurrences | grep/ripgrep, then `gleann_def`/graph tools on the hits | — |
| You know a symbol name | `gleann_def` | `grep "func X"` |
| Callers, callees, change impact | `gleann_graph_neighbors`, `gleann_impact`, `gleann_navigate_symbol` | text search over references |
| Read a file cheaply | `gleann_read` (`signatures`/`map`/`task`/`lines`) | dumping the whole file |
| Durable project facts, conventions, lessons | `memory_search` / `memory_context`, then `memory_remember` | re-asking the user |
| A command failed and was fixed | `memory_failure_add` | — |
| The workspace changed | `gleann_sync` | stale search results |

## Available Tools

### RAG & Search Tools

| Tool | Description |
|------|-------------|
| `gleann_search` | Hybrid semantic + lexical search; prefer it over grep for conceptual or cross-file questions |
| `gleann_search_multi` | Fan-out search across multiple indexes |
| `gleann_def` | Go-to-definition for symbols (functions, methods, types, structs) |
| `gleann_list` | List all available indexes |
| `gleann_ask` | RAG Q&A with an index |
| `gleann_graph_neighbors` | Query callers/callees of a symbol |
| `gleann_document_links` | Get document structure links |
| `gleann_document_toc` | Table of contents / heading outline of an indexed document, or the list of indexed documents |
| `gleann_graph_stats` | Code graph statistics for an index: file count, symbol count, edge counts by type, and the health record (which graph extractor wrote it, exact or syntactic Go resolution and why, tree-sitter, when, dirty files, edges by confidence) with a rebuild hint when the graph is older |
| `gleann_symbols_in_file` | List the symbols (functions, methods, types) defined in one source file |
| `gleann_impact` | Blast radius analysis for a symbol |
| `gleann_read_full_document` | Retrieve intact full document text via the document graph's path resolution or graph chunks |
| `gleann_communities` | Run Louvain community detection on code graph |
| `gleann_risk_analysis` | Compute change risk scores for symbols via PageRank and coupling |
| `gleann_repo_map` | Compact text map of repository ranked by symbol importance |
| `gleann_navigate_symbol` | Navigate symbol definition, callers, and callees |

The tools that read the code graph (`gleann_graph_neighbors`, `gleann_impact`, `gleann_document_links`,
`gleann_graph_stats`, `gleann_symbols_in_file`, `gleann_document_toc`, and the four graph-analysis tools `gleann_communities`,
`gleann_risk_analysis`, `gleann_repo_map`, `gleann_navigate_symbol`) read the code graph. Every index serves all of
them, including the four analysis tools, from its SQLite graph in every build, so they are always advertised. Without tree-sitter, only Go files have CALLS edges, so communities, risk and the repo map
of other languages rest on declarations alone.
`gleann_def` stays and falls back to vector search when there is no graph.

### Progressive Disclosure Tools

Token-efficient two-phase search. First call `gleann_search_ids` to get compact
results (5–10× fewer tokens), then call `gleann_fetch` only for the passages you
actually need. Use `gleann_get` to resolve citation references at any time.

| Tool | Description |
|------|-------------|
| `gleann_search_ids` | Compact search returning ref/score/source/peek (no full text) |
| `gleann_fetch` | Hydrate full passage content for selected refs from `gleann_search_ids` |
| `gleann_get` | Citation lookup — resolve a persistent `"indexname:id"` reference |

### Session Tracking Tools

Capture what an AI agent does during a gleann session. Sessions are stored in
the memory store (`memory.sqlite`; TierShort for events, TierLong for summaries) and survive process restarts.

| Tool | Description |
|------|-------------|
| `gleann_session_start` | Begin a named work session |
| `gleann_session_end` | Close the session with an optional summary |
| `gleann_session_status` | Show active session name and event count |

### Memory Engine Tools

These tools allow autonomous agents to **directly manipulate** gleann's generic
Knowledge Graph, bypassing the RAG pipeline entirely. This is useful for injecting
structured knowledge, linking requirements to code, and exploring multi-hop
relationships.

| Tool | Description |
|------|-------------|
| `inject_knowledge_graph` | Atomically upsert Entity nodes and RELATES_TO edges (rows merged; previous versions archived per call) |
| `delete_graph_entity` | Remove an entity and all its incident edges |
| `traverse_knowledge_graph` | Walk the graph N hops from a start node |

These three work in every build; the entities are kept in the memory database (`memory.sqlite`).

### Long-term Memory (Blocks) Tools

Persistent hierarchical memory (SQLite, `memory.sqlite`). Facts stored here are automatically
compiled into a `<memory_context>` system message for every LLM query. This
gives agents persistent knowledge across sessions.

| Tool | Description |
|------|-------------|
| `memory_remember` | Store a fact in long-term memory |
| `memory_forget` | Delete a memory block by ID or content match |
| `memory_search` | Full-text search across all memory tiers |
| `memory_list` | List memory blocks (optional tier filter) |
| `memory_context` | Show compiled memory context the LLM currently receives |
| `memory_failure_add` | Record a command failure and the fix that resolved it |
| `memory_failure_list` | List recorded command failures |

### Index Synchronization Tool

| Tool | Description |
|------|-------------|
| `gleann_sync` | Incrementally synchronize index and AST graph after code modifications |

### Batch Query Tool

| Tool | Description |
|------|-------------|
| `gleann_batch_ask` | Run 1–10 questions concurrently against a single index, returning all answers in one response |

### Tool Profiles (`--tools`)

Gleann supports scoping the MCP surface to prevent token bloat in client context windows:

```bash
# Core profile (default, 11 essential tools for fast coding agent loops)
gleann mcp --tools core

# Full profile (all tools registered; `all` is an alias)
gleann mcp --tools full

# Custom comma-separated list or aliases (e.g. search, recall, symbol, sync)
gleann mcp --tools "search,read,symbol,recall,sync"
```

The `core` profile exposes: `gleann_search`, `gleann_read`, `gleann_def`, `gleann_graph_neighbors`, `gleann_impact`, `gleann_sync`, `memory_remember`, `memory_failure_add`, `memory_context`, `memory_search`, `memory_forget`.

You can also set the default profile using the `GLEANN_TOOLS` environment variable. Passing `--clean-names` strips the `gleann_` prefix (e.g. `search`, `read`, `impact`).

### Tool Annotations

Every advertised tool carries MCP annotations (`title`, `readOnlyHint`,
`destructiveHint`, `idempotentHint`, `openWorldHint`). Definition, graph,
document, read and pure memory-query tools are read-only. `memory_context` is a
non-destructive write (it flags stale blocks as suspect) and `gleann_gain` is a
non-destructive write (`reset: true` zeroes the counters). `memory_remember`,
`memory_failure_add`, `inject_knowledge_graph`, the session tools and
`gleann_sync` are non-destructive writes; `memory_remember`,
`memory_failure_add` and `inject_knowledge_graph` are **not** idempotent:
repeating an identical remember/failure reinforces confidence and refreshes the
timestamp, while an identical re-inject archives the previous node versions and
re-embeds their content. Only `memory_forget`, `delete_graph_entity` and
`gleann_session_end` are destructive — the last one prunes expired short-term
blocks when it closes the session.

The open-world tools come in two groups. Eight reach the configured embedder
or LLM over HTTP: `gleann_ask` and `gleann_batch_ask` (the configured LLM),
`gleann_search` and `gleann_search_ids` (the embedder on every query, plus a
configured LLM/reranker endpoint when passed `expand: true` or `rerank: true`),
and every tool that computes embeddings through the configured embedder:
`gleann_search_multi` (query embedding), `gleann_def` (its passage search
fallback when the code graph has no match), `gleann_sync` (source contents are
embedded to build or refresh the index) and `inject_knowledge_graph` (entity
content is re-embedded for the vector store). With the default provider these
are HTTP calls to the configured Ollama `/api/embed` endpoint
(`http://localhost:11434` by default); a saved config can point the embedder at
a remote host.

Seven more tools forward memory and session I/O over HTTP REST (`/api/blocks*`)
whenever a healthy `gleann` server answers: `memory_search`, `memory_list`,
`memory_context`, `memory_remember`, `memory_forget`, `gleann_session_start`
and `gleann_session_end`. The client uses `GLEANN_REMOTE_ADDR` when set;
otherwise it probes a saved `server_addr` in `~/.gleann/config.json`, the
address in `~/.gleann/server.pid`, then `http://localhost:8081`. Set
`GLEANN_REMOTE_ADDR=off` to disable that REST forwarding path (the embedder
and LLM tools still use their configured endpoint). This lets a running
`gleann serve` and the MCP server share one memory store. The remaining 22 tools — including `memory_failure_add`,
`memory_failure_list`, `gleann_session_status`, the code and document graph
tools, `gleann_fetch`/`gleann_get`/`gleann_list`, `gleann_read`/`gleann_shell`/
`gleann_gain` and `delete_graph_entity` — work against the local index, files
and SQLite stores only. Clients can use these hints to auto-approve reads while
asking for confirmation on deletes.

### Context-Intelligence Tools

Token-saving utilities that help agents work smarter with large codebases.

| Tool | Description |
|------|-------------|
| `gleann_shell` | Compress noisy CLI output (git, npm, go, docker, cargo) before injecting into context. Reports compression ratio and token savings. |
| `gleann_read` | Read a file using one of 10 context-aware modes (`full`, `map`, `signatures`, `diff`, `entropy`, `lines:N`, `task`, `reference`, `aggressive`, `auto`). Saves 60–90% of tokens vs. raw cat. |
| `gleann_gain` | Report cumulative estimated token savings recorded by this server's `gleann_shell` and `gleann_read` calls since the server started (or the last `reset`). Gain per call = estimated tokens of the raw input (shell output, or whole file) minus estimated tokens of the compressed payload returned (~4 characters per token heuristic; metadata wrappers not counted). The counter lives in one MCP server process and is not persisted across restarts — useful for budget tracking. |

**Example: compress git log output**
```json
{ "command": "git log --stat -20", "output": "<long git log here>" }
```

**Example: read only function signatures from a Go file**
```json
{ "path": "/path/to/server.go", "mode": "signatures" }
```

---

### gleann_search

Search an index with optional filters and graph context:

```json
{
  "index": "my-code",
  "query": "how does authentication work?",
  "top_k": 5,
  "graph_context": true,
  "filters": [
    {"field": "ext", "operator": "eq", "value": ".go"}
  ]
}
```

`filters` (with `filter_logic`: `and`, the default, or `or`) are applied
during retrieval: a selective filter still returns `top_k` results when that
many passages match. The candidate pool widens until k pass (capped at 4096).
A format-2 index also pre-selects the matching IDs in SQL and scores just
those when the filter is estimated to match only a small fraction of the
index, or when the loop stops at the cap short; HNSW and DiskANN (loaded in
RAM or memory-mapped) score that set by exact vector distance. A filter
nothing matches returns 0.

### Index Governance, Scoping & Federated Search

When managing multiple indexes across different projects or sensitivity levels, you can control which indexes are exposed to MCP AI agents and group them with tags:

#### 1. Controlling Index Visibility (`mcp_exposed`)
By default, indexes are exposed to MCP. To make an index private (hidden from `gleann_list` and inaccessible to MCP agents):
```bash
# Hide an index from MCP agents
gleann index set my-secret-repo --mcp=false

# Re-expose an index to MCP
gleann index set my-secret-repo --mcp=true

# Add a semantic description to help agents understand the contents
gleann index set my-code --desc "Core Go microservices and auth backend"
```

#### 2. Tagging & Organizing Indexes
Assign tags to categorize indexes:
```bash
gleann index tag backend-api --add work --add backend
gleann index tag payment-svc --add work --add finance
gleann index tag personal-notes --add personal

# List indexes matching a tag or MCP status
gleann index list --tag work
gleann index list --mcp
```

#### 3. Restricting MCP Server with `GLEANN_TAGS`
In your AI editor's MCP configuration, you can set the `GLEANN_TAGS` environment variable to restrict the agent to only indexes matching those tags:
```json
{
  "mcpServers": {
    "gleann": {
      "command": "gleann",
      "args": ["mcp"],
      "env": {
        "GLEANN_TAGS": "work,backend"
      }
    }
  }
}
```

#### 4. Federated Multi-Index Search via `@tag`
Agents can search across all indexes tagged with a collection by prefixing the tag name with `@`:
```json
{
  "index": "@work",
  "query": "Where is the payment gateway webhook defined?",
  "top_k": 5
}
```
Gleann searches all accessible indexes tagged with `work`, combines and re-ranks the results by relevance score, and returns the top-K fragments annotated with their source index `[index: <name>]`.


### gleann_def

Go-to-definition for symbols (functions, methods, types, classes, structs). Returns file:line, signature, and documentation in a compact format (~500 tokens).

```json
{
  "symbol": "TokenManager",
  "index": "my-code",
  "kind": "struct"
}
```

### gleann_graph_neighbors

Find callers and callees of a function:

```json
{
  "index": "my-code",
  "node_fqn": "main.handleSearch"
}
```

### gleann_impact

Analyze the blast radius of changing a symbol:

```json
{
  "index": "my-code",
  "symbol": "pkg.Config",
  "max_depth": 3
}
```

### gleann_read_full_document

Retrieve the complete text of an indexed document using its virtual or relative path (e.g. `docs/architecture.md`). Resolves the physical path via the document graph, falls back to reconstructing text from ordered graph chunks (`Chunk`), or reads directly from disk if accessible.

```json
{
  "index": "my-docs",
  "vpath": "docs/architecture.md"
}
```

### inject_knowledge_graph

Inject Entity nodes and RELATES_TO edges in a single atomic transaction.
Entity and relation rows are merged (upsert), so no duplicate entities are
created; each call still archives the prior version of every updated node in
`entity_archive` and re-embeds node content in the vector store, so repeating an
identical payload has additional side effects.

```json
{
  "index": "project-memory",
  "nodes": [
    {
      "id": "req-001",
      "type": "requirement",
      "content": "User must be able to log in with email and password",
      "attributes": {"priority": "high", "sprint": 3}
    },
    {
      "id": "feat-jwt",
      "type": "code_symbol",
      "content": "JWT authentication handler"
    }
  ],
  "edges": [
    {
      "from": "req-001",
      "to": "feat-jwt",
      "relation_type": "IMPLEMENTED_BY",
      "weight": 1.0
    }
  ]
}
```

**Node fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `id` | ✅ | Globally unique stable key (UUID or slug) |
| `type` | ✅ | Semantic category (e.g. `requirement`, `concept`, `code_symbol`) |
| `content` | optional | Natural-language text → triggers vector embedding |
| `attributes` | optional | Arbitrary JSON metadata |

**Edge fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `from` | ✅ | Source node ID |
| `to` | ✅ | Destination node ID |
| `relation_type` | ✅ | Edge label (e.g. `DEPENDS_ON`, `IMPLEMENTS`, `RELATED_TO`) |
| `weight` | optional | Numeric strength (default `1.0`) |
| `attributes` | optional | Arbitrary JSON metadata |

### delete_graph_entity

Remove an entity node and **all its incident RELATES_TO edges**:

```json
{
  "index": "project-memory",
  "id": "req-001"
}
```

### traverse_knowledge_graph

Walk the knowledge graph from a starting node up to `depth` hops.
Returns all reachable nodes and the edges connecting them.

```json
{
  "index": "project-memory",
  "start_id": "req-001",
  "depth": 3
}
```

**Example use case:** *"Which code symbols are linked to requirement req-001?"*
→ `start_id=req-001`, `depth=2`

The text result highlights the start node with `*` and lists edges as:
```
[*] id="req-001" type="requirement" content="User must be able to log in..."
[ ] id="feat-jwt" type="code_symbol"

=== Edges ===
  "req-001" -[IMPLEMENTED_BY]-> "feat-jwt" (weight=1.00)
```

---

### memory_remember

Store a fact in persistent long-term memory. Facts are injected into every
subsequent LLM query as part of the `<memory_context>` system message.

```json
{
  "content": "This project uses hexagonal architecture with ports and adapters",
  "tags": ["architecture", "convention"]
}
```

### memory_forget

Delete a memory block by ID or by content match:

```json
{
  "id_or_query": "hexagonal architecture"
}
```

### memory_search

Search across all memory tiers:

```json
{
  "query": "architecture"
}
```

### memory_list

List all memory blocks, optionally filtered by tier:

```json
{
  "tier": "long"
}
```

### memory_context

Returns the compiled memory context string — exactly what the LLM currently
receives as a system message. No arguments required. The call is a
non-destructive write: blocks whose files or symbols changed since they were
stored are flagged as suspect (and later shown in `<suspect_memory>`).

```json
{}
```

### gleann_batch_ask

Run multiple questions concurrently against a single index. Useful for agents
that need to explore a topic from several angles without sequential round-trips.

```json
{
  "questions": [
    "How does authentication work?",
    "What database is used?",
    "How are errors handled?"
  ],
  "index": "my-code",
  "top_k": 5,
  "concurrency": 3
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `questions` | array | *(required)* | 1–10 questions to answer |
| `index` | string | *(required)* | Target gleann index name |
| `top_k` | number | `5` | RAG context chunks per question |
| `concurrency` | number | `3` | Parallel question slots (max 5) |

**Response** (text): numbered Q/A pairs with per-question latency.

---

### gleann_search_ids

Token-efficient search. Returns a compact result list — no full passage text.

```json
{
  "index": "my-code",
  "query": "authentication handler",
  "top_k": 10
}
```

**Response fields per result:**

| Field | Description |
|-------|-------------|
| `ref` | Persistent citation key (`"indexname:42"`) — pass to `gleann_fetch` or `gleann_get` |
| `score` | Cosine similarity (0–1) |
| `source` | File path or document name |
| `type` | Chunk type (`text`, `code`, `heading`, …) |
| `peek` | First 120 characters of the passage |

Typical token savings: **5–10× vs `gleann_search`** because full passage text is omitted.

### gleann_fetch

Hydrate full passage content for one or more passage IDs returned by `gleann_search_ids`.

```json
{
  "ids": [42, 57],
  "index": "my-code"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `ids` | ✅ | Array of integer passage IDs from `gleann_search_ids` |
| `index` | optional | Override the index that `gleann_search_ids` used (rarely needed) |

### gleann_get

Citation lookup. Resolve a single `ref` to its full passage at any point in a
conversation — useful for sourcing a claim made earlier.

```json
{
  "ref": "my-code:42"
}
```

Returns the full passage text, source path, chunk type, and score metadata.

---

### gleann_session_start

Begin a named work session. All subsequent `gleann_search` and `gleann_ask`
calls are automatically logged to this session.

```json
{
  "name": "auth-refactor"
}
```

### gleann_session_end

Close the active session. Optionally record a summary of your own to long-term
memory. Closing also prunes expired short-term blocks.

```json
{
  "summary": "Reviewed JWT handler. Identified path-traversal risk in token parser."
}
```

Omit `summary` to close without a user-written note; the generated session line
(name, duration and event count) is still recorded.

### gleann_session_status

Show the active session name and the number of events logged so far.

```json
{}
```

---

## Resources

The MCP server also exposes resources:

| Resource | URI | Description |
|----------|-----|-------------|
| Index List | `gleann://indexes` | List of all indexes |
| File Content | `gleann://{index}/{file_path}` | Read a file from an index |

## Searcher Cache

The MCP server caches loaded searchers (up to 16) using LRU eviction. Frequently accessed indexes stay warm in memory for fast responses. In addition to the count cap, an optional memory budget can be set via `searcher_cache_budget` in `config.json` or the `GLEANN_SEARCHER_CACHE_BUDGET` environment variable (e.g. `500MB`, `1GB`, `100MiB`). When total estimated resident size exceeds the budget, least-recently-used searchers are retired until cache usage fits.

## Memory Engine Lifecycle

Each Memory Engine store is a namespace in the memory database (`~/.gleann/memory/memory.sqlite`).
The first request opens the database; it stays open until the MCP server shuts down.

```
Agent                       gleann MCP server            memory.sqlite
  |                               |                          |
  |── inject_knowledge_graph ─────>|                          |
  |   {nodes: [...], edges: [...]} |── BEGIN IMMEDIATE ──────>|
  |                               |── upsert entities ──────>|
  |                               |── upsert relations ─────>|
  |                               |── COMMIT ───────────────>|
  |<── "OK — injected 2 nodes..." |                          |
  |                               |                          |
  |── traverse_knowledge_graph ──>|                          |
  |   {start_id: "req-001", depth:2}── recursive SELECT ────>|
  |                               |<── nodes + edges ────────|
  |<── formatted text result ─────|                          |
```
