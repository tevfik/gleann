// Package gleannignore implements gitignore-style pattern matching for
// excluding files during index builds. Reads patterns from .gleannignore
// files in the document root directory.
package gleannignore

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Matcher holds compiled ignore patterns from a .gleannignore file.
type Matcher struct {
	patterns []pattern
}

type pattern struct {
	raw    string // original pattern text
	negate bool   // starts with !
	dirOny bool   // ends with /
	glob   string // cleaned glob pattern
}

// Load reads a .gleannignore file from the given directory.
// Returns an empty Matcher (matches nothing) if no file exists.
func Load(dir string) *Matcher {
	path := filepath.Join(dir, ".gleannignore")
	f, err := os.Open(path)
	if err != nil {
		return &Matcher{}
	}
	defer f.Close()

	var patterns []pattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		p := pattern{raw: line}

		// Handle negation.
		if strings.HasPrefix(line, "!") {
			p.negate = true
			line = line[1:]
		}

		// Handle directory-only patterns.
		if strings.HasSuffix(line, "/") {
			p.dirOny = true
			line = strings.TrimSuffix(line, "/")
		}

		p.glob = line
		patterns = append(patterns, p)
	}

	return &Matcher{patterns: patterns}
}

// Match checks if a path (relative to the .gleannignore root) should be ignored.
// isDir indicates whether the path is a directory.
func (m *Matcher) Match(relPath string, isDir bool) bool {
	if len(m.patterns) == 0 {
		return false
	}

	// Normalize path separators.
	relPath = filepath.ToSlash(relPath)

	ignored := false
	for _, p := range m.patterns {
		matched := matchPattern(p.glob, relPath, isDir)
		if !matched {
			continue
		}

		// Directory-only patterns only match directories.
		if p.dirOny && !isDir {
			continue
		}

		if p.negate {
			ignored = false
		} else {
			ignored = true
		}
	}

	return ignored
}

// matchPattern performs gitignore-style glob matching.
func matchPattern(pat, path string, isDir bool) bool {
	// If pattern contains no slash, match against the basename.
	if !strings.Contains(pat, "/") {
		base := filepath.Base(path)
		if ok, _ := filepath.Match(pat, base); ok {
			return true
		}
		// Also try matching against each path component for directory patterns.
		parts := strings.Split(path, "/")
		for _, part := range parts {
			if ok, _ := filepath.Match(pat, part); ok {
				return true
			}
		}
		return false
	}

	// Pattern contains slash — match against the full relative path.
	// Strip leading / if present (anchored to root).
	pat = strings.TrimPrefix(pat, "/")

	// Handle ** (match any depth).
	if strings.Contains(pat, "**") {
		return matchDoublestar(pat, path)
	}

	// Simple full-path match.
	if ok, _ := filepath.Match(pat, path); ok {
		return true
	}

	// Try matching as a prefix (for directories).
	if isDir {
		if ok, _ := filepath.Match(pat, path+"/"); ok {
			return true
		}
	}

	return false
}

// matchDoublestar handles ** patterns by splitting and matching recursively.
func matchDoublestar(pat, path string) bool {
	parts := strings.Split(pat, "**")
	if len(parts) != 2 {
		// Multiple ** — fall back to simple prefix check.
		return strings.Contains(path, strings.ReplaceAll(pat, "**", ""))
	}

	prefix := strings.TrimSuffix(parts[0], "/")
	suffix := strings.TrimPrefix(parts[1], "/")

	if prefix != "" && !strings.HasPrefix(path, prefix) {
		return false
	}

	if suffix == "" {
		return true
	}

	// Check if any sub-path matches the suffix.
	pathParts := strings.Split(path, "/")
	for i := range pathParts {
		subPath := strings.Join(pathParts[i:], "/")
		if ok, _ := filepath.Match(suffix, subPath); ok {
			return true
		}
		// Also try matching just the filename against suffix.
		if ok, _ := filepath.Match(suffix, pathParts[len(pathParts)-1]); ok {
			return true
		}
	}

	return false
}

// HasFile checks if a .gleannignore file exists in the given directory.
func HasFile(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".gleannignore"))
	return err == nil
}
