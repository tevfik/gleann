package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Platform represents a supported AI coding platform.
type Platform struct {
	Name        string
	Description string
	// Detect returns true when this platform is present in dir or the user home.
	Detect func(dir, home string) bool
	// Install writes integration files into dir.
	Install func(dir, home string) error
	// Uninstall removes integration files from dir.
	Uninstall func(dir, home string) error
}

var platforms = []Platform{
	{
		Name:        "opencode",
		Description: "OpenCode AI (tool.execute.before plugin + MCP config)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(dir, ".opencode"))
			if err == nil {
				return true
			}
			_, err = os.Stat(filepath.Join(home, ".opencode"))
			return err == nil
		},
		Install:   installOpenCode,
		Uninstall: uninstallOpenCode,
	},
	{
		Name:        "claude",
		Description: "Claude Code (CLAUDE.md + PreToolUse hook in ~/.claude/settings.json)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(home, ".claude"))
			if err == nil {
				return true
			}
			_, err = os.Stat(filepath.Join(dir, "CLAUDE.md"))
			return err == nil
		},
		Install:   installClaude,
		Uninstall: uninstallClaude,
	},
	{
		Name:        "cursor",
		Description: "Cursor IDE (.cursor/rules/gleann.mdc + MCP config)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(dir, ".cursor"))
			return err == nil
		},
		Install:   installCursor,
		Uninstall: uninstallCursor,
	},
	{
		Name:        "codex",
		Description: "OpenAI Codex CLI (AGENTS.md + .codex/hooks.json)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(dir, ".codex"))
			return err == nil
		},
		Install:   installCodex,
		Uninstall: uninstallCodex,
	},
	{
		Name:        "gemini",
		Description: "Gemini CLI (GEMINI.md + .gemini/settings.json hook)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(dir, ".gemini"))
			if err == nil {
				return true
			}
			_, err = os.Stat(filepath.Join(home, ".gemini"))
			return err == nil
		},
		Install:   installGemini,
		Uninstall: uninstallGemini,
	},
	{
		Name:        "claw",
		Description: "OpenClaw (~/.openclaw/skills/gleann/SKILL.md + AGENTS.md)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(home, ".openclaw"))
			return err == nil
		},
		Install:   installClaw,
		Uninstall: uninstallClaw,
	},
	{
		Name:        "aider",
		Description: "Aider (AGENTS.md + .aider.conf.yml hint)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(dir, ".aider.conf.yml"))
			if err == nil {
				return true
			}
			_, err = os.Stat(filepath.Join(dir, ".aider"))
			return err == nil
		},
		Install:   installAider,
		Uninstall: uninstallAider,
	},
	{
		Name:        "copilot",
		Description: "GitHub Copilot CLI (~/.copilot/skills/gleann/SKILL.md)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(home, ".copilot"))
			return err == nil
		},
		Install:   installCopilot,
		Uninstall: uninstallCopilot,
	},
}

func platformByName(name string) *Platform {
	for i := range platforms {
		if platforms[i].Name == name {
			return &platforms[i]
		}
	}
	return nil
}

// cmdInstall implements `gleann install [--platform <name>] [--dir <path>] [uninstall] [--list]`
func cmdInstall(args []string) {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		printInstallUsage()
		return
	}

	dir := getFlag(args, "--dir")
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot determine working directory: %v\n", err)
			os.Exit(1)
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine home directory: %v\n", err)
		os.Exit(1)
	}

	// gleann install --list
	if hasFlag(args, "--list") {
		fmt.Println("Supported platforms:")
		for _, p := range platforms {
			detected := ""
			if p.Detect(dir, home) {
				detected = "  ✓ detected"
			}
			fmt.Printf("  %-12s  %s%s\n", p.Name, p.Description, detected)
		}
		return
	}

	// Determine subcommand (install / uninstall).
	uninstalling := false
	for _, a := range args {
		if a == "uninstall" {
			uninstalling = true
			break
		}
	}

	platformName := getFlag(args, "--platform")

	// Auto-detect if no platform specified.
	var targets []*Platform
	if platformName != "" {
		p := platformByName(platformName)
		if p == nil {
			fmt.Fprintf(os.Stderr, "error: unknown platform %q\n", platformName)
			fmt.Fprintln(os.Stderr, "Run `gleann install --list` to see supported platforms.")
			os.Exit(1)
		}
		targets = []*Platform{p}
	} else {
		for i := range platforms {
			if platforms[i].Detect(dir, home) {
				targets = append(targets, &platforms[i])
			}
		}
		if len(targets) == 0 {
			fmt.Println("No supported AI platform detected in this directory.")
			fmt.Println("Specify one with: gleann install --platform <name>")
			fmt.Println("Run `gleann install --list` to see all supported platforms.")
			return
		}
	}

	for _, p := range targets {
		if uninstalling {
			fmt.Printf("🗑️  Uninstalling gleann from %s...\n", p.Name)
			if err := p.Uninstall(dir, home); err != nil {
				fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			} else {
				fmt.Printf("  ✓ Done\n")
			}
		} else {
			fmt.Printf("🔧 Installing gleann for %s...\n", p.Name)
			if err := p.Install(dir, home); err != nil {
				fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			} else {
				fmt.Printf("  ✓ Done\n")
			}
		}
	}
}

func printInstallUsage() {
	fmt.Print(`Usage: gleann install [flags] [uninstall]

Install or configure gleann integration for AI coding platforms.

Flags:
  --platform <name>   Target a specific platform (default: auto-detect)
  --dir <path>        Target directory (default: current directory)
  --list              List supported platforms and auto-detection status

Actions:
  gleann install                   Auto-detect & install all present platforms
  gleann install --platform X      Install for a specific platform X
  gleann install uninstall         Auto-detect & uninstall from all present platforms
  gleann install uninstall --platform X  Uninstall from platform X

Supported platforms:
`)
	for _, p := range platforms {
		fmt.Printf("  %-12s  %s\n", p.Name, p.Description)
	}
}

// ─── Shared content helpers ───────────────────────────────────────────────────

const agentsMDSection = `
## gleann: Code Intelligence, Search & Long-term Memory

This project uses [gleann](https://github.com/tevfik/gleann) for AI-powered codebase
navigation and persistent cross-session memory.

### 1 — Before exploring source files

Read **GRAPH_REPORT.md** (if present) — contains god nodes (high-degree hub symbols),
community structure, and cross-cutting dependency edges.
Generate it with: ` + "`gleann graph report --index <name>`" + `

Key graph / search commands:
- ` + "`gleann search <name> <query>`" + ` — semantic search
- ` + "`gleann search idx1,idx2 <query>`" + ` — multi-index search (comma-separated)
- ` + "`gleann search --all <query>`" + ` — search across all indexes
- ` + "`gleann search <name> <query> --rerank`" + ` — add cross-encoder reranking
- ` + "`gleann ask <name> <question>`" + ` — RAG-powered Q&A from indexed content
- ` + "`gleann index list`" + ` — list available indexes
- ` + "`gleann index watch <name> --docs <dir>`" + ` — auto-rebuild on file changes
- ` + "`gleann graph explain <symbol> --index <name>`" + ` — callers, callees, blast radius
- ` + "`gleann graph query <pattern> --index <name>`" + ` — find symbols by pattern
- ` + "`gleann graph path <from> <to> --index <name>`" + ` — shortest dependency path

### 2 — Long-term memory (always ON)

gleann maintains **persistent, tiered memory** that survives across sessions.  Use it
actively to build up institutional knowledge about this codebase:

| Tier | Lifetime | Use for |
|------|----------|---------|
| **short** | session only | current task state, transient notes |
| **medium** | days / sprints | sprint goals, active decisions, pending TODOs |
| **long** | permanent | architecture decisions, conventions, team contacts |

**CLI — store & retrieve**
` + "```bash" + `
gleann memory remember "Hexagonal architecture: adapters live in internal/adapters"
gleann memory remember "DB columns use snake_case" --tag "convention"
gleann memory add short "Current task: refactor auth module"
gleann memory add medium "Sprint 14: focus on latency improvements"
gleann memory search "architecture"
gleann memory list --tier long
gleann memory stats
` + "```" + `

**CLI — rotation & housekeeping**
` + "```bash" + `
gleann memory summarize --last           # compress last conversation → long-term memory
gleann memory summarize --last --extract # also extract individual facts into blocks
gleann memory prune --age 90d            # remove entries older than 90 days
gleann memory forget "outdated fact"     # remove by content match or block ID
gleann memory clear --tier short         # wipe session-tier blocks
` + "```" + `

**Sleep-time engine** (Letta-inspired) — enable with ` + "`GLEANN_SLEEPTIME_ENABLED=1`" + `
to run a background goroutine that automatically reflects on recent conversations,
extracts facts, resolves contradictions, and promotes important information across tiers.

### 3 — MCP tools (when gleann mcp is running)

**Search & graph:**
` + "`gleann_search`" + ` · ` + "`gleann_search_multi`" + ` · ` + "`gleann_ask`" + ` · ` + "`gleann_graph_neighbors`" + ` · ` + "`gleann_impact`" + `
` + "`inject_knowledge_graph`" + ` · ` + "`delete_graph_entity`" + ` · ` + "`traverse_knowledge_graph`" + `

**Memory (always-available, no build tag):**
- ` + "`memory_remember`" + ` — store fact with tier/label/tags/scope
- ` + "`memory_forget`" + ` — remove block by ID or content match
- ` + "`memory_search`" + ` — full-text search across all tiers
- ` + "`memory_list`" + ` — browse blocks, filter by tier
- ` + "`memory_context`" + ` — returns the compiled ` + "`<memory_context>`" + ` window that gleann
  injects into LLM system prompts — call this at session start to recall everything

**Workflow:** call ` + "`memory_context`" + ` at the start of every session, then call
` + "`memory_remember`" + ` whenever you learn something important about the codebase.
`

const skillMDContent = `# gleann: Code Intelligence, Search & Memory

## When to use
Always call ` + "`memory_context`" + ` (MCP) or ` + "`gleann memory list`" + ` at session start to recall
persistent facts about this codebase.  Use gleann graph tools before exploring source code.

## Session start checklist
1. ` + "`memory_context`" + ` — load compiled memory window (all tiers)
2. If ` + "`GRAPH_REPORT.md`" + ` exists — read it (god nodes, communities)
3. ` + "`gleann index list`" + ` — check available indexes

## Long-term memory

### Tiers
| Tier | Lifetime | Use for |
|------|----------|---------|
| **short** | session | current task, transient notes |
| **medium** | days/sprints | active decisions, sprint goals |
| **long** | permanent | architecture, conventions, team |

### MCP tools
` + "```" + `
memory_context              → compiled <memory_context> window for LLM injection
memory_remember content=... tier=long|medium|short label=... tags=[...]
memory_forget   id_or_query=...
memory_search   query=...
memory_list     tier=long|medium|short  (optional filter)
` + "```" + `

### CLI equivalents
` + "```bash" + `
gleann memory remember "fact"               # store in long-term (default)
gleann memory remember "fact" --tag "arch"  # with tag
gleann memory add short "current task: ..."
gleann memory add medium "sprint goal: ..."
gleann memory search "architecture"
gleann memory list --tier long
gleann memory forget "outdated fact"
gleann memory stats
` + "```" + `

### Rotation & housekeeping
` + "```bash" + `
gleann memory summarize --last            # compress last conversation → memory
gleann memory summarize --last --extract  # also extract individual facts
gleann memory prune --age 90d             # remove entries older than 90 days
gleann memory clear --tier short          # wipe session-tier blocks
` + "```" + `

Enable background sleep-time consolidation (Letta-inspired):
` + "```bash" + `
export GLEANN_SLEEPTIME_ENABLED=1
export GLEANN_SLEEPTIME_INTERVAL=30m   # default
gleann serve                           # engine runs in background
` + "```" + `

## Knowledge graph

### Search
` + "```bash" + `
gleann search <index> "query string"              # semantic search
gleann search idx1,idx2 "query"                   # multi-index (comma-separated)
gleann search --all "query"                       # across all indexes
gleann search <index> "query" --rerank            # + cross-encoder reranking
gleann ask    <index> "natural language Q"        # RAG-powered Q&A
gleann index watch <name> --docs ./src/           # auto-rebuild on file changes
` + "```" + `

### Graph traversal
` + "```bash" + `
gleann graph explain  <symbol> --index <name>       # callers, callees, blast radius
gleann graph query    <pattern> --index <name>       # find matching symbols
gleann graph path     <from> <to> --index <name>     # shortest dependency path
gleann graph report   --index <name>                 # generate GRAPH_REPORT.md
gleann graph viz      --index <name>                 # interactive HTML graph
` + "```" + `

### OpenAI-compatible proxy
` + "```python" + `
# gleann serve → exposes /v1/chat/completions
from openai import OpenAI
client = OpenAI(base_url="http://localhost:8080/v1", api_key="none")
r = client.chat.completions.create(
    model="gleann/<index-name>",
    messages=[{"role": "user", "content": "How does auth work?"}]
)
` + "```" + `

### A2A (Agent-to-Agent) HTTP API
` + "```bash" + `
# gleann serve also exposes the Google A2A protocol
curl -X POST http://localhost:8080/a2a/v1/message:send \
  -H 'Content-Type: application/json' \
  -d '{"message":{"role":"user","parts":[{"text":"explain validateToken"}]}}'
` + "```" + `

### MCP graph tools
` + "`gleann_search`" + ` · ` + "`gleann_ask`" + ` · ` + "`gleann_graph_neighbors`" + ` · ` + "`gleann_impact`" + `
` + "`inject_knowledge_graph`" + ` · ` + "`traverse_knowledge_graph`" + `
`

// appendOrCreateFile appends content to a file, creating it if absent.
// It avoids duplicate sections by checking for the sentinel.
func appendOrCreateFile(path, content, sentinel string) error {
	existing, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(existing), sentinel) {
		fmt.Printf("  (already configured in %s)\n", filepath.Base(path))
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

// removeSection removes the gleann section from a file identified by start/end sentinels.
func removeSection(path, startSentinel, endSentinel string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil // file doesn't exist — nothing to do
	}
	s := string(data)
	start := strings.Index(s, startSentinel)
	if start < 0 {
		return nil // not present
	}
	end := strings.Index(s[start:], endSentinel)
	if end < 0 {
		// Remove from start to end of file.
		s = s[:start]
	} else {
		s = s[:start] + s[start+end+len(endSentinel):]
	}
	return os.WriteFile(path, []byte(strings.TrimRight(s, "\n")+"\n"), 0o644)
}

// ─── OpenCode ─────────────────────────────────────────────────────────────────

const openCodePlugin = `// gleann plugin for OpenCode
// Auto-generated by: gleann install --platform opencode
// Docs: https://opencode.ai/docs/plugins/
import fs from "fs";
import { spawnSync } from "child_process";

function getGleannContext() {
  const parts = [];

  // ── Knowledge graph summary ─────────────────────────────────────────────
  if (fs.existsSync("GRAPH_REPORT.md")) {
    const content = fs.readFileSync("GRAPH_REPORT.md", "utf8");
    // First 2000 chars covers the summary + god nodes section
    parts.push("## gleann Knowledge Graph\n\n" + content.slice(0, 2000));
  } else {
    parts.push(
      "## gleann\nNo GRAPH_REPORT.md found. " +
      "Run: gleann graph report --index <name>  to generate it."
    );
  }

  // ── Long-term memory ────────────────────────────────────────────────────
  try {
    const r = spawnSync(
      "gleann",
      ["memory", "list", "--json"],
      { encoding: "utf8", timeout: 3000 }
    );
    if (r.status === 0 && r.stdout.trim()) {
      const blocks = JSON.parse(r.stdout);
      if (blocks && blocks.length > 0) {
        const lines = blocks
          .map((b) => "- [" + b.tier + "] " + b.content)
          .join("\n");
        parts.push(
          "## gleann Memory (" + blocks.length + " blocks)\n\n" + lines
        );
      }
    }
  } catch (_) { /* gleann not installed or memory empty — silently skip */ }

  return parts.join("\n\n");
}

// GleannPlugin hooks into OpenCode's event system.
// See: https://opencode.ai/docs/plugins/#events
export const GleannPlugin = async ({ project, client, $, directory, worktree }) => {
  return {
    // Inject gleann context whenever the session is compacted.
    // This keeps the LLM aware of the knowledge graph and persistent memory
    // even after long sessions where the context window is rolled over.
    "experimental.session.compacting": async (input, output) => {
      const ctx = getGleannContext();
      if (ctx) {
        output.context.push(ctx);
      }
    },
  };
};
`

func installOpenCode(dir, home string) error {
	// 1. AGENTS.md
	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := appendOrCreateFile(agentsPath, agentsMDSection, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	fmt.Printf("  • %s\n", agentsPath)

	// 2. .opencode/plugins/gleann.js — auto-loaded by OpenCode at startup.
	pluginDir := filepath.Join(dir, ".opencode", "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return fmt.Errorf("creating plugin dir: %w", err)
	}
	pluginPath := filepath.Join(pluginDir, "gleann.js")
	if err := os.WriteFile(pluginPath, []byte(openCodePlugin), 0o644); err != nil {
		return fmt.Errorf("writing plugin: %w", err)
	}
	fmt.Printf("  • %s  (auto-loaded, no registration needed)\n", pluginPath)

	// 3. opencode.json — MCP server config (gleann mcp).
	// Local plugin files in .opencode/plugins/ are auto-loaded; only MCP servers
	// need to be registered in opencode.json under the "mcp" key.
	ocConfigPath := filepath.Join(dir, "opencode.json")
	if err := patchOpenCodeJSON(ocConfigPath); err != nil {
		return fmt.Errorf("patching opencode.json: %w", err)
	}
	fmt.Printf("  • %s  (mcp.gleann registered)\n", ocConfigPath)

	return nil
}

func patchOpenCodeJSON(path string) error {
	// Read or initialise config.
	var config map[string]interface{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("invalid JSON in %s: %w", path, err)
		}
	} else {
		// New file — start with $schema
		config = map[string]interface{}{
			"$schema": "https://opencode.ai/config.json",
		}
	}

	// Clean up any legacy gleann entry under "plugins" from old installs.
	if plugins, ok := config["plugins"].(map[string]interface{}); ok {
		delete(plugins, "gleann")
		if len(plugins) == 0 {
			delete(config, "plugins")
		} else {
			config["plugins"] = plugins
		}
	}

	// Ensure mcp key exists.
	mcpSection, _ := config["mcp"].(map[string]interface{})
	if mcpSection == nil {
		mcpSection = make(map[string]interface{})
	}
	// Idempotent: skip if already registered.
	if _, exists := mcpSection["gleann"]; exists {
		fmt.Printf("  (gleann mcp already in %s)\n", filepath.Base(path))
		return nil
	}
	mcpSection["gleann"] = map[string]interface{}{
		"type":    "local",
		"command": []interface{}{"gleann", "mcp"},
		"enabled": true,
	}
	config["mcp"] = mcpSection

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

func uninstallOpenCode(dir, home string) error {
	// Remove plugin file.
	pluginPath := filepath.Join(dir, ".opencode", "plugins", "gleann.js")
	_ = os.Remove(pluginPath)

	// Deregister mcp.gleann from opencode.json.
	ocConfigPath := filepath.Join(dir, "opencode.json")
	if data, err := os.ReadFile(ocConfigPath); err == nil {
		var config map[string]interface{}
		if json.Unmarshal(data, &config) == nil {
			if mcpSection, ok := config["mcp"].(map[string]interface{}); ok {
				delete(mcpSection, "gleann")
				config["mcp"] = mcpSection
				if out, err := json.MarshalIndent(config, "", "  "); err == nil {
					_ = os.WriteFile(ocConfigPath, append(out, '\n'), 0o644)
				}
			}
		}
	}

	// Remove AGENTS.md section.
	agentsPath := filepath.Join(dir, "AGENTS.md")
	return removeSection(agentsPath, "## gleann: Code Intelligence", "\n---")
}

// ─── Claude Code ──────────────────────────────────────────────────────────────

const claudeMDSection = `
<!-- gleann: Code Intelligence & Search -->
## gleann knowledge graph

Run ` + "`gleann graph report --index <name>`" + ` to generate GRAPH_REPORT.md, then read it before
searching source files.  Key commands:
- ` + "`gleann graph explain <symbol> --index <name>`" + `  — callers, callees, blast radius
- ` + "`gleann graph query <pattern> --index <name>`" + `   — symbol search
- ` + "`gleann graph path <from> <to> --index <name>`" + ` — dependency path
- ` + "`gleann search <name> <query>`" + `                  — semantic search
<!-- /gleann -->
`

const claudeHookScript = `if [ -f "GRAPH_REPORT.md" ]; then echo "[gleann] GRAPH_REPORT.md found — read it before searching source files." >&2; fi; gleann memory list 2>/dev/null | head -20 >&2`

func installClaude(dir, home string) error {
	// 1. CLAUDE.md
	claudePath := filepath.Join(dir, "CLAUDE.md")
	if err := appendOrCreateFile(claudePath, claudeMDSection, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing CLAUDE.md: %w", err)
	}
	fmt.Printf("  • %s\n", claudePath)

	// 2. ~/.claude/settings.json — PreToolUse hook.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		return fmt.Errorf("creating ~/.claude: %w", err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := patchClaudeSettings(settingsPath); err != nil {
		return fmt.Errorf("patching ~/.claude/settings.json: %w", err)
	}
	fmt.Printf("  • %s  (PreToolUse hook added)\n", settingsPath)
	return nil
}

func patchClaudeSettings(path string) error {
	var settings map[string]interface{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("invalid JSON in %s: %w", path, err)
		}
	} else {
		settings = make(map[string]interface{})
	}

	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}

	preToolUse, _ := hooks["PreToolUse"].([]interface{})
	// Check if gleann hook already present.
	for _, h := range preToolUse {
		if m, ok := h.(map[string]interface{}); ok {
			if hooks2, ok := m["hooks"].([]interface{}); ok {
				for _, h2 := range hooks2 {
					if m2, ok := h2.(map[string]interface{}); ok {
						if m2["command"] == claudeHookScript {
							fmt.Printf("  (gleann hook already in %s)\n", path)
							return nil
						}
					}
				}
			}
		}
	}

	gleannHook := map[string]interface{}{
		"matcher": "Glob|Grep|Read|LS",
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": claudeHookScript,
			},
		},
	}
	preToolUse = append(preToolUse, gleannHook)
	hooks["PreToolUse"] = preToolUse
	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}

func uninstallClaude(dir, home string) error {
	claudePath := filepath.Join(dir, "CLAUDE.md")
	if err := removeSection(claudePath, "<!-- gleann:", "<!-- /gleann -->"); err != nil {
		return err
	}
	// Remove hook from ~/.claude/settings.json.
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if data, err := os.ReadFile(settingsPath); err == nil {
		var settings map[string]interface{}
		if json.Unmarshal(data, &settings) == nil {
			if hooks, ok := settings["hooks"].(map[string]interface{}); ok {
				if preToolUse, ok := hooks["PreToolUse"].([]interface{}); ok {
					var filtered []interface{}
					for _, h := range preToolUse {
						if m, ok := h.(map[string]interface{}); ok {
							if hooks2, ok := m["hooks"].([]interface{}); ok {
								isGleann := false
								for _, h2 := range hooks2 {
									if m2, ok := h2.(map[string]interface{}); ok && m2["command"] == claudeHookScript {
										isGleann = true
									}
								}
								if isGleann {
									continue
								}
							}
						}
						filtered = append(filtered, h)
					}
					hooks["PreToolUse"] = filtered
					settings["hooks"] = hooks
					if out, err := json.MarshalIndent(settings, "", "  "); err == nil {
						_ = os.WriteFile(settingsPath, append(out, '\n'), 0o600)
					}
				}
			}
		}
	}
	return nil
}

// ─── Cursor ───────────────────────────────────────────────────────────────────

const cursorRulesContent = `---
alwaysApply: true
---
# gleann: Code Intelligence

This project uses gleann for AI-powered codebase navigation.

## Before exploring source files

1. Check **GRAPH_REPORT.md** for god nodes and community structure.
   Generate it with: ` + "`gleann graph report --index <name>`" + `

2. Key commands:
   - ` + "`gleann graph explain <symbol> --index <name>`" + ` — callers, callees, blast radius
   - ` + "`gleann graph query <pattern> --index <name>`" + ` — find symbols by pattern
   - ` + "`gleann graph path <from> <to> --index <name>`" + ` — dependency path
   - ` + "`gleann search <name> <query>`" + ` — semantic search
   - ` + "`gleann ask <name> <question>`" + ` — RAG-powered Q&A

## MCP integration

Add to ` + "`.cursor/mcp.json`" + `:
` + "```json" + `
{
  "mcpServers": {
    "gleann": { "type": "stdio", "command": "gleann", "args": ["mcp"] }
  }
}
` + "```" + `
`

func installCursor(dir, home string) error {
	rulesDir := filepath.Join(dir, ".cursor", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		return fmt.Errorf("creating .cursor/rules: %w", err)
	}
	rulesPath := filepath.Join(rulesDir, "gleann.mdc")
	if err := os.WriteFile(rulesPath, []byte(cursorRulesContent), 0o644); err != nil {
		return fmt.Errorf("writing rules file: %w", err)
	}
	fmt.Printf("  • %s  (alwaysApply: true)\n", rulesPath)

	// Write .cursor/mcp.json with correct Cursor MCP format.
	mcpPath := filepath.Join(dir, ".cursor", "mcp.json")
	cursorMCP := `{
  "mcpServers": {
    "gleann": {
      "command": "gleann",
      "args": ["mcp"]
    }
  }
}
`
	if err := writeJSONIfAbsent(mcpPath, cursorMCP); err != nil {
		return fmt.Errorf("writing mcp.json: %w", err)
	}
	fmt.Printf("  • %s\n", mcpPath)
	return nil
}

func uninstallCursor(dir, home string) error {
	_ = os.Remove(filepath.Join(dir, ".cursor", "rules", "gleann.mdc"))
	_ = os.Remove(filepath.Join(dir, ".cursor", "mcp.json"))
	return nil
}

// ─── Codex ────────────────────────────────────────────────────────────────────

const codexHooksContent = `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "command": "if [ -f GRAPH_REPORT.md ]; then echo '[gleann] GRAPH_REPORT.md exists. Check god nodes before exploring files.' >&2; fi"
      }
    ]
  }
}
`

func installCodex(dir, home string) error {
	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := appendOrCreateFile(agentsPath, agentsMDSection, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	fmt.Printf("  • %s\n", agentsPath)

	codexDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		return fmt.Errorf("creating .codex: %w", err)
	}
	hooksPath := filepath.Join(codexDir, "hooks.json")
	if err := writeJSONIfAbsent(hooksPath, codexHooksContent); err != nil {
		return fmt.Errorf("writing hooks.json: %w", err)
	}
	fmt.Printf("  • %s\n", hooksPath)
	return nil
}

func uninstallCodex(dir, home string) error {
	_ = os.Remove(filepath.Join(dir, ".codex", "hooks.json"))
	return removeSection(filepath.Join(dir, "AGENTS.md"), "## gleann: Code Intelligence", "\n---")
}

// ─── Gemini CLI ───────────────────────────────────────────────────────────────

const geminiMDSection = `
<!-- gleann -->
## gleann knowledge graph

Read GRAPH_REPORT.md before using file-search tools. Key commands:
- ` + "`gleann graph explain <symbol> --index <name>`" + `
- ` + "`gleann graph query <pattern> --index <name>`" + `
- ` + "`gleann search <name> <query>`" + `
<!-- /gleann -->
`

const geminiHook = `if [ -f GRAPH_REPORT.md ]; then echo '[gleann] GRAPH_REPORT.md found — review it before searching.' >&2; fi`

func installGemini(dir, home string) error {
	// GEMINI.md
	geminiMDPath := filepath.Join(dir, "GEMINI.md")
	if err := appendOrCreateFile(geminiMDPath, geminiMDSection, "<!-- gleann -->"); err != nil {
		return fmt.Errorf("writing GEMINI.md: %w", err)
	}
	fmt.Printf("  • %s\n", geminiMDPath)

	// .gemini/settings.json
	geminiDir := filepath.Join(dir, ".gemini")
	if err := os.MkdirAll(geminiDir, 0o755); err != nil {
		return fmt.Errorf("creating .gemini: %w", err)
	}
	settingsPath := filepath.Join(geminiDir, "settings.json")
	if err := patchGeminiSettings(settingsPath); err != nil {
		return fmt.Errorf("patching .gemini/settings.json: %w", err)
	}
	fmt.Printf("  • %s  (BeforeTool hook added)\n", settingsPath)
	return nil
}

func patchGeminiSettings(path string) error {
	var settings map[string]interface{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
	} else {
		settings = make(map[string]interface{})
	}

	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}
	if hooks["gleann"] != nil {
		fmt.Printf("  (gleann hook already in %s)\n", path)
		return nil
	}
	hooks["gleann"] = map[string]interface{}{
		"event":   "BeforeTool",
		"matcher": "read_file|list_files|search_files",
		"command": geminiHook,
	}
	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

func uninstallGemini(dir, home string) error {
	_ = removeSection(filepath.Join(dir, "GEMINI.md"), "<!-- gleann -->", "<!-- /gleann -->")
	settingsPath := filepath.Join(dir, ".gemini", "settings.json")
	if data, err := os.ReadFile(settingsPath); err == nil {
		var settings map[string]interface{}
		if json.Unmarshal(data, &settings) == nil {
			if hooks, ok := settings["hooks"].(map[string]interface{}); ok {
				delete(hooks, "gleann")
				settings["hooks"] = hooks
				if out, err := json.MarshalIndent(settings, "", "  "); err == nil {
					_ = os.WriteFile(settingsPath, append(out, '\n'), 0o644)
				}
			}
		}
	}
	return nil
}

// ─── OpenClaw ─────────────────────────────────────────────────────────────────

func installClaw(dir, home string) error {
	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := appendOrCreateFile(agentsPath, agentsMDSection, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	fmt.Printf("  • %s\n", agentsPath)

	skillDir := filepath.Join(home, ".openclaw", "skills", "gleann")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("creating skill dir: %w", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(skillMDContent), 0o644); err != nil {
		return fmt.Errorf("writing SKILL.md: %w", err)
	}
	fmt.Printf("  • %s\n", skillPath)
	return nil
}

func uninstallClaw(dir, home string) error {
	skillPath := filepath.Join(home, ".openclaw", "skills", "gleann", "SKILL.md")
	_ = os.Remove(skillPath)
	return removeSection(filepath.Join(dir, "AGENTS.md"), "## gleann: Code Intelligence", "\n---")
}

// ─── Aider ────────────────────────────────────────────────────────────────────

func installAider(dir, home string) error {
	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := appendOrCreateFile(agentsPath, agentsMDSection, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	fmt.Printf("  • %s\n", agentsPath)
	return nil
}

func uninstallAider(dir, home string) error {
	return removeSection(filepath.Join(dir, "AGENTS.md"), "## gleann: Code Intelligence", "\n---")
}

// ─── GitHub Copilot CLI ───────────────────────────────────────────────────────

func installCopilot(dir, home string) error {
	skillDir := filepath.Join(home, ".copilot", "skills", "gleann")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("creating skill dir: %w", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(skillMDContent), 0o644); err != nil {
		return fmt.Errorf("writing SKILL.md: %w", err)
	}
	fmt.Printf("  • %s\n", skillPath)
	return nil
}

func uninstallCopilot(dir, home string) error {
	return os.Remove(filepath.Join(home, ".copilot", "skills", "gleann", "SKILL.md"))
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// writeJSONIfAbsent writes content to path only if the file does not already exist.
func writeJSONIfAbsent(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("  (already exists: %s)\n", filepath.Base(path))
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
