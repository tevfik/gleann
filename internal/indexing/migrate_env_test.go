package indexing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/internal/testutil/format1"
	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
)

// writeOldIndex writes a lexical-only index in the old store format whose
// meta records docs as its source directory.
func writeOldIndex(t *testing.T, indexDir, name, docs string, graph bool) {
	t.Helper()
	meta := map[string]any{"lexical_only": true}
	if docs != "" {
		meta["source_dir"] = docs
	}
	if err := format1.Write(format1.Index{Dir: indexDir, Name: name, Graph: graph, Meta: meta,
		Passages: []format1.Passage{
			{ID: 0, Text: "package main\n\nfunc Add(a, b int) int { return a + b }", Metadata: map[string]any{"source": "main.go"}},
			{ID: 1, Text: "# Project\n\nIntro text.", Metadata: map[string]any{"source": "README.md"}},
		}}); err != nil {
		t.Fatal(err)
	}
}

// Migrate converts the passages, its memory-vector companion, and rebuilds
// the code graph from the recorded sources when the old index had one.
func TestMigrateConvertsAndRebuildsTheGraph(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	writeOldIndex(t, env.cfg.IndexDir, "legacy", docs, true)
	writeOldIndex(t, env.cfg.IndexDir, "legacy_memory_vectors", "", false)

	var out bytes.Buffer
	res, err := Migrate(context.Background(), MigrateOptions{Options: env.options(&out), Name: "legacy"})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if res.AlreadyFormat2 || res.Store == nil || !res.Store.HadGraph || !res.GraphBuilt || res.MemoryVectors == nil {
		t.Fatalf("result = %+v", res)
	}
	if gleann.StoreFormatOf(filepath.Join(env.cfg.IndexDir, "legacy", "legacy")) != 2 {
		t.Fatal("index not converted")
	}
	if gleann.StoreFormatOf(filepath.Join(env.cfg.IndexDir, "legacy_memory_vectors", "legacy_memory_vectors")) != 2 {
		t.Fatal("memory vectors not converted")
	}
	if !gleann.HasCodeGraph(env.cfg.IndexDir, "legacy") {
		t.Fatal("no code graph after the migration")
	}
	st, err := store.Open(filepath.Join(env.cfg.IndexDir, "legacy", "legacy.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if n := countRows(t, st.ReaderDB(), `SELECT count(*) FROM symbols WHERE name = 'Add'`); n != 1 {
		t.Fatalf("Add declared %d times in the rebuilt graph", n)
	}
	if !strings.Contains(out.String(), "Building the code graph") {
		t.Errorf("graph rebuild not announced:\n%s", out.String())
	}

	// A second run finds format 2 and, asked for a graph, rebuilds it.
	res, err = Migrate(context.Background(), MigrateOptions{Options: env.options(nil), Name: "legacy", Graph: true})
	if err != nil || !res.AlreadyFormat2 || !res.GraphBuilt || res.Store != nil {
		t.Fatalf("second run = %+v, %v", res, err)
	}
	// Without --graph there is nothing to do.
	res, err = Migrate(context.Background(), MigrateOptions{Options: env.options(nil), Name: "legacy"})
	if err != nil || !res.AlreadyFormat2 || res.GraphBuilt {
		t.Fatalf("third run = %+v, %v", res, err)
	}
}

// When the graph cannot be rebuilt (no recorded source, or the source is
// gone) the passages are still converted and the error says how to build the
// graph by hand.
func TestMigrateGraphNeedsTheSources(t *testing.T) {
	env := newEnv(t)
	writeOldIndex(t, env.cfg.IndexDir, "nosrc", "", true)
	res, err := Migrate(context.Background(), MigrateOptions{Options: env.options(nil), Name: "nosrc"})
	if !errors.Is(err, ErrGraphUpdate) || !strings.Contains(err.Error(), "records no source directory") {
		t.Fatalf("err = %v", err)
	}
	if res == nil || res.Store == nil || res.GraphBuilt {
		t.Fatalf("result = %+v", res)
	}
	if gleann.StoreFormatOf(filepath.Join(env.cfg.IndexDir, "nosrc", "nosrc")) != 2 {
		t.Fatal("the passages must be converted even when the graph fails")
	}

	writeOldIndex(t, env.cfg.IndexDir, "gonesrc", filepath.Join(t.TempDir(), "removed"), true)
	if _, err := Migrate(context.Background(), MigrateOptions{Options: env.options(nil), Name: "gonesrc"}); err == nil || !strings.Contains(err.Error(), "is not available") {
		t.Fatalf("err = %v", err)
	}

	if _, err := Migrate(context.Background(), MigrateOptions{Options: env.options(nil), Name: "absent"}); err == nil {
		t.Fatal("migrating a missing index succeeded")
	}
	//lint:ignore SA1012 Migrate documents that a nil context means Background.
	if _, err := Migrate(nil, MigrateOptions{Options: env.options(nil)}); err == nil || !strings.Contains(err.Error(), "name required") {
		t.Fatalf("nameless migrate: %v", err)
	}
	if err := rebuildGraphFromSources(context.Background(), "absent", &Options{Config: env.cfg}); err == nil || !strings.Contains(err.Error(), "reading index meta") {
		t.Fatalf("rebuildGraphFromSources of a missing index: %v", err)
	}
}

// A memory-vector companion that cannot be converted fails the migration
// with its name.
func TestMigrateMemoryVectorsFailure(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	writeOldIndex(t, env.cfg.IndexDir, "m", docs, false)
	writeOldIndex(t, env.cfg.IndexDir, "m_memory_vectors", "", false)
	// Damage the companion's passage store.
	memDir := filepath.Join(env.cfg.IndexDir, "m_memory_vectors")
	entries, _ := os.ReadDir(memDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".db") {
			if err := os.WriteFile(filepath.Join(memDir, e.Name()), []byte("garbage, not bbolt"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, err := Migrate(context.Background(), MigrateOptions{Options: env.options(nil), Name: "m"})
	if err == nil || !strings.Contains(err.Error(), "m_memory_vectors") {
		t.Fatalf("err = %v, want the companion's failure", err)
	}
}

func TestBuildGraphIndexErrReportsMissingIndex(t *testing.T) {
	err := BuildGraphIndexErr(GraphOptions{Name: "nope", IndexDir: t.TempDir(), DocsDir: t.TempDir()})
	if !errors.Is(err, gleann.ErrIndexNotFound) {
		t.Fatalf("err = %v", err)
	}
	var errBuf bytes.Buffer
	BuildGraphIndex(GraphOptions{Name: "nope", IndexDir: t.TempDir(), DocsDir: t.TempDir(), Stderr: &errBuf})
	if !strings.Contains(errBuf.String(), "warning:") {
		t.Fatalf("BuildGraphIndex did not warn: %q", errBuf.String())
	}
}

func TestGenerateGraphReportErrors(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)
	// The output path is a directory: the report cannot be created.
	if err := GenerateGraphReport("g", env.cfg.IndexDir, docs, t.TempDir()); err == nil || !strings.Contains(err.Error(), "creating file") {
		t.Fatalf("err = %v", err)
	}
	out := filepath.Join(t.TempDir(), "GRAPH_REPORT.md")
	if err := GenerateGraphReport("g", env.cfg.IndexDir, docs, out); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(out); len(data) == 0 {
		t.Errorf("report does not name the index: %q", data)
	}
	if err := GenerateGraphReport("absent", env.cfg.IndexDir, docs, out); err == nil {
		t.Fatal("report of a missing index")
	}
}

func TestNewEmbedderProviders(t *testing.T) {
	cases := []gleann.Config{
		{EmbeddingProvider: "ollama", EmbeddingModel: "m", OllamaHost: "http://o"},
		{EmbeddingProvider: "openai", EmbeddingModel: "m", OpenAIBaseURL: "http://oa", OpenAIAPIKey: "k"},
		{EmbeddingProvider: "openai", EmbeddingModel: "m"},
		{EmbeddingProvider: "llamacpp", EmbeddingModel: "m.gguf", OpenAIBaseURL: "http://l"},
		{EmbeddingProvider: "llamacpp", EmbeddingModel: "m.gguf"},
		{EmbeddingProvider: "eif", EmbeddingModel: "m"},
		{EmbeddingProvider: "gemini", EmbeddingModel: "m", OpenAIAPIKey: "k"},
	}
	for _, cfg := range cases {
		if c := NewEmbedder(cfg); c == nil {
			t.Errorf("NewEmbedder(%s) = nil", cfg.EmbeddingProvider)
		}
	}
}

// The OpenAI-compatible providers send the configured key to the configured
// base URL.
func TestNewEmbedderRoutesOpenAIToItsBaseURL(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": []float32{1, 2, 3}, "index": 0}}})
	}))
	defer srv.Close()
	for _, provider := range []string{"openai", "llamacpp"} {
		auth = ""
		c := NewEmbedder(gleann.Config{EmbeddingProvider: provider, EmbeddingModel: "m", OpenAIBaseURL: srv.URL, OpenAIAPIKey: "secret-key", OllamaHost: "http://127.0.0.1:1"})
		vecs, err := c.Compute(context.Background(), []string{"hello"})
		if err != nil || len(vecs) != 1 {
			t.Fatalf("%s: %v %v", provider, vecs, err)
		}
		if auth != "Bearer secret-key" {
			t.Errorf("%s: Authorization = %q", provider, auth)
		}
	}
}

// The llamacpp provider needs a GGUF model; the default model name is
// refused before anything is started.
func TestLlamaCPPRequiresAModel(t *testing.T) {
	for _, model := range []string{"", "bge-m3"} {
		cfg := gleann.Config{EmbeddingProvider: "llamacpp", EmbeddingModel: model}
		_, _, err := resolveEmbedder(context.Background(), &Options{}, &cfg, newPrinter(nil, nil))
		if err == nil || !strings.Contains(err.Error(), ".gguf") {
			t.Errorf("model %q: err = %v", model, err)
		}
		env := newEnv(t)
		opts := env.options(nil)
		opts.Config.EmbeddingProvider = "llamacpp"
		opts.Config.EmbeddingModel = model
		if _, err := Build(context.Background(), BuildOptions{Options: opts, Name: "l", DocsDir: fixture(t)}); err == nil || !strings.Contains(err.Error(), "initializing embedder") {
			t.Errorf("Build with llamacpp and model %q: %v", model, err)
		}
	}
	stop, err := startLlamaCPP(context.Background(), &gleann.Config{EmbeddingProvider: "ollama"}, newPrinter(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	stop()
}

// A model named on purpose enables multimodal processing; the capability
// line says what the model can do.
func TestNewMultimodalProcessorWithModel(t *testing.T) {
	srv := fakeVision(t, false)
	var lines []string
	out := func(f string, a ...any) { lines = append(lines, f) }
	p := NewMultimodalProcessor(srv.URL, "api-key", "llava", out)
	if p == nil || p.Model != "llava" || p.APIKey != "api-key" || p.OllamaHost != srv.URL {
		t.Fatalf("processor = %+v", p)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "Multimodal indexing") {
		t.Errorf("capability lines = %v", lines)
	}
	t.Setenv("GLEANN_MULTIMODAL_MODEL", "from-env")
	if p := NewMultimodalProcessor(srv.URL, "", "", nil); p == nil || p.Model != "from-env" {
		t.Fatalf("model from the environment: %+v", p)
	}
	if opts := (&Options{Config: gleann.Config{MultimodalModel: "cfg-model"}}); opts.multimodalModel() != "cfg-model" {
		t.Error("the configured multimodal model is not used")
	}
}

// The default tracker is shared by every caller in the process and closed
// with the last release; PruneTrackedPaths prunes it.
func TestDefaultTrackerSharingAndPrune(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	t1, rel1 := acquireTracker(&Options{}, newPrinter(nil, nil), "")
	t2, rel2 := acquireTracker(&Options{}, newPrinter(nil, nil), "")
	if t1 == nil || t1 != t2 {
		t.Fatalf("trackers %p %p, want one shared handle", t1, t2)
	}
	ctx := context.Background()
	for _, p := range []string{"/proj/a.go", "/proj/b.go", "/other/c.go"} {
		if err := t1.UpsertRecord(ctx, "h"+p, p, time.Now().Unix(), 1); err != nil {
			t.Fatal(err)
		}
	}
	rel1()
	rel1() // releasing twice is harmless
	if rec, _ := t2.GetRecordByPath(ctx, "/proj/a.go"); rec == nil {
		t.Fatal("the shared tracker was closed while still in use")
	}
	rel2()

	n, err := PruneTrackedPaths(ctx, "/proj/")
	if err != nil || n != 2 {
		t.Fatalf("PruneTrackedPaths = %d, %v; want 2", n, err)
	}
	t3, rel3 := acquireTracker(&Options{}, newPrinter(nil, nil), "")
	defer rel3()
	if rec, _ := t3.GetRecordByPath(ctx, "/other/c.go"); rec == nil {
		t.Error("a path outside the prefix was pruned")
	}
	if tr, _ := acquireTracker(&Options{NoTracker: true}, newPrinter(nil, nil), ""); tr != nil {
		t.Error("NoTracker returned a tracker")
	}
}

// When the default tracker cannot be opened, tracking is skipped with a
// warning and the caller proceeds.
func TestAcquireTrackerFailureWarns(t *testing.T) {
	orig := openDefaultTracker
	t.Cleanup(func() { openDefaultTracker = orig })
	openDefaultTracker = func() (*vault.Tracker, func(), error) {
		return nil, func() {}, errors.New("vault locked")
	}
	var errBuf bytes.Buffer
	tr, release := acquireTracker(&Options{}, newPrinter(nil, &errBuf), "warning: no tracker: %v\n")
	release()
	if tr != nil || !strings.Contains(errBuf.String(), "no tracker: vault locked") {
		t.Fatalf("tracker %v, warning %q", tr, errBuf.String())
	}
	if _, err := PruneTrackedPaths(context.Background(), "/x"); err == nil || !strings.Contains(err.Error(), "opening the vault") {
		t.Fatalf("PruneTrackedPaths: %v", err)
	}
}

func TestPluginResultToDocSections(t *testing.T) {
	res := &gleann.PluginResult{
		Nodes: []gleann.PluginNode{
			{Type: "Document", Data: map[string]any{"title": "T", "format": "pdf", "summary": "S", "word_count": 10.0, "page_count": 2.0}},
			{Type: "Section", Data: map[string]any{"id": "a", "heading": "A", "level": 1.0, "content": "ca"}},
			{Type: "Section", Data: map[string]any{"id": "b", "heading": "B", "level": 2.0, "content": "cb", "summary": 7}},
			{Type: "Other", Data: map[string]any{}},
		},
		Edges: []gleann.PluginEdge{{Type: "HAS_SUBSECTION", From: "a", To: "b"}, {Type: "HAS_SECTION", From: "doc", To: "a"}},
	}
	doc := PluginResultToDoc(res)
	if doc.Document.Title != "T" || doc.Document.Format != "pdf" || doc.Document.WordCount != 10 || doc.Document.PageCount == nil || *doc.Document.PageCount != 2 {
		t.Fatalf("document = %+v", doc.Document)
	}
	if len(doc.Sections) != 2 || doc.Sections[1].ParentID != "a" || doc.Sections[0].ParentID != "" || doc.Sections[1].Level != 2 {
		t.Fatalf("sections = %+v", doc.Sections)
	}
	if doc.Sections[1].Summary != "" {
		t.Error("a non-string summary must read as empty")
	}
}

func TestMarkdownSectionsToPluginResult(t *testing.T) {
	text := "## Second level first\n\nintro\n\n# Top\n\nbody\n\n## Child\n\nchild body\n"
	sections := chunking.ParseMarkdownHeadings(text)
	res := MarkdownSectionsToPluginResult(sections, "doc.md", 9, text)
	if res.Nodes[0].Data["title"] != "Top" {
		t.Errorf("title = %v, want the first level-1 heading", res.Nodes[0].Data["title"])
	}
	var hasSection, hasSub int
	for _, e := range res.Edges {
		switch e.Type {
		case "HAS_SECTION":
			hasSection++
			if e.From != "doc.md" {
				t.Errorf("top-level edge from %q", e.From)
			}
		case "HAS_SUBSECTION":
			hasSub++
		}
	}
	if hasSection == 0 || hasSub == 0 || len(res.Nodes) != len(sections)+1 {
		t.Errorf("edges %d/%d nodes %d for %d sections", hasSection, hasSub, len(res.Nodes), len(sections))
	}
	// Without a level-1 heading the first heading is the title.
	only := chunking.ParseMarkdownHeadings("## Only second\n\ntext\n")
	if got := MarkdownSectionsToPluginResult(only, "o.md", 2, "").Nodes[0].Data["title"]; got != "Only second" {
		t.Errorf("title = %v", got)
	}

	// The round trip keeps the hierarchy.
	doc := PluginResultToDoc(res)
	parents := 0
	for _, s := range doc.Sections {
		if s.ParentID != "" {
			parents++
		}
	}
	if parents != hasSub {
		t.Errorf("%d sections with a parent after the round trip, want %d", parents, hasSub)
	}
}

func TestPluginDocToDelta(t *testing.T) {
	if d := pluginDocToDelta(&PluginDoc{SourcePath: "x.pdf"}); d.RPath != "x.pdf" || len(d.Nodes) != 0 {
		t.Fatalf("nil result = %+v", d)
	}
	pd := &PluginDoc{SourcePath: "reports/q1.pdf", Result: &gleann.PluginResult{
		Nodes: []gleann.PluginNode{
			{Type: "Document", Data: map[string]any{"title": "Q1", "vpath": "archive/2024/q1.pdf", "hash": "h", "summary": "s"}},
			{Type: "Section", Data: map[string]any{"id": "s1", "heading": "Intro", "level": 1.0}},
			{Type: "Section", Data: map[string]any{"id": "s2", "heading": "Deep", "level": int64(2)}},
			{Type: "Section", Data: map[string]any{"heading": "no id"}},
			{Type: "Chunk", Data: map[string]any{"id": "c1", "text": "chunk text", "start_char": 3, "end_char": 14.0}},
			{Type: "Chunk", Data: map[string]any{"text": "no id"}},
		},
		Edges: []gleann.PluginEdge{
			{Type: "HAS_SUBSECTION", From: "s1", To: "s2"},
			{Type: "EXPLAINS", From: "c1", To: "pkg.Func"},
			{Type: "EXPLAINS", From: "", To: "pkg.Func"},
		},
	}}
	d := pluginDocToDelta(pd)
	byID := map[string]int{}
	for i, n := range d.Nodes {
		byID[n.ID] = i
	}
	if len(d.Nodes) != 5 {
		t.Fatalf("nodes = %+v", d.Nodes)
	}
	folder := d.Nodes[byID["folder:archive/2024"]]
	if folder.Type != "folder" || folder.Name != "2024" {
		t.Errorf("folder = %+v", folder)
	}
	doc := d.Nodes[byID["doc:archive/2024/q1.pdf"]]
	if doc.Parent != "folder:archive/2024" || doc.RPath != "reports/q1.pdf" || doc.Name != "Q1" || doc.Hash != "h" {
		t.Errorf("document = %+v", doc)
	}
	if s2 := d.Nodes[byID["s2"]]; s2.Parent != "s1" || s2.Level != 2 {
		t.Errorf("nested heading = %+v", s2)
	}
	if s1 := d.Nodes[byID["s1"]]; s1.Parent != "doc:archive/2024/q1.pdf" {
		t.Errorf("top heading = %+v", s1)
	}
	if c := d.Nodes[byID["c1"]]; c.Type != "chunk" || c.StartChar != 3 || c.EndChar != 14 || c.Text != "chunk text" {
		t.Errorf("chunk = %+v", c)
	}
	if len(d.Explains) != 1 || d.Explains[0].FQN != "pkg.Func" {
		t.Errorf("explains = %+v", d.Explains)
	}
	if intVal(map[string]any{"k": "7"}, "k") != 0 {
		t.Error("a string is not an int")
	}
}

func TestGraphExactEnabled(t *testing.T) {
	for v, want := range map[string]bool{"": true, "1": true, "0": false, "false": false, " OFF ": false, "no": false} {
		t.Setenv("GLEANN_GRAPH_EXACT", v)
		if graphExactEnabled() != want {
			t.Errorf("GLEANN_GRAPH_EXACT=%q: %v", v, !want)
		}
	}
}

func TestDescribeUpdate(t *testing.T) {
	cases := []struct {
		st   gleann.UpdateStats
		want string
	}{
		{gleann.UpdateStats{Mode: gleann.UpdateInPlace, Added: 2, Embedded: 2, EmbedTime: 500 * time.Microsecond}, "updated in place: +2 passages, 2 text(s) embedded in <1ms"},
		{gleann.UpdateStats{Mode: gleann.UpdateMerge, Added: 1, Removed: 3, Passages: 9}, "merged: -3 +1 passages (now 9)"},
		{gleann.UpdateStats{Mode: gleann.UpdateCompact, Added: 1, Removed: 1, Passages: 4, Embedded: 4}, "compacted: -1 +1 passages (now 4), 4 text(s) re-embedded"},
		{gleann.UpdateStats{Mode: gleann.UpdateEmptied, Removed: 5}, "emptied: -5 passages"},
		{gleann.UpdateStats{}, "unchanged"},
	}
	for _, tc := range cases {
		if got := describeUpdate(tc.st); !strings.Contains(got, tc.want) {
			t.Errorf("describeUpdate(%v) = %q, want %q", tc.st.Mode, got, tc.want)
		}
	}
	if _, _, ok := cacheCounters(shortComputer{}); ok {
		t.Error("a plain computer has no cache counters")
	}
	cached := embedding.NewCachedComputer(shortComputer{model: "x"}, embedding.CacheOptions{MemoryOnly: true})
	if _, _, ok := cacheCounters(cached); !ok {
		t.Error("the cached computer reports its counters")
	}
}

// Build with a mode and a matching specialised model uses that model.
func TestBuildModeSelectsTheSpecialisedModel(t *testing.T) {
	for _, tc := range []struct {
		mode Mode
		bo   func(*BuildOptions)
	}{
		{ModeCode, func(b *BuildOptions) { b.CodeModel = "code-embed" }},
		{ModeDocs, func(b *BuildOptions) { b.DocModel = "doc-embed" }},
	} {
		env := newEnv(t)
		opts := env.options(nil)
		opts.Mode = tc.mode
		bo := BuildOptions{Options: opts, Name: "sp", DocsDir: fixture(t)}
		tc.bo(&bo)
		if _, err := Build(context.Background(), bo); err != nil {
			t.Fatalf("%s: %v", tc.mode, err)
		}
		meta, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "sp")
		want := map[Mode]string{ModeCode: "code-embed", ModeDocs: "doc-embed"}[tc.mode]
		if meta.EmbeddingModel != want {
			t.Errorf("%s build used %q, want %q", tc.mode, meta.EmbeddingModel, want)
		}
	}
	env := newEnv(t)
	//lint:ignore SA1012 Build documents that a nil context means Background.
	if _, err := Build(nil, BuildOptions{Options: env.options(nil), Name: "x"}); err == nil || !strings.Contains(err.Error(), "source directory required") {
		t.Fatalf("Build without a source dir: %v", err)
	}
}

// relToRoot falls back to the plain relative path when neither the path
// nor its directory resolves.
func TestRelToRootUnresolvable(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "nowhere", "deeper", "f.go")
	got := relToRoot(root, outside)
	if !strings.HasPrefix(got, "../") {
		t.Errorf("relToRoot = %q, want a ../ path", got)
	}
}

// convergeDirtyFormat2Graph is a no-op for an index that is not format 2 and
// reports an index database it cannot open.
func TestConvergeDirtyGraphEdges(t *testing.T) {
	dir := t.TempDir()
	if err := convergeDirtyFormat2Graph(dir, "none", t.TempDir(), GraphOptions{}, newPrinter(nil, nil)); err != nil {
		t.Fatalf("no index: %v", err)
	}
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)
	if _, err := db.Exec(`UPDATE files SET graph_dirty = 1 WHERE path LIKE '%.go'`); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := convergeDirtyFormat2Graph(env.cfg.IndexDir, "g", docs, GraphOptions{Name: "g", DocsDir: docs, IndexDir: env.cfg.IndexDir}, newPrinter(&out, nil)); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM files WHERE graph_dirty = 1`); n != 0 {
		t.Errorf("%d files still dirty after converging", n)
	}
}
