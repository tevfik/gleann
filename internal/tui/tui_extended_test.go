package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"tilde", "~", home},
		{"tilde slash", "~/docs", filepath.Join(home, "docs")},
		{"absolute", "/usr/local/bin", filepath.Clean(filepath.FromSlash("/usr/local/bin"))},
		{"relative", "relative/path", filepath.Clean(filepath.FromSlash("relative/path"))},
		{"dot", ".", "."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandPath(tt.input)
			if got != tt.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDefaultIndexDir(t *testing.T) {
	dir := DefaultIndexDir()
	if dir == "" {
		t.Error("DefaultIndexDir should not be empty")
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".gleann", "indexes")
	if dir != want {
		t.Errorf("DefaultIndexDir = %q, want %q", dir, want)
	}
}

func TestDefaultModelsDir(t *testing.T) {
	dir := DefaultModelsDir()
	if dir == "" {
		t.Error("DefaultModelsDir should not be empty")
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".gleann", "models")
	if dir != want {
		t.Errorf("DefaultModelsDir = %q, want %q", dir, want)
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	// Use temp dir so we don't modify real config
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// Create .gleann dir
	os.MkdirAll(filepath.Join(tmpDir, ".gleann"), 0o755)

	cfg := OnboardResult{
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        "http://localhost:11434",
		Completed:         true,
	}

	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded := LoadSavedConfig()
	if loaded == nil {
		t.Fatal("LoadSavedConfig returned nil")
	}
	if loaded.EmbeddingProvider != "ollama" {
		t.Errorf("EmbeddingProvider = %q", loaded.EmbeddingProvider)
	}
	if loaded.EmbeddingModel != "bge-m3" {
		t.Errorf("EmbeddingModel = %q", loaded.EmbeddingModel)
	}
}

func TestUpdateConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	os.MkdirAll(filepath.Join(tmpDir, ".gleann"), 0o755)

	// First save
	SaveConfig(OnboardResult{EmbeddingModel: "old-model"})

	// Update
	err := UpdateConfig(func(cfg *OnboardResult) {
		cfg.EmbeddingModel = "new-model"
	})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	loaded := LoadSavedConfig()
	if loaded.EmbeddingModel != "new-model" {
		t.Errorf("EmbeddingModel = %q, want new-model", loaded.EmbeddingModel)
	}
}

func TestFilterEmbeddingModels(t *testing.T) {
	models := []ModelInfo{
		{Name: "bge-m3"},
		{Name: "llama3.2"},
		{Name: "nomic-embed-text"},
		{Name: "phi-4"},
		{Name: "text-embedding-ada-002"},
	}

	filtered := filterEmbeddingModels(models)
	if len(filtered) != 3 { // bge-m3, nomic-embed-text, text-embedding-ada-002
		t.Errorf("expected 3 embedding models, got %d: %v", len(filtered), tuiTestModelNames(filtered))
	}
}

func TestFilterEmbeddingModelsEmpty(t *testing.T) {
	// When no embedding models found, return all
	models := []ModelInfo{
		{Name: "llama3.2"},
		{Name: "phi-4"},
	}
	filtered := filterEmbeddingModels(models)
	if len(filtered) != 2 {
		t.Errorf("should return all when no embeds found, got %d", len(filtered))
	}
}

func TestFilterLLMModels(t *testing.T) {
	models := []ModelInfo{
		{Name: "bge-m3"},
		{Name: "llama3.2"},
		{Name: "nomic-embed-text"},
		{Name: "phi-4"},
	}

	filtered := filterLLMModels(models)
	if len(filtered) != 2 { // llama3.2, phi-4
		t.Errorf("expected 2 LLM models, got %d: %v", len(filtered), tuiTestModelNames(filtered))
	}
}

func TestFilterRerankerModels(t *testing.T) {
	models := []ModelInfo{
		{Name: "llama3"},
		{Name: "bge-reranker-v2-m3"},
		{Name: "jina-reranker-v2"},
	}

	rerankers := filterRerankerModels(models)
	if len(rerankers) != 2 {
		t.Errorf("expected 2 rerankers, got %d", len(rerankers))
	}
}

func TestFilterRerankerModelsNone(t *testing.T) {
	models := []ModelInfo{
		{Name: "llama3"},
		{Name: "bge-m3"},
	}

	rerankers := filterRerankerModels(models)
	if len(rerankers) != 0 {
		t.Errorf("expected 0 rerankers, got %d", len(rerankers))
	}
}

func TestFormatModelSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, ""},
		{500, ""},
		{1048576, "1MB"},
		{5242880, "5MB"},
		{1073741824, "1.0GB"},
		{4294967296, "4.0GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatModelSize(tt.bytes)
			if got != tt.want {
				t.Errorf("formatModelSize(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestPickPreferredExtended(t *testing.T) {
	models := []ModelInfo{
		{Name: "llama3.2"},
		{Name: "bge-m3"},
		{Name: "nomic-embed-text"},
	}

	tests := []struct {
		name      string
		preferred []string
		want      string
	}{
		{"first match", []string{"bge-m3"}, "bge-m3"},
		{"second preference", []string{"nonexistent", "nomic"}, "nomic-embed-text"},
		{"no match falls back to first", []string{"nonexistent1", "nonexistent2"}, "llama3.2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickPreferred(models, tt.preferred)
			if got != tt.want {
				t.Errorf("pickPreferred() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPickBestModels(t *testing.T) {
	result := &OnboardResult{}
	models := []ModelInfo{
		{Name: "llama3.2"},
		{Name: "bge-m3"},
		{Name: "phi-4"},
		{Name: "bge-reranker-v2-m3"},
	}

	pickBestModels(result, models)

	if result.EmbeddingModel != "bge-m3" {
		t.Errorf("EmbeddingModel = %q, want bge-m3", result.EmbeddingModel)
	}
	if result.RerankEnabled != true {
		t.Error("RerankEnabled should be true when reranker found")
	}
	if result.RerankModel != "bge-reranker-v2-m3" {
		t.Errorf("RerankModel = %q", result.RerankModel)
	}
}

func TestFetchOllamaModels(t *testing.T) {
	// Mock Ollama server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		resp := ollamaTagsResponse{
			Models: []ollamaModel{
				{Name: "bge-m3", Size: 1073741824, Details: ollamaModelDetail{ParameterSize: "567M", QuantizationLevel: "F16"}},
				{Name: "llama3.2", Size: 4294967296, Details: ollamaModelDetail{ParameterSize: "3B", QuantizationLevel: "Q4_K_M"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	models, err := fetchOllamaModels(server.URL)
	if err != nil {
		t.Fatalf("fetchOllamaModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	// Should be sorted by name
	if models[0].Name != "bge-m3" {
		t.Errorf("first model = %q, want bge-m3", models[0].Name)
	}
	if models[0].Size == "" {
		t.Error("expected size to be formatted")
	}
}

func TestFetchOllamaModelsError(t *testing.T) {
	// Server that returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := fetchOllamaModels(server.URL)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestFetchOpenAIModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		resp := openAIModelsResponse{
			Data: []openAIModel{
				{ID: "text-embedding-3-small", OwnedBy: "openai"},
				{ID: "gpt-4o", OwnedBy: "openai"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	models, err := fetchOpenAIModels(server.URL, "test-key")
	if err != nil {
		t.Fatalf("fetchOpenAIModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
}

func TestFetchModels(t *testing.T) {
	_, err := fetchModels("unsupported", "", "")
	if err == nil {
		t.Error("expected error for unsupported provider")
	}
}

func TestSharedLibNames(t *testing.T) {
	names := sharedLibNames()
	if len(names) == 0 {
		t.Error("sharedLibNames should return non-empty list")
	}
}

func TestInstallDirs(t *testing.T) {
	dirs := installDirs()
	if len(dirs) == 0 {
		t.Error("installDirs should return non-empty list")
	}
}

func TestBashCompletion(t *testing.T) {
	comp := BashCompletion()
	if comp == "" {
		t.Error("BashCompletion should not be empty")
	}
	if len(comp) < 100 {
		t.Error("BashCompletion seems too short")
	}
}

func TestZshCompletion(t *testing.T) {
	comp := ZshCompletion()
	if comp == "" {
		t.Error("ZshCompletion should not be empty")
	}
}

func TestFishCompletion(t *testing.T) {
	comp := FishCompletion()
	if comp == "" {
		t.Error("FishCompletion should not be empty")
	}
}

func TestIsWritable(t *testing.T) {
	// Temp dir should be writable
	tmp := t.TempDir()
	if !isWritable(tmp) {
		t.Error("temp dir should be writable")
	}

	// Non-existent dir should not be writable
	if isWritable("/nonexistent/path/to/dir") {
		t.Error("nonexistent dir should not be writable")
	}
}

func tuiTestModelNames(models []ModelInfo) []string {
	names := make([]string, len(models))
	for i, m := range models {
		names[i] = m.Name
	}
	return names
}
