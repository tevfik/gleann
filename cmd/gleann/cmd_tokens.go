package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/tevfik/gleann/pkg/gleann"
)

func cmdTokens(args []string) {
	if len(args) < 1 {
		printTokensUsage()
		os.Exit(1)
	}

	path := args[0]
	absPath, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		os.Exit(1)
	}

	_, err = os.Stat(absPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("📊 Token Estimation for: %s\n", path)
	fmt.Println(strings.Repeat("─", 60))

	modes := []gleann.ReadMode{
		gleann.ReadModeFull,
		gleann.ReadModeAggressive,
		gleann.ReadModeSignatures,
		gleann.ReadModeMap,
		gleann.ReadModeEntropy,
	}

	// Calculate counts
	counts := make(map[gleann.ReadMode]int)
	fileCount := 0

	err = filepath.WalkDir(absPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binaries
		ext := strings.ToLower(filepath.Ext(p))
		if isLocalBinaryExt(ext) {
			return nil
		}

		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if !utf8.Valid(data) {
			return nil
		}

		fileCount++

		for _, mode := range modes {
			opt := gleann.ReadModeOptions{Mode: mode}
			res, err := gleann.ReadFileWithMode(p, opt)
			if err != nil {
				// Fallback to characters length / 4 if read mode error
				counts[mode] += len(data) / 4
				continue
			}
			// Est tokens: 4 characters per token
			counts[mode] += len(res) / 4
		}

		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking directory: %v\n", err)
		os.Exit(1)
	}

	if fileCount == 0 {
		fmt.Println("No readable text files found.")
		return
	}

	fmt.Printf("Scanned %d file(s)\n\n", fileCount)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "Read Mode\tTokens (est)\tSavings\tCompression")
	fmt.Fprintln(w, "─────────\t────────────\t───────\t───────────")

	baseTokens := counts[gleann.ReadModeFull]
	if baseTokens == 0 {
		baseTokens = 1
	}

	for _, mode := range modes {
		tokens := counts[mode]
		savings := (1 - float64(tokens)/float64(baseTokens)) * 100
		compression := float64(baseTokens) / float64(tokens)
		if tokens == 0 {
			compression = 1.0
		}
		fmt.Fprintf(w, "%s\t%d\t%.1f%%\t%.1fx\n", mode, tokens, savings, compression)
	}
	w.Flush()
}

func isLocalBinaryExt(ext string) bool {
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

func printTokensUsage() {
	fmt.Println(`gleann tokens — Token consumption comparison

Usage:
  gleann tokens <file-or-dir>

Estimates how many LLM tokens a file or directory will consume
under different smart read modes.

Example:
  gleann tokens pkg/gleann/readmodes.go
  gleann tokens ./internal/tui/`)
}
