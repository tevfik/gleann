package indexer

import (
	"path"
	"path/filepath"
	"strings"
)

// buildFQNOf constructs a Fully Qualified Name for a symbol.
// Format: <module>/<package>.<SymbolName>
func buildFQNOf(module, relPath, symbolName string) string {
	pkg := filepath.Dir(relPath)
	pkg = strings.ReplaceAll(pkg, string(filepath.Separator), "/")
	if pkg == "." {
		pkg = ""
	}

	var prefix string
	if module != "" {
		if pkg != "" {
			prefix = module + "/" + pkg
		} else {
			prefix = module
		}
	} else {
		prefix = pkg
	}

	if prefix == "" {
		return symbolName
	}
	return prefix + "." + symbolName
}

// buildPkgPrefixOf derives the FQN prefix for the package containing relPath,
// exactly as buildFQNOf does (so IMPLEMENTS/REFERENCES sources match the
// DECLARES rows): a file in the root directory gets the bare module, a path
// with either separator is split on its directory, and an empty module gives
// the bare directory.
func buildPkgPrefixOf(module, relPath string) string {
	dir := path.Dir(strings.ReplaceAll(relPath, "\\", "/"))
	if dir == "." || dir == "/" {
		dir = ""
	}
	switch {
	case module == "":
		return dir
	case dir == "":
		return module
	default:
		return module + "/" + dir
	}
}

// relPathOf converts an absolute path to a slash-separated path relative to
// root, the form the graph tables store on every OS.
func relPathOf(root, absPath string) string {
	return filepath.ToSlash(relPathNative(root, absPath))
}

func relPathNative(root, absPath string) string {
	cleanAbs := filepath.Clean(absPath)
	if rel, err := filepath.Rel(root, cleanAbs); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	// Try after resolving symlinks on both (handles e.g. /var vs /private/var on macOS)
	realRoot, err1 := filepath.EvalSymlinks(root)
	realAbs, err2 := filepath.EvalSymlinks(cleanAbs)
	if err1 == nil && err2 == nil {
		if rel, err := filepath.Rel(realRoot, realAbs); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	rel, err := filepath.Rel(root, cleanAbs)
	if err != nil {
		return absPath
	}
	return rel
}
