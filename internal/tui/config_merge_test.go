package tui

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestMergeConfig_IncomingOverlaysOnlyWhatItSets(t *testing.T) {
	existing := &OnboardResult{
		EmbeddingProvider: "ollama", EmbeddingModel: "old-emb", LLMProvider: "ollama", LLMModel: "old-llm",
		OllamaHost: "http://old:11434", IndexDir: "/old/idx", Completed: true,
		Temperature: 0.2, TopK: 3, SystemPrompt: "keep me",
		RerankEnabled: true, RerankModel: "old-rr",
	}
	incoming := &OnboardResult{
		EmbeddingProvider: "openai", EmbeddingModel: "new-emb",
		CodeEmbeddingModel: "code", DocEmbeddingModel: "doc",
		OpenAIKey: "ok", OpenAIBaseURL: "http://oa", AnthropicKey: "ak", OllamaAPIKey: "olk",
		Backend: "hnsw", StorageMode: "compact", MultimodalModel: "mm",
		MaxTokens: 99, RepeatPenalty: 1.2, Roles: map[string][]string{"r": {"p"}}, FormatText: map[string]string{"f": "t"},
		ServerAddr: ":1", InstallPath: "/inst", InstallCompletions: true,
	}
	got := mergeConfig(existing, incoming)
	if got.EmbeddingProvider != "openai" || got.EmbeddingModel != "new-emb" || got.CodeEmbeddingModel != "code" || got.DocEmbeddingModel != "doc" {
		t.Errorf("models not overlaid: %+v", got)
	}
	if got.OpenAIKey != "ok" || got.OpenAIBaseURL != "http://oa" || got.AnthropicKey != "ak" || got.OllamaAPIKey != "olk" {
		t.Errorf("credentials not overlaid: %+v", got)
	}
	if got.Backend != "hnsw" || got.StorageMode != "compact" || got.MultimodalModel != "mm" || got.MaxTokens != 99 || got.RepeatPenalty != 1.2 {
		t.Errorf("settings not overlaid: %+v", got)
	}
	if got.ServerAddr != ":1" || got.InstallPath != "/inst" || !got.InstallCompletions || len(got.Roles["r"]) != 1 || got.FormatText["f"] != "t" {
		t.Errorf("install/roles not overlaid: %+v", got)
	}
	// Unset incoming fields keep the existing values.
	if got.LLMModel != "old-llm" || got.OllamaHost != "http://old:11434" || got.IndexDir != "/old/idx" || got.Temperature != 0.2 || got.TopK != 3 || got.SystemPrompt != "keep me" {
		t.Errorf("unset fields must survive: %+v", got)
	}
	// An incoming config that is not "Completed" must not switch features off.
	if !got.RerankEnabled || got.RerankModel != "old-rr" || !got.Completed {
		t.Errorf("partial update must not clear toggles: %+v", got)
	}
	if existing.EmbeddingModel != "old-emb" {
		t.Error("existing must not be mutated")
	}
}

func TestMergeConfig_CompletedReplacesToggles(t *testing.T) {
	yes := true
	existing := &OnboardResult{RerankEnabled: true, RerankModel: "rr", ServerEnabled: true, MCPEnabled: true}
	got := mergeConfig(existing, &OnboardResult{Completed: true, A2AEnabled: &yes, LlamaCPPConfig: gleann.LlamaCPPConfig{ModelStorageDir: "/m"},
		Uninstall: true, UninstallData: true})
	if got.RerankEnabled || got.RerankModel != "" || got.ServerEnabled || got.MCPEnabled {
		t.Errorf("a completed wizard result is authoritative for the toggles: %+v", got)
	}
	if got.A2AEnabled == nil || !*got.A2AEnabled || got.LlamaCPPConfig.ModelStorageDir != "/m" || !got.Uninstall || !got.UninstallData {
		t.Errorf("%+v", got)
	}

	// Not completed: toggles only switch on.
	got = mergeConfig(&OnboardResult{}, &OnboardResult{RerankEnabled: true, RerankModel: "x", RerankProvider: "p", ServerEnabled: true, MCPEnabled: true, A2AEnabled: &yes})
	if !got.RerankEnabled || got.RerankModel != "x" || got.RerankProvider != "p" || !got.ServerEnabled || !got.MCPEnabled || got.A2AEnabled == nil {
		t.Errorf("%+v", got)
	}
	if got := mergeConfig(nil, &OnboardResult{LLMModel: "m"}); got.LLMModel != "m" {
		t.Errorf("no existing config: %+v", got)
	}
}

func TestSaveExact_RefusesInvalidExistingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := writeSecretFile(path, []byte("{broken")); err != nil {
		t.Fatal(err)
	}
	err := saveExact(path, OnboardResult{LLMModel: "x"})
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("must refuse to overwrite a broken config, got %v", err)
	}
}

func TestWriteSecretFile_FailsWhenDirectoryMissing(t *testing.T) {
	if err := writeSecretFile(filepath.Join(t.TempDir(), "missing", "c.json"), []byte("x")); err == nil {
		t.Error("writing into a missing directory must fail")
	}
}

func TestFirstLine(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name string
		out  string
		err  error
		want string
	}{
		{"last line of multi-line output", "step 1\nstep 2\nfailed: disk full\n", boom, "failed: disk full"},
		{"single line output", "only line", boom, "only line"},
		{"no output falls back to the error", "", boom, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstLine(tt.out, tt.err); got != tt.want {
				t.Errorf("firstLine = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLocalModelValue(t *testing.T) {
	tests := []struct {
		name string
		mi   ModelInfo
		want string
	}{
		{"server model keeps its name", ModelInfo{Name: "bge-m3", Tag: "latest"}, "bge-m3"},
		{"gguf by bare file name", ModelInfo{Name: "x", Tag: "model.gguf"}, "model.gguf"},
		{"gguf elsewhere keeps its path", ModelInfo{Name: "x", Tag: filepath.Join("opt", "models", "m.gguf")}, filepath.Join("opt", "models", "m.gguf")},
		{"gguf in the models dir is saved by name", ModelInfo{Name: "x", Tag: filepath.Join(DefaultModelsDir(), "m.GGUF")}, "m.GGUF"},
		{"eifm counts as a local file", ModelInfo{Name: "x", Tag: "m.eifm"}, "m.eifm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := localModelValue(tt.mi); got != tt.want {
				t.Errorf("localModelValue = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCatalogEntryFor(t *testing.T) {
	if _, ok := catalogEntryFor("bge-m3"); ok {
		t.Error("a server model name is not a downloadable file")
	}
	all := modelcatalog.All()
	if len(all) == 0 {
		t.Skip("empty catalog")
	}
	e, ok := catalogEntryFor(filepath.Join("some", "dir", strings.ToUpper(all[0].File)))
	if !ok || e.ID != all[0].ID {
		t.Errorf("a catalog file is found by base name, case-insensitively: %+v %v", e, ok)
	}
}

func TestDownloadChosenModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var out bytes.Buffer

	if downloadChosenModels(context.Background(), OnboardResult{EmbeddingProvider: "ollama", EmbeddingModel: "bge-m3", LLMProvider: "ollama"}, &out) || out.Len() != 0 {
		t.Errorf("server models are never downloaded: %q", out.String())
	}
	all := modelcatalog.All()
	if len(all) == 0 {
		t.Skip("empty catalog")
	}
	e := all[0]
	// A cancelled context makes the download fail before any network access;
	// the failure must be reported with the manual alternative, once per model.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := OnboardResult{EmbeddingProvider: "llamacpp", EmbeddingModel: e.File, CodeEmbeddingModel: e.File, LLMProvider: "llamacpp", LLMModel: e.File}
	if !downloadChosenModels(ctx, r, &out) {
		t.Fatal("a missing catalog model must be attempted")
	}
	s := out.String()
	if strings.Count(s, "Downloading") != 1 {
		t.Errorf("the same model must be downloaded once:\n%s", s)
	}
	if !strings.Contains(s, "✗") || !strings.Contains(s, "gleann models download "+e.ID) {
		t.Errorf("failure must explain how to download manually:\n%s", s)
	}
}
