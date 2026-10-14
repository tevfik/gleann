package indexing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"
)

// newFakeOllama implements /api/embed with deterministic 8-dim vectors and
// counts embedded texts.
func newFakeOllama(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var texts []string
		switch v := req.Input.(type) {
		case []any:
			for _, s := range v {
				str, _ := s.(string)
				texts = append(texts, str)
			}
		case string:
			texts = []string{v}
		}
		n.Add(int64(len(texts)))
		embs := make([][]float32, len(texts))
		for i, txt := range texts {
			e := make([]float32, 8)
			for j := range e {
				e[j] = float32((len(txt)+j*7)%13) / 13
			}
			e[i%8]++
			embs[i] = e
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": embs})
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture creates a small mixed source tree.
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\n// Add adds two numbers.\nfunc Add(a, b int) int { return a + b }\n\nfunc main() { println(Add(1, 2)) }\n")
	writeFile(t, filepath.Join(dir, "README.md"), "# Project\n\nIntro text about the project.\n\n## Usage\n\nRun the binary.\n")
	writeFile(t, filepath.Join(dir, "notes.txt"), "Plain text notes about the system architecture and design decisions.\n")
	writeFile(t, filepath.Join(dir, "web", "bundle.min.js"), "var a=1;")
	writeFile(t, filepath.Join(dir, "blob.dat"), "\x00\x01\x02binary")
	return dir
}

type testEnv struct {
	cfg     gleann.Config
	tracker *vault.Tracker
	embeds  *atomic.Int64
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	srv, n := newFakeOllama(t)
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	cfg.EmbeddingProvider = "ollama"
	// Unique model name: the embedding cache is keyed by model + text and
	// persists on disk across test runs.
	cfg.EmbeddingModel = fmt.Sprintf("fake-embed-%d", time.Now().UnixNano())
	cfg.OllamaHost = srv.URL
	tr, err := vault.NewTracker(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	return &testEnv{cfg: cfg, tracker: tr, embeds: n}
}

func (e *testEnv) options(out *bytes.Buffer) Options {
	o := Options{Config: e.cfg, Tracker: e.tracker, NoPlugins: true, MultimodalModel: "none"}
	if out != nil {
		w := &lockedBuffer{b: out}
		o.Stdout, o.Stderr = w, w
	}
	return o
}

type lockedBuffer struct {
	mu sync.Mutex
	b  *bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func TestBuildThenSync(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	var out bytes.Buffer
	var progressCalls atomic.Int64
	sawRead := atomic.Bool{}

	exposed := false
	opts := BuildOptions{
		Options:     env.options(&out),
		Name:        "proj",
		DocsDir:     docs,
		Tags:        []string{"work", " @team "},
		Description: "test project",
		MCPExposed:  &exposed,
	}
	opts.Progress = func(p Progress) {
		progressCalls.Add(1)
		if p.Phase == "read_documents" && p.Force {
			sawRead.Store(true)
		}
	}
	res, err := Build(context.Background(), opts)
	if err != nil {
		t.Fatalf("Build: %v\n%s", err, out.String())
	}
	if res.Chunks == 0 {
		t.Fatal("expected chunks")
	}
	if env.embeds.Load() == 0 {
		t.Fatal("expected the embedder to be called")
	}
	if !sawRead.Load() || progressCalls.Load() == 0 {
		t.Fatal("expected progress callbacks")
	}
	if !strings.Contains(out.String(), `Vector Index "proj" built`) {
		t.Errorf("missing build summary in output:\n%s", out.String())
	}

	meta, err := gleann.GetIndexMeta(env.cfg.IndexDir, "proj")
	if err != nil {
		t.Fatalf("GetIndexMeta: %v", err)
	}
	if meta.SourceDir != docs || meta.Description != "test project" {
		t.Errorf("meta not recorded: %+v", meta)
	}
	if !meta.HasTag("work") || !meta.HasTag("team") {
		t.Errorf("tags not recorded: %v", meta.Tags)
	}
	if meta.MCPExposed == nil || *meta.MCPExposed {
		t.Errorf("MCPExposed not recorded: %v", meta.MCPExposed)
	}

	mf, err := gleann.LoadManifest(manifestPath(env.cfg.IndexDir, "proj"))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	for _, e := range mf.Entries {
		if strings.Contains(e.Path, "bundle.min.js") || strings.Contains(e.Path, "blob.dat") {
			t.Errorf("filtered file %q should not be indexed", e.Path)
		}
	}

	// Untouched tree: Merkle fast path.
	sres, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "proj"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !sres.UpToDate {
		t.Fatalf("expected up to date, got %+v", sres)
	}

	// Modify, add and delete files.
	writeFile(t, filepath.Join(docs, "main.go"), "package main\n\nfunc Sub(a, b int) int { return a - b }\n")
	writeFile(t, filepath.Join(docs, "extra.md"), "# Extra\n\nMore docs.\n")
	if err := os.Remove(filepath.Join(docs, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	sres, err = Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "proj"})
	if err != nil {
		t.Fatalf("Sync after edits: %v", err)
	}
	if sres.UpToDate || len(sres.Changed) != 2 || len(sres.Deleted) != 1 {
		t.Fatalf("unexpected sync result: changed=%v deleted=%v", sres.Changed, sres.Deleted)
	}

	sres, err = Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "proj"})
	if err != nil || !sres.UpToDate {
		t.Fatalf("expected up to date after sync, got %+v, %v", sres, err)
	}
}

func TestBuildInstantAndSyncFiles(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "lex", DocsDir: docs, InstantOnly: true}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if env.embeds.Load() != 0 {
		t.Fatalf("instant build must not embed, got %d texts", env.embeds.Load())
	}
	writeFile(t, filepath.Join(docs, "notes.txt"), "Updated notes.\n")
	res, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "lex", Files: []string{"notes.txt", "gone.txt"}})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Changed) != 1 || len(res.Deleted) != 1 {
		t.Fatalf("unexpected result %+v", res)
	}
	if env.embeds.Load() != 0 {
		t.Fatalf("lexical-only sync must not embed")
	}
}

func TestBuildErrors(t *testing.T) {
	env := newEnv(t)

	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "empty", DocsDir: t.TempDir()}); !errors.Is(err, ErrNoDocuments) {
		t.Errorf("expected ErrNoDocuments, got %v", err)
	}
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), DocsDir: t.TempDir()}); err == nil {
		t.Error("expected error for missing name")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, BuildOptions{Options: env.options(nil), Name: "c", DocsDir: fixture(t)}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "nope"}); err == nil {
		t.Error("expected error for missing index")
	}
}

func TestSyncNoSourceDir(t *testing.T) {
	env := newEnv(t)
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "x", DocsDir: fixture(t), InstantOnly: true}); err != nil {
		t.Fatal(err)
	}
	if err := gleann.UpdateIndexMeta(env.cfg.IndexDir, "x", func(m *gleann.IndexMeta) { m.SourceDir = "" }); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "x"}); !errors.Is(err, ErrNoSourceDir) {
		t.Fatalf("expected ErrNoSourceDir, got %v", err)
	}
}

func TestBuildDualModel(t *testing.T) {
	env := newEnv(t)
	res, err := Build(context.Background(), BuildOptions{
		Options:   env.options(nil),
		Name:      "repo",
		DocsDir:   fixture(t),
		CodeModel: "code-embed",
		DocModel:  "doc-embed",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(res.Parts) != 2 || res.Parts[0].Name != "repo-code" || res.Parts[1].Name != "repo-docs" {
		t.Fatalf("unexpected parts: %+v", res.Parts)
	}
	for _, name := range []string{"repo-code", "repo-docs"} {
		if _, err := gleann.GetIndexMeta(env.cfg.IndexDir, name); err != nil {
			t.Errorf("index %s missing: %v", name, err)
		}
	}
}

func TestConcurrentBuildsSameIndex(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "same", DocsDir: docs})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("build %d: %v", i, err)
		}
	}
}

func TestRebuildAndIncremental(t *testing.T) {
	env := newEnv(t)
	docs := fixture(t)
	opts := env.options(nil)
	if _, err := RebuildIndex(context.Background(), "w", docs, opts); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}
	changed := filepath.Join(docs, "main.go")
	writeFile(t, changed, "package main\n\nfunc Mul(a, b int) int { return a * b }\n")
	if _, err := UpdateIncremental(context.Background(), "w", docs, []string{changed}, opts); err != nil {
		t.Fatalf("UpdateIncremental: %v", err)
	}
}

func TestCollectEligibleFilesModes(t *testing.T) {
	dir := fixture(t)
	writeFile(t, filepath.Join(dir, "report.docx"), "not really a docx")

	names := func(mode Mode) map[string]bool {
		entries, err := CollectEligibleFiles(context.Background(), dir, CollectOptions{Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, e := range entries {
			m[filepath.Base(e.Path)] = true
		}
		return m
	}

	code := names(ModeCode)
	if !code["main.go"] || !code["README.md"] || code["bundle.min.js"] || code["blob.dat"] || code["report.docx"] {
		t.Errorf("code mode: %v", code)
	}
	docsMode := names(ModeDocs)
	if docsMode["main.go"] || !docsMode["README.md"] {
		t.Errorf("docs mode: %v", docsMode)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CollectEligibleFiles(ctx, dir, CollectOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestParseModeAndExtensions(t *testing.T) {
	if ParseMode("") != ModeAll || ParseMode(" CODE ") != ModeCode || ParseMode("docs") != ModeDocs {
		t.Error("ParseMode")
	}
	if !IsCodeExtension(".GO") || IsCodeExtension(".md") || !IsDocumentationExtension(".md") || !IsOfficeDocExtension(".pdf") {
		t.Error("extension helpers")
	}
}

func TestPluginResultRoundTrip(t *testing.T) {
	res := MarkdownSectionsToPluginResult(nil, "a.md", 3, "# A\n\ntext")
	if len(res.Nodes) != 1 || res.Nodes[0].Type != "Document" {
		t.Fatalf("unexpected nodes: %+v", res.Nodes)
	}
	doc := PluginResultToDoc(res)
	if doc.Document.Title != "Untitled" || doc.Document.WordCount != 3 {
		t.Fatalf("unexpected doc: %+v", doc.Document)
	}
}

func TestBuildGraphIndexWithoutTreesitter(t *testing.T) {
	if GraphSupported {
		t.Skip("graph supported in this build")
	}
	var errBuf bytes.Buffer
	BuildGraphIndex(GraphOptions{Name: "g", DocsDir: t.TempDir(), IndexDir: t.TempDir(), Stderr: &errBuf})
	if !strings.Contains(errBuf.String(), "requires CGo") {
		t.Errorf("expected warning, got %q", errBuf.String())
	}
	if err := GenerateGraphReport("g", t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "r.md")); err == nil {
		t.Error("expected error without treesitter")
	}
}
