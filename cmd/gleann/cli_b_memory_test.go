package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/memory"
)

// CLI tests of `gleann memory` against the local bbolt store (no server:
// GLEANN_REMOTE_ADDR=off via newCLIEnv).

// bMemList returns the blocks `gleann memory list --json [extra...]` prints.
func bMemList(t *testing.T, e *cliEnv, extra ...string) []memory.Block {
	t.Helper()
	r := e.run(append([]string{"memory", "list", "--json"}, extra...)...)
	wantExit(t, r, 0)
	var blocks []memory.Block
	if err := json.Unmarshal([]byte(r.Out), &blocks); err != nil {
		t.Fatalf("memory list --json is not JSON: %v\n%s", err, r.Out)
	}
	return blocks
}

// bSaveConv stores a conversation in the default conversation store.
func bSaveConv(t *testing.T, title string, msgs ...string) *conversations.Conversation {
	t.Helper()
	conv := &conversations.Conversation{Title: title, Indexes: []string{"demo"}, Model: "llama3.2"}
	for i, m := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		conv.Messages = append(conv.Messages, conversations.Message{Role: role, Content: m})
	}
	if err := conversations.DefaultStore().Save(conv); err != nil {
		t.Fatal(err)
	}
	return conv
}

func TestCLIBMemoryRememberListSearchForget(t *testing.T) {
	e := newCLIEnv(t)

	// Empty store.
	r := e.run("memory", "list")
	wantExit(t, r, 0)
	mustContain(t, "empty list", r.Out, "No memories stored.")
	r = e.run("memory", "list", "--tier", "medium")
	wantExit(t, r, 0)
	mustContain(t, "empty tier list", r.Out, `No memories in tier "medium".`)

	r = e.run("memory", "remember", "Project", "uses", "hexagonal", "architecture", "--tag", "arch,design", "--label", "convention")
	wantExit(t, r, 0)
	mustContain(t, "remember", r.Out, "✅ Remembered [", "Project uses hexagonal architecture")
	mustNotContain(t, "remember", r.Out, "via running server")

	// "--" ends flag parsing: the rest is content even when it looks like a flag.
	r = e.run("memory", "remember", "--", "--tag", "is", "literal")
	wantExit(t, r, 0)
	mustContain(t, "remember --", r.Out, "--tag is literal")

	r = e.run("memory", "add", "medium", "Sprint", "12", "focus", "--tag", "sprint", "--label", "plan")
	wantExit(t, r, 0)
	mustContain(t, "add medium", r.Out, "✅ Added to medium [", "Sprint 12 focus")
	r = e.run("memory", "add", "long", "--", "--label", "text")
	wantExit(t, r, 0)
	mustContain(t, "add --", r.Out, "Added to long", "--label text")

	blocks := bMemList(t, e)
	if len(blocks) != 4 {
		t.Fatalf("want 4 blocks, got %d: %+v", len(blocks), blocks)
	}
	var arch *memory.Block
	for i := range blocks {
		if blocks[i].Content == "Project uses hexagonal architecture" {
			arch = &blocks[i]
		}
	}
	if arch == nil {
		t.Fatalf("remembered block missing: %+v", blocks)
	}
	if arch.Tier != memory.TierLong || arch.Label != "convention" || strings.Join(arch.Tags, ",") != "arch,design" {
		t.Errorf("remembered block = tier %q label %q tags %v; want long/convention/[arch design]", arch.Tier, arch.Label, arch.Tags)
	}
	if got := bMemList(t, e, "--tier", "medium"); len(got) != 1 || got[0].Label != "plan" || got[0].Tags[0] != "sprint" {
		t.Errorf("medium tier = %+v, want the sprint note", got)
	}

	// Text listing shows the short id, tier, label and tags.
	r = e.run("memory", "list", "--tier", "long")
	wantExit(t, r, 0)
	mustContain(t, "list long", r.Out, "Memories (3, long)", arch.ID[:8], "convention", "[arch,design]")
	r = e.run("memory", "list")
	wantExit(t, r, 0)
	mustContain(t, "list all", r.Out, "Memories (4, all tiers)")

	// Search: text, JSON, no match.
	r = e.run("memory", "search", "hexagonal")
	wantExit(t, r, 0)
	mustContain(t, "search", r.Out, `Found 1 memory block(s) matching "hexagonal"`, arch.ID[:8])
	r = e.run("memory", "search", "hexagonal", "--json")
	wantExit(t, r, 0)
	var found []memory.Block
	if err := json.Unmarshal([]byte(r.Out), &found); err != nil || len(found) != 1 || found[0].ID != arch.ID {
		t.Errorf("search --json = %q (%v)", r.Out, err)
	}
	r = e.run("memory", "search", "nonexistent-zebra")
	wantExit(t, r, 0)
	mustContain(t, "search miss", r.Out, `No memories matching "nonexistent-zebra"`)

	// Forget by content and by id.
	r = e.run("memory", "forget", "hexagonal")
	wantExit(t, r, 0)
	mustContain(t, "forget", r.Out, `Forgot 1 memory block(s) matching "hexagonal"`)
	medium := bMemList(t, e, "--tier", "medium")[0]
	r = e.run("memory", "forget", medium.ID)
	wantExit(t, r, 0)
	mustContain(t, "forget id", r.Out, "Forgot 1 memory block(s)")
	if got := bMemList(t, e); len(got) != 2 {
		t.Errorf("after two forgets want 2 blocks, got %d", len(got))
	}
}

func TestCLIBMemoryUsageErrors(t *testing.T) {
	e := newCLIEnv(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"memory", "remember"}, "usage: gleann memory remember"},
		{[]string{"memory", "remember", "--tag", "x"}, "no content provided"},
		{[]string{"memory", "forget"}, "usage: gleann memory forget"},
		{[]string{"memory", "search"}, "usage: gleann memory search"},
		{[]string{"memory", "search", "--json"}, "usage: gleann memory search"},
		{[]string{"memory", "list", "--tier", "galaxy"}, "error:"},
		{[]string{"memory", "add", "long"}, "usage: gleann memory add"},
		{[]string{"memory", "add", "galaxy", "text"}, "error:"},
		{[]string{"memory", "add", "long", "--label", "x"}, "no content provided"},
		{[]string{"memory", "prune", "--age", "forever"}, `invalid duration "forever"`},
		{[]string{"memory", "summarize"}, "usage: gleann memory summarize"},
		{[]string{"memory", "summarize", "--last"}, "no saved conversations"},
		{[]string{"memory", "summarize", "--id", "deadbeef"}, "error loading conversation"},
		{[]string{"memory", "reflect", "--dry-run", "--revert", "x"}, "cannot use --dry-run and --revert together"},
		{[]string{"memory", "reflect", "--revert", "no-such-run"}, "error:"},
		{[]string{"memory", "failure"}, "usage: gleann memory failure"},
		{[]string{"memory", "failure", "explode"}, "unknown memory failure subcommand: explode"},
		{[]string{"memory", "failure", "add", "--command", "go test"}, "usage: gleann memory failure add"},
		{[]string{"memory", "export"}, "usage: gleann memory export"},
		{[]string{"memory", "import"}, "usage: gleann memory import"},
		{[]string{"memory", "import-graph"}, "usage: gleann memory import-graph"},
		{[]string{"memory", "import-graph", filepath.Join(e.Home, "missing.json")}, "error importing graph"},
		{[]string{"memory", "teleport"}, "unknown memory subcommand: teleport"},
	} {
		r := e.run(tc.args...)
		if r.Code != 1 {
			t.Errorf("%v: exit %d, want 1 (stdout %q stderr %q)", tc.args, r.Code, r.Out, r.Err)
			continue
		}
		if !strings.Contains(r.Err, tc.want) {
			t.Errorf("%v: stderr %q lacks %q", tc.args, r.Err, tc.want)
		}
	}
	// Help paths exit 0.
	for _, args := range [][]string{{"memory"}, {"memory", "help"}, {"memory", "--help"}} {
		r := e.run(args...)
		wantExit(t, r, 0)
		mustContain(t, strings.Join(args, " "), r.Out, "Memory Tiers:")
	}
}

func TestCLIBMemoryStatsClearPruneCompactMaintain(t *testing.T) {
	e := newCLIEnv(t)
	for _, args := range [][]string{
		{"memory", "add", "medium", "digest one"},
		{"memory", "add", "medium", "digest two"},
		{"memory", "remember", "permanent fact"},
	} {
		wantExit(t, e.run(args...), 0)
	}

	r := e.run("memory", "stats")
	wantExit(t, r, 0)
	mustContain(t, "stats", r.Out, "Memory Statistics:", "Medium-term: 2 blocks", "Long-term:   1 blocks",
		"Total:       3 blocks", "Summaries:   0", "Store path:  "+filepath.Join(e.Home, ".gleann", "memory", "memory.db"))

	// Nothing is old enough to prune.
	r = e.run("memory", "prune")
	wantExit(t, r, 0)
	mustContain(t, "prune default", r.Out, "Pruned 0 item(s) older than 90d")
	r = e.run("memory", "prune", "--age", "2w")
	wantExit(t, r, 0)
	mustContain(t, "prune 2w", r.Out, "older than 2w")
	// A Go duration works too; 1ns prunes the medium blocks but never long ones.
	r = e.run("memory", "prune", "--age", "1ns")
	wantExit(t, r, 0)
	mustContain(t, "prune 1ns", r.Out, "Pruned 2 item(s) older than 1ns")
	if got := bMemList(t, e); len(got) != 1 || got[0].Tier != memory.TierLong {
		t.Fatalf("after prune want only the long block, got %+v", got)
	}

	r = e.run("memory", "compact")
	wantExit(t, r, 0)
	mustContain(t, "compact", r.Out, "Memory compacted: 0 stale/unreliable block(s) pruned (min validity: 0.20)")
	if got := bMemList(t, e); len(got) != 1 {
		t.Errorf("compact removed a fresh block: %+v", got)
	}
	// No block is more than fully valid: a threshold above 1 prunes everything.
	r = e.run("memory", "compact", "--min-validity", "1.5")
	wantExit(t, r, 0)
	mustContain(t, "compact 1.5", r.Out, "Memory compacted: 1 stale/unreliable block(s) pruned (min validity: 1.50)")
	if got := bMemList(t, e); len(got) != 0 {
		t.Errorf("compact 1.5 left %+v", got)
	}

	r = e.run("memory", "maintain")
	wantExit(t, r, 0)
	mustContain(t, "maintain", r.Out, "Running memory maintenance...", "✅ Maintenance complete.")

	wantExit(t, e.run("memory", "add", "medium", "again"), 0)
	wantExit(t, e.run("memory", "remember", "kept until clear"), 0)
	r = e.run("memory", "clear", "--tier", "medium")
	wantExit(t, r, 0)
	mustContain(t, "clear medium", r.Out, `Cleared 1 memory block(s) from tier "medium"`)
	r = e.run("memory", "clear")
	wantExit(t, r, 0)
	mustContain(t, "clear all", r.Out, "Cleared 1 memory block(s) from all tiers")
	if got := bMemList(t, e); len(got) != 0 {
		t.Errorf("clear left %d blocks", len(got))
	}
}

func TestCLIBMemoryContext(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("memory", "context")
	wantExit(t, r, 0)
	mustContain(t, "empty context", r.Out, "(empty memory context)")

	wantExit(t, e.run("memory", "remember", "Deploys go through the staging cluster"), 0)
	r = e.run("memory", "context", "--verbose")
	wantExit(t, r, 0)
	mustContain(t, "context", r.Out, "Deploys go through the staging cluster")
	if r.Err == "" {
		t.Error("context --verbose printed no budget report on stderr")
	}

	r = e.run("memory", "context", "--json")
	wantExit(t, r, 0)
	var cw map[string]any
	if err := json.Unmarshal([]byte(r.Out), &cw); err != nil {
		t.Fatalf("context --json: %v\n%s", err, r.Out)
	}

	// Task context with a recorded failure for the command.
	wantExit(t, e.run("memory", "failure", "add", "--command", "make lint", "--error", "staticcheck SA1019", "--fix", "use the new API", "--path", "pkg/a.go"), 0)
	r = e.run("memory", "context", "--command", "make lint", "--path", "pkg/a.go")
	wantExit(t, r, 0)
	mustContain(t, "task context", r.Out, "staticcheck SA1019")
}

func TestCLIBMemorySummarize(t *testing.T) {
	e := newCLIEnv(t)
	conv := bSaveConv(t, "Language choice", "Which language for the backend?", "We standardise on Go 1.25.")
	e.Ollama.setReply("decision: Backend services use Go 1.25\npreference: tabs over spaces")

	r := e.run("memory", "summarize", "--last", "--extract")
	wantExit(t, r, 0)
	mustContain(t, "summarize", r.Out, `Summary saved for "Language choice"`,
		"📝 [decision] Backend services use Go 1.25", "📝 [preference] tabs over spaces")
	mustContain(t, "summarize stderr", r.Err, `Summarizing conversation "Language choice"`, "Extracting memories...")
	if e.Ollama.count("/api/generate") != 2 {
		t.Errorf("want 2 LLM calls (summary + extraction), got %d", e.Ollama.count("/api/generate"))
	}

	blocks := bMemList(t, e, "--tier", "long")
	if len(blocks) != 2 {
		t.Fatalf("extracted blocks = %+v", blocks)
	}
	r = e.run("memory", "stats")
	mustContain(t, "stats after summarize", r.Out, "Summaries:   1")

	// By id prefix, with an LLM that finds nothing worth keeping.
	e.Ollama.setReply("NONE")
	r = e.run("memory", "summarize", "--id", conv.ID[:8], "--extract")
	wantExit(t, r, 0)
	mustContain(t, "summarize --id", r.Out, "No important facts extracted.")
}

func TestCLIBMemoryReflectApplyAndRevert(t *testing.T) {
	e := newCLIEnv(t)
	bSaveConv(t, "Testing policy", "How do we test?", "Every package runs go test -race in CI.")
	e.Ollama.setReply("fact: Every package runs go test -race in CI\n")

	r := e.run("memory", "reflect")
	wantExit(t, r, 0)
	mustContain(t, "reflect", r.Out, "Reflection run ", "applied:", "Blocks added (")
	// The run id is the third word of "Reflection run <id> applied:".
	fields := strings.Fields(strings.SplitN(r.Out, "\n", 2)[0])
	if len(fields) < 3 {
		t.Fatalf("cannot parse run id from %q", r.Out)
	}
	runID := fields[2]
	if len(bMemList(t, e)) == 0 {
		t.Fatal("reflect wrote no blocks")
	}

	// A second reflection has nothing new.
	r = e.run("memory", "reflect", "--dry-run")
	wantExit(t, r, 0)
	mustContain(t, "reflect dry-run", r.Out, "Reflection change set (run id:")

	r = e.run("memory", "reflect", "--revert", runID)
	wantExit(t, r, 0)
	mustContain(t, "revert", r.Out, "Reverted reflection run "+runID)
	if got := bMemList(t, e); len(got) != 0 {
		t.Errorf("revert left %d blocks: %+v", len(got), got)
	}
}

func TestCLIBMemoryReflectNothingToDo(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("memory", "reflect")
	wantExit(t, r, 0)
	mustContain(t, "reflect empty", r.Out, "no new memories found")
	r = e.run("memory", "reflect", "--dry-run")
	wantExit(t, r, 0)
	mustContain(t, "reflect empty dry-run", r.Out, "No changes to apply.")
}

func TestCLIBMemoryOpenFailure(t *testing.T) {
	e := newCLIEnv(t)
	// A directory where the database file should be cannot be opened.
	memDir := filepath.Join(e.Home, "badmem")
	if err := os.MkdirAll(filepath.Join(memDir, "memory.db"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_MEMORY_DIR", memDir)
	r := e.run("memory", "stats")
	wantExit(t, r, 1)
	mustContain(t, "open failure", r.Err, "error opening memory store")
}

func TestCLIBDefaultSummarizerConfig(t *testing.T) {
	e := newCLIEnv(t)
	cfg := defaultSummarizerConfig()
	if cfg.Provider != "ollama" || cfg.BaseURL != e.Ollama.URL || cfg.Model != DefaultLLMModel {
		t.Errorf("without saved config = %+v", cfg)
	}

	// Saved config: provider/model come from it. openai gets openai_base_url
	// (empty: the summarizer fails, never OpenAI's cloud by default), never
	// the Ollama host or OLLAMA_HOST.
	bWriteConfig(t, e, map[string]any{"llm_provider": "openai", "llm_model": "gpt-x", "openai_api_key": "sk-1", "ollama_host": "http://saved:1"})
	cfg = defaultSummarizerConfig()
	if cfg.Provider != "openai" || cfg.Model != "gpt-x" || cfg.APIKey != "sk-1" || cfg.BaseURL != "" {
		t.Errorf("openai saved config = %+v", cfg)
	}
	bWriteConfig(t, e, map[string]any{"llm_provider": "openai", "llm_model": "gpt-x", "openai_api_key": "sk-1", "openai_base_url": "http://vllm:8000/v1", "ollama_host": "http://saved:1"})
	if cfg = defaultSummarizerConfig(); cfg.BaseURL != "http://vllm:8000/v1" || cfg.APIKey != "sk-1" {
		t.Errorf("openai with base url = %+v", cfg)
	}

	// anthropic: its own default endpoint and key, not the Ollama host.
	t.Setenv("OLLAMA_HOST", "")
	bWriteConfig(t, e, map[string]any{"llm_provider": "anthropic", "anthropic_api_key": "ak-1", "ollama_host": "http://saved:1"})
	cfg = defaultSummarizerConfig()
	if cfg.Provider != "anthropic" || cfg.APIKey != "ak-1" || cfg.BaseURL != "" {
		t.Errorf("anthropic saved config = %+v", cfg)
	}

	bWriteConfig(t, e, map[string]any{"llm_provider": "ollama", "ollama_api_key": "ok-1"})
	cfg = defaultSummarizerConfig()
	if cfg.Provider != "ollama" || cfg.APIKey != "ok-1" {
		t.Errorf("ollama saved config = %+v", cfg)
	}
}

// bWriteConfig writes ~/.gleann/config.json for the environment.
func bWriteConfig(t *testing.T, e *cliEnv, cfg map[string]any) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.Home, ".gleann")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// bAgeConversation rewrites a stored conversation's updated_at.
func bAgeConversation(t *testing.T, e *cliEnv, id string, age time.Duration) {
	t.Helper()
	p := filepath.Join(e.Home, ".gleann", "conversations", id+".json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["updated_at"] = time.Now().Add(-age).Format(time.RFC3339Nano)
	data, _ = json.Marshal(m)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCLIBMemoryFailureListFormats(t *testing.T) {
	e := newCLIEnv(t)
	r := e.run("memory", "failure", "list")
	wantExit(t, r, 0)
	mustContain(t, "empty failures", r.Out, "No failures recorded.")

	r = e.run("memory", "failure", "add", "--command", "go build", "--error", "undefined: Foo", "--fix", "import pkg/foo", "--path", "a.go", "--path", "b.go", "--symbol", "Foo")
	wantExit(t, r, 0)
	mustContain(t, "failure add", r.Out, "Recorded failure [", `for "go build"`)

	r = e.run("memory", "failure", "list")
	wantExit(t, r, 0)
	mustContain(t, "failure list", r.Out, "Recorded failures (1)", "command: go build", "error: undefined: Foo", "fix: import pkg/foo", "files: a.go, b.go")

	r = e.run("memory", "failure", "list", "--json")
	wantExit(t, r, 0)
	var fs []memory.Block
	if err := json.Unmarshal([]byte(r.Out), &fs); err != nil || len(fs) != 1 || fs[0].Metadata[memory.FailureMetaFix] != "import pkg/foo" {
		t.Errorf("failure list --json = %q (%v)", r.Out, err)
	}
}

func TestCLIBMemoryExportImportErrors(t *testing.T) {
	e := newCLIEnv(t)
	wantExit(t, e.run("memory", "remember", "exported fact"), 0)

	// Exporting below a regular file fails.
	file := filepath.Join(e.Home, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := e.run("memory", "export", "--dir", filepath.Join(file, "sub"))
	wantExit(t, r, 1)
	mustContain(t, "export error", r.Err, "error exporting memory")

	r = e.run("memory", "import", "--dir", filepath.Join(e.Home, "no-such-backup"))
	wantExit(t, r, 1)
	mustContain(t, "import error", r.Err, "error importing memory")

	// Re-importing an unchanged export reports the unchanged block.
	dir := filepath.Join(e.Home, "backup")
	wantExit(t, e.run("memory", "export", "--dir", dir), 0)
	r = e.run("memory", "import", "--dir", dir)
	wantExit(t, r, 0)
	mustContain(t, "reimport", r.Out, "1 unchanged")
}
