package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
)

// PluginDoc holds a plugin result for deferred graph indexing.
type PluginDoc = indexing.PluginDoc

// pluginResultToDoc converts a PluginResult to a StructuredDocument for chunking.
func pluginResultToDoc(result *gleann.PluginResult) *chunking.StructuredDocument {
	return indexing.PluginResultToDoc(result)
}

// strVal extracts a string value from a map with a zero-value fallback.
func strVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// markdownToPluginResult converts parsed MarkdownSections into a graph-ready PluginResult.
func markdownToPluginResult(sections []chunking.MarkdownSection, sourcePath string, wordCount int, rawMarkdown string) *gleann.PluginResult {
	return indexing.MarkdownSectionsToPluginResult(sections, sourcePath, wordCount, rawMarkdown)
}

// formatSize formats a byte count as a human-readable string.
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// isCodeExtension returns true if the file extension is a recognized source code format.
func isCodeExtension(ext string) bool { return indexing.IsCodeExtension(ext) }

// resolveInstalledGleannBin returns a persistent, reliable binary path for gleann,
// explicitly avoiding ephemeral extraction paths like ~/.gleann/runtime or /tmp.
func resolveInstalledGleannBin() string {
	home, _ := os.UserHomeDir()
	if _, err := os.Stat("/usr/local/bin/gleann"); err == nil {
		return "/usr/local/bin/gleann"
	} else if home != "" {
		userBin := filepath.Join(home, ".local", "bin", "gleann")
		if _, err := os.Stat(userBin); err == nil {
			return userBin
		}
	}

	if pathBin, err := exec.LookPath("gleann"); err == nil {
		return pathBin
	}

	if exe, err := os.Executable(); err == nil && !strings.Contains(exe, ".gleann/runtime") && !strings.Contains(exe, "/tmp") {
		return exe
	}

	return "gleann"
}
