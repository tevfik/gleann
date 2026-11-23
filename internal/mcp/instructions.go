package mcp

import (
	"regexp"
	"strings"
)

// The MCP initialize response carries server instructions. Clients that surface
// them (Claude Code, Cursor, OpenCode, ...) show this text to the model before
// the first tool call, so it is the one place to teach the routing rules that
// the individual tool descriptions cannot state globally.
//
// The text is profile-aware: it only recommends tools the server actually
// registers, because a `core` client that is told to call gleann_fetch would
// get "tool not found" (TestMCP06_InitializeInstructions enforces this).
const instructionsIntro = `gleann is a local code-intelligence, search and long-term-memory server.
Its index combines semantic + lexical vectors with an AST code graph; all data
stays local.

Before you reach for grep, find or cat, pick the right gleann tool:
`

// joinRuleList renders ["a","b","c"] as "a, b and c".
func joinRuleList(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}

// serverInstructionsFor returns the initialize instructions for the resolved
// tools profile (Config.ToolsProfile, or GLEANN_TOOLS, or "core"). Every tool
// name it emits is registered for that profile.
func serverInstructionsFor(profile string) string {
	enabled := func(name string) bool { return toolEnabledForProfile(profile, name) }

	search, ids, fetch := enabled("gleann_search"), enabled("gleann_search_ids"), enabled("gleann_fetch")

	var rules []string
	add := func(rule string) { rules = append(rules, rule) }

	// ── Search strategy ───────────────────────────────────────────────
	switch {
	case search && ids && fetch:
		add(`- Conceptual, behavioral or cross-file questions ("how does auth work", "where
  are failed charges retried") → gleann_search (hybrid search), or the cheaper
  gleann_search_ids → gleann_fetch two-step. Prefer this over grepping for
  keywords: it matches meaning and returns ranked passages with source paths.`)
	case search && ids:
		add(`- Conceptual, behavioral or cross-file questions ("how does auth work", "where
  are failed charges retried") → gleann_search (hybrid search) for full passages,
  or gleann_search_ids for ranked IDs and peeks only. Prefer these over grepping
  for keywords: they match meaning, not just literals.`)
	case search:
		add(`- Conceptual, behavioral or cross-file questions ("how does auth work", "where
  are failed charges retried") → gleann_search (hybrid search). Prefer this over
  grepping for keywords: it matches meaning and returns ranked passages with
  source paths.`)
	case ids && fetch:
		add(`- Conceptual, behavioral or cross-file questions ("how does auth work", "where
  are failed charges retried") → gleann_search_ids, then gleann_fetch for the
  passages you need. Prefer these over grepping for keywords: they match meaning,
  not just literals.`)
	case ids:
		add(`- Conceptual, behavioral or cross-file questions ("how does auth work", "where
  are failed charges retried") → gleann_search_ids, which returns ranked IDs and
  peeks. Prefer this over grepping for keywords: it matches meaning, not just
  literals.`)
	}
	if search || ids || enabled("gleann_search_multi") {
		add(`- Exact literal, regex or identifier occurrences are the one case where plain
  grep/ripgrep is better; use gleann afterwards to understand the hits.`)
	}

	// ── Definitions and the code graph ────────────────────────────────
	if enabled("gleann_def") {
		add(`- You know a symbol name (TokenManager, OpenStore, handleSync) →
  gleann_def for its declaration/file:line/signature; it does not guess like
  grep "func X" does.`)
	}
	graphTools := []struct{ name, what string }{
		{"gleann_graph_neighbors", "who calls / what it calls"},
		{"gleann_impact", "blast radius"},
		{"gleann_navigate_symbol", "resolve a symbol name to its declaration"},
		{"gleann_repo_map", "ranked repository outline"},
		{"gleann_communities", "module clusters"},
		{"gleann_risk_analysis", "churn × complexity risk"},
	}
	var graphParts []string
	for _, g := range graphTools {
		if enabled(g.name) {
			graphParts = append(graphParts, g.name+" ("+g.what+")")
		}
	}
	if len(graphParts) > 0 {
		add("- Callers, callees, dependencies or change impact → the AST graph tools:\n  " +
			joinRuleList(graphParts) + ". These follow real code edges across files, unlike a\n  text search.")
	}

	// ── Reading files and documents ───────────────────────────────────
	if enabled("gleann_read") {
		line := "- Read source with gleann_read (mode=signatures|map|task|lines|auto) instead of\n  dumping whole files"
		var docs []string
		if enabled("gleann_document_toc") {
			docs = append(docs, "gleann_document_toc")
		}
		if enabled("gleann_read_full_document") {
			docs = append(docs, "gleann_read_full_document")
		}
		if len(docs) > 0 {
			line += "; " + joinRuleList(docs) + " cover indexed documents"
		}
		add(line + ".")
	}

	// ── Persistent memory ─────────────────────────────────────────────
	if enabled("memory_search") || enabled("memory_context") {
		var b strings.Builder
		b.WriteString("- Persistent project facts, conventions and lessons → ")
		switch {
		case enabled("memory_search") && enabled("memory_context"):
			b.WriteString("memory_search and memory_context before starting")
		case enabled("memory_search"):
			b.WriteString("memory_search before starting")
		default:
			b.WriteString("memory_context before starting")
		}
		if enabled("memory_remember") {
			b.WriteString("; memory_remember to store a durable fact")
		}
		if enabled("memory_failure_add") {
			b.WriteString("; memory_failure_add after fixing a failing command so the fix is recalled")
		}
		b.WriteString(".")
		switch {
		case enabled("memory_remember") && enabled("memory_search"):
			b.WriteString("\n  Deduplicate with memory_search before remembering")
			if enabled("memory_forget") {
				b.WriteString("; memory_forget removes a block by ID or content match")
			}
			b.WriteString(".")
		case enabled("memory_forget"):
			b.WriteString("\n  memory_forget removes a memory block by ID or content match.")
		}
		add(b.String())
	} else if enabled("memory_forget") {
		add("- memory_forget removes a memory block by ID or content match.")
	}

	// ── Sessions, batches and answers ─────────────────────────────────
	var sessions []string
	if enabled("gleann_session_start") {
		sessions = append(sessions, "gleann_session_start to open a named session")
	}
	if enabled("gleann_session_end") {
		sessions = append(sessions, "gleann_session_end to close it with an optional summary")
	}
	if enabled("gleann_session_status") {
		sessions = append(sessions, "gleann_session_status to see the active session")
	}
	if len(sessions) > 0 {
		add("- Long-running work → " + joinRuleList(sessions) + ".")
	}
	if enabled("gleann_batch_ask") {
		add("- Several questions about one index in one round trip → gleann_batch_ask.")
	}
	if enabled("gleann_ask") {
		add("- A synthesized, cited answer instead of raw passages → gleann_ask.")
	}

	// ── Token-saving helpers ──────────────────────────────────────────
	if enabled("gleann_shell") {
		add("- Noisy CLI output (git, go test, npm, docker) → gleann_shell compresses it\n  before it enters context.")
	}
	if enabled("gleann_gain") {
		var sources []string
		if enabled("gleann_shell") {
			sources = append(sources, "gleann_shell")
		}
		if enabled("gleann_read") {
			sources = append(sources, "gleann_read")
		}
		if len(sources) > 0 {
			add("- gleann_gain reports the cumulative token savings of " + joinRuleList(sources) +
				" calls\n  since the server started (or the last reset).")
		} else {
			add("- gleann_gain reports the server's cumulative token savings since it started\n  (or the last reset).")
		}
	}

	// ── Keeping the index fresh ───────────────────────────────────────
	if enabled("gleann_sync") {
		add("- After editing files run gleann_sync so search, definitions and the graph\n  reflect the new code.")
	}

	// ── A typical loop, built only from registered tools ──────────────
	var steps []string
	if enabled("memory_context") {
		steps = append(steps, "memory_context")
	} else if enabled("memory_search") {
		steps = append(steps, "memory_search")
	}
	switch {
	case search && ids && fetch:
		steps = append(steps, "gleann_search or gleann_search_ids → gleann_fetch")
	case search && ids:
		steps = append(steps, "gleann_search or gleann_search_ids")
	case search:
		steps = append(steps, "gleann_search")
	case ids && fetch:
		steps = append(steps, "gleann_search_ids → gleann_fetch")
	case ids:
		steps = append(steps, "gleann_search_ids")
	}
	if enabled("gleann_read") {
		steps = append(steps, "gleann_read")
	}
	if enabled("gleann_def") {
		steps = append(steps, "gleann_def")
	}
	switch {
	case enabled("gleann_graph_neighbors") && enabled("gleann_impact"):
		steps = append(steps, "gleann_graph_neighbors or gleann_impact")
	case enabled("gleann_graph_neighbors"):
		steps = append(steps, "gleann_graph_neighbors")
	case enabled("gleann_impact"):
		steps = append(steps, "gleann_impact")
	}
	if enabled("memory_remember") {
		steps = append(steps, "memory_remember if you learned something durable")
	}
	if len(steps) >= 3 {
		add("A typical loop is: " + strings.Join(steps, " → ") + ".")
	}

	var b strings.Builder
	b.WriteString(instructionsIntro)
	for _, rule := range rules {
		b.WriteString(rule)
		b.WriteString("\n")
	}

	switch strings.TrimSpace(strings.ToLower(profile)) {
	case "", "core":
		b.WriteString("\nThis server runs the \"core\" tool profile, which registers exactly the tools\n" +
			"named above. Start gleann with --tools full for the progressive fetch, document,\n" +
			"session, batch and analysis extras.")
	case "full", "all":
		b.WriteString("\nThis server runs the \"full\" tool profile, so every tool gleann supports\n" +
			"(including the progressive, document, session, batch and analysis extras) is\n" +
			"registered.")
	default:
		// A custom profile is an arbitrary subset, and the rules above only
		// cover the common routing paths. Name the rest explicitly so the
		// closing note stays true for every valid --tools subset.
		mentioned := map[string]bool{}
		for _, name := range instructionToolName.FindAllString(b.String(), -1) {
			mentioned[name] = true
		}
		var extras []string
		for _, name := range allToolNames() {
			if enabled(name) && !mentioned[name] {
				extras = append(extras, name)
			}
		}
		if len(extras) > 0 {
			b.WriteString("\nAdditional tools enabled in this custom profile: " +
				joinRuleList(extras) + ".\n")
		}
		b.WriteString("\nThis server registers a custom tool profile; only the tools named above are\n" +
			"available.")
	}
	return b.String()
}

// instructionToolName matches the tool names the rule text can mention. The KG
// tools have no gleann_/memory_ prefix, so they are listed explicitly.
var instructionToolName = regexp.MustCompile(`\b(?:(?:gleann|memory)_[a-z0-9_]+|inject_knowledge_graph|delete_graph_entity|traverse_knowledge_graph)\b`)
