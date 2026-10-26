package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
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
	// InstallUser / UninstallUser are optional. Platforms with a user-level
	// instructions file (~/.claude/CLAUDE.md, ~/.gemini/GEMINI.md) use them
	// by default; --project or an explicit --dir selects Install/Uninstall.
	InstallUser   func(home string) error
	UninstallUser func(home string) error
}

var platforms = []Platform{
	{
		Name:        "agents",
		Description: "Universal AGENTS.md instructions for AI coding agents",
		Detect: func(dir, home string) bool {
			return false // on-demand or explicit target
		},
		Install: func(dir, home string) error {
			agentsPath := filepath.Join(dir, "AGENTS.md")
			content := getAgentsMDContent(getActiveInstallIndex(dir))
			if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err != nil {
				return err
			}
			fmt.Printf("  • %s\n", agentsPath)
			return nil
		},
		Uninstall: func(dir, home string) error {
			return removeSection(filepath.Join(dir, "AGENTS.md"), "## gleann: Code Intelligence", "\n---")
		},
	},
	{
		Name:        "opencode",
		Description: "OpenCode AI (tool.execute.before plugin + MCP config)",
		Detect: func(dir, home string) bool {
			if _, err := os.Stat(filepath.Join(dir, ".opencode")); err == nil {
				return true
			}
			if _, err := os.Stat(filepath.Join(dir, "opencode.json")); err == nil {
				return true
			}
			if _, err := os.Stat(filepath.Join(home, ".config", "opencode")); err == nil {
				return true
			}
			_, err := os.Stat(filepath.Join(home, ".opencode"))
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
		Install:       installClaude,
		Uninstall:     uninstallClaude,
		InstallUser:   installClaudeUser,
		UninstallUser: uninstallClaudeUser,
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
		Install:       installGemini,
		Uninstall:     uninstallGemini,
		InstallUser:   installGeminiUser,
		UninstallUser: uninstallGeminiUser,
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
	{
		Name:        "windsurf",
		Description: "Windsurf IDE (.windsurf/rules + MCP config)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(dir, ".windsurf"))
			return err == nil
		},
		Install:   installWindsurf,
		Uninstall: uninstallWindsurf,
	},
	{
		Name:        "cline",
		Description: "Cline / Roo Code (.clinerules + MCP config)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(dir, ".clinerules"))
			if err == nil {
				return true
			}
			_, err = os.Stat(filepath.Join(dir, ".cline"))
			return err == nil
		},
		Install:   installCline,
		Uninstall: uninstallCline,
	},
	{
		Name:        "amp",
		Description: "Amp (AGENTS.md + MCP config)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(dir, ".amp"))
			return err == nil
		},
		Install:   installAmp,
		Uninstall: uninstallAmp,
	},
	{
		Name:        "kiro",
		Description: "Kiro IDE (.kiro/rules + MCP config)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(dir, ".kiro"))
			return err == nil
		},
		Install:   installKiro,
		Uninstall: uninstallKiro,
	},
	{
		Name:        "amazonq",
		Description: "Amazon Q Developer (AGENTS.md + MCP config)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(dir, ".amazonq"))
			if err == nil {
				return true
			}
			_, err = os.Stat(filepath.Join(home, ".amazonq"))
			return err == nil
		},
		Install:   installAmazonQ,
		Uninstall: uninstallAmazonQ,
	},
	{
		Name:        "continue",
		Description: "Continue (AGENTS.md + .continue/config.json MCP)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(dir, ".continue"))
			if err == nil {
				return true
			}
			_, err = os.Stat(filepath.Join(home, ".continue"))
			return err == nil
		},
		Install:   installContinue,
		Uninstall: uninstallContinue,
	},
	{
		Name:        "zed",
		Description: "Zed IDE (~/.config/zed/settings.json MCP)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(home, ".config", "zed"))
			return err == nil
		},
		Install:   installZed,
		Uninstall: uninstallZed,
	},
	{
		Name:        "neovim",
		Description: "Neovim (AGENTS.md + MCP via avante/codecompanion)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(home, ".config", "nvim"))
			return err == nil
		},
		Install:   installNeovim,
		Uninstall: uninstallNeovim,
	},
	{
		Name:        "jetbrains",
		Description: "JetBrains IDEs (AGENTS.md + .junie/guidelines.md)",
		Detect: func(dir, home string) bool {
			_, err := os.Stat(filepath.Join(dir, ".idea"))
			return err == nil
		},
		Install:   installJetBrains,
		Uninstall: uninstallJetBrains,
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

	// Binary installation directly from CLI
	if hasFlag(args, "--user") || hasFlag(args, "--system") || (len(args) > 0 && (args[0] == "binary" || args[0] == "user" || args[0] == "system")) {
		installPath := "~/.local/bin"
		if hasFlag(args, "--system") || (len(args) > 0 && args[0] == "system") {
			installPath = "/usr/local/bin"
		}
		tui.RunInstall(&tui.OnboardResult{
			InstallPath:        installPath,
			InstallCompletions: true,
			MCPEnabled:         true,
		})
		return
	}

	dirFlag := getFlag(args, "--dir")
	indexFlag := getFlag(args, "--index")
	followSymlinkFlag = hasFlag(args, "--follow-symlink")

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine home directory: %v\n", err)
		os.Exit(1)
	}

	targetDir, err := resolveTargetDirectory(dirFlag, indexFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine target directory: %v\n", err)
		os.Exit(1)
	}
	dir := targetDir

	resolvedIndex := resolveIndexNameForDir(dir, indexFlag)
	activeInstallIndex = resolvedIndex
	defer func() { activeInstallIndex = "" }()

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
	if platformName == "" {
		// `gleann install claude` is shorthand for `--platform claude`.
		platformName = positionalPlatform(args)
	}

	// User-level platforms (claude, gemini) install globally unless the
	// caller asks for a project (--project) or names a directory (--dir).
	projectScope := hasFlag(args, "--project") || hasFlag(args, "--dir")

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

	if !uninstalling {
		if resolvedIndex != "" {
			fmt.Printf("Target directory: %s (index: %q)\n", dir, resolvedIndex)
		} else {
			fmt.Printf("Target directory: %s\n", dir)
			if dirFlag == "" && indexFlag == "" {
				fmt.Printf("ℹ️  No index found matching %s (using <name> placeholder). You can build an index with: gleann index build <name> --docs %s\n", dir, dir)
			}
		}
	} else {
		fmt.Printf("Target directory: %s\n", dir)
	}

	for _, p := range targets {
		if uninstalling {
			fmt.Printf("🗑️  Uninstalling gleann from %s...\n", p.Name)
			var err error
			if p.UninstallUser != nil && !projectScope {
				err = p.UninstallUser(home)
			} else {
				err = p.Uninstall(dir, home)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			} else {
				fmt.Printf("  ✓ Done\n")
			}
		} else {
			fmt.Printf("🔧 Installing gleann for %s...\n", p.Name)
			var err error
			if p.InstallUser != nil && !projectScope {
				err = p.InstallUser(home)
			} else {
				err = p.Install(dir, home)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			} else {
				fmt.Printf("  ✓ Done\n")
			}
		}
	}
}

// positionalPlatform returns the first bare argument that names a platform,
// skipping flag values such as `--dir <path>`.
func positionalPlatform(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--dir" || a == "--platform" || a == "--index" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if platformByName(a) != nil {
			return a
		}
	}
	return ""
}

func printInstallUsage() {
	fmt.Print(`Usage: gleann install [flags] [uninstall]

Install gleann binary & completions, or configure integration for AI coding platforms.

Binary Installation:
  gleann install --user            Install binary to ~/.local/bin + completions
  gleann install --system          Install binary to /usr/local/bin (requires sudo)

AI Platform Integration:
  gleann install                   Auto-detect & install all present platforms
  gleann install --platform <name> Install for a specific platform
  gleann install <name>            Same as --platform <name>
  gleann install uninstall         Remove platform integration files
  gleann install --list            List supported platforms and auto-detection status

Flags:
  --user              Install binary to ~/.local/bin (user-only)
  --system            Install binary to /usr/local/bin (system-wide)
  --platform <name>   Target a specific AI platform (default: auto-detect)
  --index <name>      Target a specific gleann index name
  --dir <path>        Target directory for platform integration (default: git root or cwd)
  --project           Write claude/gemini files into the project (cwd or --dir)
                      instead of ~/.claude and ~/.gemini
  --follow-symlink    Allow modifying files that are symlinks
  --list              List supported platforms

Supported platforms:
`)
	for _, p := range platforms {
		fmt.Printf("  %-12s  %s\n", p.Name, p.Description)
	}
}

var (
	followSymlinkFlag  bool
	activeInstallIndex string
)

func isSymlink(path string) (bool, string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		return true, target, err
	}
	return false, "", nil
}

func getGitToplevel() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return "", fmt.Errorf("empty git toplevel")
	}
	return top, nil
}

func resolveTargetDirectory(explicitDir, explicitIndex string) (string, error) {
	if explicitDir != "" {
		return filepath.Abs(explicitDir)
	}

	if explicitIndex != "" {
		cfg := getConfig(nil)
		applySavedConfig(&cfg, nil)
		if meta, err := gleann.GetIndexMeta(cfg.IndexDir, explicitIndex); err == nil && meta.SourceDir != "" {
			if _, statErr := os.Stat(meta.SourceDir); statErr == nil {
				return filepath.Abs(meta.SourceDir)
			}
		}
	}

	if gitTop, err := getGitToplevel(); err == nil && gitTop != "" {
		return filepath.Abs(gitTop)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(cwd)
}

func resolveIndexNameForDir(dir, explicitIndex string) string {
	if explicitIndex != "" {
		return explicitIndex
	}
	if envIdx := os.Getenv("GLEANN_INDEX"); envIdx != "" {
		return envIdx
	}

	cfg := getConfig(nil)
	applySavedConfig(&cfg, nil)
	indexes, err := gleann.ListIndexes(cfg.IndexDir)
	if err != nil || len(indexes) == 0 {
		return ""
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}
	absDir = filepath.Clean(absDir)
	if realDir, err := filepath.EvalSymlinks(absDir); err == nil {
		absDir = filepath.Clean(realDir)
	}

	// 1. Exact SourceDir match (require absolute SourceDir to prevent relative '.' matching process cwd)
	for _, idx := range indexes {
		src := strings.TrimSpace(idx.SourceDir)
		if src == "" || !filepath.IsAbs(src) {
			continue
		}
		cleanSrc := filepath.Clean(src)
		if realSrc, err := filepath.EvalSymlinks(cleanSrc); err == nil {
			cleanSrc = filepath.Clean(realSrc)
		}
		if cleanSrc == absDir {
			return idx.Name
		}
	}

	// 2. Directory contains or is contained in SourceDir (require absolute SourceDir)
	for _, idx := range indexes {
		src := strings.TrimSpace(idx.SourceDir)
		if src == "" || !filepath.IsAbs(src) {
			continue
		}
		cleanSrc := filepath.Clean(src)
		if realSrc, err := filepath.EvalSymlinks(cleanSrc); err == nil {
			cleanSrc = filepath.Clean(realSrc)
		}
		rel, err1 := filepath.Rel(cleanSrc, absDir)
		if err1 == nil && !strings.HasPrefix(rel, "..") {
			return idx.Name
		}
		rel2, err2 := filepath.Rel(absDir, cleanSrc)
		if err2 == nil && !strings.HasPrefix(rel2, "..") {
			return idx.Name
		}
	}

	// 3. Name matches base directory
	dirBase := strings.ToLower(filepath.Base(absDir))
	for _, idx := range indexes {
		if strings.EqualFold(idx.Name, dirBase) {
			return idx.Name
		}
	}

	return ""
}

func getActiveInstallIndex(dir string) string {
	if activeInstallIndex != "" {
		return activeInstallIndex
	}
	return resolveIndexNameForDir(dir, "")
}

func getClaudeMDSection(indexName string) string {
	idx := indexName
	if idx == "" {
		idx = "<name>"
	}
	return strings.ReplaceAll(claudeMDSection, "<name>", idx)
}

func getGeminiMDSection(indexName string) string {
	idx := indexName
	if idx == "" {
		idx = "<name>"
	}
	return strings.ReplaceAll(geminiMDSection, "<name>", idx)
}

func getCursorRulesContent(indexName string) string {
	idx := indexName
	if idx == "" {
		idx = "<name>"
	}
	return strings.ReplaceAll(cursorRulesContent, "<name>", idx)
}

func getSkillMDContent(indexName string) string {
	idx := indexName
	if idx == "" {
		idx = "<name>"
	}
	res := strings.ReplaceAll(skillMDContent, "<name>", idx)
	res = strings.ReplaceAll(res, "<index>", idx)
	res = strings.ReplaceAll(res, "<index-name>", idx)
	return res
}

// ─── Shared content helpers ───────────────────────────────────────────────────

var agentsMDSection = getAgentsMDContent("")

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
` + "`gleann_search`" + ` · ` + "`gleann_ask`" + ` · ` + "`gleann_graph_neighbors`" + ` · ` + "`gleann_impact`" + ` · ` + "`gleann_read_full_document`" + `
` + "`inject_knowledge_graph`" + ` · ` + "`traverse_knowledge_graph`" + `
`

// appendOrCreateFile appends content to a file, creating it if absent.
// It avoids duplicate sections by checking for the sentinel.
func appendOrCreateFile(path, content, sentinel string) error {
	sym, target, _ := isSymlink(path)
	if sym && !followSymlinkFlag {
		fmt.Printf("  ⚠ Skipped %s (points to symlink %s; use --follow-symlink to modify)\n", path, target)
		return nil
	}
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
	sym, target, _ := isSymlink(path)
	if sym && !followSymlinkFlag {
		fmt.Printf("  ⚠ Skipped %s (points to symlink %s; use --follow-symlink to modify)\n", path, target)
		return nil
	}
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

export default {
  id: "gleann",
  setup: GleannPlugin,
};
`

func installOpenCode(dir, home string) error {
	// 1. AGENTS.md
	agentsPath := filepath.Join(dir, "AGENTS.md")
	content := getAgentsMDContent(getActiveInstallIndex(dir))
	if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err != nil {
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

	// 4. Global ~/.config/opencode/opencode.json & ~/.config/opencode/AGENTS.md
	globalConfigDir := filepath.Join(home, ".config", "opencode")
	_ = os.MkdirAll(globalConfigDir, 0o755)
	globalConfigPath := filepath.Join(globalConfigDir, "opencode.json")
	if err := patchOpenCodeJSON(globalConfigPath); err == nil {
		fmt.Printf("  • %s  (mcp.gleann registered globally)\n", globalConfigPath)
	}
	globalAgentsPath := filepath.Join(globalConfigDir, "AGENTS.md")
	if err := appendOrCreateFile(globalAgentsPath, getAgentsMDContent(""), "gleann: Code Intelligence"); err == nil {
		fmt.Printf("  • %s  (global instructions for OpenCode agents)\n", globalAgentsPath)
	}

	return nil
}

func patchOpenCodeJSON(path string) error {
	sym, target, _ := isSymlink(path)
	if sym && !followSymlinkFlag {
		fmt.Printf("  ⚠ Skipped %s (points to symlink %s; use --follow-symlink to modify)\n", path, target)
		return nil
	}

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
	binPath := resolveInstalledGleannBin()

	// In OpenCode V2, servers are defined under mcp.servers.<name>.
	servers, _ := mcpSection["servers"].(map[string]interface{})
	if servers == nil {
		servers = make(map[string]interface{})
		if legacy, ok := mcpSection["gleann"].(map[string]interface{}); ok {
			servers["gleann"] = legacy
		}
	}
	delete(mcpSection, "gleann") // purge legacy flat key to prevent OpenCode V2 validation warnings

	// Check if already registered.
	if existing, exists := servers["gleann"]; exists {
		// Ensure --clean-names is in command args, and heal any stale ephemeral runtime paths.
		if exMap, ok := existing.(map[string]interface{}); ok {
			cmdList, _ := exMap["command"].([]interface{})
			hasClean := false
			updatedCmd := false
			for i, arg := range cmdList {
				if str, ok := arg.(string); ok && i == 0 {
					if strings.Contains(str, ".gleann/runtime") || strings.Contains(str, "/tmp") {
						cmdList[0] = binPath
						updatedCmd = true
					}
				}
				if arg == "--clean-names" {
					hasClean = true
				}
			}
			if !hasClean {
				cmdList = append(cmdList, "--clean-names")
				updatedCmd = true
			}
			if updatedCmd {
				exMap["command"] = cmdList
				servers["gleann"] = exMap
				mcpSection["servers"] = servers
				config["mcp"] = mcpSection
				out, err := json.MarshalIndent(config, "", "  ")
				if err == nil {
					_ = os.WriteFile(path, append(out, '\n'), 0o644)
				}
			}
		}
		mcpSection["servers"] = servers
		config["mcp"] = mcpSection
		fmt.Printf("  (gleann mcp already in %s)\n", filepath.Base(path))
		return nil
	}

	servers["gleann"] = map[string]interface{}{
		"type":    "local",
		"command": []interface{}{binPath, "mcp", "--clean-names"},
		"enabled": true,
	}
	mcpSection["servers"] = servers
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

	// Deregister mcp.servers.gleann (and legacy mcp.gleann) from opencode.json.
	ocConfigPath := filepath.Join(dir, "opencode.json")
	if data, err := os.ReadFile(ocConfigPath); err == nil {
		var config map[string]interface{}
		if json.Unmarshal(data, &config) == nil {
			if mcpSection, ok := config["mcp"].(map[string]interface{}); ok {
				delete(mcpSection, "gleann")
				if servers, ok := mcpSection["servers"].(map[string]interface{}); ok {
					delete(servers, "gleann")
					if len(servers) == 0 {
						delete(mcpSection, "servers")
					}
				}
				if len(mcpSection) == 0 {
					delete(config, "mcp")
				} else {
					config["mcp"] = mcpSection
				}
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

// installClaude writes the project-level CLAUDE.md into dir.
func installClaude(dir, home string) error {
	return installClaudeAt(filepath.Join(dir, "CLAUDE.md"), home, getClaudeMDSection(getActiveInstallIndex(dir)))
}

// installClaudeUser writes the user-level ~/.claude/CLAUDE.md.
func installClaudeUser(home string) error {
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		return fmt.Errorf("creating ~/.claude: %w", err)
	}
	return installClaudeAt(filepath.Join(home, ".claude", "CLAUDE.md"), home, getClaudeMDSection(""))
}

func installClaudeAt(claudePath, home string, content string) error {
	// 1. CLAUDE.md
	if err := appendOrCreateFile(claudePath, content, "gleann: Code Intelligence"); err != nil {
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
	sym, target, _ := isSymlink(path)
	if sym && !followSymlinkFlag {
		fmt.Printf("  ⚠ Skipped %s (points to symlink %s; use --follow-symlink to modify)\n", path, target)
		return nil
	}

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
	return uninstallClaudeAt(filepath.Join(dir, "CLAUDE.md"), home)
}

func uninstallClaudeUser(home string) error {
	return uninstallClaudeAt(filepath.Join(home, ".claude", "CLAUDE.md"), home)
}

func uninstallClaudeAt(claudePath, home string) error {
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
	content := getCursorRulesContent(getActiveInstallIndex(dir))
	if err := os.WriteFile(rulesPath, []byte(content), 0o644); err != nil {
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
	content := getAgentsMDContent(getActiveInstallIndex(dir))
	if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err != nil {
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

// installGemini writes the project-level GEMINI.md and .gemini/ into dir.
func installGemini(dir, home string) error {
	return installGeminiAt(filepath.Join(dir, "GEMINI.md"), filepath.Join(dir, ".gemini"), getGeminiMDSection(getActiveInstallIndex(dir)))
}

// installGeminiUser writes the user-level ~/.gemini/GEMINI.md and settings.
func installGeminiUser(home string) error {
	geminiDir := filepath.Join(home, ".gemini")
	return installGeminiAt(filepath.Join(geminiDir, "GEMINI.md"), geminiDir, getGeminiMDSection(""))
}

func installGeminiAt(geminiMDPath, geminiDir, content string) error {
	if err := os.MkdirAll(geminiDir, 0o755); err != nil {
		return fmt.Errorf("creating .gemini: %w", err)
	}
	// GEMINI.md
	if err := appendOrCreateFile(geminiMDPath, content, "<!-- gleann -->"); err != nil {
		return fmt.Errorf("writing GEMINI.md: %w", err)
	}
	fmt.Printf("  • %s\n", geminiMDPath)

	// .gemini/settings.json
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
	sym, target, _ := isSymlink(path)
	if sym && !followSymlinkFlag {
		fmt.Printf("  ⚠ Skipped %s (points to symlink %s; use --follow-symlink to modify)\n", path, target)
		return nil
	}

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
	return uninstallGeminiAt(filepath.Join(dir, "GEMINI.md"), filepath.Join(dir, ".gemini"))
}

func uninstallGeminiUser(home string) error {
	geminiDir := filepath.Join(home, ".gemini")
	return uninstallGeminiAt(filepath.Join(geminiDir, "GEMINI.md"), geminiDir)
}

func uninstallGeminiAt(geminiMDPath, geminiDir string) error {
	_ = removeSection(geminiMDPath, "<!-- gleann -->", "<!-- /gleann -->")
	settingsPath := filepath.Join(geminiDir, "settings.json")
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
	content := getAgentsMDContent(getActiveInstallIndex(dir))
	if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	fmt.Printf("  • %s\n", agentsPath)

	skillDir := filepath.Join(home, ".openclaw", "skills", "gleann")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("creating skill dir: %w", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	skillContent := getSkillMDContent(getActiveInstallIndex(dir))
	if err := os.WriteFile(skillPath, []byte(skillContent), 0o644); err != nil {
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
	content := getAgentsMDContent(getActiveInstallIndex(dir))
	if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err != nil {
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

// ─── Windsurf ─────────────────────────────────────────────────────────────────

func installWindsurf(dir, home string) error {
	rulesDir := filepath.Join(dir, ".windsurf", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		return fmt.Errorf("creating .windsurf/rules: %w", err)
	}
	rulesPath := filepath.Join(rulesDir, "gleann.md")
	content := getCursorRulesContent(getActiveInstallIndex(dir))
	if err := os.WriteFile(rulesPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing rules: %w", err)
	}
	fmt.Printf("  • %s\n", rulesPath)
	return nil
}

func uninstallWindsurf(dir, home string) error {
	return os.Remove(filepath.Join(dir, ".windsurf", "rules", "gleann.md"))
}

// ─── Cline / Roo Code ────────────────────────────────────────────────────────

func installCline(dir, home string) error {
	agentsPath := filepath.Join(dir, "AGENTS.md")
	content := getAgentsMDContent(getActiveInstallIndex(dir))
	if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	fmt.Printf("  • %s\n", agentsPath)

	mcpPath := filepath.Join(dir, ".cline", "mcp.json")
	clineMCP := `{
  "mcpServers": {
    "gleann": {
      "command": "gleann",
      "args": ["mcp"]
    }
  }
}
`
	clineDir := filepath.Join(dir, ".cline")
	if err := os.MkdirAll(clineDir, 0o755); err != nil {
		return fmt.Errorf("creating .cline: %w", err)
	}
	if err := writeJSONIfAbsent(mcpPath, clineMCP); err != nil {
		return fmt.Errorf("writing mcp.json: %w", err)
	}
	fmt.Printf("  • %s\n", mcpPath)
	return nil
}

func uninstallCline(dir, home string) error {
	_ = os.Remove(filepath.Join(dir, ".cline", "mcp.json"))
	return removeSection(filepath.Join(dir, "AGENTS.md"), "## gleann: Code Intelligence", "\n---")
}

// ─── Amp ──────────────────────────────────────────────────────────────────────

func installAmp(dir, home string) error {
	agentsPath := filepath.Join(dir, "AGENTS.md")
	content := getAgentsMDContent(getActiveInstallIndex(dir))
	if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	fmt.Printf("  • %s\n", agentsPath)
	return nil
}

func uninstallAmp(dir, home string) error {
	return removeSection(filepath.Join(dir, "AGENTS.md"), "## gleann: Code Intelligence", "\n---")
}

// ─── Kiro ─────────────────────────────────────────────────────────────────────

func installKiro(dir, home string) error {
	rulesDir := filepath.Join(dir, ".kiro", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		return fmt.Errorf("creating .kiro/rules: %w", err)
	}
	rulesPath := filepath.Join(rulesDir, "gleann.md")
	content := getCursorRulesContent(getActiveInstallIndex(dir))
	if err := os.WriteFile(rulesPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing rules: %w", err)
	}
	fmt.Printf("  • %s\n", rulesPath)
	return nil
}

func uninstallKiro(dir, home string) error {
	return os.Remove(filepath.Join(dir, ".kiro", "rules", "gleann.md"))
}

// ─── Amazon Q ─────────────────────────────────────────────────────────────────

func installAmazonQ(dir, home string) error {
	agentsPath := filepath.Join(dir, "AGENTS.md")
	content := getAgentsMDContent(getActiveInstallIndex(dir))
	if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	fmt.Printf("  • %s\n", agentsPath)
	return nil
}

func uninstallAmazonQ(dir, home string) error {
	return removeSection(filepath.Join(dir, "AGENTS.md"), "## gleann: Code Intelligence", "\n---")
}

// ─── Continue ─────────────────────────────────────────────────────────────────

func installContinue(dir, home string) error {
	agentsPath := filepath.Join(dir, "AGENTS.md")
	content := getAgentsMDContent(getActiveInstallIndex(dir))
	if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	fmt.Printf("  • %s\n", agentsPath)
	return nil
}

func uninstallContinue(dir, home string) error {
	return removeSection(filepath.Join(dir, "AGENTS.md"), "## gleann: Code Intelligence", "\n---")
}

// ─── Zed ──────────────────────────────────────────────────────────────────────

func installZed(dir, home string) error {
	agentsPath := filepath.Join(dir, "AGENTS.md")
	content := getAgentsMDContent(getActiveInstallIndex(dir))
	if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	fmt.Printf("  • %s\n", agentsPath)
	return nil
}

func uninstallZed(dir, home string) error {
	return removeSection(filepath.Join(dir, "AGENTS.md"), "## gleann: Code Intelligence", "\n---")
}

// ─── Neovim ───────────────────────────────────────────────────────────────────

func installNeovim(dir, home string) error {
	agentsPath := filepath.Join(dir, "AGENTS.md")
	content := getAgentsMDContent(getActiveInstallIndex(dir))
	if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	fmt.Printf("  • %s\n", agentsPath)
	return nil
}

func uninstallNeovim(dir, home string) error {
	return removeSection(filepath.Join(dir, "AGENTS.md"), "## gleann: Code Intelligence", "\n---")
}

// ─── JetBrains ────────────────────────────────────────────────────────────────

func installJetBrains(dir, home string) error {
	agentsPath := filepath.Join(dir, "AGENTS.md")
	content := getAgentsMDContent(getActiveInstallIndex(dir))
	if err := appendOrCreateFile(agentsPath, content, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	fmt.Printf("  • %s\n", agentsPath)

	// Junie guidelines.md
	junieDir := filepath.Join(dir, ".junie")
	if err := os.MkdirAll(junieDir, 0o755); err != nil {
		return fmt.Errorf("creating .junie: %w", err)
	}
	guidelinesPath := filepath.Join(junieDir, "guidelines.md")
	if err := appendOrCreateFile(guidelinesPath, content, "gleann: Code Intelligence"); err != nil {
		return fmt.Errorf("writing guidelines.md: %w", err)
	}
	fmt.Printf("  • %s\n", guidelinesPath)
	return nil
}

func uninstallJetBrains(dir, home string) error {
	_ = os.Remove(filepath.Join(dir, ".junie", "guidelines.md"))
	return removeSection(filepath.Join(dir, "AGENTS.md"), "## gleann: Code Intelligence", "\n---")
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// writeJSONIfAbsent writes content to path only if the file does not already exist.
func writeJSONIfAbsent(path, content string) error {
	sym, target, _ := isSymlink(path)
	if sym && !followSymlinkFlag {
		fmt.Printf("  ⚠ Skipped %s (points to symlink %s; use --follow-symlink to modify)\n", path, target)
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("  (already exists: %s)\n", filepath.Base(path))
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
