package gleann

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// ── Searcher tests ─────────────────────────────────────────────

func TestNewSearcher(t *testing.T) {
	cfg := DefaultConfig()
	s := NewSearcher(cfg, nil)
	if s == nil {
		t.Fatal("NewSearcher returned nil")
	}
	if s.loaded {
		t.Error("should not be loaded initially")
	}
}

func TestSearcherSetters(t *testing.T) {
	s := NewSearcher(DefaultConfig(), nil)
	s.SetScorer(nil)
	s.SetReranker(nil)
	s.SetEmbeddingServer(nil)
	if s.scorer != nil || s.reranker != nil || s.embServer != nil {
		t.Error("setters should accept nil")
	}
}

func TestSearcherMeta(t *testing.T) {
	s := NewSearcher(DefaultConfig(), nil)
	s.meta = IndexMeta{Name: "test", NumPassages: 42}
	m := s.Meta()
	if m.Name != "test" || m.NumPassages != 42 {
		t.Errorf("Meta() = %+v", m)
	}
}

func TestSearcherGraphDB(t *testing.T) {
	s := NewSearcher(DefaultConfig(), nil)
	if s.GraphDB() != nil {
		t.Error("GraphDB should be nil initially")
	}
}

func TestSearcherPassageManager(t *testing.T) {
	s := NewSearcher(DefaultConfig(), nil)
	if s.PassageManager() != nil {
		t.Error("PassageManager should be nil initially")
	}
}

func TestSearcherClose(t *testing.T) {
	s := NewSearcher(DefaultConfig(), nil)
	if err := s.Close(); err != nil {
		t.Errorf("Close() on empty searcher: %v", err)
	}
}

func TestSearchOptions(t *testing.T) {
	cfg := SearchConfig{}

	WithTopK(10)(&cfg)
	if cfg.TopK != 10 {
		t.Errorf("TopK = %d, want 10", cfg.TopK)
	}

	WithHybridAlpha(0.5)(&cfg)
	if cfg.HybridAlpha != 0.5 {
		t.Errorf("HybridAlpha = %f, want 0.5", cfg.HybridAlpha)
	}

	WithMinScore(0.3)(&cfg)
	if cfg.MinScore != 0.3 {
		t.Errorf("MinScore = %f, want 0.3", cfg.MinScore)
	}

	WithReranker(true)(&cfg)
	if !cfg.UseReranker {
		t.Error("UseReranker should be true")
	}

	WithGraphContext(true)(&cfg)
	if !cfg.UseGraphContext {
		t.Error("UseGraphContext should be true")
	}
}

func TestListIndexes(t *testing.T) {
	tmpDir := t.TempDir()

	// Empty dir.
	indexes, err := ListIndexes(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 0 {
		t.Errorf("expected 0 indexes, got %d", len(indexes))
	}

	// Non-existent dir.
	indexes, err = ListIndexes("/nonexistent")
	if err != nil {
		t.Error("non-existent dir should not error (returns nil)")
	}
	if indexes != nil {
		t.Error("expected nil for non-existent dir")
	}

	// Create fake index.
	idxDir := filepath.Join(tmpDir, "myindex")
	os.MkdirAll(idxDir, 0755)
	meta := IndexMeta{Name: "myindex", NumPassages: 10, Backend: "hnsw", EmbeddingModel: "test"}
	data, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(idxDir, "myindex.meta.json"), data, 0644)

	indexes, err = ListIndexes(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 1 {
		t.Fatalf("expected 1 index, got %d", len(indexes))
	}
	if indexes[0].Name != "myindex" {
		t.Errorf("Name = %q", indexes[0].Name)
	}
}

func TestRemoveIndex(t *testing.T) {
	tmpDir := t.TempDir()

	// Create index structure.
	idxDir := filepath.Join(tmpDir, "myindex")
	os.MkdirAll(idxDir, 0755)
	os.WriteFile(filepath.Join(idxDir, "test.index"), []byte("data"), 0644)

	// Create graph dir.
	graphDir := filepath.Join(tmpDir, "myindex_graph")
	os.MkdirAll(graphDir, 0755)

	// Create sync file.
	os.WriteFile(filepath.Join(tmpDir, "myindex.sync.json"), []byte("{}"), 0644)

	err := RemoveIndex(tmpDir, "myindex")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(idxDir); !os.IsNotExist(err) {
		t.Error("index dir should be removed")
	}
	if _, err := os.Stat(graphDir); !os.IsNotExist(err) {
		t.Error("graph dir should be removed")
	}
}

func TestCosineSimilarity(t *testing.T) {
	// Identical vectors.
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	sim := cosineSimilarity(a, b)
	if sim < 0.99 {
		t.Errorf("identical vectors similarity = %f, want ~1.0", sim)
	}

	// Orthogonal vectors.
	c := []float32{0, 1, 0}
	sim = cosineSimilarity(a, c)
	if sim > 0.01 {
		t.Errorf("orthogonal similarity = %f, want ~0", sim)
	}
}

// ── Reranker tests ─────────────────────────────────────────────

func TestDefaultRerankerConfig(t *testing.T) {
	cfg := DefaultRerankerConfig()
	if cfg.Provider != RerankerOllama {
		t.Errorf("Provider = %q", cfg.Provider)
	}
	if cfg.Model == "" {
		t.Error("Model should not be empty")
	}
	if cfg.BaseURL == "" {
		t.Error("BaseURL should not be empty")
	}
}

func TestNewReranker(t *testing.T) {
	cfg := DefaultRerankerConfig()
	r := NewReranker(cfg)
	if r == nil {
		t.Fatal("NewReranker returned nil")
	}
	if r.config.BaseURL == "" {
		t.Error("BaseURL should be set")
	}
}

func TestNewRerankerProviderDefaults(t *testing.T) {
	for _, provider := range []RerankerProvider{RerankerOllama, RerankerJina, RerankerCohere, RerankerVoyage, RerankerLlamacpp} {
		cfg := RerankerConfig{Provider: provider, Model: "test"}
		r := NewReranker(cfg)
		if r.config.BaseURL == "" {
			t.Errorf("provider %q: BaseURL should be auto-set", provider)
		}
	}
}

func TestRerankerProviderConstants(t *testing.T) {
	if RerankerOllama != "ollama" {
		t.Error("RerankerOllama wrong")
	}
	if RerankerJina != "jina" {
		t.Error("RerankerJina wrong")
	}
	if RerankerCohere != "cohere" {
		t.Error("RerankerCohere wrong")
	}
	if RerankerVoyage != "voyage" {
		t.Error("RerankerVoyage wrong")
	}
	if RerankerLlamacpp != "llamacpp" {
		t.Error("RerankerLlamacpp wrong")
	}
}

// ── Plugin tests ───────────────────────────────────────────────

func TestPluginDefaults(t *testing.T) {
	if DefaultPluginTimeout <= 0 {
		t.Error("DefaultPluginTimeout should be positive")
	}
}

func TestPluginRegistryJSON(t *testing.T) {
	reg := PluginRegistry{
		Plugins: []Plugin{
			{Name: "test", URL: "http://localhost:8080", Extensions: []string{".pdf"}},
		},
	}
	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}

	var decoded PluginRegistry
	json.Unmarshal(data, &decoded)
	if len(decoded.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(decoded.Plugins))
	}
	if decoded.Plugins[0].Name != "test" {
		t.Error("plugin name mismatch")
	}
}

func TestNewPluginManager(t *testing.T) {
	// Will fail gracefully or succeed depending on ~/.gleann/plugins.json existence.
	pm, err := NewPluginManager()
	if err != nil {
		t.Logf("NewPluginManager: %v (expected if no plugins.json)", err)
		return
	}
	defer pm.Close()
	if pm.Registry == nil {
		t.Error("Registry should not be nil")
	}
}

func TestPluginManagerClose(t *testing.T) {
	pm := &PluginManager{
		Registry:   &PluginRegistry{},
		activeCmds: make(map[string]*exec.Cmd),
		logFiles:   make(map[string]*os.File),
	}
	// Should not panic.
	pm.Close()
}

func TestLoadPlugins(t *testing.T) {
	// With temp HOME that has no plugins.json.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)
	reg, err := LoadPlugins()
	if err != nil {
		// May still use os.UserHomeDir which might not respect HOME on some systems.
		t.Logf("LoadPlugins: %v", err)
		return
	}
	if reg == nil {
		t.Error("expected non-nil registry")
	}
	if len(reg.Plugins) != 0 {
		t.Error("expected empty plugins")
	}
}
