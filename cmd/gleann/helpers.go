package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/indexing"
)

// PluginDoc holds a plugin result for deferred graph indexing.
type PluginDoc = indexing.PluginDoc

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
