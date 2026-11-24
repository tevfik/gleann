package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/mcp"
)

// Older gleann installers wrote these hook commands; the migration must
// recognise them without a marker.
const (
	legacyHookV1 = `if [ -f "GRAPH_REPORT.md" ]; then echo "[gleann] GRAPH_REPORT.md found — read it before searching source files." >&2; fi; gleann memory list 2>/dev/null | head -20 >&2`
	legacyHookV3 = `if [ -f "GRAPH_REPORT.md" ]; then echo "[gleann] GRAPH_REPORT.md exists — read it before searching source files." >&2; fi; MC=$(gleann memory list --json 2>/dev/null | head -c 2000); if [ -n "$MC" ]; then echo "[gleann memory] $MC" >&2; fi`
	legacyHookV2 = `if [ -f GRAPH_REPORT.md ]; then echo "[gleann] GRAPH_REPORT.md found: read it for the repository overview."; fi; ctx=$(gleann memory context 2>/dev/null | head -c 2000); if [ -n "$ctx" ] && [ "$ctx" != "(empty memory context)" ]; then printf '[gleann] memory:\n%s\n' "$ctx"; fi`
	foreignHook  = `echo "my own hook"`
	// Foreign hooks that mention gleann; they must never be removed.
	foreignMentionList   = `gleann memory list --tier long | head -20 >> ~/my-notes.txt`
	foreignMentionMarker = `grep -q '[gleann]' /var/log/x || true`
	foreignMentionJSON   = `if [ -f GRAPH_REPORT.md ]; then echo hi; fi; gleann memory list --json 2>/dev/null | head -c 2000`
)

// isolateInstallHome points every home-derived path at a temp dir so no test reads
// or writes the real configuration or indexes.
func isolateInstallHome(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(h, ".local", "share"))
	t.Setenv("GLEANN_INDEX", "")
	t.Setenv("GLEANN_HOME", filepath.Join(h, ".gleann"))
	return h
}

func writeSettings(t *testing.T, path string, v map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// hookCommands returns every hook command registered under event.
func hookCommands(t *testing.T, path, event string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	var out []string
	entries, _ := hooks[event].([]any)
	for _, e := range entries {
		m, _ := e.(map[string]any)
		inner, _ := m["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			if c, ok := hm["command"].(string); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

func countGleann(cmds []string) int {
	n := 0
	for _, c := range cmds {
		if isGleannHookCommand(c) {
			n++
		}
	}
	return n
}

func legacySettings() map[string]any {
	entry := func(matcher, cmd string) map[string]any {
		return map[string]any{
			"matcher": matcher,
			"hooks":   []any{map[string]any{"type": "command", "command": cmd}},
		}
	}
	return map[string]any{
		"theme": "dark",
		"hooks": map[string]any{
			"PreToolUse": []any{
				entry("Glob|Grep|Read|LS", legacyHookV1),
				entry("Bash", foreignHook),
				entry("Glob|Grep|Read|LS", legacyHookV2),
				entry("Glob|Grep|Read|LS", legacyHookV3),
			},
		},
	}
}

func TestInstallClaudeFreshWritesSessionStartOnly(t *testing.T) {
	home := isolateInstallHome(t)
	dir := t.TempDir()
	if err := installClaude(dir, home); err != nil {
		t.Fatal(err)
	}
	sp := filepath.Join(home, ".claude", "settings.json")
	if n := countGleann(hookCommands(t, sp, "SessionStart")); n != 1 {
		t.Errorf("want one gleann SessionStart hook, got %d", n)
	}
	if got := hookCommands(t, sp, "PreToolUse"); len(got) != 0 {
		t.Errorf("fresh install must not write a PreToolUse hook, got %v", got)
	}
}

func TestInstallClaudeMigratesLegacyHooks(t *testing.T) {
	home := isolateInstallHome(t)
	dir := t.TempDir()
	sp := filepath.Join(home, ".claude", "settings.json")
	writeSettings(t, sp, legacySettings())

	if err := installClaude(dir, home); err != nil {
		t.Fatal(err)
	}
	if n := countGleann(hookCommands(t, sp, "SessionStart")); n != 1 {
		t.Errorf("want exactly one gleann SessionStart hook, got %d", n)
	}
	pre := hookCommands(t, sp, "PreToolUse")
	if len(pre) != 1 || pre[0] != foreignHook {
		t.Errorf("PreToolUse must keep only the foreign hook, got %v", pre)
	}
	for _, c := range pre {
		if c == legacyHookV1 || c == legacyHookV2 || c == legacyHookV3 {
			t.Errorf("legacy hook survived: %s", c)
		}
	}
	var st map[string]any
	b, _ := os.ReadFile(sp)
	_ = json.Unmarshal(b, &st)
	if st["theme"] != "dark" {
		t.Errorf("unrelated settings lost: %s", b)
	}
}

func TestInstallClaudeIdempotent(t *testing.T) {
	home := isolateInstallHome(t)
	dir := t.TempDir()
	sp := filepath.Join(home, ".claude", "settings.json")
	writeSettings(t, sp, legacySettings())
	if err := installClaude(dir, home); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(sp)
	md1, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	for i := 0; i < 2; i++ {
		if err := installClaude(dir, home); err != nil {
			t.Fatal(err)
		}
	}
	second, _ := os.ReadFile(sp)
	md2, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if string(first) != string(second) {
		t.Errorf("settings changed on reinstall:\n%s\n---\n%s", first, second)
	}
	if string(md1) != string(md2) {
		t.Errorf("CLAUDE.md changed on reinstall")
	}
}

func TestUninstallClaudeRemovesAllGleannHooksKeepsForeign(t *testing.T) {
	home := isolateInstallHome(t)
	dir := t.TempDir()
	sp := filepath.Join(home, ".claude", "settings.json")
	s := legacySettings()
	// A foreign SessionStart hook and a gleann hook that shares an entry with
	// a foreign one.
	s["hooks"].(map[string]any)["SessionStart"] = []any{
		map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": "echo foreign-start"},
			map[string]any{"type": "command", "command": legacyHookV2},
		}},
	}
	writeSettings(t, sp, s)
	if err := installClaude(dir, home); err != nil {
		t.Fatal(err)
	}
	if err := uninstallClaude(dir, home); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"PreToolUse", "SessionStart"} {
		for _, c := range hookCommands(t, sp, ev) {
			if isGleannHookCommand(c) {
				t.Errorf("%s still has gleann hook: %s", ev, c)
			}
		}
	}
	if got := hookCommands(t, sp, "PreToolUse"); len(got) != 1 || got[0] != foreignHook {
		t.Errorf("foreign PreToolUse hook lost: %v", got)
	}
	if got := hookCommands(t, sp, "SessionStart"); len(got) != 1 || got[0] != "echo foreign-start" {
		t.Errorf("foreign SessionStart hook lost: %v", got)
	}
}

func TestClaudeSessionStartHookIsBounded(t *testing.T) {
	if !strings.Contains(claudeHookScript, "# gleann-managed-hook v2") {
		t.Error("hook must carry the ownership marker")
	}
	if !strings.Contains(claudeHookScript, "--max-bytes 2000") {
		t.Error("memory output must be bounded")
	}
}

func TestUserLevelClaudeSectionIsConditionalAndNamesMCPTools(t *testing.T) {
	isolateInstallHome(t)
	s := getClaudeMDSection("")
	if strings.Contains(s, "<name>") {
		t.Errorf("user-level section must not carry a literal <name>:\n%s", s)
	}
	if !strings.Contains(s, "When the current project has a gleann index") {
		t.Errorf("user-level section must be conditional:\n%s", s)
	}
	for _, tool := range mcp.DefaultProfileTools() {
		if !strings.Contains(s, tool) {
			t.Errorf("section misses MCP tool %s", tool)
		}
	}
	if strings.Contains(s, "graph report --index") && !strings.Contains(s, "--output") {
		t.Errorf("graph report must only be offered with --output")
	}
	for _, want := range []string{"graph map", "graph callers", "graph deps", "graph explain", "<fqn>", "BFS", "gleann index sync"} {
		if !strings.Contains(s, want) {
			t.Errorf("section misses %q", want)
		}
	}
	if strings.Contains(s, "symbol search") {
		t.Errorf("graph query is not a symbol search")
	}
	// Every MCP tool the text names exists in the server's default profile.
	valid := map[string]bool{}
	for _, n := range mcp.DefaultProfileTools() {
		valid[n] = true
	}
	for _, m := range regexp.MustCompile("`((?:gleann|memory)_[a-z_]+)`").FindAllStringSubmatch(s, -1) {
		if !valid[m[1]] {
			t.Errorf("section names unknown MCP tool %s", m[1])
		}
	}
}

func TestProjectLevelClaudeSectionNamesIndex(t *testing.T) {
	home := isolateInstallHome(t)
	dir := t.TempDir()
	prev := activeInstallIndex
	activeInstallIndex = "myproj"
	t.Cleanup(func() { activeInstallIndex = prev })
	if err := installClaude(dir, home); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	s := string(b)
	for _, want := range []string{"gleann graph explain <fqn> --index myproj", "gleann graph map --index myproj", "gleann index sync myproj", "gleann search myproj"} {
		if !strings.Contains(s, want) {
			t.Errorf("project section misses %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "<name>") {
		t.Errorf("project section left a placeholder:\n%s", s)
	}
}

const legacyClaudeSection = `
<!-- gleann: Code Intelligence & Search -->
## gleann knowledge graph

Run ` + "`gleann graph report --index <name>`" + ` to generate GRAPH_REPORT.md, then read it before
searching source files.
<!-- /gleann -->
`

func TestInstallClaudeReplacesLegacySection(t *testing.T) {
	home := isolateInstallHome(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(p, []byte("# Mine\n\nkeep me\n"+legacyClaudeSection+"\ntail text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installClaude(dir, home); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if n := strings.Count(s, "<!-- gleann: Code Intelligence"); n != 1 {
		t.Errorf("want one section, got %d:\n%s", n, s)
	}
	if strings.Contains(s, "gleann knowledge graph") || strings.Contains(s, "to generate GRAPH_REPORT.md") {
		t.Errorf("legacy text survived:\n%s", s)
	}
	if !strings.Contains(s, "keep me") || !strings.Contains(s, "tail text") {
		t.Errorf("surrounding text lost:\n%s", s)
	}
}

func TestInstallGeminiReplacesLegacySection(t *testing.T) {
	home := isolateInstallHome(t)
	gdir := filepath.Join(home, ".gemini")
	_ = os.MkdirAll(gdir, 0o755)
	legacy := "mine\n\n<!-- gleann -->\n## gleann knowledge graph\n\nRead GRAPH_REPORT.md before using file-search tools.\n<!-- /gleann -->\nafter\n"
	p := filepath.Join(gdir, "GEMINI.md")
	_ = os.WriteFile(p, []byte(legacy), 0o644)
	if err := installGeminiUser(home); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if strings.Count(s, "<!-- gleann -->") != 1 || strings.Contains(s, "Read GRAPH_REPORT.md before") {
		t.Errorf("legacy gemini section not replaced:\n%s", s)
	}
	if !strings.Contains(s, "mine") || !strings.Contains(s, "after") {
		t.Errorf("surrounding text lost:\n%s", s)
	}
}

func TestInstallAndUninstallKeepForeignHooksThatMentionGleann(t *testing.T) {
	dir := t.TempDir()
	home := isolateInstallHome(t)
	sp := filepath.Join(home, ".claude", "settings.json")
	entry := func(m, c string) map[string]any {
		return map[string]any{"matcher": m, "hooks": []any{map[string]any{"type": "command", "command": c}}}
	}
	writeSettings(t, sp, map[string]any{"hooks": map[string]any{
		"PreToolUse": []any{
			entry("Bash", foreignMentionList),
			entry("Bash", foreignMentionMarker),
			entry("Bash", foreignMentionJSON),
		},
	}})
	if err := installClaude(dir, home); err != nil {
		t.Fatal(err)
	}
	if got := hookCommands(t, sp, "PreToolUse"); len(got) != 3 {
		t.Errorf("install removed a foreign hook: %v", got)
	}
	if err := uninstallClaude(dir, home); err != nil {
		t.Fatal(err)
	}
	got := hookCommands(t, sp, "PreToolUse")
	if len(got) != 3 || got[0] != foreignMentionList || got[1] != foreignMentionMarker || got[2] != foreignMentionJSON {
		t.Errorf("uninstall removed a foreign hook: %v", got)
	}
}

func TestClaudeHookHasTimeout(t *testing.T) {
	dir := t.TempDir()
	home := isolateInstallHome(t)
	if err := installClaude(dir, home); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	var st map[string]any
	_ = json.Unmarshal(b, &st)
	h := st["hooks"].(map[string]any)["SessionStart"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if h["timeout"] != float64(10) {
		t.Errorf("hook timeout = %v, want 10", h["timeout"])
	}
}

func TestInstallPrintsRemovedHooks(t *testing.T) {
	dir := t.TempDir()
	home := isolateInstallHome(t)
	writeSettings(t, filepath.Join(home, ".claude", "settings.json"), legacySettings())
	r := captureRun(t, "", func() { _ = installClaude(dir, home) })
	mustContain(t, "install output", r.Out, "removed gleann hook (PreToolUse:")
}

func TestUpsertSectionNeverDeletesUserText(t *testing.T) {
	const start, end = "<!-- gleann: Code Intelligence", "<!-- /gleann -->"
	run := func(in string) (string, string) {
		p := filepath.Join(t.TempDir(), "CLAUDE.md")
		_ = os.WriteFile(p, []byte(in), 0o644)
		var warn strings.Builder
		if err := upsertSection(io.Discard, &warn, p, "\n"+start+" & Search -->\nNEW\n"+end+"\n", start, end); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(p)
		return string(b), warn.String()
	}
	// Missing end marker, a later foreign gleann-looking block: file untouched.
	in := "<!-- gleann: Code Intelligence & Search -->\nold\nUSER-AFTER\n<!-- gleann -->\nother\n<!-- /gleann -->\nUSER-TAIL\n"
	if got, warn := run(in); got != in || !strings.Contains(warn, "left unchanged") {
		t.Errorf("malformed block must be left alone with a warning; warn=%q got:\n%s", warn, got)
	}
	// Missing end marker only.
	in = "<!-- gleann: Code Intelligence & Search -->\nold\nUSER-AFTER\n"
	if got, warn := run(in); got != in || warn == "" {
		t.Errorf("missing end marker: want untouched + warning; got %q warn %q", got, warn)
	}
	// Duplicate sections: all replaced by one.
	blk := "<!-- gleann: Code Intelligence & Search -->\nold\n<!-- /gleann -->"
	got, _ := run("a\n" + blk + "\nmid\n" + blk + "\nz\n")
	if strings.Count(got, "NEW") != 1 || strings.Contains(got, "old") || !strings.Contains(got, "mid") || !strings.Contains(got, "z\n") {
		t.Errorf("duplicates not collapsed:\n%s", got)
	}
}

func agentsFile(t *testing.T, content string) string {
	p := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAgentsSectionHasOwnMarkers(t *testing.T) {
	c := getAgentsMDContent("px4")
	if !strings.Contains(c, "<!-- gleann:agents -->") || !strings.Contains(c, "<!-- /gleann:agents -->") {
		t.Errorf("AGENTS.md text lacks its marker pair")
	}
	if strings.Contains(c, "<!-- /gleann -->") {
		t.Errorf("AGENTS.md must not use the CLAUDE.md end marker")
	}
}

// Last paragraph of the generated text: the one-line form of later releases
// and the two-line form of the first ones.
const (
	oldWorkflow  = "**Workflow:** call `memory_context` at the start of every session, use `gleann_sync` after modifying files so code intelligence tools immediately reflect changes, then call `memory_remember` whenever you learn something important about the codebase.\n"
	oldWorkflow2 = "**Workflow:** call `memory_context` at the start of every session, then call\n`memory_remember` whenever you learn something important about the codebase.\n"
)

func TestAgentsSectionKeepsParagraphAfterOldSection(t *testing.T) {
	body := "\n## gleann: Code Intelligence, Search & Long-term Memory\n\n### 1 — Before exploring source files\nold text\n\n```bash\ngleann memory remember \"x\"\n```\n\n"
	for name, wf := range map[string]string{"one-line": oldWorkflow, "two-line": oldWorkflow2} {
		t.Run(name, func(t *testing.T) {
			in := "# Mine\nRULE-A\n" + body + wf + "\nUSER-PARA-NO-HEADING\n"
			p := agentsFile(t, in)
			if err := upsertAgentsSection(io.Discard, io.Discard, p, getAgentsMDContent("px4")); err != nil {
				t.Fatal(err)
			}
			got := readFile(t, p)
			if !strings.Contains(got, "USER-PARA-NO-HEADING") || strings.Contains(got, "old text") || strings.Count(got, "<!-- gleann:agents -->") != 1 {
				t.Errorf("install lost the paragraph or kept the old text:\n%s", got)
			}
			p = agentsFile(t, in)
			if err := removeAgentsSection(p); err != nil {
				t.Fatal(err)
			}
			got = readFile(t, p)
			if !strings.Contains(got, "USER-PARA-NO-HEADING") || !strings.Contains(got, "RULE-A") || strings.Contains(got, "gleann") {
				t.Errorf("uninstall lost the paragraph or left the section:\n%s", got)
			}
		})
	}
}

func TestAgentsSectionWithoutKnownEndIsLeftAlone(t *testing.T) {
	in := "# Mine\n\n## gleann: Code Intelligence, Search & Long-term Memory\n\n### 1 — Before exploring source files\nold text\n\nUSER-PARA-NO-HEADING\n"
	p := agentsFile(t, in)
	var warn strings.Builder
	if err := upsertAgentsSection(io.Discard, &warn, p, getAgentsMDContent("px4")); err != nil {
		t.Fatal(err)
	}
	if readFile(t, p) != in || !strings.Contains(warn.String(), "cannot be told") {
		t.Errorf("install must leave the file alone with a warning; warn=%q", warn.String())
	}
	if err := removeAgentsSection(p); err != nil {
		t.Fatal(err)
	}
	if readFile(t, p) != in {
		t.Errorf("uninstall must leave the file alone")
	}
}

func TestMarkedSectionWithForeignCommentIsRejected(t *testing.T) {
	// A hand-deleted end marker followed by another tool's block that ends in
	// the same end marker.
	in := "<!-- gleann: Code Intelligence & Search -->\nold\nUSER-AFTER\n<!-- mytool -->\nx\n<!-- /gleann -->\nUSER-TAIL\n"
	dir := t.TempDir()
	p := filepath.Join(dir, "CLAUDE.md")
	_ = os.WriteFile(p, []byte(in), 0o644)
	var warn strings.Builder
	if err := upsertSection(io.Discard, &warn, p, getClaudeMDSection("px4"), "<!-- gleann: Code Intelligence", claudeSectionEnd); err != nil {
		t.Fatal(err)
	}
	if readFile(t, p) != in || warn.Len() == 0 {
		t.Errorf("install must leave the file alone with a warning; warn=%q\n%s", warn.String(), readFile(t, p))
	}
	if err := removeMarkedSection(p, "<!-- gleann: Code Intelligence", claudeSectionEnd); err != nil {
		t.Fatal(err)
	}
	if readFile(t, p) != in {
		t.Errorf("uninstall must leave the file alone:\n%s", readFile(t, p))
	}
}

func TestAgentsSectionReplaceAndRemoveBoundaries(t *testing.T) {
	old := "\n## gleann: Code Intelligence, Search & Long-term Memory\n\n### 1 — Before exploring source files\nold text\n\n```bash\ngleann memory remember \"x\"\n```\n\n" + oldWorkflow
	cases := []struct{ name, in string }{
		{"eof", "# Mine\nRULE-A\n" + old},
		{"next ##", "# Mine\nRULE-A\n" + old + "\n## My later rules\nUSER-RULE-B\n"},
		{"next #", "# Mine\nRULE-A\n" + old + "\n# Other\nUSER-RULE-B\n"},
		{"hr", "# Mine\nRULE-A\n" + old + "\n---\nUSER-RULE-B\n"},
		{"hr far below", "# Mine\nRULE-A\n" + old + "\n## My later rules\nUSER-RULE-B\n\n---\ntail\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keep := []string{"RULE-A"}
			if strings.Contains(c.in, "USER-RULE-B") {
				keep = append(keep, "USER-RULE-B")
			}
			if strings.Contains(c.in, "tail") {
				keep = append(keep, "tail")
			}
			// Replace.
			p := agentsFile(t, c.in)
			if err := upsertAgentsSection(io.Discard, io.Discard, p, getAgentsMDContent("px4")); err != nil {
				t.Fatal(err)
			}
			got := readFile(t, p)
			if strings.Contains(got, "old text") || strings.Count(got, "<!-- gleann:agents -->") != 1 {
				t.Errorf("not replaced once:\n%s", got)
			}
			for _, k := range keep {
				if !strings.Contains(got, k) {
					t.Errorf("replace lost %q:\n%s", k, got)
				}
			}
			// Uninstall of the marker-less original keeps the user text.
			p = agentsFile(t, c.in)
			if err := removeAgentsSection(p); err != nil {
				t.Fatal(err)
			}
			got = readFile(t, p)
			if strings.Contains(got, "gleann") {
				t.Errorf("section not removed:\n%s", got)
			}
			for _, k := range keep {
				if !strings.Contains(got, k) {
					t.Errorf("uninstall lost %q:\n%s", k, got)
				}
			}
		})
	}
}

func TestAgentsSectionMarkedRoundTripAndIdempotent(t *testing.T) {
	p := agentsFile(t, "# Mine\nRULE-A\n")
	for i := 0; i < 2; i++ {
		if err := upsertAgentsSection(io.Discard, io.Discard, p, getAgentsMDContent("px4")); err != nil {
			t.Fatal(err)
		}
	}
	got := readFile(t, p)
	if strings.Count(got, "<!-- gleann:agents -->") != 1 {
		t.Errorf("want one section:\n%s", got)
	}
	// User text after the marked section survives uninstall.
	if err := os.WriteFile(p, []byte(got+"\n## Later\nUSER\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeAgentsSection(p); err != nil {
		t.Fatal(err)
	}
	got = readFile(t, p)
	if strings.Contains(got, "gleann") || !strings.Contains(got, "RULE-A") || !strings.Contains(got, "USER") {
		t.Errorf("uninstall result wrong:\n%s", got)
	}
}

func TestAgentsSectionUserEditedLegacyIsSkippedWithWarning(t *testing.T) {
	in := "# Mine\n\n## gleann: Code Intelligence\n\nMy own words about it.\n"
	p := agentsFile(t, in)
	var warn strings.Builder
	if err := upsertAgentsSection(io.Discard, &warn, p, getAgentsMDContent("px4")); err != nil {
		t.Fatal(err)
	}
	if readFile(t, p) != in || !strings.Contains(warn.String(), "does not look generated") {
		t.Errorf("user-edited section must be skipped with a warning; warn=%q", warn.String())
	}
	if err := removeAgentsSection(p); err != nil {
		t.Fatal(err)
	}
	if readFile(t, p) != in {
		t.Errorf("uninstall must skip a user-edited section")
	}
}

func TestAgentsAndClaudeSectionsDoNotCrossMatch(t *testing.T) {
	// A CLAUDE.md symlinked to AGENTS.md holds both kinds of block.
	p := agentsFile(t, "")
	if err := upsertAgentsSection(io.Discard, io.Discard, p, getAgentsMDContent("px4")); err != nil {
		t.Fatal(err)
	}
	if err := upsertSection(io.Discard, io.Discard, p, getClaudeMDSection("px4"), "<!-- gleann: Code Intelligence", claudeSectionEnd); err != nil {
		t.Fatal(err)
	}
	if err := removeMarkedSection(p, "<!-- gleann: Code Intelligence", claudeSectionEnd); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p)
	if !strings.Contains(got, "<!-- gleann:agents -->") || strings.Contains(got, "gleann code intelligence") {
		t.Errorf("claude removal touched the agents block or missed its own:\n%s", got)
	}
}

func TestNoUnconditionalPlaceholderInUserLevelTexts(t *testing.T) {
	for name, text := range map[string]string{
		"gemini user": getGeminiMDSection(""),
		"agents user": getAgentsMDContent(""),
	} {
		if !strings.Contains(text, "has a gleann index") {
			t.Errorf("%s text is not conditional:\n%s", name, text)
		}
		if strings.Contains(text, "`<name>`") && !strings.Contains(text, "is that index's name") {
			t.Errorf("%s text uses <name> without explaining it", name)
		}
	}
	if strings.Contains(openCodePlugin, "<name>") {
		t.Error("OpenCode plugin text keeps a literal <name>")
	}
}

func TestInstallTextsDescribeGraphQueryAndReport(t *testing.T) {
	for name, text := range map[string]string{
		"claude": getClaudeMDSection("p"), "gemini": getGeminiMDSection("p"),
		"agents": getAgentsMDContent("p"), "cursor": getCursorRulesContent("p"),
	} {
		if !strings.Contains(text, "find FQNs") {
			t.Errorf("%s: graph query must say it finds FQNs", name)
		}
		if strings.Contains(text, "out of the repository root") {
			t.Errorf("%s: stale keep-out-of-root advice", name)
		}
	}
	if s := getClaudeMDSection(""); strings.Contains(s, "read a file or symbol") {
		t.Error("gleann_read takes a path, not a symbol")
	}
}
