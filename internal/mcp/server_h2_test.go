package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

// h2WriteMeta writes a bare index meta file (no data) under dir.
func h2WriteMeta(t *testing.T, dir string, meta gleann.IndexMeta) {
	t.Helper()
	idxDir := filepath.Join(dir, meta.Name)
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idxDir, meta.Name+".meta.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// h2FileAsIndexDir returns a server whose index directory is a regular file,
// so listing the indexes fails on every platform.
func h2FileAsIndexDir(t *testing.T) *Server {
	t.Helper()
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Config{IndexDir: f, EmbeddingProvider: "custom"})
	t.Cleanup(srv.Close)
	return srv
}

func TestH2ListingErrorsAreReported(t *testing.T) {
	t.Setenv("GLEANN_INDEX", "")
	srv := h2FileAsIndexDir(t)

	res := h2Call(t, srv.handleList, map[string]any{})
	if !res.IsError || !strings.Contains(extractText(res), "Error listing indexes") {
		t.Errorf("handleList: %q", extractText(res))
	}
	res = h2Call(t, srv.handleSearch, map[string]any{"index": "@all", "query": "q"})
	if !res.IsError || !strings.Contains(extractText(res), "Error listing indexes") {
		t.Errorf("federated search: %q", extractText(res))
	}
	req := mcpsdk.ReadResourceRequest{}
	req.Params.URI = "gleann://indexes"
	if _, err := srv.handleIndexListResource(context.Background(), req); err == nil || !strings.Contains(err.Error(), "error listing indexes") {
		t.Errorf("index list resource: err = %v", err)
	}
	if got := srv.resolveIndexName(); got != "" {
		t.Errorf("resolveIndexName on an unreadable dir = %q", got)
	}
}

func TestH2ListShowsDescriptionsAndHidesPrivate(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	dir := t.TempDir()
	no := false
	h2WriteMeta(t, dir, gleann.IndexMeta{Name: "docs", Backend: "hnsw", EmbeddingModel: "m", NumPassages: 3, Description: "Team handbook"})
	h2WriteMeta(t, dir, gleann.IndexMeta{Name: "secret", Backend: "hnsw", MCPExposed: &no})
	srv := NewServer(Config{IndexDir: dir, EmbeddingProvider: "custom"})
	defer srv.Close()

	out := extractText(h2Call(t, srv.handleList, map[string]any{}))
	if !strings.Contains(out, "- docs: 3 passages, backend=hnsw, model=m - Team handbook") || strings.Contains(out, "secret") {
		t.Errorf("handleList:\n%s", out)
	}
	req := mcpsdk.ReadResourceRequest{}
	req.Params.URI = "gleann://indexes"
	rc, err := srv.handleIndexListResource(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text := rc[0].(mcpsdk.TextResourceContents).Text
	if !strings.Contains(text, "- docs: 3 passages, backend=hnsw — Team handbook") || strings.Contains(text, "secret") {
		t.Errorf("index list resource:\n%s", text)
	}

	// Only the private index left: nothing accessible.
	if err := os.RemoveAll(filepath.Join(dir, "docs")); err != nil {
		t.Fatal(err)
	}
	if out := extractText(h2Call(t, srv.handleList, map[string]any{})); out != "No accessible indexes found for MCP." {
		t.Errorf("handleList with only a private index: %q", out)
	}
}

func TestH2ResolveIndexName(t *testing.T) {
	dir := t.TempDir()
	srv := NewServer(Config{IndexDir: dir, EmbeddingProvider: "custom"})
	defer srv.Close()

	t.Setenv("GLEANN_INDEX", "from-env")
	if got := srv.resolveIndexName(); got != "from-env" {
		t.Errorf("GLEANN_INDEX: got %q", got)
	}
	t.Setenv("GLEANN_INDEX", "")
	if got := srv.resolveIndexName(); got != "" {
		t.Errorf("no indexes: got %q", got)
	}
	h2WriteMeta(t, dir, gleann.IndexMeta{Name: "alpha", Backend: "hnsw"})
	if got := srv.resolveIndexName(); got != "alpha" {
		t.Errorf("single exposed index: got %q", got)
	}
	h2WriteMeta(t, dir, gleann.IndexMeta{Name: "beta", Backend: "hnsw"})
	if got := srv.resolveIndexName(); got != "" {
		t.Errorf("two indexes, no cwd match: got %q", got)
	}
	// An index named like the working directory wins.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(cwd)
	h2WriteMeta(t, dir, gleann.IndexMeta{Name: strings.ToUpper(base), Backend: "hnsw"})
	if got := srv.resolveIndexName(); !strings.EqualFold(got, base) {
		t.Errorf("cwd match: got %q, want %q", got, base)
	}
}

func TestH2SanitizeIndexNameTruncatesLongNames(t *testing.T) {
	long := strings.Repeat("a b", gleann.MaxNameLength)
	got := sanitizeIndexName(long)
	if len(got) != gleann.MaxNameLength || gleann.ValidateName(got) != nil {
		t.Fatalf("sanitizeIndexName(long) = %q (len %d)", got, len(got))
	}
	if got := sanitizeIndexName("..."); got != "" {
		t.Errorf("nothing usable left: got %q", got)
	}
}

func TestH2SearchOptions_RerankGraphContextFocus(t *testing.T) {
	srv, _ := h2LLMServer(t, nil)
	t.Setenv("GLEANN_SEARCHER_CACHE_BUDGET", "")
	// rerank=true without a configured reranker still answers.
	res := h2Call(t, srv.handleSearch, map[string]any{"index": "idx", "query": "payment retries", "rerank": true, "graph_context": true, "focus": true})
	if res.IsError {
		t.Fatalf("search: %s", extractText(res))
	}
	if !strings.Contains(extractText(res), "pay/retry.go") {
		t.Errorf("expected the payment passage:\n%s", extractText(res))
	}
	// A filter matching nothing.
	res = h2Call(t, srv.handleSearch, map[string]any{"index": "idx", "query": "payment", "filters": []any{map[string]any{"field": "ext", "operator": "eq", "value": ".rs"}}})
	if res.IsError || extractText(res) != "No relevant memory fragments found." {
		t.Errorf("no-match search: %q", extractText(res))
	}
}

func TestH2FederatedSearchSkipsBrokenIndexes(t *testing.T) {
	srv, _ := h2LLMServer(t, nil)
	t.Setenv("GLEANN_TAGS", "")
	// A meta-only index that cannot load sits beside the real one.
	h2WriteMeta(t, srv.config.IndexDir, gleann.IndexMeta{Name: "broken", Backend: "hnsw", EmbeddingModel: "test-model"})
	res := h2Call(t, srv.handleSearch, map[string]any{"index": "@all", "query": "JWT login"})
	if res.IsError {
		t.Fatalf("federated search: %s", extractText(res))
	}
	if !strings.Contains(extractText(res), "auth/login.go") {
		t.Errorf("federated search lost the healthy index:\n%s", extractText(res))
	}
}

func TestH2EvictIndexAndCacheLimit(t *testing.T) {
	srv, _ := h2LLMServer(t, nil)
	_, release, err := srv.acquireSearcher("idx")
	if err != nil {
		t.Fatal(err)
	}
	release()
	srv.evictIndex("idx")
	if len(srv.searchers) != 0 || len(srv.searcherLRU) != 0 {
		t.Fatalf("evictIndex left %d searchers, LRU %v", len(srv.searchers), srv.searcherLRU)
	}
	srv.evictIndex("idx") // nothing cached: a no-op

	// More cached searchers than maxCachedSearchers: the oldest go.
	srv.searcherMu.Lock()
	for i := 0; i < maxCachedSearchers+2; i++ {
		name := "s" + string(rune('a'+i))
		sr := gleann.NewSearcher(srv.config, srv.embedder)
		srv.searchers[name] = sr
		srv.searcherLRU = append(srv.searcherLRU, name)
	}
	toClose := srv.enforceCacheBudgetLocked()
	n := len(srv.searchers)
	first := srv.searcherLRU[0]
	srv.searcherMu.Unlock()
	for _, sr := range toClose {
		sr.Close()
	}
	if n != maxCachedSearchers || len(toClose) != 2 {
		t.Errorf("after enforcing the cap: %d cached, %d closed", n, len(toClose))
	}
	if first != "sc" {
		t.Errorf("oldest remaining = %q, want sc", first)
	}
}

func TestH2SyncBranches(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", dir)
	t.Setenv("GLEANN_MEMORY_DIR", t.TempDir())
	srv := NewServer(Config{IndexDir: dir, EmbeddingProvider: "custom"})
	defer srv.Close()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}

	var got SyncOptions
	srv.syncRunner = func(ctx context.Context, opts SyncOptions) (string, error) {
		got = opts
		return "ok", nil
	}

	t.Run("GLEANN_INDEX names the index", func(t *testing.T) {
		t.Setenv("GLEANN_INDEX", "envidx")
		res := h2Call(t, srv.handleSync, map[string]any{"docs_dir": src})
		if res.IsError || got.IndexName != "envidx" {
			t.Fatalf("res %q, opts %+v", extractText(res), got)
		}
		if !strings.Contains(extractText(res), "Index built successfully") {
			t.Errorf("new index reported as %q", extractText(res))
		}
	})
	t.Run("invalid mode", func(t *testing.T) {
		res := h2Call(t, srv.handleSync, map[string]any{"index": "m", "docs_dir": src, "mode": "binary"})
		if !res.IsError || !strings.Contains(extractText(res), `invalid mode "binary"`) {
			t.Fatalf("got %q", extractText(res))
		}
	})
	t.Run("empty mode means code, no_plugins and []string files", func(t *testing.T) {
		req := mcpsdk.CallToolRequest{}
		req.Params.Arguments = map[string]any{"index": "m2", "docs_dir": src, "mode": "", "no_plugins": false, "files": []string{" a.go ", "", "b.go"}}
		res, err := srv.handleSync(context.Background(), req)
		if err != nil || res.IsError {
			t.Fatalf("err %v res %q", err, extractText(res))
		}
		if got.Mode != "code" || got.NoPlugins || len(got.Files) != 2 || got.Files[0] != "a.go" {
			t.Errorf("opts = %+v", got)
		}
	})
	t.Run("runner failure", func(t *testing.T) {
		srv.syncRunner = func(ctx context.Context, opts SyncOptions) (string, error) {
			return "partial log", errors.New("disk full")
		}
		res := h2Call(t, srv.handleSync, map[string]any{"index": "m3", "docs_dir": src})
		if !res.IsError || !strings.Contains(extractText(res), `build failed for index "m3": disk full`) || !strings.Contains(extractText(res), "partial log") {
			t.Fatalf("got %q", extractText(res))
		}
	})
}

// A background build that finishes after the first call returned
// in_progress reports its outcome on the next call.
func TestH2SyncFinishedBackgroundTaskOutcome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", dir)
	t.Setenv("GLEANN_MEMORY_DIR", t.TempDir())
	srv := NewServer(Config{IndexDir: dir, EmbeddingProvider: "custom"})
	defer srv.Close()
	srv.syncWaitTimeout = 10 * time.Millisecond

	for _, tc := range []struct {
		name    string
		err     error
		want    string
		isError bool
	}{
		{"fails", errors.New("embedder down"), `build failed for index "bg-fail": embedder down`, true},
		{"succeeds", nil, "Index built successfully", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := "bg-fail"
			if tc.err == nil {
				index = "bg-ok"
			}
			release := make(chan struct{})
			srv.syncRunner = func(ctx context.Context, opts SyncOptions) (string, error) {
				<-release
				return "log line", tc.err
			}
			args := map[string]any{"index": index, "docs_dir": dir}
			if out := extractText(h2Call(t, srv.handleSync, args)); !strings.Contains(out, `"in_progress"`) {
				t.Fatalf("first call: %s", out)
			}
			srv.syncMu.Lock()
			task := srv.syncTasks[index]
			srv.syncMu.Unlock()
			close(release)
			<-task.done

			res := h2Call(t, srv.handleSync, args)
			if res.IsError != tc.isError || !strings.Contains(extractText(res), tc.want) {
				t.Fatalf("second call: error=%v %q", res.IsError, extractText(res))
			}
			srv.syncMu.Lock()
			_, still := srv.syncTasks[index]
			srv.syncMu.Unlock()
			if still {
				t.Error("finished task still registered")
			}
		})
	}
}

func TestH2DocsDirAllowed(t *testing.T) {
	t.Setenv("GLEANN_ALLOWED_ROOTS", "")
	root := t.TempDir()
	other := t.TempDir()
	srv := NewServer(Config{IndexDir: t.TempDir(), EmbeddingProvider: "custom", BuildConfig: &gleann.Config{AllowedRoots: []string{root}}})
	defer srv.Close()
	inRoot := filepath.Join(root, "repo")
	if !srv.docsDirAllowed(inRoot, nil) {
		t.Errorf("%s under the configured allowed root was refused", inRoot)
	}
	if !srv.docsDirAllowed(filepath.Join(other, "x"), &gleann.IndexMeta{SourceDir: other}) {
		t.Error("the index's own source dir was refused")
	}
	if srv.docsDirAllowed(filepath.Join(other, "x"), nil) {
		t.Error("a directory outside every root was allowed")
	}
}
