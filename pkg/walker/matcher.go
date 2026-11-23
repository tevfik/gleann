package walker

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// defaultIgnoredDirs are common dependency, build, and IDE directories always ignored.
var defaultIgnoredDirs = map[string]bool{
	".git":         true,
	".svn":         true,
	".hg":          true,
	".bzr":         true,
	".idea":        true,
	".vscode":      true,
	".gemini":      true,
	".next":        true,
	".nuxt":        true,
	".venv":        true,
	"venv":         true,
	"env":          true,
	"__pycache__":  true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".gradle":      true,
	"Pods":         true,
}

// scopedPattern represents an ignore pattern tied to the directory where its file was found.
type scopedPattern struct {
	scopeDir string // relative to root with forward slashes (e.g. "" or "sub/dir")
	raw      string
	negate   bool
	dirOnly  bool
	glob     string

	// Derived from scopeDir and glob by addPattern, so a walk does not
	// recompute them for every path (see compile). The zero values mean "not
	// derived": matchScopedPattern then works from scopeDir and glob alone.
	scopePrefix string   // scopeDir + "/"
	nameKind    nameKind // how a glob without a slash matches one path segment
	nameLit     string   // the literal part of the glob for nameKind != nameGlob
}

// nameKind is the shape of a glob without a slash. Such a glob is tried on
// every segment of every path of a walk, and most ignore patterns are a plain
// name ("node_modules"), a suffix ("*.log") or a prefix ("coverage*"), which
// need no pattern matching at all.
type nameKind uint8

const (
	nameGlob    nameKind = iota // anything else: filepath.Match
	nameLiteral                 // no metacharacter: the segment equals the glob
	nameSuffix                  // "*" + literal: the segment ends with the literal
	namePrefix                  // literal + "*": the segment starts with the literal
)

// globMeta are the characters filepath.Match gives a meaning to. The
// backslash is one of them everywhere but on Windows; a glob holding one is
// left to filepath.Match on every platform.
const globMeta = `*?[\`

// compile derives the fields that depend only on the pattern.
func (p *scopedPattern) compile() {
	if p.scopeDir != "" {
		p.scopePrefix = p.scopeDir + "/"
	}
	p.nameKind, p.nameLit = nameGlob, ""
	g := p.glob
	if strings.Contains(g, "/") {
		return // matched against the whole path, not per segment
	}
	switch {
	case !strings.ContainsAny(g, globMeta):
		p.nameKind, p.nameLit = nameLiteral, g
	case g[0] == '*' && !strings.ContainsAny(g[1:], globMeta):
		p.nameKind, p.nameLit = nameSuffix, g[1:]
	case g[len(g)-1] == '*' && !strings.ContainsAny(g[:len(g)-1], globMeta):
		p.nameKind, p.nameLit = namePrefix, g[:len(g)-1]
	}
}

// matchName reports whether a glob without a slash matches one path segment
// (a name without a separator): filepath.Match(p.glob, name) without its
// error. For such a name "*lit" matches exactly the names ending in lit and
// "lit*" those starting with it, because "*" matches any run of
// non-separator characters, the empty one included.
func (p *scopedPattern) matchName(name string) bool {
	switch p.nameKind {
	case nameLiteral:
		return name == p.nameLit
	case nameSuffix:
		return strings.HasSuffix(name, p.nameLit)
	case namePrefix:
		return strings.HasPrefix(name, p.nameLit)
	}
	ok, _ := filepath.Match(p.glob, name)
	return ok
}

// Matcher evaluates ignore rules across root and nested ignore files.
type Matcher struct {
	root              string
	includeSubmodules bool
	submodules        map[string]bool
	patterns          []scopedPattern
	loadedDirs        map[string]bool
}

// NewMatcher creates a Matcher initialized with default ignores, .gitmodules,
// .git/info/exclude, and root .gitignore / .gleannignore.
func NewMatcher(root string, includeSubmodules bool, extraIgnores []string) *Matcher {
	root = filepath.Clean(root)
	m := &Matcher{
		root:              root,
		includeSubmodules: includeSubmodules,
		submodules:        make(map[string]bool),
		loadedDirs:        make(map[string]bool),
	}

	// 1. Parse .gitmodules if submodules should be excluded
	if !includeSubmodules {
		gitmodulesPath := filepath.Join(root, ".gitmodules")
		if subs, err := ParseGitmodules(gitmodulesPath); err == nil {
			for _, sub := range subs {
				m.submodules[filepath.ToSlash(sub)] = true
			}
		}
	}

	// 2. Load .git/info/exclude
	gitExcludePath := filepath.Join(root, ".git", "info", "exclude")
	m.loadIgnoreFile(gitExcludePath, "")

	// 3. Load root ignore files
	m.LoadDirIgnores(root)

	// 4. Add extra pattern strings
	for _, raw := range extraIgnores {
		m.addPattern("", raw)
	}

	return m
}

// LoadDirIgnores checks for and loads .gitignore and .gleannignore in the given directory.
// absDir must be an absolute path or a path relative to the process working dir.
func (m *Matcher) LoadDirIgnores(absDir string) {
	relDir, err := filepath.Rel(m.root, absDir)
	if err != nil {
		return
	}
	relDir = filepath.ToSlash(relDir)
	if relDir == "." {
		relDir = ""
	}

	if m.loadedDirs[relDir] {
		return
	}
	m.loadedDirs[relDir] = true

	// Load .gitignore first, then .gleannignore (which takes higher priority if conflicting)
	m.loadIgnoreFile(filepath.Join(absDir, ".gitignore"), relDir)
	m.loadIgnoreFile(filepath.Join(absDir, ".gleannignore"), relDir)
}

func (m *Matcher) loadIgnoreFile(filePath, scopeDir string) {
	f, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		m.addPattern(scopeDir, line)
	}
}

func (m *Matcher) addPattern(scopeDir, line string) {
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
		return
	}

	p := scopedPattern{
		scopeDir: scopeDir,
		raw:      line,
	}

	if strings.HasPrefix(line, "!") {
		p.negate = true
		line = line[1:]
	}

	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}

	p.glob = filepath.ToSlash(line)
	p.compile()
	m.patterns = append(m.patterns, p)
}

// EnsureDirIgnoresLoaded ensures that all ignore files from root down to relPath are loaded.
func (m *Matcher) EnsureDirIgnoresLoaded(relPath string) {
	dir := relPath
	// If it has an extension or is a file path, get its directory
	if strings.Contains(filepath.Base(relPath), ".") || strings.Contains(relPath, "/") {
		dir = filepath.Dir(relPath)
	}
	if dir == "." || dir == "" || dir == "/" {
		return
	}

	parts := strings.Split(filepath.ToSlash(dir), "/")
	accum := ""
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		if accum == "" {
			accum = part
		} else {
			accum = accum + "/" + part
		}
		absSubDir := filepath.Join(m.root, filepath.FromSlash(accum))
		m.LoadDirIgnores(absSubDir)
	}
}

// ShouldIgnore tests if relPath (relative to root) should be ignored.
// isDir indicates whether relPath represents a directory.
func (m *Matcher) ShouldIgnore(relPath string, isDir bool) bool {
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if relPath == "." || relPath == "" {
		return false
	}

	m.EnsureDirIgnoresLoaded(relPath)

	base := filepath.Base(relPath)

	// 1. Built-in defaults: check all directory components in relPath
	parts := strings.Split(relPath, "/")
	checkParts := parts
	if !isDir && len(parts) > 1 {
		checkParts = parts[:len(parts)-1]
	}
	for _, part := range checkParts {
		if defaultIgnoredDirs[part] || (part != "" && strings.HasPrefix(part, ".")) {
			return true
		}
	}
	if isDir && defaultIgnoredDirs[base] {
		return true
	}
	if strings.HasPrefix(base, ".") {
		return true
	}

	// 2. Submodules check
	if !m.includeSubmodules && len(m.submodules) > 0 {
		if m.submodules[relPath] {
			return true
		}
		// Also check if relPath is inside any submodule
		for sub := range m.submodules {
			if strings.HasPrefix(relPath, sub+"/") {
				return true
			}
		}
	}

	// 3. Pattern evaluation: evaluate in order; later matching patterns override earlier ones
	ignored := false
	for _, p := range m.patterns {
		if p.dirOnly && !isDir {
			continue
		}

		if matchScopedPattern(p, relPath, isDir) {
			if p.negate {
				ignored = false
			} else {
				ignored = true
			}
		}
	}

	return ignored
}

func matchScopedPattern(p scopedPattern, relPath string, isDir bool) bool {
	// The path must be within the pattern's scope
	target := relPath
	if p.scopeDir != "" {
		scopePrefix := p.scopePrefix
		if scopePrefix == "" {
			scopePrefix = p.scopeDir + "/"
		}
		if relPath != p.scopeDir && !strings.HasPrefix(relPath, scopePrefix) {
			return false
		}
		target = strings.TrimPrefix(relPath, scopePrefix)
		if target == "" {
			// Exactly the scope directory itself
			target = filepath.Base(p.scopeDir)
		}
	}

	pat := p.glob

	// If pattern contains slash (other than trailing slash which was trimmed)
	if strings.Contains(pat, "/") {
		// Leading slash anchors to scope directory
		pat = strings.TrimPrefix(pat, "/")

		if strings.Contains(pat, "**") {
			return matchDoublestar(pat, target)
		}

		if ok, _ := filepath.Match(pat, target); ok {
			return true
		}
		if isDir {
			if ok, _ := filepath.Match(pat, target+"/"); ok {
				return true
			}
		}
		return false
	}

	// No slash: matches filename/basename anywhere in scope
	if p.nameKind != nameGlob {
		// A plain name, suffix or prefix: the same test on the same names
		// (every path segment, then the basename) without filepath.Match and
		// without splitting the path.
		rest := target
		for {
			seg, tail, more := strings.Cut(rest, "/")
			if p.matchName(seg) {
				return true
			}
			if !more {
				// The basename is the last segment, except where
				// filepath.Base strips more than directories (a volume name
				// on Windows).
				if base := filepath.Base(target); base != seg && p.matchName(base) {
					return true
				}
				return false
			}
			rest = tail
		}
	}
	base := filepath.Base(target)
	if ok, _ := filepath.Match(pat, base); ok {
		return true
	}

	// Also check if any path segment matches
	parts := strings.Split(target, "/")
	for _, part := range parts {
		if ok, _ := filepath.Match(pat, part); ok {
			return true
		}
	}

	return false
}

func matchDoublestar(pat, path string) bool {
	parts := strings.Split(pat, "**")
	if len(parts) != 2 {
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

	pathParts := strings.Split(path, "/")
	for i := range pathParts {
		subPath := strings.Join(pathParts[i:], "/")
		if ok, _ := filepath.Match(suffix, subPath); ok {
			return true
		}
		if ok, _ := filepath.Match(suffix, pathParts[len(pathParts)-1]); ok {
			return true
		}
	}

	return false
}
