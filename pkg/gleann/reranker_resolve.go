package gleann

import (
	"os"
	"path/filepath"
	"strings"
)

// Reranker defaults. The in-process (eif) default is the model the RAG
// benchmark (gleann-rag-test) recommends for CPUs: multilingual, about 0.4 s
// per query for 20 candidates. Server providers keep their usual model name.
const (
	DefaultEIFRerankModel    = "mmarco-mMiniLMv2-L12-H384-v1-Q8_0.gguf"
	DefaultServerRerankModel = "bge-reranker-v2-m3"
)

// RerankerAuto lets gleann pick the reranker provider (see AutoRerankerProvider).
const RerankerAuto RerankerProvider = "auto"

// EIFRerankAvailable reports whether this build reranks in process with
// bitllm.c (built with the eif tag).
func EIFRerankAvailable() bool { return eifRank != nil }

// AutoRerankerProvider picks the reranker provider when none is configured:
// the in-process bitllm.c reranker when this build has it and the model is a
// local GGUF (or no model is set), otherwise the embedding provider when that
// can rerank, otherwise Ollama.
func AutoRerankerProvider(embeddingProvider, model string) RerankerProvider {
	if EIFRerankAvailable() && (model == "" || isLocalGGUF(model)) {
		return RerankerEIF
	}
	switch p := RerankerProvider(embeddingProvider); p {
	case RerankerOllama, RerankerLlamacpp, RerankerJina, RerankerCohere, RerankerVoyage:
		return p
	case RerankerEIF:
		if EIFRerankAvailable() {
			return RerankerEIF
		}
	}
	return RerankerOllama
}

// isLocalGGUF reports whether model names a GGUF file: a .gguf path or name,
// or a name with a matching file in ~/.gleann/models.
func isLocalGGUF(model string) bool {
	if strings.HasSuffix(strings.ToLower(model), ".gguf") {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil || model == "" || strings.ContainsAny(model, `/\`) {
		return false
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".gleann", "models", model+"*.gguf"))
	return len(matches) > 0
}

// ResolveRerankerConfig returns the reranker to use for cfg. model overrides
// the configured model (a request or a flag); an empty or "auto" provider is
// chosen with AutoRerankerProvider; the model defaults per provider. Every
// caller (CLI, REST, MCP, TUI chat) uses this, so one setting means the same
// reranker everywhere.
func ResolveRerankerConfig(cfg Config, model string) RerankerConfig {
	rc := cfg.SearchConfig.RerankerConfig
	if model != "" {
		rc.Model = model
	}
	if rc.Provider == "" || rc.Provider == RerankerAuto {
		rc.Provider = AutoRerankerProvider(cfg.EmbeddingProvider, rc.Model)
	}
	if rc.Model == "" {
		rc.Model = DefaultServerRerankModel
		if rc.Provider == RerankerEIF {
			rc.Model = DefaultEIFRerankModel
		}
	}
	switch rc.Provider {
	case RerankerOllama:
		if rc.BaseURL == "" {
			rc.BaseURL = cfg.OllamaHost
		}
		if rc.APIKey == "" {
			rc.APIKey = cfg.APIKeyForProvider(string(LLMOllama))
		}
	case RerankerLlamacpp:
		if rc.BaseURL == "" && cfg.EmbeddingProvider == string(RerankerLlamacpp) {
			rc.BaseURL = cfg.OpenAIBaseURL
		}
	}
	return rc
}
