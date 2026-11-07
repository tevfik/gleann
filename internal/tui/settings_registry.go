package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/modules/settings"
	"github.com/tevfik/gleann/pkg/gleann"
)

// Settings lists gleann's own settings once (~/.gleann/config.json). The web
// UI (GET/PUT /api/settings), the TUI settings screen and the setup wizard's
// summary all edit through it, so they offer the same fields, defaults,
// options and checks. A config value no field covers fails
// TestSettingsCoverEveryConfigKey.
func Settings() *settings.Registry[OnboardResult] {
	return &settings.Registry[OnboardResult]{
		Title:      "gleann",
		ModelsPath: "/api/models/catalog",
		Note:       "Saved to ~/.gleann/config.json. Restart `gleann serve` (and the MCP server) to apply.",
		Apply:      settings.Live,
		Load:       loadSettings,
		Save: func(c *OnboardResult) error {
			c.CORSOrigins = splitOrigins(c.corsText)
			return saveExact(configPath(), *c)
		},
		BeforeSave: func(c *OnboardResult) { c.Completed = true },
		Validate:   validateSettings,
		// Install state, lists edited elsewhere (roles, format text in
		// `gleann config`), and the llama.cpp download defaults.
		Internal: []string{"completed", "llamacpp_config", "install_path", "install_completions", "roles", "format_text"},
		Fields:   settingsFields(),
	}
}

const (
	groupEmbedding = "Embedding"
	groupLLM       = "Language model"
	groupReranker  = "Reranker"
	groupProviders = "Provider endpoints & keys"
	groupIndex     = "Indexes"
	groupChat      = "Chat"
	groupServices  = "Services"
	groupCLI       = "Command line"
)

func settingsFields() []settings.Field[OnboardResult] {
	type F = settings.Field[OnboardResult]
	modelField := func(key, label, help string, kind string, bind func(c *OnboardResult) any, provider func(c *OnboardResult) string) F {
		return F{Key: key, Label: label, Type: settings.Select, Group: groupEmbedding, Help: help, AllowCustom: true,
			Bind:        bind,
			OptionsFrom: func(c *OnboardResult) ([]settings.Option, error) { return modelOptions(c, provider(c), kind) }}
	}
	embProvider := func(c *OnboardResult) string { return c.EmbeddingProvider }
	llmProvider := func(c *OnboardResult) string { return c.LLMProvider }

	emb := modelField("embedding_model", "Embedding model", "Model that turns passages and queries into vectors. "+
		"Changing it requires rebuilding existing indexes. Multilingual / Turkish: multilingual-e5-small (fast) or bge-m3 (best, 5x slower).",
		modelcatalog.Embedding, func(c *OnboardResult) any { return &c.EmbeddingModel }, embProvider)
	code := modelField("code_embedding_model", "Code embedding model", "Used for source code in mixed indexes. Empty = the embedding model.",
		modelcatalog.Embedding, func(c *OnboardResult) any { return &c.CodeEmbeddingModel }, embProvider)
	docs := modelField("doc_embedding_model", "Docs embedding model", "Used for documents in mixed indexes. Empty = the embedding model.",
		modelcatalog.Embedding, func(c *OnboardResult) any { return &c.DocEmbeddingModel }, embProvider)
	llm := modelField("llm_model", "LLM model", "Model that writes answers in ask and chat.",
		modelcatalog.LLM, func(c *OnboardResult) any { return &c.LLMModel }, llmProvider)
	llm.Group = groupLLM
	mm := modelField("multimodal_model", "Multimodal model", "Vision model for images (e.g. minicpm-v, llava). Empty = off.",
		modelcatalog.LLM, func(c *OnboardResult) any { return &c.MultimodalModel }, llmProvider)
	mm.Group = groupLLM

	return []F{
		// ── Embedding ──
		{Key: "embedding_provider", Label: "Embedding provider", Type: settings.Select, Group: groupEmbedding,
			Help: "Where embeddings are computed. Built-in (bitllm.c) runs GGUF models inside gleann and is the fastest " +
				"choice on a CPU; Ollama and OpenAI-compatible servers are reached over HTTP.",
			Options: embeddingProviderOptions(),
			Bind:    func(c *OnboardResult) any { return &c.EmbeddingProvider },
			Apply:   settings.Restart, // the server starts the embedded llama-server only at start-up
			Check:   checkProvider("embedding")},
		emb, code, docs,

		// ── Language model ──
		{Key: "llm_provider", Label: "LLM provider", Type: settings.Select, Group: groupLLM,
			Help:    "Where answers are generated.",
			Options: llmProviderOptions(),
			Bind:    func(c *OnboardResult) any { return &c.LLMProvider },
			Apply:   settings.Restart, // the server starts the embedded llama-server only at start-up
			Check:   checkProvider("llm")},
		llm, mm,

		// ── Reranker ──
		{Key: "rerank_enabled", Label: "Rerank results", Type: settings.Bool, Group: groupReranker,
			Help: "Re-score the best candidates with a cross-encoder before answering. Clearly better on hard and " +
				"multilingual questions (+6 to +9 nDCG@10 on Turkish), costs about 0.4 s per query with the recommended model.",
			Bind: func(c *OnboardResult) any { return &c.RerankEnabled }},
		{Key: "rerank_provider", Label: "Reranker engine", Type: settings.Select, Group: groupReranker, Default: string(gleann.RerankerAuto),
			Help: "auto: the built-in bitllm.c reranker when this build has it and the model is a local GGUF, " +
				"otherwise the embedding provider. Ollama has no cross-encoder API: its \"reranker\" compares embeddings and is much weaker.",
			VisibleIf: &settings.Cond{Key: "rerank_enabled", Equals: true},
			Options:   rerankProviderOptions(),
			Bind:      func(c *OnboardResult) any { return &c.RerankProvider },
			Check: func(c *OnboardResult, v any) (any, error) {
				if v == string(gleann.RerankerEIF) && !gleann.EIFRerankAvailable() {
					return nil, errors.New("this gleann build has no built-in reranker (needs the eif build, e.g. gleann-full)")
				}
				return v, nil
			}},
		{Key: "rerank_model", Label: "Reranker model", Type: settings.Select, Group: groupReranker, AllowCustom: true,
			Help: "Recommended: mMiniLMv2 mMARCO — multilingual, ~0.4 s per query on a CPU. BGE Reranker v2 M3 is the most " +
				"accurate but ~3 s per query on a CPU (up to 13 s on long passages): pick it with a GPU server or for batch work. " +
				"Built-in models are Q8_0 GGUF files in ~/.gleann/models; download them under Models.",
			VisibleIf:   &settings.Cond{Key: "rerank_enabled", Equals: true},
			Bind:        func(c *OnboardResult) any { return &c.RerankModel },
			OptionsFrom: rerankModelOptions,
			Placeholder: func(c *OnboardResult) string { return resolvedReranker(c).Model }},

		// ── Provider endpoints & keys ──
		{Key: "ollama_host", Label: "Ollama URL", Type: settings.Text, Group: groupProviders, Default: gleann.DefaultOllamaHost,
			Help: "Used when a provider is ollama.",
			Bind: func(c *OnboardResult) any { return &c.OllamaHost }},
		{Key: "ollama_api_key", Label: "Ollama API key", Type: settings.Secret, Group: groupProviders,
			Help: "Optional bearer key for Ollama behind an auth proxy or a hosted Ollama.",
			Bind: func(c *OnboardResult) any { return &c.OllamaAPIKey }},
		{Key: "openai_base_url", Label: "OpenAI-compatible URL", Type: settings.Text, Group: groupProviders,
			Help:        "Base URL of an OpenAI-compatible API (vLLM, LM Studio, llama-server, ...). Empty = api.openai.com.",
			Placeholder: func(*OnboardResult) string { return "https://api.openai.com/v1" },
			Bind:        func(c *OnboardResult) any { return &c.OpenAIBaseURL }},
		{Key: "openai_api_key", Label: "OpenAI API key", Type: settings.Secret, Group: groupProviders,
			Bind: func(c *OnboardResult) any { return &c.OpenAIKey }},
		{Key: "anthropic_api_key", Label: "Anthropic API key", Type: settings.Secret, Group: groupProviders,
			Bind: func(c *OnboardResult) any { return &c.AnthropicKey }},

		// ── Indexes ──
		{Key: "index_dir", Label: "Index directory", Type: settings.Path, Group: groupIndex, Default: DefaultIndexDir(),
			Apply: settings.Restart,
			Bind:  func(c *OnboardResult) any { return &c.IndexDir }},
		{Key: "backend", Label: "Vector backend", Type: settings.Select, Group: groupIndex, Default: gleann.DefaultConfig().Backend,
			Help:    "Engine for new indexes. DiskANN keeps RAM low; HNSW is in memory; FAISS needs the full build.",
			Options: backendOptions(),
			Bind:    func(c *OnboardResult) any { return &c.Backend }},
		{Key: "storage_mode", Label: "Vector storage", Type: settings.Select, Group: groupIndex, Default: string(gleann.StorageStore),
			Help: "For new indexes. store keeps the vectors: a search embeds only the query (fast). recompute prunes them " +
				"to save disk and re-embeds candidates on every search (much slower). Convert an existing index in " +
				"Indexes → Index Manager (s) or with `gleann index storage <name> --set store|recompute`.",
			Options: settings.Opts(
				string(gleann.StorageStore), "store — keep vectors, fast search (recommended)",
				string(gleann.StorageRecompute), "recompute — prune vectors, smaller, slow search"),
			Bind: func(c *OnboardResult) any { return &c.StorageMode }},

		// ── Chat ──
		{Key: "temperature", Label: "Temperature", Type: settings.Number, Group: groupChat, Default: 0.7,
			Help: "0 = focused and repeatable, higher = more creative.",
			Min:  settings.F(0.05), Max: settings.F(2), Step: settings.F(0.05),
			Bind: func(c *OnboardResult) any { return &c.Temperature }},
		{Key: "repeat_penalty", Label: "Repeat penalty", Type: settings.Number, Group: groupChat, Default: 1.1,
			Help: "1.0 = off, higher discourages the model from repeating itself.",
			Min:  settings.F(1), Max: settings.F(2), Step: settings.F(0.05),
			Bind: func(c *OnboardResult) any { return &c.RepeatPenalty }},
		{Key: "max_tokens", Label: "Max tokens", Type: settings.Number, Group: groupChat, Default: 2048,
			Help: "Upper limit on the length of one answer.",
			Min:  settings.F(64), Max: settings.F(131072),
			Options: settings.Opts("512", "512", "1024", "1024", "2048", "2048", "4096", "4096", "8192", "8192"),
			Bind:    func(c *OnboardResult) any { return &c.MaxTokens }},
		{Key: "top_k", Label: "Passages per question", Type: settings.Number, Group: groupChat, Default: 10,
			Help: "How many passages are retrieved for each question (top-k).",
			Min:  settings.F(1), Max: settings.F(100),
			Options: settings.Opts("3", "3", "5", "5", "10", "10", "15", "15", "20", "20", "30", "30"),
			Bind:    func(c *OnboardResult) any { return &c.TopK }},
		{Key: "system_prompt", Label: "System prompt", Type: settings.Text, Group: groupChat,
			Help:        "Instructions given to the model before every conversation. Empty = gleann's default.",
			Placeholder: func(*OnboardResult) string { return "gleann default" },
			Bind:        func(c *OnboardResult) any { return &c.SystemPrompt }},

		// ── Services ──
		{Key: "server_enabled", Label: "REST API server", Type: settings.Bool, Group: groupServices, Apply: settings.Restart,
			Help: "Run `gleann serve` (web UI and REST API) as part of the installation.",
			Bind: func(c *OnboardResult) any { return &c.ServerEnabled }},
		{Key: "server_addr", Label: "Server address", Type: settings.Text, Group: groupServices, Default: gleann.DefaultServerAddr,
			Apply: settings.Restart,
			Help:  "host:port of `gleann serve`. 0.0.0.0 exposes it to the network (a token is required).",
			Bind:  func(c *OnboardResult) any { return &c.ServerAddr }},
		{Key: "auth", Label: "API authentication", Type: settings.Select, Group: groupServices, Default: "on",
			Apply: settings.Restart,
			Help: "on: every API call needs a token (gleann token show). off is accepted only while the server " +
				"listens on a loopback address. GLEANN_AUTH overrides it.",
			Options: settings.Opts("on", "on — token required (recommended)", "off", "off — no token (loopback only)"),
			Bind:    func(c *OnboardResult) any { return &c.Auth }},
		{Key: "cors_origins", Label: "Allowed browser origins", Type: settings.Text, Group: groupServices,
			Apply: settings.Restart,
			Help: "Comma-separated origins (https://app.example.com) whose web pages may call the API. " +
				"Empty: none besides the built-in web UI. GLEANN_CORS_ORIGINS adds more.",
			Bind: func(c *OnboardResult) any { return &c.corsText },
			Check: func(_ *OnboardResult, v any) (any, error) {
				origins := splitOrigins(fmt.Sprint(v))
				for _, o := range origins {
					if o == "*" || !(strings.HasPrefix(o, "http://") || strings.HasPrefix(o, "https://")) {
						return nil, fmt.Errorf("%q is not an origin like https://app.example.com (wildcards are not allowed)", o)
					}
				}
				return strings.Join(origins, ", "), nil
			}},
		{Key: "rate_limit", Label: "Rate limit (req/s)", Type: settings.Number, Group: groupServices, Default: 60,
			Apply: settings.Restart,
			Help:  "Sustained requests per second per client address. Local clients (127.0.0.1) are not limited. GLEANN_RATE_LIMIT overrides it.",
			Min:   settings.F(1), Max: settings.F(100000), Step: settings.F(10),
			Bind: func(c *OnboardResult) any { return &c.RateLimit }},
		{Key: "rate_burst", Label: "Rate limit burst", Type: settings.Number, Group: groupServices, Default: 120,
			Apply: settings.Restart,
			Help:  "Requests a client may send at once before the limit applies. GLEANN_RATE_BURST overrides it.",
			Min:   settings.F(1), Max: settings.F(100000), Step: settings.F(10),
			Bind: func(c *OnboardResult) any { return &c.RateBurst }},
		{Key: "mcp_enabled", Label: "MCP server", Type: settings.Bool, Group: groupServices, Apply: settings.Restart,
			Help: "Offer gleann's tools to AI agents (Claude Code, Cursor, ...) over MCP.",
			Bind: func(c *OnboardResult) any { return &c.MCPEnabled }},
		{Key: "a2a_enabled", Label: "Agent-to-Agent (A2A)", Type: settings.Bool, Group: groupServices, Apply: settings.Restart,
			Help: "Publish the A2A agent card and endpoints on the REST server.",
			Bind: func(c *OnboardResult) any {
				if c.A2AEnabled == nil { // unset means on
					on := true
					c.A2AEnabled = &on
				}
				return c.A2AEnabled
			}},

		// ── Command line ──
		{Key: "quiet", Label: "Quiet output", Type: settings.Bool, Group: groupCLI,
			Help: "Suppress status messages in the command line.",
			Bind: func(c *OnboardResult) any { return &c.Quiet }},
		{Key: "word_wrap", Label: "Word wrap", Type: settings.Number, Group: groupCLI,
			Help: "Wrap answers at this many columns. 0 = terminal width.",
			Min:  settings.F(0), Max: settings.F(400), Step: settings.F(10),
			Bind: func(c *OnboardResult) any { return &c.WordWrap }},
	}
}

// loadSettings reads config.json; a missing file is an empty config, an
// invalid one an error (so a save never overwrites a file it could not read).
func loadSettings() (*OnboardResult, error) {
	var r OnboardResult
	data, err := os.ReadFile(configPath())
	if errors.Is(err, os.ErrNotExist) {
		return &r, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("%s is not valid JSON: %w", configPath(), err)
		}
	}
	r.corsText = strings.Join(r.CORSOrigins, ", ")
	return &r, nil
}

// splitOrigins turns "a, b" into [a b], dropping empty entries and a trailing slash.
func splitOrigins(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimRight(strings.TrimSpace(p), "/"); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// validateSettings checks rules that span fields.
func validateSettings(c *OnboardResult) error {
	if strings.EqualFold(c.Auth, "off") {
		addr := c.ServerAddr
		if addr == "" {
			addr = gleann.DefaultServerAddr
		}
		host, _, err := net.SplitHostPort(addr)
		if ip := net.ParseIP(host); err != nil || !(host == "localhost" || (ip != nil && ip.IsLoopback())) {
			return fmt.Errorf("authentication can be off only while the server address is a loopback address (now %s)", addr)
		}
	}
	if c.RerankEnabled {
		rc := resolvedReranker(c)
		if rc.Provider == gleann.RerankerEIF && !gleann.EIFRerankAvailable() {
			return errors.New("the reranker engine is built-in (bitllm.c), which this gleann build does not have")
		}
	}
	return nil
}

// resolvedReranker is the reranker the saved settings select, as every
// command will resolve it (gleann.ResolveRerankerConfig).
func resolvedReranker(c *OnboardResult) gleann.RerankerConfig {
	cfg := gleann.DefaultConfig()
	cfg.EmbeddingProvider = c.EmbeddingProvider
	cfg.OllamaHost = c.OllamaHost
	cfg.OllamaAPIKey = c.OllamaAPIKey
	cfg.SearchConfig.RerankerConfig.Provider = gleann.RerankerProvider(c.RerankProvider)
	return gleann.ResolveRerankerConfig(cfg, c.RerankModel)
}

// checkProvider refuses a provider this build cannot run.
func checkProvider(what string) func(*OnboardResult, any) (any, error) {
	return func(_ *OnboardResult, v any) (any, error) {
		switch v {
		case "eif":
			if !gleann.IsEIFSupported() {
				return nil, fmt.Errorf("this gleann build has no built-in %s engine (needs the eif build, e.g. gleann-full)", what)
			}
		case "native":
			if !gleann.IsNativeSupported() {
				return nil, errors.New("this gleann build has no native embedder")
			}
		}
		return v, nil
	}
}

func embeddingProviderOptions() []settings.Option {
	opts := settings.Opts(
		"ollama", "Ollama server",
		"openai", "OpenAI-compatible API",
		"llamacpp", "llama.cpp (embedded llama-server, local GGUF)")
	if gleann.IsEIFSupported() {
		opts = append([]settings.Option{{Value: "eif", Label: "Built-in bitllm.c (in process, local GGUF) — recommended on CPU"}}, opts...)
	}
	if gleann.IsNativeSupported() {
		opts = append(opts, settings.Option{Value: "native", Label: "Native Go"})
	}
	return opts
}

func llmProviderOptions() []settings.Option {
	opts := settings.Opts(
		"ollama", "Ollama server",
		"openai", "OpenAI-compatible API",
		"anthropic", "Anthropic",
		"llamacpp", "llama.cpp (embedded llama-server, local GGUF)")
	if gleann.IsEIFSupported() {
		opts = append(opts, settings.Option{Value: "eif", Label: "Built-in bitllm.c (in process, local GGUF)"})
	}
	return opts
}

func rerankProviderOptions() []settings.Option {
	opts := []settings.Option{{Value: string(gleann.RerankerAuto), Label: "auto (recommended)"}}
	if gleann.EIFRerankAvailable() {
		opts = append(opts, settings.Option{Value: string(gleann.RerankerEIF), Label: "Built-in bitllm.c cross-encoder (in process, local GGUF)"})
	}
	return append(opts, settings.Opts(
		string(gleann.RerankerOllama), "Ollama (embedding similarity, weaker)",
		string(gleann.RerankerLlamacpp), "llama.cpp server",
		string(gleann.RerankerJina), "Jina AI API (JINA_API_KEY)",
		string(gleann.RerankerCohere), "Cohere API (COHERE_API_KEY)",
		string(gleann.RerankerVoyage), "Voyage AI API (VOYAGE_API_KEY)")...)
}

func backendOptions() []settings.Option {
	opts := settings.Opts(
		"diskann", "DiskANN — disk-efficient, low RAM",
		"hnsw", "HNSW — in memory, fast")
	if gleann.IsFaissSupported() {
		opts = append(opts, settings.Opts(
			"faiss", "FAISS — SIMD-accelerated",
			"faiss-hybrid", "FAISS hybrid — FAISS build + Go search")...)
	}
	return opts
}

// ── Model lists ────────────────────────────────────────────────

// optionsCache keeps run-time model lists for a short while: a TUI row asks
// for its options on every redraw, and the web form on every GET.
var optionsCache = struct {
	sync.Mutex
	m map[string]cachedOptions
}{m: map[string]cachedOptions{}}

type cachedOptions struct {
	opts []settings.Option
	err  error
	at   time.Time
}

const optionsTTL = 30 * time.Second

func cached(key string, fetch func() ([]settings.Option, error)) ([]settings.Option, error) {
	optionsCache.Lock()
	if e, ok := optionsCache.m[key]; ok && time.Since(e.at) < optionsTTL {
		optionsCache.Unlock()
		return e.opts, e.err
	}
	optionsCache.Unlock()
	opts, err := fetch()
	optionsCache.Lock()
	optionsCache.m[key] = cachedOptions{opts, err, time.Now()}
	optionsCache.Unlock()
	return opts, err
}

// modelOptions lists the models provider offers for kind: local GGUF files
// and catalog downloads for the in-process providers, the server's list for
// Ollama and OpenAI-compatible APIs.
func modelOptions(c *OnboardResult, provider, kind string) ([]settings.Option, error) {
	switch provider {
	case "eif", "llamacpp":
		// A download renames a file into the models directory, which changes
		// its mtime: the list is fresh right after a download.
		stamp := ""
		if fi, err := os.Stat(DefaultModelsDir()); err == nil {
			stamp = fi.ModTime().String()
		}
		return cached("local|"+kind+"|"+stamp, func() ([]settings.Option, error) { return localModelOptions(kind), nil })
	case "ollama", "openai":
		host, key := c.OllamaHost, c.OllamaAPIKey
		if host == "" {
			host = gleann.DefaultOllamaHost
		}
		if provider == "openai" {
			host, key = c.OpenAIBaseURL, c.OpenAIKey
		}
		return cached(provider+"|"+host+"|"+kind, func() ([]settings.Option, error) {
			models, err := fetchModels(provider, host, key)
			if err != nil {
				return nil, fmt.Errorf("could not list %s models: %w", provider, err)
			}
			switch kind {
			case modelcatalog.Embedding:
				models = filterEmbeddingModels(models)
			case modelcatalog.LLM:
				models = filterLLMModels(models)
			case modelcatalog.Reranker:
				models = filterRerankerModels(models)
			}
			return infoOptions(models), nil
		})
	}
	return nil, nil
}

// rerankModelOptions lists reranker models for the engine the settings
// resolve to; the recommended catalog models come first, with a hint.
func rerankModelOptions(c *OnboardResult) ([]settings.Option, error) {
	switch resolvedReranker(c).Provider {
	case gleann.RerankerEIF, gleann.RerankerLlamacpp:
		return modelOptions(c, "eif", modelcatalog.Reranker)
	case gleann.RerankerOllama:
		return modelOptions(c, "ollama", modelcatalog.Reranker)
	case gleann.RerankerJina:
		return settings.Opts("jina-reranker-v2-base-multilingual", "jina-reranker-v2-base-multilingual"), nil
	case gleann.RerankerCohere:
		return settings.Opts("rerank-v3.5", "rerank-v3.5", "rerank-multilingual-v3.0", "rerank-multilingual-v3.0"), nil
	case gleann.RerankerVoyage:
		return settings.Opts("rerank-2", "rerank-2", "rerank-2-lite", "rerank-2-lite"), nil
	}
	return nil, nil
}

// localModelOptions lists the catalog models of kind (downloaded or not)
// and the other GGUF files on disk that look like kind. A file in
// ~/.gleann/models is stored by name, anything else by path.
func localModelOptions(kind string) []settings.Option {
	var opts []settings.Option
	seen := map[string]bool{}
	for _, e := range modelcatalog.OfKind(kind) {
		label := e.Name
		if !e.Installed() {
			label += " — download under Models (" + settings.HumanSize(e.Bytes) + ")"
		}
		opts = append(opts, settings.Option{Value: e.File, Label: label})
		seen[strings.ToLower(e.File)] = true
	}
	files, _ := fetchEIFModels("")
	switch kind {
	case modelcatalog.Embedding:
		files = filterEmbeddingModels(files)
	case modelcatalog.LLM:
		files = filterLLMModels(files)
	case modelcatalog.Reranker:
		files = filterRerankerModels(files)
	}
	modelsDir := DefaultModelsDir()
	for _, f := range files {
		if seen[strings.ToLower(f.Name)] {
			continue
		}
		seen[strings.ToLower(f.Name)] = true
		v := f.Tag
		if filepath.Dir(f.Tag) == modelsDir {
			v = f.Name
		}
		opts = append(opts, settings.Option{Value: v, Label: f.Name + " (" + f.Size + ")"})
	}
	return opts
}

func infoOptions(models []ModelInfo) []settings.Option {
	opts := make([]settings.Option, 0, len(models))
	for _, m := range models {
		label := m.Name
		if m.Size != "" {
			label += " (" + m.Size + ")"
		}
		opts = append(opts, settings.Option{Value: m.Name, Label: label})
	}
	return opts
}

// ResetModelOptionsCache drops the cached model lists, e.g. after a download.
func ResetModelOptionsCache() {
	optionsCache.Lock()
	optionsCache.m = map[string]cachedOptions{}
	optionsCache.Unlock()
}
