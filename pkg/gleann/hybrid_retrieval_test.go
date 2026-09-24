package gleann

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type mockHybridBackendSearcher struct{}

func (m *mockHybridBackendSearcher) Load(ctx context.Context, indexData []byte, meta IndexMeta) error {
	return nil
}

func (m *mockHybridBackendSearcher) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	return []int64{0, 1, 2}, []float32{0.5, 0.5, 0.5}, nil
}

func (m *mockHybridBackendSearcher) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error) {
	return m.Search(ctx, query, topK)
}

func (m *mockHybridBackendSearcher) Close() error {
	return nil
}

type mockHybridBackendFactory struct{}

func (m mockHybridBackendFactory) Name() string {
	return "mock-hybrid-backend"
}

func (m mockHybridBackendFactory) NewBuilder(config Config) BackendBuilder {
	return nil
}

func (m mockHybridBackendFactory) NewSearcher(config Config) BackendSearcher {
	return &mockHybridBackendSearcher{}
}

var registerMockBackendOnce sync.Once

func initMockBackend() {
	registerMockBackendOnce.Do(func() {
		RegisterBackend(mockHybridBackendFactory{})
	})
}

// mockEmbeddingComputer provides a deterministic dummy embedding for testing
type mockEmbeddingComputer struct{}

func (m *mockEmbeddingComputer) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, 16)
	}
	return out, nil
}

func (m *mockEmbeddingComputer) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	return make([]float32, 16), nil
}

func (m *mockEmbeddingComputer) Dimensions() int {
	return 16
}

func (m *mockEmbeddingComputer) ModelName() string {
	return "mock-embedder"
}

func TestHybridRetrieval_ExactSymbolMatch(t *testing.T) {
	initMockBackend()
	tmpDir := t.TempDir()
	indexName := "test_hybrid"

	idxDir := filepath.Join(tmpDir, indexName)
	if err := os.MkdirAll(idxDir, 0755); err != nil {
		t.Fatal(err)
	}

	basePath := filepath.Join(idxDir, indexName)
	pm := NewPassageManager(basePath)

	items := []Item{
		{
			Text: "func InitStorage() error { return nil }",
			Metadata: map[string]any{
				"source": "storage.go",
				"name":   "InitStorage",
				"fqn":    "pkg.InitStorage",
				"kind":   "function",
			},
		},
		{
			Text: "func OpenStore(path string) (*Store, error) { return nil, nil }",
			Metadata: map[string]any{
				"source": "store.go",
				"name":   "OpenStore",
				"fqn":    "pkg.OpenStore",
				"kind":   "function",
			},
		},
		{
			Text: "func Query() { log.Println(\"querying store\") }",
			Metadata: map[string]any{
				"source": "query.go",
				"name":   "Query",
				"fqn":    "pkg.Query",
				"kind":   "function",
			},
		},
	}
	ids, err := pm.Add(items)
	if err != nil {
		t.Fatal(err)
	}
	pm.Close()

	// Write mock index file
	os.WriteFile(basePath+".index", []byte("mock"), 0644)

	// Write index metadata
	meta := IndexMeta{
		Name:           indexName,
		Backend:        "mock-hybrid-backend",
		EmbeddingModel: "mock-embedder",
		NumPassages:    len(ids),
	}
	metaBytes, _ := meta.MarshalJSON()
	os.WriteFile(basePath+".meta.json", metaBytes, 0644)

	cfg := DefaultConfig()
	cfg.IndexDir = tmpDir

	searcher := NewSearcher(cfg, &mockEmbeddingComputer{})
	if err := searcher.Load(context.Background(), indexName); err != nil {
		t.Fatal(err)
	}
	defer searcher.Close()

	// Search for exact symbol "OpenStore"
	results, err := searcher.Search(context.Background(), "OpenStore", WithTopK(3))
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results, got 0")
	}

	// OpenStore (id 1) must be the #1 result
	if results[0].ID != ids[1] {
		t.Errorf("expected top result to be OpenStore (ID %d), got ID %d (name: %v)", ids[1], results[0].ID, results[0].Metadata["name"])
	}
}

func TestHybridRetrieval_TestAndVendorDemotion(t *testing.T) {
	initMockBackend()
	tmpDir := t.TempDir()
	indexName := "test_demotion"

	idxDir := filepath.Join(tmpDir, indexName)
	if err := os.MkdirAll(idxDir, 0755); err != nil {
		t.Fatal(err)
	}

	basePath := filepath.Join(idxDir, indexName)
	pm := NewPassageManager(basePath)

	items := []Item{
		{
			Text: "func ProcessOrder(id string) error { return nil }",
			Metadata: map[string]any{
				"source":  "order.go",
				"name":    "ProcessOrder",
				"is_test": false,
			},
		},
		{
			Text: "func TestProcessOrder(t *testing.T) { ProcessOrder(\"123\") }",
			Metadata: map[string]any{
				"source":  "order_test.go",
				"name":    "TestProcessOrder",
				"is_test": true,
			},
		},
	}
	ids, err := pm.Add(items)
	if err != nil {
		t.Fatal(err)
	}
	pm.Close()

	os.WriteFile(basePath+".index", []byte("mock"), 0644)

	meta := IndexMeta{
		Name:           indexName,
		Backend:        "mock-hybrid-backend",
		EmbeddingModel: "mock-embedder",
		NumPassages:    len(ids),
	}
	metaBytes, _ := meta.MarshalJSON()
	os.WriteFile(basePath+".meta.json", metaBytes, 0644)

	cfg := DefaultConfig()
	cfg.IndexDir = tmpDir

	searcher := NewSearcher(cfg, &mockEmbeddingComputer{})
	if err := searcher.Load(context.Background(), indexName); err != nil {
		t.Fatal(err)
	}
	defer searcher.Close()

	// Default search without IncludeTests: production code must rank higher than test code
	res, err := searcher.Search(context.Background(), "ProcessOrder", WithTopK(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) < 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
	if res[0].ID != ids[0] {
		t.Errorf("expected production code (ID %d) to rank first, got ID %d", ids[0], res[0].ID)
	}
	if res[1].ID != ids[1] {
		t.Errorf("expected test code (ID %d) to rank second, got ID %d", ids[1], res[1].ID)
	}

	// With IncludeTests = true, demotion is disabled
	resInc, err := searcher.Search(context.Background(), "ProcessOrder", WithTopK(2), WithIncludeTests(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(resInc) < 2 {
		t.Fatalf("expected 2 results, got %d", len(resInc))
	}
}

func TestHybridRetrieval_KindFilter(t *testing.T) {
	initMockBackend()
	tmpDir := t.TempDir()
	indexName := "test_kind"

	idxDir := filepath.Join(tmpDir, indexName)
	os.MkdirAll(idxDir, 0755)

	basePath := filepath.Join(idxDir, indexName)
	pm := NewPassageManager(basePath)
	items := []Item{
		{
			Text: "func Authenticate(token string) bool { return true }",
			Metadata: map[string]any{
				"source": "auth.go",
				"kind":   "function",
			},
		},
		{
			Text: "# Authentication Guide\nUse token authentication.",
			Metadata: map[string]any{
				"source": "auth.md",
				"kind":   "doc",
			},
		},
	}
	ids, err := pm.Add(items)
	if err != nil {
		t.Fatal(err)
	}
	pm.Close()

	os.WriteFile(basePath+".index", []byte("mock"), 0644)

	meta := IndexMeta{
		Name:           indexName,
		Backend:        "mock-hybrid-backend",
		EmbeddingModel: "mock-embedder",
		NumPassages:    len(ids),
	}
	metaBytes, _ := meta.MarshalJSON()
	os.WriteFile(basePath+".meta.json", metaBytes, 0644)

	cfg := DefaultConfig()
	cfg.IndexDir = tmpDir

	searcher := NewSearcher(cfg, &mockEmbeddingComputer{})
	if err := searcher.Load(context.Background(), indexName); err != nil {
		t.Fatal(err)
	}
	defer searcher.Close()

	// Filter kind=code
	codeRes, err := searcher.Search(context.Background(), "Authenticate", WithKind("code"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range codeRes {
		if r.ID == ids[1] {
			t.Errorf("doc passage should be excluded with WithKind('code')")
		}
	}

	// Filter kind=docs
	docRes, err := searcher.Search(context.Background(), "Authentication", WithKind("docs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range docRes {
		if r.ID == ids[0] {
			t.Errorf("code passage should be excluded with WithKind('docs')")
		}
	}
}

func TestHybridRetrieval_MetadataFilterAliases(t *testing.T) {
	initMockBackend()
	tmpDir := t.TempDir()
	indexName := "test_alias"

	idxDir := filepath.Join(tmpDir, indexName)
	os.MkdirAll(idxDir, 0755)

	basePath := filepath.Join(idxDir, indexName)
	pm := NewPassageManager(basePath)
	items := []Item{
		{
			Text: "package main\nfunc Run() {}",
			Metadata: map[string]any{
				"source": "cmd/main.go",
				"kind":   "function",
			},
		},
		{
			Text: "def run(): pass",
			Metadata: map[string]any{
				"source": "app/main.py",
				"kind":   "function",
			},
		},
	}
	ids, err := pm.Add(items)
	if err != nil {
		t.Fatal(err)
	}
	pm.Close()

	os.WriteFile(basePath+".index", []byte("mock"), 0644)

	meta := IndexMeta{
		Name:           indexName,
		Backend:        "mock-hybrid-backend",
		EmbeddingModel: "mock-embedder",
		NumPassages:    len(ids),
	}
	metaBytes, _ := meta.MarshalJSON()
	os.WriteFile(basePath+".meta.json", metaBytes, 0644)

	cfg := DefaultConfig()
	cfg.IndexDir = tmpDir

	searcher := NewSearcher(cfg, &mockEmbeddingComputer{})
	if err := searcher.Load(context.Background(), indexName); err != nil {
		t.Fatal(err)
	}
	defer searcher.Close()

	// Filter by 'ext' = '.go' even though metadata only has 'source': 'cmd/main.go'
	filters := []MetadataFilter{
		{Field: "ext", Operator: OpEqual, Value: ".go"},
	}
	res, err := searcher.Search(context.Background(), "Run", WithMetadataFilters(filters))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != ids[0] {
		t.Errorf("expected 1 result with ID %d, got %d results: %+v", ids[0], len(res), res)
	}

	// Filter by 'type' = 'function' even though metadata uses 'kind'
	typeFilters := []MetadataFilter{
		{Field: "type", Operator: OpEqual, Value: "function"},
	}
	resType, err := searcher.Search(context.Background(), "Run", WithMetadataFilters(typeFilters))
	if err != nil {
		t.Fatal(err)
	}
	if len(resType) != 2 {
		t.Errorf("expected 2 results matching type=function alias, got %d", len(resType))
	}
}
