package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// CLI tests of `gleann index build|rebuild|sync|watch`: argument handling,
// the dual-model and approval-gate paths, and the failure modes.

func TestCLIEBuildArgumentErrors(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()

	r := e.run("index", "build")
	wantExit(t, r, 1)
	mustContain(t, "no name", r.combined(), "Usage: gleann index build")

	r = e.run("index", "build", "--help")
	wantExit(t, r, 0)
	mustContain(t, "help", r.Out+r.Err, "Usage: gleann index build")

	r = e.run("index", "build", "--graph")
	wantExit(t, r, 1)
	mustContain(t, "flag as name", r.Err, `index name "--graph" looks like a flag`)

	r = e.run("index", "build", "demo")
	wantExit(t, r, 1)
	mustContain(t, "no docs", r.Err, "--docs flag required")

	r = e.run("index", "build", "demo", "--docs", e.Docs, "--provider", "llamacpp", "--model", "bge-m3", "--yes")
	wantExit(t, r, 1)
	mustContain(t, "llamacpp needs a gguf model", r.Err, "error initializing llamacpp")

	// An empty source directory has nothing to index.
	empty := filepath.Join(e.Home, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	r = e.run("index", "build", "demo", "--docs", empty, "--no-report", "--no-agents")
	wantExit(t, r, 1)
	mustContain(t, "no documents", r.Err, "error: no documents found")

	// A failing embedding backend is reported with its cause.
	e.Ollama.mu.Lock()
	e.Ollama.failEmb = true
	e.Ollama.mu.Unlock()
	r = e.run("index", "build", "demo", "--docs", e.Docs, "--no-report", "--no-agents")
	wantExit(t, r, 1)
	mustContain(t, "embedding failure", r.Err, "error:", "500")
}

func TestCLIEBuildModesAndGate(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()

	// An estimate over the --max-minutes budget asks for a decision (exit 4)
	// and builds nothing.
	r := e.run("index", "build", "gated", "--docs", e.Docs, "--max-minutes", "0.0000001")
	wantExit(t, r, 4)
	mustContain(t, "gate", r.Out, "estimated_time_exceeds_threshold", `"proceed"`, `"fast"`, `"cancel"`)
	if _, err := gleann.GetIndexMeta(e.IndexDir, "gated"); err == nil {
		t.Error("a gated build must not create the index")
	}

	r = e.run("index", "build", "gated", "--docs", e.Docs, "--approve", "cancel")
	wantExit(t, r, 0)
	mustContain(t, "cancelled", r.Out, "Indexing cancelled by approval flag.")
	if _, err := gleann.GetIndexMeta(e.IndexDir, "gated"); err == nil {
		t.Error("a cancelled build must not create the index")
	}

	// "fast" switches the build to code-only indexing.
	r = e.run("index", "build", "fast", "--docs", e.Docs, "--approve", "fast", "--no-report", "--no-agents")
	wantExit(t, r, 0)
	mustContain(t, "fast", r.Out, "Approval mode 'fast' selected: switching to code-only indexing.")
	if meta, err := gleann.GetIndexMeta(e.IndexDir, "fast"); err != nil || meta.NumPassages == 0 {
		t.Errorf("fast build produced no index: %+v %v", meta, err)
	}

	// --yes is --approve proceed; --docs-only and --code-only select the mode.
	r = e.run("index", "build", "docsonly", "--docs", e.Docs, "--docs-only", "--yes", "--no-report", "--no-agents", "--doc-model", "bge-m3")
	wantExit(t, r, 0)
	r = e.run("index", "build", "codeonly", "--docs", e.Docs, "--code-only", "--yes", "--no-report", "--no-agents", "--code-model", "nomic-embed-text")
	wantExit(t, r, 0)
	metaCode, err := gleann.GetIndexMeta(e.IndexDir, "codeonly")
	if err != nil {
		t.Fatal(err)
	}
	if metaCode.EmbeddingModel != "nomic-embed-text" {
		t.Errorf("code-only build used model %q, want the --code-model", metaCode.EmbeddingModel)
	}
	metaDocs, err := gleann.GetIndexMeta(e.IndexDir, "docsonly")
	if err != nil {
		t.Fatal(err)
	}
	if metaDocs.EmbeddingModel != "bge-m3" {
		t.Errorf("docs-only build used model %q, want the --doc-model", metaDocs.EmbeddingModel)
	}
}

func TestCLIEBuildDualModelAndAgents(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()

	r := e.run("index", "build", "repo", "--docs", e.Docs, "--code-model", "nomic-embed-text", "--doc-model", "bge-m3", "--no-report", "--agents", "--yes")
	wantExit(t, r, 0)
	mustContain(t, "dual", r.Out,
		"Dual-model mode active", `Building code index "repo-code"`, `Building docs index "repo-docs"`,
		"Dual-index build complete", "AGENTS.md generated/updated in")
	for name, model := range map[string]string{"repo-code": "nomic-embed-text", "repo-docs": "bge-m3"} {
		meta, err := gleann.GetIndexMeta(e.IndexDir, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if meta.EmbeddingModel != model {
			t.Errorf("%s was built with %q, want %q", name, meta.EmbeddingModel, model)
		}
	}
	agents, err := os.ReadFile(filepath.Join(e.Docs, "AGENTS.md"))
	if err != nil || !strings.Contains(string(agents), "gleann") {
		t.Errorf("AGENTS.md not written: %v %q", err, agents)
	}
}

func TestCLEDualPassArgs(t *testing.T) {
	args := []string{"repo", "--docs", "src", "--mode", "all", "--model", "m", "--code-model", "c", "--doc-model", "d", "--docs-model", "x", "--graph"}
	got := dualPassArgs(args, "repo", "repo-code", "code", "granite")
	want := []string{"repo-code", "--docs", "src", "--graph", "--mode", "code", "--model", "granite"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("dualPassArgs = %v, want %v", got, want)
	}
}

func TestCLIERebuildAndSyncEdges(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()

	// Rebuilding an index that does not exist builds it.
	r := e.run("index", "rebuild", "fresh", "--docs", e.Docs, "--no-report", "--no-agents")
	wantExit(t, r, 0)
	mustContain(t, "rebuild of a missing index", r.Out, `Removing existing index "fresh"`, `Rebuilding index "fresh"`)

	// --files restricts a sync to the listed files.
	changed := e.write("docs/guide.md", "# Guide\n\nA different sentence about pineapples.\n")
	r = e.run("index", "sync", "fresh", "--docs", e.Docs, "--files", changed+","+filepath.Join(e.Docs, "gone.md"))
	wantExit(t, r, 0)
	r = e.run("search", "fresh", "pineapples", "--json")
	wantExit(t, r, 0)
	mustContain(t, "synced file", r.Out, "pineapples")

	// An index without a recorded source directory needs --docs for a sync.
	meta := filepath.Join(e.IndexDir, "fresh", "fresh.meta.json")
	data, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	// meta.json holds the path JSON-escaped (backslashes doubled on Windows).
	quoted, _ := json.Marshal(e.Docs)
	stripped := strings.ReplaceAll(string(data), strings.Trim(string(quoted), `"`), "")
	if stripped == string(data) {
		t.Fatalf("source dir %q not found in %s", e.Docs, data)
	}
	if err := os.WriteFile(meta, []byte(stripped), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run("index", "sync", "fresh")
	wantExit(t, r, 1)
	mustContain(t, "no source dir", r.Err, "--docs flag required", `"fresh" does not have a recorded source_dir`)
}

func TestCLIEWatchFailures(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	orig := watchNotify
	t.Cleanup(func() { watchNotify = orig })
	watchNotify = func(stop chan<- os.Signal) { stop <- os.Interrupt }

	r := e.run("index", "watch", "w", "--docs", e.Docs, "--provider", "llamacpp", "--model", "bge-m3")
	wantExit(t, r, 1)
	mustContain(t, "llamacpp", r.Err, "error initializing llamacpp")

	// A vault database that cannot be opened (a non-empty directory sits at
	// its path) stops the watcher before it starts.
	vaultDB := filepath.Join(e.Home, ".gleann", "vault", "vault.db")
	if err := os.MkdirAll(vaultDB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vaultDB, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run("index", "watch", "w", "--docs", e.Docs)
	wantExit(t, r, 1)
	mustContain(t, "vault", r.Err, "error initializing vault tracker")
	if err := os.RemoveAll(vaultDB); err != nil {
		t.Fatal(err)
	}

	// A failing initial sync of an existing index is reported, and the
	// watcher keeps running (it stops here on the signal).
	e.build("w")
	e.write("docs/guide.md", "# Guide\n\nChanged so that the sync has to embed.\n")
	e.Ollama.mu.Lock()
	e.Ollama.failEmb = true
	e.Ollama.mu.Unlock()
	r = e.run("index", "watch", "w", "--docs", e.Docs)
	wantExit(t, r, 0)
	mustContain(t, "failed initial sync", r.Err, "initial sync failed", "gleann index rebuild w")
	mustContain(t, "still stops", r.Out, "Stopping watcher...")
}

func TestCLIEWatchUpdatesOnChange(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("w")
	before, err := gleann.GetIndexMeta(e.IndexDir, "w")
	if err != nil {
		t.Fatal(err)
	}

	orig := watchNotify
	t.Cleanup(func() { watchNotify = orig })
	// Once the watcher runs, keep writing new files until the index grows,
	// then stop it. The deadline only guards against a watcher that never
	// reacts; the assertions below then fail.
	watchNotify = func(stop chan<- os.Signal) {
		go func() {
			deadline := time.Now().Add(60 * time.Second)
			tick := time.NewTicker(150 * time.Millisecond)
			defer tick.Stop()
			for i := 0; time.Now().Before(deadline); i++ {
				<-tick.C
				p := filepath.Join(e.Docs, "docs", fmt.Sprintf("added-%d.md", i))
				_ = os.WriteFile(p, []byte(fmt.Sprintf("# Added %d\n\nKumquat note number %d.\n", i, i)), 0o644)
				if meta, err := gleann.GetIndexMeta(e.IndexDir, "w"); err == nil && meta.NumPassages > before.NumPassages {
					break
				}
			}
			stop <- os.Interrupt
		}()
	}

	r := e.run("index", "watch", "w", "--docs", e.Docs, "--interval", "1")
	wantExit(t, r, 0)
	mustContain(t, "watch output", r.Out, `Index "w" exists, syncing`, "file(s) changed, updating index", "Update complete", "Stopping watcher...")
	after, err := gleann.GetIndexMeta(e.IndexDir, "w")
	if err != nil || after.NumPassages <= before.NumPassages {
		t.Errorf("watcher did not index the new files: %+v (before %d)", after, before.NumPassages)
	}
}
