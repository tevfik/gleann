package indexing

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/tevfik/gleann/internal/graph/indexer"
	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// syncExact gives the Go files of one incremental sync exact go/types
// resolution. A full build type-checks the whole module once; a sync loads
// only the packages that contain the files it re-extracts (the changed files,
// then the graph_dirty ones), with indexer.LoadExactDirs, plus the packages
// that declare the interfaces the graph says types of those directories
// implement (interfaceDirs), so a method-set IMPLEMENTS edge is re-judged
// instead of carried blind. A directory triggers at most one new load per
// sync; a second load (the dirty pass reaching a package the first did not
// cover) re-type-checks the earlier directories too, so its resolver judges
// every interface the earlier ones judged.
//
// It is best-effort like the full build's resolver: with GLEANN_GRAPH_EXACT
// off, outside a Go module, without a go toolchain or when a package does not
// type-check, the affected files keep the syntactic resolver and the sync
// goes on; a failed load and a package with errors are reported as warnings.
type syncExact struct {
	module, root string
	modules      *indexer.ModuleIndex
	g            *graphstore.SQLite // stored IMPLEMENTS edges feed interfaceDirs; nil skips the extension
	p            *printer
	enabled      bool
	loaded       map[string]bool // package directories a load was attempted for
	failedFiles  map[string]bool // files of packages that did not type-check (retry when fixed)
	resolvers    []*indexer.ExactResolver
}

func newSyncExact(g *graphstore.SQLite, module, root string, p *printer) *syncExact {
	s := &syncExact{g: g, module: module, root: root, p: p, enabled: graphExactEnabled(), loaded: make(map[string]bool), failedFiles: make(map[string]bool)}
	mods, err := indexer.DiscoverModules(root)
	if err != nil {
		p.Warnf("warning: discovering Go modules: %v\n", err)
	}
	s.modules = mods
	return s
}

// exactCandidate reports whether a file can be resolved exactly at all: any Go
// file of a package, its _test.go files included, because the load type-checks
// each package with Tests: true. A sync therefore loads the directory of a
// changed test file and re-derives the same exact edges a full build stores;
// a document or another language never starts a go/packages load. A .go file
// inside testdata, vendor, a hidden or a "_"-prefixed directory is no package
// of the tree either (indexer.GoPathIgnored), so a non-Go project with Go
// fixtures there neither loads nor warns on the moduleless-Go tree.
func exactCandidate(rel string) bool {
	return strings.HasSuffix(rel, ".go") && !indexer.GoPathIgnored(rel)
}

// load type-checks the packages of relFiles (slash paths relative to the
// root) that no earlier load of this sync covered, together with the packages
// declaring the interfaces their stored IMPLEMENTS edges point at. A load
// that fails as a whole turns exact resolution off for the rest of the sync:
// a second attempt would fail the same way. A second load re-type-checks the
// earlier directories too (a superset), so the newest resolver judges every
// interface an earlier one judged; resolverFor prefers it.
func (s *syncExact) load(ctx context.Context, relFiles []string) error {
	if s == nil || !s.enabled {
		return nil
	}
	var dirs []string
	for _, rel := range relFiles {
		if !exactCandidate(rel) {
			continue
		}
		dir := path.Dir(rel)
		if s.loaded[dir] {
			continue
		}
		s.loaded[dir] = true
		dirs = append(dirs, dir)
	}
	if len(dirs) == 0 {
		return nil
	}
	if s.g != nil {
		extra, err := s.interfaceDirs(dirs)
		if err != nil {
			return err
		}
		for _, dir := range extra {
			if !s.loaded[dir] {
				s.loaded[dir] = true
				dirs = append(dirs, dir)
			}
		}
	}
	all := make([]string, 0, len(s.loaded))
	for dir := range s.loaded {
		all = append(all, dir)
	}
	sort.Strings(all)
	if s.modules.Empty() {
		// Go files were re-extracted but no go.mod/go.work was discovered
		// under the root: say why those files keep the syntactic resolver.
		// A moduleless tree is synced file by file, so the same warning on
		// every pass is noise: it is printed once per index (v1.22 E4) and
		// re-armed by the branches below and by a full rebuild. A failed flag
		// write never hides the warning.
		first, err := s.g.MarkMetaOnce(ctx, store.MetaGraphNoGoWarned)
		if err != nil || first {
			s.p.Warnf("warning: found Go file(s) but no Go module (no go.mod or go.work) under %s, so calls through a receiver, variable or interface are missing from the synced files\n", s.root)
		}
		s.enabled = false
		return nil
	}
	// A module was found after all: re-arm the warning for the next time the
	// tree loses it. Best-effort — the flag only tunes a warning.
	if s.g != nil {
		_ = s.g.ClearMeta(ctx, store.MetaGraphNoGoWarned)
	}
	r, err := indexer.LoadExactDirsModules(s.modules, s.module, all)
	if err != nil {
		// No package type-checked: the error still names the files of the
		// broken packages, so the sync can retry them once they are fixed.
		var le *indexer.LoadError
		if errors.As(err, &le) {
			for _, f := range le.FailedFiles {
				s.failedFiles[f] = true
			}
		}
		if r == nil {
			s.enabled = false
			s.p.Warnf("warning: exact Go call resolution is unavailable for this sync, so calls through a receiver, variable or interface are missing from the synced files: %v\n", err)
			return nil
		}
		s.p.Warnf("warning: exact Go call resolution is unavailable for some Go modules of this sync; their files keep the syntactic resolver: %v\n", err)
	}
	if r == nil {
		return nil // no module holds a loaded directory
	}
	if n, first := r.Skipped(); n > 0 {
		s.p.Warnf("warning: %d Go package(s) of this sync did not type-check, so calls through a receiver, variable or interface are missing from their synced files: %s\n", n, first)
	}
	for _, f := range r.FailedFiles() {
		s.failedFiles[f] = true
	}
	s.resolvers = append(s.resolvers, r)
	return nil
}

// failed reports whether rel keeps the syntactic resolver because its package
// did not type-check (as opposed to a file build tags exclude, whose package
// type-checked without it). Such a file is retried by the next syncs until its
// package is fixed (reviews Q4, G5).
func (s *syncExact) failed(rel string) bool {
	return s != nil && s.failedFiles[rel]
}

// interfaceDirs returns the directories of the declarations of every
// interface the graph holds an exact internal IMPLEMENTS edge to from a type
// declared in one of dirs. It reads the edges of ALL files of those
// directories, not only the re-extracted ones: an IMPLEMENTS edge is bucketed
// under its implementor's declaration file, which may be an unchanged sibling
// of the changed file (a methods-only file edited or deleted). Loading those
// directories lets the partial load judge the edges instead of carrying them
// over without re-validation (a method removed while the interface lives in
// an importing package must drop the edge, as a full build does).
func (s *syncExact) interfaceDirs(dirs []string) ([]string, error) {
	var ifaces []string
	seenIface := make(map[string]bool)
	for _, dir := range dirs {
		files, err := s.g.FilesInDir(dir)
		if err != nil {
			return nil, fmt.Errorf("listing graph files of %s: %w", dir, err)
		}
		for _, f := range files {
			edges, err := s.g.FileEdges(f, "IMPLEMENTS")
			if err != nil {
				return nil, fmt.Errorf("reading stored IMPLEMENTS edges of %s: %w", f, err)
			}
			for _, e := range edges {
				if e.Confidence != "exact" || e.DstExternal || seenIface[e.Dst] {
					continue
				}
				seenIface[e.Dst] = true
				ifaces = append(ifaces, e.Dst)
			}
		}
	}
	if len(ifaces) == 0 {
		return nil, nil
	}
	owners, err := s.g.DeclaredFQNs(ifaces)
	if err != nil {
		return nil, fmt.Errorf("locating interface declarations: %w", err)
	}
	var out []string
	seenDir := make(map[string]bool)
	for _, file := range owners {
		dir := path.Dir(file)
		if indexer.GoDirIgnored(dir) {
			continue // a fixture inside testdata/vendor/hidden/"_": no package
		}
		if !seenDir[dir] {
			seenDir[dir] = true
			out = append(out, dir)
		}
	}
	return out, nil
}

// resolverFor returns the resolver that type-checked rel, or nil when the
// file keeps the syntactic resolver. The newest resolver wins: a later load
// covers every directory an earlier one did and judges at least as many
// interfaces.
func (s *syncExact) resolverFor(rel string) *indexer.ExactResolver {
	if s == nil {
		return nil
	}
	for i := len(s.resolvers) - 1; i >= 0; i-- {
		if s.resolvers[i].HasFile(rel) {
			return s.resolvers[i]
		}
	}
	return nil
}

// carryImplements returns the method-set IMPLEMENTS edges of rel that are in
// the graph and that the partial load behind r did not re-derive: the ones to
// an interface it never saw. The load is extended to the declaration packages
// of the stored edges' interfaces (interfaceDirs), so what remains unjudged
// is chiefly an interface of a package that did not type-check. Dropping
// those edges would lose them until the next full build; they are carried as
// they are, so one whose truth the edit changed is corrected only by a full
// build. The delta still drops an edge whose type left this file or whose
// interface is no longer declared.
func carryImplements(g *graphstore.SQLite, rel string, r *indexer.ExactResolver) ([]gtypes.EdgeImplements, error) {
	if !r.Partial() || !r.HasFile(rel) {
		return nil, nil
	}
	stored, err := g.FileEdges(rel, "IMPLEMENTS")
	if err != nil {
		return nil, fmt.Errorf("reading stored IMPLEMENTS edges of %s: %w", rel, err)
	}
	var out []gtypes.EdgeImplements
	for _, e := range stored {
		if e.Confidence != "exact" || e.DstExternal || r.JudgedInterface(e.Dst) {
			continue
		}
		out = append(out, gtypes.EdgeImplements{ImplFQN: e.Src, IfaceFQN: e.Dst, Confidence: "exact"})
	}
	return out, nil
}
