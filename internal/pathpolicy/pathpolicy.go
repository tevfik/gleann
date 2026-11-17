// Package pathpolicy decides whether a client-supplied path may be read or
// indexed. A path is resolved (made absolute, cleaned, symlinks followed on
// its longest existing prefix) and must then lie inside one of a set of roots.
package pathpolicy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrOutside is returned when a path resolves outside every allowed root.
var ErrOutside = errors.New("path is outside the allowed roots")

// Resolve returns the absolute, cleaned form of p with symlinks resolved. The
// longest existing prefix is passed through EvalSymlinks and the remaining
// (not yet existing) components are appended, so a path that does not exist
// yet still cannot escape through a symlinked parent.
func Resolve(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	if strings.ContainsRune(p, 0) {
		return "", errors.New("path contains a NUL byte")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("making path absolute: %w", err)
	}
	abs = filepath.Clean(abs)

	existing := abs
	var rest []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return abs, nil // nothing exists, not even the root
		}
		rest = append([]string{filepath.Base(existing)}, rest...)
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("resolving symlinks: %w", err)
	}
	return filepath.Join(append([]string{real}, rest...)...), nil
}

// Within reports whether the resolved path p is root itself or below it. Both
// must already be absolute and cleaned. On Windows the comparison ignores
// case and compares the volume name separately.
func Within(root, p string) bool {
	if runtime.GOOS == "windows" {
		root, p = strings.ToLower(root), strings.ToLower(p)
	}
	if filepath.VolumeName(root) != filepath.VolumeName(p) {
		return false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || filepath.IsLocal(rel)
}

// Contain resolves p and checks that it lies inside one of roots. Each root is
// resolved the same way, so a symlinked root works. It returns the resolved
// path. With no usable root nothing is allowed.
func Contain(p string, roots []string) (string, error) {
	resolved, err := Resolve(p)
	if err != nil {
		return "", err
	}
	for _, r := range roots {
		if r == "" {
			continue
		}
		rr, err := Resolve(r)
		if err != nil {
			continue
		}
		if Within(rr, resolved) {
			return resolved, nil
		}
	}
	return "", ErrOutside
}

// DefaultRoots returns configured when it is not empty, otherwise the user's
// home directory.
func DefaultRoots(configured []string) []string {
	var out []string
	for _, r := range configured {
		if strings.TrimSpace(r) != "" {
			out = append(out, r)
		}
	}
	if len(out) > 0 {
		return out
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return []string{home}
	}
	return nil
}
