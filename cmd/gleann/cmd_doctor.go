package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// cmdDoctor runs a series of health checks and prints a diagnostic summary.
// Exit code 0 = all checks pass; 1 = at least one critical check failed.
func cmdDoctor() {
	passed := 0
	warned := 0
	failed := 0

	ok := func(msg string) { fmt.Printf("  ✅ %s\n", msg); passed++ }
	warn := func(msg string) { fmt.Printf("  ⚠  %s\n", msg); warned++ }
	fail := func(msg string) { fmt.Printf("  ❌ %s\n", msg); failed++ }

	fmt.Println("gleann doctor — system health check")
	fmt.Println()

	// ── 1. Configuration ────────────────────────────────────────────────
	fmt.Println("Configuration")
	cfg, cfgErr := tui.LoadSavedConfigChecked()
	if cfgErr != nil {
		fail(fmt.Sprintf("Config is malformed: %v", cfgErr))
	} else if cfg == nil || !cfg.Completed {
		fail("No config found — run 'gleann setup' or 'gleann setup --bootstrap'")
	} else {
		configPath := configFilePath()
		ok(fmt.Sprintf("Config loaded (%s)", configPath))
	}

	// ── 2. Ollama connectivity ──────────────────────────────────────────
	fmt.Println()
	fmt.Println("Ollama")
	cfgHost := ""
	if cfg != nil {
		cfgHost = cfg.OllamaHost
	}
	host := gleann.ResolveOllamaHost("", cfgHost)

	ollamaUsed := cfg == nil || cfg.EmbeddingProvider == "ollama" || cfg.LLMProvider == "ollama"

	ollamaKey := ""
	if cfg != nil {
		ollamaKey = cfg.OllamaAPIKey
	}
	ollamaKey = gleann.ResolveOllamaAPIKey(ollamaKey)
	keyStatus := "no API key"
	if ollamaKey != "" {
		keyStatus = "API key configured"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := gleann.OllamaGet(client, host+"/api/tags", ollamaKey)
	if err != nil {
		if ollamaUsed {
			fail(fmt.Sprintf("Cannot reach Ollama at %s — is it running?", host))
			fail("  Fix: ollama serve   (or systemctl start ollama)")
		} else {
			warn(fmt.Sprintf("Cannot reach Ollama at %s (optional — current providers: emb=%s, llm=%s)", host, cfg.EmbeddingProvider, cfg.LLMProvider))
		}
	} else {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			ok(fmt.Sprintf("Ollama reachable at %s (%s)", host, keyStatus))
			checkModels(cfg, host, ok, warn, fail)
		} else {
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				warn(fmt.Sprintf("Ollama at %s requires authentication (%s) — set: gleann config set ollama_api_key <key>  (or OLLAMA_API_KEY)", host, keyStatus))
			}
			if ollamaUsed {
				fail(fmt.Sprintf("Ollama returned HTTP %d at %s", resp.StatusCode, host))
			} else {
				warn(fmt.Sprintf("Ollama returned HTTP %d at %s (optional)", resp.StatusCode, host))
			}
		}
	}

	// ── 3. Indexes ──────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("Indexes")
	indexDir := tui.DefaultIndexDir()
	if cfg != nil && cfg.IndexDir != "" {
		indexDir = cfg.IndexDir
	}
	indexes, listErr := gleann.ListIndexes(indexDir)
	if listErr != nil {
		warn(fmt.Sprintf("Cannot read index directory: %v", listErr))
	} else if len(indexes) == 0 {
		warn("No indexes found — build one with: gleann index build <name> --docs <dir>")
	} else {
		ok(fmt.Sprintf("%d index(es) in %s", len(indexes), indexDir))
		for _, idx := range indexes {
			fmt.Printf("       • %s\n", describeIndex(indexDir, idx))
		}
		checkIndexHealth(indexDir, indexes, warn)
		// Graph provenance (v1.22 T3): a graph written by an older extractor
		// needs `gleann graph build <name>` — nothing migrates graph rows.
		warnStaleGraphs(indexDir, indexes, warn)
	}
	warnPendingSyncBackups(indexDir, warn)
	warnLegacyMemoryStores(indexDir, warn)

	// ── 4. Disk space ───────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("Disk")
	checkDiskSpace(indexDir, ok, warn)

	// ── 5. Plugins ──────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("Plugins")
	checkPlugins(ok, warn)

	// ── Summary ─────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Printf("Summary: %d passed, %d warnings, %d failed\n", passed, warned, failed)
	if failed > 0 {
		fmt.Println()
		fmt.Println("Run 'gleann setup --bootstrap' to fix common issues automatically.")
		exitFunc(1)
	}
}

// configFilePath returns the expected config path without loading it.
func configFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gleann", "config.json")
}

// checkModels verifies that the configured embedding and LLM models are available.
func checkModels(cfg *tui.OnboardResult, host string, ok, warn, fail func(string)) {
	ollamaKey := ""
	if cfg != nil {
		ollamaKey = cfg.OllamaAPIKey
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := gleann.OllamaGet(client, host+"/api/tags", gleann.ResolveOllamaAPIKey(ollamaKey))
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		warn("Cannot parse Ollama model list")
		return
	}

	available := make(map[string]bool, len(tagsResp.Models))
	for _, m := range tagsResp.Models {
		available[m.Name] = true
	}

	// Check embedding model.
	embModel := "bge-m3"
	if cfg != nil && cfg.EmbeddingModel != "" {
		embModel = cfg.EmbeddingModel
	}
	if cfg != nil && (cfg.EmbeddingProvider == "eif" || cfg.EmbeddingProvider == "llamacpp" || strings.HasSuffix(embModel, ".gguf") || strings.HasSuffix(embModel, ".eifm") || filepath.IsAbs(embModel)) {
		path := embModel
		if !filepath.IsAbs(path) {
			path = filepath.Join(tui.DefaultModelsDir(), path)
		}
		if _, err := os.Stat(path); err == nil {
			ok(fmt.Sprintf("Embedding model '%s' available (local file)", filepath.Base(path)))
		} else {
			fail(fmt.Sprintf("Embedding model file not found at %s", path))
		}
	} else if available[embModel] || available[embModel+":latest"] {
		ok(fmt.Sprintf("Embedding model '%s' available", embModel))
	} else {
		fail(fmt.Sprintf("Embedding model '%s' not found — run: ollama pull %s", embModel, embModel))
	}

	// Check LLM model.
	llmModel := DefaultLLMModel
	if cfg != nil && cfg.LLMModel != "" {
		llmModel = cfg.LLMModel
	}
	if cfg != nil && (cfg.LLMProvider == "eif" || cfg.LLMProvider == "llamacpp" || strings.HasSuffix(llmModel, ".gguf") || strings.HasSuffix(llmModel, ".eifm") || filepath.IsAbs(llmModel)) {
		path := llmModel
		if !filepath.IsAbs(path) {
			path = filepath.Join(tui.DefaultModelsDir(), path)
		}
		if _, err := os.Stat(path); err == nil {
			ok(fmt.Sprintf("LLM model '%s' available (local file)", filepath.Base(path)))
		} else {
			fail(fmt.Sprintf("LLM model file not found at %s", path))
		}
	} else if cfg != nil && cfg.LLMProvider == "openai" && gleann.ResolveOpenAIBaseURL(cfg.OpenAIBaseURL) == "" {
		fail(fmt.Sprintf("LLM provider openai has no endpoint: %v", gleann.ErrOpenAIEndpointUnset))
	} else if cfg != nil && cfg.LLMProvider == "openai" {
		endpoint := gleann.ResolveOpenAIBaseURL(cfg.OpenAIBaseURL)
		if gleann.IsOpenAICloud(endpoint) {
			endpoint += " — OpenAI cloud: documents and questions leave this machine"
		}
		if cfg.OpenAIKey != "" {
			ok(fmt.Sprintf("LLM model '%s' via OpenAI-compatible endpoint (%s, API key configured)", llmModel, endpoint))
		} else {
			ok(fmt.Sprintf("LLM model '%s' via OpenAI-compatible endpoint (%s, no API key)", llmModel, endpoint))
		}
	} else if cfg != nil && cfg.LLMProvider == "anthropic" {
		ok(fmt.Sprintf("LLM model '%s' via Anthropic API", llmModel))
	} else if available[llmModel] || available[llmModel+":latest"] {
		ok(fmt.Sprintf("LLM model '%s' available", llmModel))
	} else {
		fail(fmt.Sprintf("LLM model '%s' not found — run: ollama pull %s", llmModel, llmModel))
	}

	// Check reranker (optional). An Ollama reranker is reported once, by
	// rerankerUnusable below: "ollama pull" would not make it work.
	unusable := rerankerUnusable(cfg)
	if cfg != nil && cfg.RerankEnabled && cfg.RerankModel != "" {
		rerankModel := cfg.RerankModel
		if strings.HasSuffix(rerankModel, ".gguf") || filepath.IsAbs(rerankModel) {
			path := rerankModel
			if !filepath.IsAbs(path) {
				path = filepath.Join(tui.DefaultModelsDir(), path)
			}
			if _, err := os.Stat(path); err == nil {
				ok(fmt.Sprintf("Reranker model '%s' available (local file)", filepath.Base(path)))
			} else {
				warn(fmt.Sprintf("Reranker model file not found at %s", path))
			}
		} else if available[rerankModel] || available[rerankModel+":latest"] {
			ok(fmt.Sprintf("Reranker model '%s' available", rerankModel))
		} else if unusable == "" {
			warn(fmt.Sprintf("Reranker model '%s' not found — run: ollama pull %s", rerankModel, rerankModel))
		}
	}
	// Presence of the model is not enough: the provider must be able to rerank.
	if unusable != "" {
		warn(unusable)
	}
}

// rerankerUnusable explains why a configured reranker cannot work, or returns
// "" when it can (or reranking is off). Ollama lists reranker models but has
// no rerank API, so presence in `ollama list` does not make one usable.
func rerankerUnusable(cfg *tui.OnboardResult) string {
	if cfg == nil || !cfg.RerankEnabled {
		return ""
	}
	provider := gleann.RerankerProvider(cfg.RerankProvider)
	if provider == "" || provider == gleann.RerankerAuto {
		provider = gleann.AutoRerankerProvider(cfg.EmbeddingProvider, cfg.RerankModel)
	}
	if provider == gleann.RerankerOllama {
		return "Reranker is enabled with provider ollama, which has no rerank API: every search falls back to unreranked results — set rerank_provider to \"eif\" or \"llamacpp\" (with rerank_url), or disable rerank"
	}
	return ""
}

// checkDiskSpace reports available space in the index directory.
func checkDiskSpace(indexDir string, ok, warn func(string)) {
	var stat os.FileInfo
	dir := indexDir
	for {
		var err error
		stat, err = os.Stat(dir)
		if err == nil && stat.IsDir() {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			warn("Cannot determine disk space")
			return
		}
		dir = parent
	}

	// Use du to estimate index size (portable, no syscall needed).
	// Just report the directory size if it exists.
	var totalSize int64
	_ = filepath.Walk(indexDir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})

	if totalSize > 0 {
		ok(fmt.Sprintf("Index storage: %s used in %s", formatBytes(totalSize), indexDir))
	} else {
		ok(fmt.Sprintf("Index directory: %s (empty)", indexDir))
	}
}

// checkPlugins verifies plugin registration and binary availability.
func checkPlugins(ok, warn func(string)) {
	home, err := os.UserHomeDir()
	if err != nil {
		warn("Cannot determine home directory for plugin check")
		return
	}

	pluginFile := filepath.Join(home, ".gleann", "plugins.json")
	data, err := os.ReadFile(pluginFile)
	if err != nil {
		if os.IsNotExist(err) {
			ok("No plugins registered (this is fine — plugins are optional)")
			return
		}
		warn(fmt.Sprintf("Cannot read %s: %v", pluginFile, err))
		return
	}

	var reg gleann.PluginRegistry
	if err := json.Unmarshal(data, &reg); err != nil || len(reg.Plugins) == 0 {
		var legacy []gleann.Plugin
		if errLegacy := json.Unmarshal(data, &legacy); errLegacy == nil {
			reg.Plugins = legacy
		} else if err != nil {
			warn(fmt.Sprintf("Invalid plugins.json: %v", err))
			return
		}
	}

	if len(reg.Plugins) == 0 {
		ok("No plugins registered (this is fine — plugins are optional)")
		return
	}

	ok(fmt.Sprintf("%d plugin(s) registered", len(reg.Plugins)))
	for _, p := range reg.Plugins {
		cmdBinary := ""
		if len(p.Command) > 0 {
			cmdBinary = p.Command[0]
		}
		if cmdBinary != "" {
			expanded := tui.ExpandPath(cmdBinary)
			if _, err := os.Stat(expanded); err != nil {
				warn(fmt.Sprintf("Plugin '%s' binary not found at %s", p.Name, expanded))
			} else {
				ok(fmt.Sprintf("Plugin '%s' binary exists (%s)", p.Name, expanded))
				if filepath.Base(expanded) == "gleann-plugin-docs" {
					checkDocsRuntime(expanded, ok, warn)
				}
			}
		} else if p.URL != "" {
			ok(fmt.Sprintf("Plugin '%s' configured via URL (%s)", p.Name, p.URL))
		} else {
			ok(fmt.Sprintf("Plugin '%s' registered", p.Name))
		}
	}
}

// checkDocsRuntime asks the docs plugin whether its ONNX Runtime library and
// OCR models are in place. Without them scanned pages are silently skipped, so
// the plugin looks healthy while producing no text for them.
func checkDocsRuntime(binary string, ok, warn func(string)) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "models", "verify").CombinedOutput()
	if err == nil {
		ok("Plugin 'docs' OCR runtime and models are installed")
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, "FAIL") {
			warn(fmt.Sprintf("Plugin 'docs': %s", line))
		}
	}
	warn("Scanned pages will be skipped. Fix: gleann-plugin-docs models download")
}

// formatBytes formats a byte count in human-readable form.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// checkIndexHealth reports indexes whose passage store, metadata and vector
// file disagree. Indexes edited in place by older versions are the usual cause:
// the passage IDs have gaps, vectors end up attached to the wrong passages and
// searches return the wrong text, with no error to show for it.
func checkIndexHealth(indexDir string, indexes []gleann.IndexMeta, warn func(string)) {
	for _, idx := range indexes {
		h, err := gleann.CheckIndexHealth(indexDir, idx.Name)
		if err != nil {
			warn(fmt.Sprintf("Index %q: cannot check (%v)", idx.Name, err))
			continue
		}
		if !h.NeedsRepair() {
			continue
		}
		for _, p := range h.Problems {
			warn(fmt.Sprintf("Index %q: %s", idx.Name, p))
		}
		switch {
		case gleann.CheckStoreFormat(indexDir, idx.Name) != nil:
			// The problem line already names `gleann index migrate`.
		case h.Repairable:
			fmt.Printf("       → repair it with: gleann index repair %s\n", idx.Name)
		default:
			fmt.Printf("       → rebuild it with: gleann index rebuild %s\n", idx.Name)
		}
	}
	warnMigrationLeftovers(indexDir, indexes, warn)
}

// warnMigrationLeftovers lists the old-format files (<name>_graph,
// _graph_file_hashes.db, .passages.db, .lex.db, ...) that a migrated index
// still has next to it. They are never deleted implicitly (RFC 004 B3).
func warnMigrationLeftovers(indexDir string, indexes []gleann.IndexMeta, warn func(string)) {
	for _, idx := range indexes {
		if gleann.DetectStoreFormat(filepath.Join(indexDir, idx.Name, idx.Name)) != gleann.StoreV2 {
			continue
		}
		left := gleann.MigrationLeftovers(indexDir, idx.Name)
		if len(left) == 0 {
			continue
		}
		warn(fmt.Sprintf("Index %q: %d old-format file(s) left after the migration:", idx.Name, len(left)))
		for _, p := range left {
			fmt.Printf("       %s\n", p)
		}
		fmt.Printf("       → delete them with: gleann index migrate %s --cleanup\n", idx.Name)
	}
}

// describeIndex is the one-line summary `gleann doctor` prints for an index:
// passages, how it searches (backend, embedding model and dimensions, or
// lexical-only) and what it takes on disk.
func describeIndex(indexDir string, idx gleann.IndexMeta) string {
	parts := []string{fmt.Sprintf("%d passages", idx.NumPassages)}
	switch {
	case idx.LexicalOnly || !idx.VectorReady:
		parts = append(parts, "lexical-only (no vectors)")
	default:
		if idx.Backend != "" {
			parts = append(parts, idx.Backend)
		}
		if idx.EmbeddingModel != "" {
			parts = append(parts, idx.EmbeddingModel)
		}
		if idx.Dimensions > 0 {
			parts = append(parts, fmt.Sprintf("%d dims", idx.Dimensions))
		}
	}
	// Incremental updates leave dead vector slots behind until the next merge.
	if idx.VectorSlots > 0 && idx.VectorSlots > int64(idx.NumPassages) {
		dead := idx.VectorSlots - int64(idx.NumPassages)
		parts = append(parts, fmt.Sprintf("%.0f%% dead vector slots", 100*float64(dead)/float64(idx.VectorSlots)))
	}
	parts = append(parts, formatBytes(indexDiskSize(indexDir, idx.Name)))
	return idx.Name + " — " + strings.Join(parts, ", ")
}

// indexDiskSize is the total size of the files in an index's directory.
func indexDiskSize(indexDir, name string) int64 {
	var total int64
	_ = filepath.Walk(filepath.Join(indexDir, name), func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// warnPendingSyncBackups reports indexes with a rollback copy (<dir>.sync-bak)
// on disk: a sync is rewriting them right now, or one was killed mid-rebuild
// and the index directory may be partial or missing. `gleann index sync <name>`
// settles it (restores the copy or discards a stale one).
func warnPendingSyncBackups(indexDir string, warn func(string)) {
	for _, name := range indexing.PendingSyncBackups(indexDir) {
		warn(fmt.Sprintf("Index %q: a sync left a rollback copy (%s); if no sync is running it was interrupted. Run: gleann index sync %s", name, indexing.SyncBackupPath(indexDir, name), name))
	}
}

// warnLegacyMemoryStores lists v1.9 <name>_memory Kùzu stores whose entities
// were not imported into memory.sqlite yet.
func warnLegacyMemoryStores(indexDir string, warn func(string)) {
	if len(memory.ScanLegacyKuzuStores(indexDir)) == 0 {
		return // nothing to import; do not open (or create) the memory database
	}
	store, err := memory.DefaultStore()
	if err != nil {
		warn(fmt.Sprintf("Memory store not checked for old Kùzu entity stores: %v", err))
		return
	}
	defer store.Close()
	legacy := store.LegacyKuzuStores(indexDir)
	if len(legacy) == 0 {
		return
	}
	for _, l := range legacy {
		exp, imp := memory.LegacyImportCommands(l)
		warn(fmt.Sprintf("Entities of index %q are in an old Kùzu store (%s) and not imported. Run: %s ; then: %s", l.Name, l.Path, exp, imp))
	}
}
