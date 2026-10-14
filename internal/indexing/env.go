package indexing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tevfik/gleann/internal/backend/llamacpp"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/multimodal"
	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"
)

// NewEmbedder initializes an embedding.Computer from cfg, routing the base
// URL and API key based on the configured provider.
func NewEmbedder(cfg gleann.Config) *embedding.Computer {
	baseURL := cfg.OllamaHost
	apiKey := ""

	switch cfg.EmbeddingProvider {
	case "", "ollama":
		apiKey = gleann.ResolveOllamaAPIKey(cfg.OllamaAPIKey)
	case "openai":
		if cfg.OpenAIBaseURL != "" {
			baseURL = cfg.OpenAIBaseURL
		}
		apiKey = cfg.OpenAIAPIKey
	case "llamacpp":
		if cfg.OpenAIBaseURL != "" {
			baseURL = cfg.OpenAIBaseURL
		}
		apiKey = cfg.OpenAIAPIKey
	case "eif":
		baseURL = ""
		apiKey = ""
	case "gemini":
		apiKey = cfg.OpenAIAPIKey
	}

	return embedding.NewComputer(embedding.Options{
		Provider:    embedding.Provider(cfg.EmbeddingProvider),
		Model:       cfg.EmbeddingModel,
		BaseURL:     baseURL,
		APIKey:      apiKey,
		BatchSize:   cfg.BatchSize,
		Concurrency: cfg.Concurrency,
	})
}

// startLlamaCPP starts an embedded llama-server when cfg uses the
// "llamacpp" embedding provider and rewrites cfg to talk to it through the
// OpenAI-compatible API. The returned stop function must always be called.
// The server process is bound to ctx.
func startLlamaCPP(ctx context.Context, cfg *gleann.Config, p *printer) (stop func(), err error) {
	if cfg.EmbeddingProvider != "llamacpp" {
		return func() {}, nil
	}
	if cfg.EmbeddingModel == "" || cfg.EmbeddingModel == "bge-m3" {
		return func() {}, fmt.Errorf("llamacpp provider requires an absolute path to a .gguf file as the embedding model")
	}
	p.Printf("🚀 Starting embedded llama.cpp server for model %s\n", cfg.EmbeddingModel)
	runner := llamacpp.NewRunner(cfg.EmbeddingModel)
	runner.SetEmbedding(true)
	if err := runner.Start(ctx); err != nil {
		return func() {}, fmt.Errorf("failed to start embedded llama-server: %w", err)
	}
	cfg.EmbeddingProvider = "openai"
	cfg.OpenAIBaseURL = runner.BaseURL()
	cfg.OllamaHost = runner.BaseURL()
	cfg.OpenAIAPIKey = "gleann-embedded"
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 32
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 2
	}
	p.Printf("✅ Embedded llama-server is ready at %s\n", cfg.OpenAIBaseURL)
	return func() { _ = runner.Stop() }, nil
}

// resolveEmbedder returns the embedder to use for a vector build, wrapped
// in an embedding cache, plus a cleanup func. cfg may be rewritten (llamacpp).
func resolveEmbedder(ctx context.Context, opts *Options, cfg *gleann.Config, p *printer) (*embedding.CachedComputer, func(), error) {
	base := opts.Embedder
	cleanup := func() {}
	if base == nil {
		stop, err := startLlamaCPP(ctx, cfg, p)
		if err != nil {
			stop()
			return nil, func() {}, err
		}
		cleanup = stop
		base = NewEmbedder(*cfg)
	}
	return embedding.NewCachedComputer(base, embedding.CacheOptions{}), cleanup, nil
}

// NewMultimodalProcessor creates a multimodal processor only when a model
// is explicitly requested via model or the GLEANN_MULTIMODAL_MODEL env var.
// Auto-detect is disabled to avoid unintentional HTTP calls for every media
// file in large projects. "none", "off" and "disable" explicitly disable it.
// ollamaAPIKey is an optional Bearer token for authenticated Ollama endpoints.
// A one-line capability summary is written to out (may be nil).
func NewMultimodalProcessor(ollamaHost, ollamaAPIKey, model string, out func(format string, a ...any)) *multimodal.Processor {
	if model == "" {
		model = os.Getenv("GLEANN_MULTIMODAL_MODEL")
	}
	if strings.EqualFold(model, "none") || strings.EqualFold(model, "off") || strings.EqualFold(model, "disable") {
		return nil
	}
	if model == "" {
		return nil
	}
	p := multimodal.NewProcessor(ollamaHost, model)
	if ollamaAPIKey != "" {
		p.APIKey = ollamaAPIKey
	}
	caps := multimodal.DetectCapabilities(ollamaHost, model, p.APIKey)
	var features []string
	if caps.Vision {
		features = append(features, "vision")
	}
	if caps.Audio {
		features = append(features, "audio")
	}
	if len(features) > 0 && out != nil {
		out("🎨 Multimodal indexing: %s (%s)\n", model, strings.Join(features, "+"))
	}
	return p
}

// ── Process-wide shared vault tracker ─────────────────────────────────

// The default tracker is a bbolt file guarded by an exclusive flock, so
// two concurrent opens inside one process (e.g. two server builds) would
// block each other for the open timeout. In-process builds share a single
// refcounted handle instead; bbolt itself is goroutine-safe.
var sharedTracker struct {
	mu   sync.Mutex
	t    *vault.Tracker
	refs int
}

// openDefaultTracker returns the shared default tracker and a release func.
var openDefaultTracker = func() (*vault.Tracker, func(), error) {
	sharedTracker.mu.Lock()
	defer sharedTracker.mu.Unlock()
	if sharedTracker.t == nil {
		t, err := vault.NewTracker(vault.DefaultDBPath())
		if err != nil {
			return nil, func() {}, err
		}
		sharedTracker.t = t
	}
	sharedTracker.refs++
	var once sync.Once
	return sharedTracker.t, func() {
		once.Do(func() {
			sharedTracker.mu.Lock()
			defer sharedTracker.mu.Unlock()
			sharedTracker.refs--
			if sharedTracker.refs == 0 && sharedTracker.t != nil {
				sharedTracker.t.Close()
				sharedTracker.t = nil
			}
		})
	}, nil
}

// acquireTracker resolves the tracker for opts. It returns nil (with a
// warning written via p when warn is non-empty) if the tracker cannot be
// opened; tracking is best-effort.
func acquireTracker(opts *Options, p *printer, warnFormat string) (*vault.Tracker, func()) {
	if opts.Tracker != nil {
		return opts.Tracker, func() {}
	}
	if opts.NoTracker {
		return nil, func() {}
	}
	t, release, err := openDefaultTracker()
	if err != nil {
		if warnFormat != "" {
			p.Warnf(warnFormat, err)
		}
		return nil, func() {}
	}
	return t, release
}

// ── Per-index serialisation ───────────────────────────────────────────

var indexLocks sync.Map // key: cleaned index dir + name → chan struct{} (cap 1)

// lockIndex serialises builds/syncs of the same index within this process
// so concurrent server/MCP requests cannot interleave writes to one index.
// Waiting for the lock honours ctx.
func lockIndex(ctx context.Context, indexDir, name string) (func(), error) {
	key := filepath.Join(filepath.Clean(indexDir), name)
	v, _ := indexLocks.LoadOrStore(key, make(chan struct{}, 1))
	sem := v.(chan struct{})
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
