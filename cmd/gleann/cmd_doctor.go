package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
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
	cfg := tui.LoadSavedConfig()
	if cfg == nil || !cfg.Completed {
		fail("No config found — run 'gleann setup' or 'gleann setup --bootstrap'")
	} else {
		configPath := configFilePath()
		ok(fmt.Sprintf("Config loaded (%s)", configPath))
	}

	// ── 2. Ollama connectivity ──────────────────────────────────────────
	fmt.Println()
	fmt.Println("Ollama")
	host := "http://localhost:11434"
	if cfg != nil && cfg.OllamaHost != "" {
		host = cfg.OllamaHost
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(host + "/api/tags")
	if err != nil {
		fail(fmt.Sprintf("Cannot reach Ollama at %s — is it running?", host))
		fail("  Fix: ollama serve   (or systemctl start ollama)")
	} else {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			ok(fmt.Sprintf("Ollama reachable at %s", host))
			checkModels(cfg, host, ok, warn, fail)
		} else {
			fail(fmt.Sprintf("Ollama returned HTTP %d at %s", resp.StatusCode, host))
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
			fmt.Printf("       • %s (%d passages)\n", idx.Name, idx.NumPassages)
		}
	}

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
		os.Exit(1)
	}
}

// configFilePath returns the expected config path without loading it.
func configFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gleann", "config.json")
}

// checkModels verifies that the configured embedding and LLM models are available.
func checkModels(cfg *tui.OnboardResult, host string, ok, warn, fail func(string)) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(host + "/api/tags")
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
	if available[embModel] || available[embModel+":latest"] {
		ok(fmt.Sprintf("Embedding model '%s' available", embModel))
	} else {
		fail(fmt.Sprintf("Embedding model '%s' not found — run: ollama pull %s", embModel, embModel))
	}

	// Check LLM model.
	llmModel := "gemma3:4b"
	if cfg != nil && cfg.LLMModel != "" {
		llmModel = cfg.LLMModel
	}
	if available[llmModel] || available[llmModel+":latest"] {
		ok(fmt.Sprintf("LLM model '%s' available", llmModel))
	} else {
		fail(fmt.Sprintf("LLM model '%s' not found — run: ollama pull %s", llmModel, llmModel))
	}

	// Check reranker (optional).
	if cfg != nil && cfg.RerankEnabled && cfg.RerankModel != "" {
		if available[cfg.RerankModel] || available[cfg.RerankModel+":latest"] {
			ok(fmt.Sprintf("Reranker model '%s' available", cfg.RerankModel))
		} else {
			warn(fmt.Sprintf("Reranker model '%s' not found — run: ollama pull %s", cfg.RerankModel, cfg.RerankModel))
		}
	}
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

	var plugins []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &plugins); err != nil {
		warn(fmt.Sprintf("Invalid plugins.json: %v", err))
		return
	}

	if len(plugins) == 0 {
		ok("No plugins registered (this is fine — plugins are optional)")
		return
	}

	ok(fmt.Sprintf("%d plugin(s) registered", len(plugins)))
	for _, p := range plugins {
		expanded := tui.ExpandPath(p.Path)
		if _, err := os.Stat(expanded); err != nil {
			warn(fmt.Sprintf("Plugin '%s' binary not found at %s", p.Name, expanded))
		} else {
			ok(fmt.Sprintf("Plugin '%s' binary exists", p.Name))
		}
	}
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
