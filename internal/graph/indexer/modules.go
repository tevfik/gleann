package indexer

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/tevfik/gleann/pkg/walker"
)

// Module is one Go module discovered under an index root.
type Module struct {
	// Dir is the absolute directory holding go.mod.
	Dir string
	// Rel is the module directory relative to the index root, slash separated
	// ("" for a module whose go.mod sits at the root).
	Rel string
	// Path is the module path declared by go.mod (example.com/app).
	Path string
	// Work is the absolute go.work governing the module, or "" when the tree
	// has no workspace under the index root or none of them lists the module
	// in its use directives. A workspace lets a module import another one
	// without a require/replace line; a module a workspace does not list is
	// loaded on its own (GOWORK=off), because Go rejects "./..." there.
	Work string
}

// ModuleIndex maps source paths of one indexed tree to the Go modules that
// own them. It is the single source of truth for the FQN prefix of every Go
// file: a file of a nested submodule is prefixed with that submodule's real
// path, not with the root module path plus the nested directory.
//
// The zero value (and a nil *ModuleIndex) holds no module: callers fall back to
// the index root's directory name, as for a tree without any go.mod.
type ModuleIndex struct {
	root    string
	modules []Module // deepest module directory first
}

// Root is the absolute index root the modules were discovered under.
func (m *ModuleIndex) Root() string {
	if m == nil {
		return ""
	}
	return m.root
}

// Modules returns the discovered modules, deepest directory first. The caller
// must not modify the slice.
func (m *ModuleIndex) Modules() []Module {
	if m == nil {
		return nil
	}
	return m.modules
}

// Empty reports whether no module was discovered.
func (m *ModuleIndex) Empty() bool {
	return m == nil || len(m.modules) == 0
}

// ModuleFor returns the module owning rel (a file or directory, slash
// separated and relative to the index root). The deepest enclosing module wins,
// so a nested submodule is not attributed to its parent module. rel "." and ""
// name the index root.
func (m *ModuleIndex) ModuleFor(rel string) (Module, bool) {
	if m == nil {
		return Module{}, false
	}
	rel = cleanModuleRel(rel)
	for _, mod := range m.modules {
		if mod.Rel == "" || rel == mod.Rel || strings.HasPrefix(rel, mod.Rel+"/") {
			return mod, true
		}
	}
	return Module{}, false
}

// RelPath returns rel (relative to the index root) relative to the module
// directory. It is the path buildFQNOf/buildPkgPrefixOf expect for the module.
func (m Module) RelPath(rel string) string {
	rel = cleanModuleRel(rel)
	if m.Rel == "" || rel == "" {
		return rel
	}
	if rel == m.Rel {
		return ""
	}
	return strings.TrimPrefix(rel, m.Rel+"/")
}

// PrefixFor returns the FQN prefix of the package containing rel: the module
// path plus the file's directory inside the module.
func (m Module) PrefixFor(rel string) string {
	return buildPkgPrefixOf(m.Path, m.RelPath(rel))
}

// DiscoverModules walks root and returns every Go module under it, together
// with the go.work workspace governing each (if any). A module is governed only
// by a workspace whose use directives list the module's own directory; a
// standalone module inside a tree that also holds a workspace stays independent
// (GOWORK=off), and a workspace that lists nothing governs nothing.
// Directories the walker
// ignores (vendor, node_modules, hidden and VCS directories, .gitignore
// matches) are skipped, and so are testdata and directories starting with "_":
// the first set holds no first-party sources, the second is ignored by the Go
// tool's "./..." pattern. Nested modules are therefore discovered, unlike a
// root-only go.mod lookup, while the scan stays consistent with the code
// walker.
//
// A go.mod without a readable module line is ignored (the Go tool rejects it
// too); it does not abort discovery. The returned index is never nil.
func DiscoverModules(root string) (*ModuleIndex, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return &ModuleIndex{root: filepath.Clean(root)}, fmt.Errorf("resolving index root: %w", err)
	}
	abs = filepath.Clean(abs)
	idx := &ModuleIndex{root: abs}

	matcher := walker.NewMatcher(abs, false, nil)
	var modFiles, workFiles []string // slash paths relative to abs
	walkErr := filepath.WalkDir(abs, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir // unreadable directory: skip, keep discovering
			}
			return nil
		}
		relFS, relErr := filepath.Rel(abs, p)
		if relErr != nil {
			return nil
		}
		rel := filepath.ToSlash(relFS)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if GoDirIgnored(rel) || matcher.ShouldIgnore(rel, true) {
				return fs.SkipDir
			}
			matcher.LoadDirIgnores(p)
			return nil
		}
		switch d.Name() {
		case "go.mod":
			modFiles = append(modFiles, rel)
		case "go.work":
			workFiles = append(workFiles, rel)
		}
		return nil
	})
	if walkErr != nil {
		return idx, fmt.Errorf("walking %s for Go modules: %w", root, walkErr)
	}

	workspaces := make([]workspace, 0, len(workFiles))
	for _, rel := range workFiles {
		absWork := filepath.Join(abs, filepath.FromSlash(rel))
		workspaces = append(workspaces, workspace{
			file: absWork,
			dir:  filepath.Dir(absWork),
			uses: workspaceUses(absWork),
		})
	}

	for _, rel := range modFiles {
		modPath, ok := readModulePath(filepath.Join(abs, filepath.FromSlash(rel)))
		if !ok {
			continue
		}
		dirRel := path.Dir(rel)
		if dirRel == "." {
			dirRel = ""
		}
		modDir := filepath.Join(abs, filepath.FromSlash(dirRel))
		idx.modules = append(idx.modules, Module{
			Dir:  modDir,
			Rel:  dirRel,
			Path: modPath,
			Work: nearestWorkspace(modDir, workspaces),
		})
	}
	sort.Slice(idx.modules, func(i, j int) bool {
		li, lj := len(idx.modules[i].Rel), len(idx.modules[j].Rel)
		if li != lj {
			return li > lj // deepest first
		}
		return idx.modules[i].Rel < idx.modules[j].Rel
	})
	return idx, nil
}

// GoDirIgnored reports whether rel (slash separated and relative to an index
// root) names a directory the Go tool excludes from "./..." beyond the walker's
// ignore rules: testdata, vendor, a hidden (dot) directory, or one starting with
// "_". DiscoverModules prunes those directories and GoPathIgnored applies the
// same rule to a single file, so module discovery, the exact load and the
// moduleless-Go-tree warning agree on which files are part of the tree's
// packages.
func GoDirIgnored(rel string) bool {
	for _, elem := range strings.Split(cleanModuleRel(rel), "/") {
		if elem == "testdata" || elem == "vendor" || strings.HasPrefix(elem, "_") || strings.HasPrefix(elem, ".") {
			return true
		}
	}
	return false
}

// GoPathIgnored reports whether rel (slash separated and relative to an index
// root) is a Go source path the Go tool excludes from its package patterns:
// one whose directory is testdata, vendor, a hidden directory or one starting
// with "_", and one whose own base name starts with "_" or "." (the Go tool
// ignores both prefixes in file names too: "_gen.go" and ".gen.go" belong to no
// package, and a directory holding only such files matches no package). Such a
// file must not count as Go source (no moduleless warning) and must not start
// an exact load.
func GoPathIgnored(rel string) bool {
	rel = cleanModuleRel(rel)
	if rel == "" {
		return false
	}
	if base := path.Base(rel); strings.HasPrefix(base, "_") || strings.HasPrefix(base, ".") {
		return true
	}
	return GoDirIgnored(path.Dir(rel))
}

// workspace is one go.work file found under the index root, with the module
// directories its use directives list.
type workspace struct {
	file string          // absolute go.work path
	dir  string          // absolute directory holding go.work
	uses map[string]bool // moduleDirKey of every module the workspace uses
}

// nearestWorkspace returns the absolute go.work governing the module at
// modDir. Membership in a workspace's use list is the only condition Go
// enforces: a module a workspace does not list loads on its own (GOWORK=off),
// because Go rejects "./..." there ("directory prefix . does not contain
// modules listed in go.work"), while a listed module is governed even when the
// workspace file sits outside the module's own directory (a go.work in
// build/ or sub/ that lists "../a" is honoured by Go). An empty, comment-only
// or malformed go.work lists nothing and governs nothing.
//
// Among the workspaces that list modDir, a workspace whose directory contains
// modDir wins -- the deepest of those, matching how the go command finds
// go.work by walking up from the module. If none contains the module, the
// workspace nearest it in the tree wins (longest shared directory prefix, then
// deeper directory, then path order), so the choice is deterministic for
// sibling workspaces that both list the module.
func nearestWorkspace(modDir string, workspaces []workspace) string {
	key := moduleDirKey(modDir)
	var best *workspace
	var bestRank workspaceRank
	for i := range workspaces {
		w := &workspaces[i]
		if !w.uses[key] {
			continue // the workspace does not list this module
		}
		rank := rankWorkspace(w.dir, modDir)
		if best == nil || rank.betterThan(bestRank) {
			best, bestRank = w, rank
		}
	}
	if best == nil {
		return ""
	}
	return best.file
}

// workspaceRank orders the workspaces that list one module: a containing
// workspace first, then the longest shared directory prefix with the module,
// then the deeper directory, then path order.
type workspaceRank struct {
	contains bool
	prefix   int
	depth    int
	dir      string
}

// rankWorkspace computes the rank of a workspace directory for modDir.
func rankWorkspace(dir, modDir string) workspaceRank {
	d := moduleDirKey(dir)
	return workspaceRank{
		contains: dirContains(dir, modDir),
		prefix:   commonDirPrefix(d, moduleDirKey(modDir)),
		depth:    len(d),
		dir:      d,
	}
}

// betterThan reports whether r ranks before o.
func (r workspaceRank) betterThan(o workspaceRank) bool {
	if r.contains != o.contains {
		return r.contains
	}
	if r.prefix != o.prefix {
		return r.prefix > o.prefix
	}
	if r.depth != o.depth {
		return r.depth > o.depth
	}
	return r.dir < o.dir
}

// commonDirPrefix returns the length of the longest common prefix of two
// absolute, slash-separated, cleaned paths, measured on whole path elements.
func commonDirPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	if n == 0 {
		return 0
	}
	if n == len(a) && n == len(b) {
		return n
	}
	if (n == len(a) && (a[n-1] == '/' || b[n] == '/')) || (n == len(b) && (b[n-1] == '/' || a[n] == '/')) {
		return n // one path is an element-aligned prefix of the other
	}
	if i := strings.LastIndexByte(a[:n], '/'); i >= 0 {
		return i
	}
	return 0
}

// dirContains reports whether dir is target or one of its ancestors.
func dirContains(dir, target string) bool {
	if moduleDirKey(dir) == moduleDirKey(target) {
		return true
	}
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// readModulePath reads the module path from a go.mod file. It reports false
// when the file cannot be read or holds no module directive.
func readModulePath(goMod string) (string, bool) {
	f, err := os.Open(goMod)
	if err != nil {
		return "", false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return strings.Trim(fields[1], "\"`"), true
		}
	}
	return "", false
}

// moduleDirKey normalises a directory for workspace membership comparison: an
// absolute, cleaned, slash-separated path, so a go.work "use ./a" and the
// module directory it names compare equal on every platform.
func moduleDirKey(dir string) string {
	return filepath.ToSlash(filepath.Clean(dir))
}

// workspaceUses returns the module directories a go.work file lists in its use
// directives, keyed by moduleDirKey and resolved against the directory holding
// the file. The file is parsed with the Go tool's own go.work parser
// (golang.org/x/mod/modfile), so the grammar matches "go work edit -json"
// exactly: the single-line form (use ./a), the block form (use ( ... )) and
// interpreted strings ("./a") are read, an unquoted token may contain a
// backslash, and // line comments are ignored -- while a raw (backtick) string
// in use, an inline block, two paths on one line or an unterminated string make
// Go reject the whole file. Such a malformed file, like an unreadable, empty or
// comment-only one, yields an empty set: it lists no module and therefore
// governs none.
func workspaceUses(goWork string) map[string]bool {
	uses := make(map[string]bool)
	data, err := os.ReadFile(goWork)
	if err != nil {
		return uses
	}
	wf, err := modfile.ParseWork(goWork, data, nil)
	if err != nil {
		return uses
	}
	base := filepath.Dir(goWork)
	for _, u := range wf.Use {
		uses[moduleDirKey(joinUseDir(base, u.Path))] = true
	}
	return uses
}

// joinUseDir resolves a use directive's directory against the directory
// holding go.work; an absolute path is kept as it is.
func joinUseDir(base, dir string) string {
	dir = filepath.FromSlash(dir)
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(base, dir)
}

// cleanModuleRel normalises a slash-separated path relative to an index root:
// "." and "./x" become "" and "x", and native separators are converted.
func cleanModuleRel(rel string) string {
	rel = filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	rel = strings.TrimPrefix(rel, "./")
	if rel == "." || rel == "/" {
		return ""
	}
	return strings.Trim(rel, "/")
}
