package walker

import (
	"io/fs"
	"os"
	"path/filepath"
)

// Options configures the directory walking behavior.
type Options struct {
	// IncludeSubmodules controls whether Git submodules declared in .gitmodules are traversed.
	// Default is false (submodules skipped).
	IncludeSubmodules bool

	// ExtraIgnores specifies additional gitignore-style patterns to ignore.
	ExtraIgnores []string

	// FollowSymlinks specifies whether symlinked files are followed.
	// Dangling symlinks or symlinks to directories are always skipped.
	FollowSymlinks bool
}

// Walk traverses the directory tree rooted at root using common ignore rules:
// - Default ignored directories (.git, node_modules, vendor, build, dist, .venv, etc.)
// - Submodules in .gitmodules (skipped by default unless opts.IncludeSubmodules is true)
// - Root .git/info/exclude
// - Nested .gitignore and .gleannignore files discovered during traversal
// - Extra ignore patterns passed in opts.ExtraIgnores
func Walk(root string, opts Options, walkFn func(path string, d fs.DirEntry, err error) error) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	matcher := NewMatcher(absRoot, opts.IncludeSubmodules, opts.ExtraIgnores)

	return filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return walkFn(path, d, err)
		}

		relPath, relErr := filepath.Rel(absRoot, path)
		if relErr != nil {
			return relErr
		}

		if d.IsDir() {
			if path != absRoot {
				// Check if this directory should be skipped
				if matcher.ShouldIgnore(relPath, true) {
					return filepath.SkipDir
				}
				// Load any nested ignore files found in this directory
				matcher.LoadDirIgnores(path)
			}
			return nil
		}

		// Handle symlinks
		if d.Type()&os.ModeSymlink != 0 {
			if !opts.FollowSymlinks {
				return nil
			}
			target, statErr := os.Stat(path)
			if statErr != nil || target.IsDir() {
				return nil // Skip dangling symlinks and directory symlinks
			}
		}

		// Check if file should be ignored
		if matcher.ShouldIgnore(relPath, false) {
			return nil
		}

		return walkFn(path, d, nil)
	})
}

// CollectFiles walks root and returns all non-ignored file paths that satisfy the optional filter.
func CollectFiles(root string, opts Options, filter func(path string, d fs.DirEntry) bool) ([]string, error) {
	var files []string
	err := Walk(root, opts, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filter != nil && !filter(path, d) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files, err
}
