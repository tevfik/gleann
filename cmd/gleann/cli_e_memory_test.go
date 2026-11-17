package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// CLI tests of `gleann memory` against a store whose database is damaged
// (tables missing), the summary/export/import flows and the graph import.

// eBreakMemoryStore drops the tables of the memory database the test home
// uses, after the store was created and its schema recorded as current: the
// store still opens, but every read and write fails.
func eBreakMemoryStore(t *testing.T) {
	t.Helper()
	dsn := filepath.Join(os.Getenv("GLEANN_MEMORY_DIR"), "memory.sqlite")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"blocks_lex", "blocks", "summaries", "entities", "relations", "entity_archive", "reflection_runs"} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
}

func TestCLIEMemoryDamagedStore(t *testing.T) {
	e := newCLIEnv(t)
	wantExit(t, e.run("memory", "remember", "seed the store"), 0)
	eBreakMemoryStore(t)

	exportDir := filepath.Join(e.Home, "export")
	cases := []struct {
		name string
		args []string
	}{
		{"remember", []string{"memory", "remember", "something new"}},
		{"forget", []string{"memory", "forget", "seed"}},
		{"list", []string{"memory", "list"}},
		{"list tier", []string{"memory", "list", "--tier", "long"}},
		{"search", []string{"memory", "search", "seed"}},
		{"clear", []string{"memory", "clear"}},
		{"add", []string{"memory", "add", "medium", "a block"}},
		{"stats", []string{"memory", "stats"}},
		{"context", []string{"memory", "context"}},
		{"task context", []string{"memory", "context", "--command", "go test"}},
		{"maintain", []string{"memory", "maintain"}},
		{"failure add", []string{"memory", "failure", "add", "--command", "go test", "--error", "boom", "--fix", "retry"}},
		{"failure list", []string{"memory", "failure", "list"}},
		{"export", []string{"memory", "export", "--dir", exportDir}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := e.run(c.args...)
			wantExit(t, r, 1)
			mustContain(t, strings.Join(c.args, " "), r.Err, "error")
			if strings.Contains(r.Err, "opening memory store") {
				t.Errorf("the damaged store must open; only its use fails: %s", r.Err)
			}
		})
	}
}

func TestCLIEMemoryStoreLocked(t *testing.T) {
	e := newCLIEnv(t)
	// A store directory that is a file cannot be opened.
	file := filepath.Join(e.Home, "memdir-is-a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_MEMORY_DIR", file)
	r := e.run("memory", "list")
	wantExit(t, r, 1)
	mustContain(t, "unopenable store", r.Err, "error opening memory store")
	mustNotContain(t, "not a lock problem", r.Err, "hint: another process")
}

func TestCLIEMemorySummarizeAndTransfer(t *testing.T) {
	e := newCLIEnv(t)
	conv := bSaveConv(t, "Deploy chat", "How do we deploy?", "Run the deploy script.")
	e.Ollama.setReply("The team deploys with the deploy script.")

	r := e.run("memory", "summarize", "--id", conv.ID, "--extract")
	wantExit(t, r, 0)
	mustContain(t, "summary", r.Out, "Summary saved for", "The team deploys with the deploy script.")
	mustContain(t, "extraction", r.Err, "Extracting memories...")

	// "LABEL: fact" lines of the reply are stored as labelled blocks.
	e.Ollama.setReply("decision: Deploys use the deploy script")
	r = e.run("memory", "summarize", "--last", "--extract")
	wantExit(t, r, 0)
	mustContain(t, "extracted fact", r.Out, "[decision] Deploys use the deploy script")

	e.Ollama.setReply("NONE")
	r = e.run("memory", "summarize", "--last", "--extract")
	wantExit(t, r, 0)
	mustContain(t, "no facts", r.Out, "No important facts extracted.")
	e.Ollama.setReply("The team deploys with the deploy script.")

	// Export, then import into a fresh store: blocks and the summary come back.
	wantExit(t, e.run("memory", "remember", "Exported fact about caching", "--tag", "cache"), 0)
	dir := filepath.Join(e.Home, "memexport")
	r = e.run("memory", "export", "--dir", dir)
	wantExit(t, r, 0)
	mustContain(t, "export", r.Out, "Exported", "summary(ies)")

	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(e.Home, "other-memory"))
	r = e.run("memory", "import", "--dir", dir)
	wantExit(t, r, 0)
	mustContain(t, "import", r.Out, "Imported memory blocks from", "imported", "summary(ies)")
	r = e.run("memory", "import", "--dir", dir)
	wantExit(t, r, 0)
	mustContain(t, "second import", r.Out, "0 imported", "3 unchanged")

	// A block edited in the export directory differs from the stored one: a conflict, resolved.
	var edited int
	var walk func(string)
	walk = func(d string) {
		es, _ := os.ReadDir(d)
		for _, en := range es {
			p := filepath.Join(d, en.Name())
			if en.IsDir() {
				walk(p)
				continue
			}
			data, err := os.ReadFile(p)
			if err == nil && strings.Contains(string(data), "Exported fact about caching") {
				if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(data), "Exported fact about caching", "Exported fact about caching, revised")), 0o644); err != nil {
					t.Fatal(err)
				}
				edited++
			}
		}
	}
	walk(dir)
	if edited == 0 {
		t.Fatal("exported block file not found")
	}
	r = e.run("memory", "import", "--dir", dir)
	wantExit(t, r, 0)
	mustContain(t, "edited block", r.Out, "1 conflict(s) resolved")
}

func TestCLIEMemoryImportGraphSyncsEntities(t *testing.T) {
	e := newCLIEnv(t)
	write := func(name string, doc map[string]any) string {
		t.Helper()
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(e.Home, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	export := func(ns string, entities ...map[string]any) map[string]any {
		return map[string]any{
			"format": "gleann-kuzu-memory-export", "version": 1, "namespace": ns,
			"entities": entities, "relations": []any{}, "archive": []any{},
		}
	}
	entity := func(id, content string) map[string]any {
		return map[string]any{"id": id, "type": "Concept", "content": content, "attributes": `{"source":"test"}`}
	}

	file := write("graph.json", export("docs", entity("e1", "Rate limiting uses a token bucket"), entity("e2", "Tokens expire after an hour")))
	r := e.run("memory", "import-graph", file)
	wantExit(t, r, 0)
	mustContain(t, "import", r.Out, "docs: 2 entities, 0 relations, 0 archive rows added",
		"2 entities added to the memory vector index docs_memory_vectors")
	if _, err := os.Stat(filepath.Join(e.IndexDir, "docs_memory_vectors")); err != nil {
		t.Errorf("memory vector index not built: %v", err)
	}

	r = e.run("memory", "import-graph", file)
	wantExit(t, r, 0)
	mustContain(t, "again", r.Out, "docs: already imported from this file, nothing changed")

	// An export without entities has nothing to add to the vector index.
	r = e.run("memory", "import-graph", write("empty.json", export("empty")))
	wantExit(t, r, 0)
	mustNotContain(t, "no entities", r.Out, "memory vector index")

	// The entities stay imported when the embedding backend is down.
	e.Ollama.mu.Lock()
	e.Ollama.failEmb = true
	e.Ollama.mu.Unlock()
	r = e.run("memory", "import-graph", write("down.json", export("down", entity("e4", "Embedded later"))))
	wantExit(t, r, 0)
	mustContain(t, "backend down", r.Out+r.Err, "down: 1 entities", "memory vector index down_memory_vectors not updated")
}

func TestCLIEMemorySummaryNotSaved(t *testing.T) {
	e := newCLIEnv(t)
	bSaveConv(t, "Deploy chat", "How do we deploy?", "Run the deploy script.")
	wantExit(t, e.run("memory", "remember", "seed"), 0)
	eBreakMemoryStore(t)
	r := e.run("memory", "summarize", "--last")
	wantExit(t, r, 1)
	mustContain(t, "summary store failure", r.Err, "error saving summary")
}
