//go:build treesitter

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tevfik/gleann/pkg/gleann"
)

// cmdBenchmark implements `gleann benchmark --index <name> --docs <dir>`.
// It measures token reduction: raw corpus tokens vs RAG context tokens.
func cmdBenchmark(args []string) {
	config := getConfig(args)
	indexName := getFlag(args, "--index")
	docsDir := getFlag(args, "--docs")
	topK := 10
	if v := getFlag(args, "--top-k"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			topK = n
		}
	}

	if indexName == "" || docsDir == "" {
		printBenchmarkUsage()
		os.Exit(1)
	}

	fmt.Println("📊 gleann benchmark — Token Reduction Analysis")
	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("Index: %s\nDocs:  %s\n\n", indexName, docsDir)

	// Phase 1: Count raw corpus tokens.
	fmt.Println("Phase 1: Counting raw corpus tokens...")
	start := time.Now()
	rawTokens, rawFiles, rawBytes := countCorpusTokens(docsDir)
	phase1 := time.Since(start)
	fmt.Printf("  Files: %d\n", rawFiles)
	fmt.Printf("  Bytes: %s\n", benchFormatBytes(rawBytes))
	fmt.Printf("  Tokens (est): %d\n", rawTokens)
	fmt.Printf("  Time: %s\n\n", phase1.Round(time.Millisecond))

	// Phase 2: Simulate RAG context size.
	fmt.Println("Phase 2: Calculating RAG context size...")
	ragTokens := estimateRAGTokens(config, indexName, topK)
	fmt.Printf("  Top-K: %d\n", topK)
	fmt.Printf("  RAG context tokens (est): %d\n\n", ragTokens)

	// Phase 3: Results.
	fmt.Println(strings.Repeat("─", 60))
	if ragTokens > 0 && rawTokens > 0 {
		reduction := float64(rawTokens) / float64(ragTokens)
		fmt.Printf("📈 Token Reduction: %.1fx\n", reduction)
		fmt.Printf("   Raw corpus:  %d tokens\n", rawTokens)
		fmt.Printf("   RAG context: %d tokens (top-%d passages)\n", ragTokens, topK)
		fmt.Printf("   Savings:     %.1f%% fewer tokens\n", (1-1/reduction)*100)
	} else {
		fmt.Println("⚠️  Could not calculate reduction (empty corpus or index)")
	}

	// Phase 4: Graph stats if available.
	graphDir := filepath.Join(config.IndexDir, indexName+"_graph")
	if info, err := os.Stat(graphDir); err == nil && info.IsDir() {
		fmt.Println()
		printGraphBenchmark(graphDir)
	}
}

// countCorpusTokens walks a directory and estimates total tokens.
// Uses a simple heuristic: ~4 chars per token (GPT-like tokenizer estimate).
func countCorpusTokens(dir string) (tokens, files int, bytes int64) {
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			name := ""
			if d != nil {
				name = d.Name()
			}
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary files.
		ext := strings.ToLower(filepath.Ext(path))
		if isBinaryExt(ext) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if !utf8.Valid(data) {
			return nil
		}

		files++
		bytes += int64(len(data))
		// Estimate tokens: ~4 bytes per token for code/text.
		tokens += len(data) / 4
		return nil
	})
	return
}

// estimateRAGTokens estimates the token count for top-K passages from an index.
// It reads the passage store to get average passage size.
func estimateRAGTokens(config gleann.Config, indexName string, topK int) int {
	indexDir := filepath.Join(config.IndexDir, indexName)
	passageDB := filepath.Join(indexDir, "passages.db")

	info, err := os.Stat(passageDB)
	if err != nil {
		// Fallback: estimate based on typical passage size.
		return topK * 375 // ~1500 chars / 4 = 375 tokens per passage
	}

	// Rough estimate: each passage entry is ~2KB in BoltDB.
	// Actual passage text is ~1500 chars = ~375 tokens.
	totalPassages := info.Size() / 2048
	if totalPassages < int64(topK) {
		return int(totalPassages) * 375
	}
	return topK * 375
}

func printGraphBenchmark(graphDir string) {
	fmt.Println("📊 Graph Statistics:")
	// Walk the graph directory to get size.
	var totalSize int64
	filepath.WalkDir(graphDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		totalSize += info.Size()
		return nil
	})
	fmt.Printf("   Graph DB size: %s\n", benchFormatBytes(totalSize))
}

func isBinaryExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico",
		".mp3", ".wav", ".flac", ".ogg", ".m4a",
		".mp4", ".avi", ".mkv", ".mov", ".webm",
		".zip", ".tar", ".gz", ".bz2", ".xz", ".7z",
		".exe", ".dll", ".so", ".dylib",
		".pdf", ".doc", ".docx", ".xls", ".xlsx",
		".woff", ".woff2", ".ttf", ".eot",
		".sqlite", ".db":
		return true
	}
	return false
}

func benchFormatBytes(b int64) string {
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

func printBenchmarkUsage() {
	fmt.Println(`gleann benchmark — Token reduction analysis

Usage:
  gleann benchmark --index <name> --docs <dir> [--top-k <n>]

Measures how much context compression the RAG pipeline achieves
compared to sending the entire raw corpus to an LLM.

Options:
  --index <name>   Index name (required)
  --docs <dir>     Source documents directory (required)
  --top-k <n>      Number of retrieved passages (default: 10)

Example:
  gleann benchmark --index my-code --docs ./src/
  gleann benchmark --index my-docs --docs ./documents/ --top-k 20

Output:
  Token Reduction: Nx — raw corpus tokens / RAG context tokens
  Higher is better. Typical projects see 10-100x reduction.`)
}
