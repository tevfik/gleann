// Package gleann provides the MarkItDown CLI wrapper for Go-native document extraction.
//
// This is "Layer 0" of the document extraction stack:
//   - Layer 0: Go calls `markitdown` CLI directly → no Python server needed
//   - Layer 1: Python plugin with Docling → higher quality PDF (optional)
//
// When markitdown is on PATH, gleann can extract text from PDF, DOCX, XLSX,
// PPTX, CSV, and image files without any Python server running.
package gleann

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// MarkItDownExtractor provides Go-native document extraction via the markitdown CLI.
// It requires `markitdown` to be installed (`pipx install markitdown`).
type MarkItDownExtractor struct {
	binaryPath string
	timeout    time.Duration
}

// markitdown supported extensions (same set as the Python plugin).
var markitdownExts = map[string]bool{
	".pdf":  true,
	".docx": true, ".doc": true,
	".xlsx": true, ".xls": true,
	".pptx": true, ".ppt": true,
	".csv":  true,
	".png":  true, ".jpg": true, ".jpeg": true,
}

// NewMarkItDownExtractor creates a new extractor, auto-detecting the markitdown binary.
// Returns nil if markitdown is not found on PATH.
func NewMarkItDownExtractor() *MarkItDownExtractor {
	path, err := FindMarkItDown()
	if err != nil {
		return nil
	}
	return &MarkItDownExtractor{
		binaryPath: path,
		timeout:    60 * time.Second,
	}
}

// FindMarkItDown locates the markitdown binary on PATH.
// Returns the absolute path or an error if not found.
func FindMarkItDown() (string, error) {
	// Check standard PATH lookup first (works on all platforms).
	candidates := []string{
		"markitdown", // PATH lookup
	}

	// Platform-specific fallback locations.
	if home, err := os.UserHomeDir(); err == nil {
		switch runtime.GOOS {
		case "windows":
			// Windows: pipx/pip install to %APPDATA%\Python\Scripts or %USERPROFILE%\.local\...
			if appData := os.Getenv("APPDATA"); appData != "" {
				candidates = append(candidates,
					filepath.Join(appData, "Python", "Scripts", "markitdown.exe"),
				)
			}
			if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
				candidates = append(candidates,
					filepath.Join(localAppData, "Programs", "Python", "Scripts", "markitdown.exe"),
				)
			}
			candidates = append(candidates,
				filepath.Join(home, ".local", "pipx", "venvs", "markitdown", "Scripts", "markitdown.exe"),
			)
		default: // linux, darwin, freebsd, ...
			candidates = append(candidates,
				filepath.Join(home, ".local", "bin", "markitdown"),
				filepath.Join(home, ".local", "pipx", "venvs", "markitdown", "bin", "markitdown"),
			)
		}
	}

	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("markitdown not found on PATH (install with: pipx install markitdown)")
}

// InstallMarkItDown attempts to install markitdown using pipx or pip.
// Returns the installed binary path.
func InstallMarkItDown() (string, error) {
	// Prefer pipx (isolated environment).
	if pipx, err := exec.LookPath("pipx"); err == nil {
		cmd := exec.Command(pipx, "install", "markitdown")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("pipx install markitdown: %w", err)
		}
		return FindMarkItDown()
	}

	// Fallback: pip install --user.
	for _, pip := range []string{"pip3", "pip"} {
		if pipPath, err := exec.LookPath(pip); err == nil {
			cmd := exec.Command(pipPath, "install", "--user", "markitdown")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				continue
			}
			return FindMarkItDown()
		}
	}

	return "", fmt.Errorf("neither pipx nor pip found — cannot auto-install markitdown")
}

// Available returns true if the markitdown binary was found.
func (m *MarkItDownExtractor) Available() bool {
	return m != nil && m.binaryPath != ""
}

// BinaryPath returns the resolved path to the markitdown binary.
func (m *MarkItDownExtractor) BinaryPath() string {
	if m == nil {
		return ""
	}
	return m.binaryPath
}

// CanHandle returns true if this extension is supported by markitdown.
func (m *MarkItDownExtractor) CanHandle(ext string) bool {
	return markitdownExts[strings.ToLower(ext)]
}

// Extract converts a file to markdown using the markitdown CLI.
// Returns the markdown string or an error.
func (m *MarkItDownExtractor) Extract(filePath string) (string, error) {
	if m == nil || m.binaryPath == "" {
		return "", fmt.Errorf("markitdown not available")
	}

	cmd := exec.Command(m.binaryPath, filePath)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("markitdown failed: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("markitdown exec: %w", err)
	}

	return string(output), nil
}

// SupportedExtensions returns the list of extensions this extractor handles.
func (m *MarkItDownExtractor) SupportedExtensions() []string {
	exts := make([]string, 0, len(markitdownExts))
	for ext := range markitdownExts {
		exts = append(exts, ext)
	}
	return exts
}
