package indexing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/graph/indexer"
	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/walker"
	"golang.org/x/sync/errgroup"
)

// buildGraphIndexSQLite is the format-2 graph write path (RFC 003 section 8
// step 7): the code graph lives in the symbols/edges tables of
// <IndexDir>/<name>/<name>.db and the document graph in doc_nodes/doc_explains,
// so it is built and served in every build including CGO_ENABLED=0.
//
// A full build extracts every source file and replaces the graph in one
// transaction (graphstore.ReplaceAll). An incremental sync applies one
// graphstore.FileDelta per changed file and RemoveFile per deleted file, then
// re-applies the deltas of the graph_dirty files (DirtyFiles) so references
// broken by another file's update converge (RFC 003 section 6.5).
//
// Every per-file write is atomic: on failure the file's graph_hash does not
// advance (AGENTS.md section 2.3), the file is marked graph_dirty best-effort
// and the error is returned so Build/Sync report ErrGraphUpdate.
func buildGraphIndexSQLite(opts GraphOptions, p *printer) error {
	absDocsDir, err := filepath.Abs(opts.DocsDir)
	if err != nil {
		absDocsDir = opts.DocsDir
	}
	if realPath, err := filepath.EvalSymlinks(absDocsDir); err == nil {
		absDocsDir = realPath
	}

	p.Printf("🕸️  Building API Graph Index (SQLite) from %s...\n", absDocsDir)
	graphStart := time.Now()

	dbPath := filepath.Join(opts.IndexDir, opts.Name, opts.Name+".db")
	st, err := store.Open(dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("could not open index database for graph: %w", err)
	}
	defer st.Close()
	g := graphstore.New(st)

	// recordGraphPresence mirrors "the store holds graph rows" into meta.json,
	// so a later sync that finds <name>.db missing knows it must rebuild the
	// graph from the sources (a missing store is otherwise indistinguishable
	// from an index that never had a graph).
	recordGraphPresence := func() {
		has, err := g.HasData()
		if err != nil {
			return
		}
		_ = gleann.UpdateIndexMeta(opts.IndexDir, opts.Name, func(m *gleann.IndexMeta) {
			m.HasGraph = has
		})
	}

	module := indexer.DetectGoModule(absDocsDir)
	ctx := context.Background()

	// C-04: a graph that does not exist yet (the index was built without
	// --graph) is built from the whole tree; an incremental update would
	// leave it holding only the files changed in this sync.
	if len(opts.ChangedFiles) > 0 {
		has, err := g.HasData()
		if err != nil {
			return fmt.Errorf("checking graph data: %w", err)
		}
		if !has {
			opts.ChangedFiles = nil
		}
	}

	if len(opts.ChangedFiles) > 0 {
		p.Printf("🔄 Incremental graph update: %d changed files\n", len(opts.ChangedFiles))
		var deleted []string
		for _, f := range opts.ChangedFiles {
			if _, err := os.Stat(f); err != nil {
				deleted = append(deleted, f)
			}
		}
		// The code and document steps are independent: a failed code file
		// must not keep deleted documents in the graph or skip updated ones.
		var errs []error
		if err := retractLegacyNonCode(ctx, st, g, p); err != nil {
			errs = append(errs, err)
		}
		if err := incrementalGraphSQLite(ctx, g, opts.ChangedFiles, module, absDocsDir, p); err != nil {
			errs = append(errs, fmt.Errorf("incremental graph indexing failed: %w", err))
		}
		if err := removeDeletedDocs(ctx, g, deleted, absDocsDir); err != nil {
			errs = append(errs, err)
		}
		if err := applyPluginDocs(ctx, g, opts.PluginDocs, false, absDocsDir); err != nil {
			errs = append(errs, err)
		}
		if err := errors.Join(errs...); err != nil {
			return err
		}
		p.Printf("✅ Incremental Graph Index updated in %s\n", time.Since(graphStart).Round(time.Millisecond))
		recordGraphPresence()
		return nil
	}

	if err := fullGraphSQLite(ctx, g, module, absDocsDir, opts.IncludeSubmodules, p); err != nil {
		return fmt.Errorf("graph indexing failed: %w", err)
	}
	if err := applyPluginDocs(ctx, g, opts.PluginDocs, true, absDocsDir); err != nil {
		return err
	}
	p.Printf("✅ Code Graph Index built in %s\n", time.Since(graphStart).Round(time.Millisecond))
	recordGraphPresence()
	return nil
}

// fullGraphSQLite extracts every code source file under root and rebuilds the
// whole graph in one transaction. Files are processed in sorted path order so
// the cross-file declaration dedupe (first declaration wins, the rule kuzu's
// dedupeSymbols applied) is deterministic.
func fullGraphSQLite(ctx context.Context, g *graphstore.SQLite, module, root string, includeSubmodules bool, p *printer) error {
	var files []string
	walkErr := walker.Walk(root, walker.Options{IncludeSubmodules: includeSubmodules, FollowSymlinks: true}, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !chunking.IsCodeSourceFile(path) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("walking source tree: %w", walkErr)
	}
	sort.Strings(files)

	var nonGoFiles int
	for _, f := range files {
		if chunking.DetectLanguage(f) != chunking.LangGo {
			nonGoFiles++
		}
	}
	if notice := NonGoBuildNotice(nonGoFiles); notice != "" {
		p.Warnf("%s\n", notice)
	}

	// The Go modules under the tree, not just a root go.mod: a nested
	// submodule or a go.work workspace module is loaded by its own module
	// root and prefixes its files with its real module path.
	modules, modErr := indexer.DiscoverModules(root)
	if modErr != nil {
		p.Warnf("warning: discovering Go modules: %v\n", modErr)
	}

	// Exact go/types resolution (WP-G3): load and type-check every module
	// once. A nil resolver (no module, load failure, nothing type-checked)
	// means every file keeps the syntactic resolver; per-package type errors
	// leave just those files syntactic. No failure here aborts the build, but
	// a failed load is reported: the syntactic resolver cannot follow a call
	// made through a receiver, a variable or an interface, so the graph of a
	// Go module then lacks nearly all of its method calls.
	var resolver *indexer.ExactResolver
	if graphExactEnabled() {
		var err error
		resolver, err = indexer.LoadExactModules(modules, module)
		switch {
		case err != nil && resolver == nil:
			p.Warnf("warning: exact Go call resolution is unavailable, so calls through a receiver, variable or interface are missing from the graph: %v\n", err)
		case err != nil:
			p.Warnf("warning: exact Go call resolution is incomplete for some Go modules; their files keep the syntactic resolver: %v\n", err)
		case resolver == nil && hasGoSource(root, files):
			p.Warnf("warning: found %d Go file(s) under %s but no Go module (no go.mod or go.work) was loaded; the graph uses the syntactic resolver, so calls through a receiver, variable or interface are missing\n", countGoSource(root, files), root)
		}
	}

	// Extraction is CPU-bound (parsing), so it runs on a pool of NumCPU
	// workers. Each result lands at its file's index, so everything after
	// this block sees the sorted-path order and the first-declaration-wins
	// dedupe stays deterministic; the reported error is the lowest index's.
	graphs := make([]indexer.FileGraph, len(files))
	hashes := make([]string, len(files))
	readErrs := make([]error, len(files))
	var eg errgroup.Group
	eg.SetLimit(runtime.NumCPU())
	for i, path := range files {
		eg.Go(func() error {
			src, err := os.ReadFile(path)
			if err != nil {
				readErrs[i] = fmt.Errorf("read %s: %w", path, err)
				return nil
			}
			graphs[i] = indexer.ExtractFileExactInModules(modules, module, root, path, string(src), resolver)
			hashes[i] = contentHash(src)
			return nil
		})
	}
	_ = eg.Wait()
	for _, err := range readErrs {
		if err != nil {
			return err
		}
	}

	// First declaration wins across files (build-tag variants declare the
	// same FQN in several files); a later duplicate contributes neither its
	// declaration nor its outgoing edges.
	owner := make(map[string]string)
	for _, fg := range graphs {
		for _, s := range fg.Symbols {
			if s.File == "" {
				continue // callee/type stub, never a declaration
			}
			if _, ok := owner[s.FQN]; !ok {
				owner[s.FQN] = fg.File.Path
			}
		}
	}

	members := make(memberIndex)
	for _, fg := range graphs {
		for _, s := range fg.Symbols {
			if s.File != "" && owner[s.FQN] == fg.File.Path {
				members.add(s.FQN, s.Name)
			}
		}
	}

	deltas := make([]graphstore.FileDelta, 0, len(graphs))
	for i, fg := range graphs {
		deltas = append(deltas, fileGraphToDeltaModules(fg, modules, module, hashes[i], func(fqn string) bool {
			return owner[fqn] == fg.File.Path
		}, func(fqn string) bool {
			_, ok := owner[fqn]
			return ok
		}, members))
	}
	// Reset the per-file lite markers of a previous build; ReplaceAllWithMeta
	// applies the prefix deletion before the sets, then recomputes
	// graph_treesitter from the rows and markers this transaction writes.
	// Setting the global key here too would duplicate that derivation and
	// could disagree with it (for example a non-Go file that declares no
	// symbol).
	//
	// In a lite build every non-Go file gets a marker: it records that the
	// file was extracted without tree-sitter. relToRoot returns the
	// slash-separated form the graph tables use, so the marker key is stable
	// on Windows too.
	metaMutations := map[string]*string{"graph_nongo_lite:%": nil}
	if nonGoFiles > 0 && !chunking.TreeSitterAvailable() {
		one := "1"
		for _, f := range files {
			rel := relToRoot(root, f)
			if isNonGoFile(rel) {
				metaMutations["graph_nongo_lite:"+rel] = &one
			}
		}
	}

	if err := g.ReplaceAllWithMeta(ctx, deltas, metaMutations); err != nil {
		return fmt.Errorf("writing code graph: %w", err)
	}
	return nil
}

// incrementalGraphSQLite retracts the deleted files first, then applies one
// delta per changed file, then a second pass over the graph_dirty files. A
// failed file write keeps its old graph_hash (the delta transaction rolled
// back), marks the file dirty best-effort and joins the error; the remaining
// files still apply, and the next sync retries the failed ones.
//
// Go files are resolved exactly, as in a full build (WP-G3), by type-checking
// only the packages that contain the files re-extracted here (syncExact): the
// changed files before the first pass, the dirty files of other packages
// before the second. A per-file delta stays self-contained and atomic, so the
// graph stays referentially consistent. Where exact resolution is not
// available (GLEANN_GRAPH_EXACT off, no Go module, a failed load, a package
// with type errors, test-only packages or broken test files) the file is extracted syntactically and its
// edges are "extracted" until it is synced again or the next full build.
//
// Only the re-extracted files are re-resolved. An unchanged file whose calls
// resolve differently after the edit (the type of a variable it uses changed
// elsewhere) keeps its stored edges until a full build, unless the edit
// removed the target: then the file is dirty and re-extracted here.
func incrementalGraphSQLite(ctx context.Context, g *graphstore.SQLite, changedFiles []string, module, root string, p *printer) error {
	var nonGoChanged int
	for _, f := range changedFiles {
		rel := relToRoot(root, f)
		if isNonGoFile(rel) {
			nonGoChanged++
		}
	}
	if notice := NonGoBuildNotice(nonGoChanged); notice != "" {
		p.Warnf("%s\n", notice)
	}

	var errs []error
	failed := make(map[string]bool)
	exact := newSyncExact(g, module, root, p)
	present := make([]string, 0, len(changedFiles))
	var existing, absent []string
	for _, absPath := range changedFiles {
		if _, err := os.Stat(absPath); err == nil {
			present = append(present, relToRoot(root, absPath))
			existing = append(existing, absPath)
		} else if os.IsNotExist(err) {
			absent = append(absent, absPath)
		} else {
			// Unreadable but present: applyOne reports the read error and
			// marks the file dirty.
			existing = append(existing, absPath)
		}
	}
	if err := exact.load(present); err != nil {
		errs = append(errs, err)
	}
	// Declarations of the whole batch, so a call into a file applied later in
	// this sync is classified internal (dropped as unresolved with the caller
	// marked dirty until that file lands, then re-applied by the dirty pass)
	// instead of being stored as an external edge for good.
	batch := batchDeclarations(exact.modules, module, root, changedFiles)
	applyOne := func(absPath string, dirty bool) {
		rel := relToRoot(root, absPath)
		if !chunking.IsCodeSourceFile(absPath) {
			// The code graph holds code source files only, as in a full build
			// (fullGraphSQLite). Extracting a document, a go.mod or a binary
			// would store the chunker's "block_N" pieces as symbols, and their
			// appearance would mark every file of the directory dirty. Rows an
			// earlier sync wrote for such a file are retracted, and so is its
			// dirty flag.
			if err := retractNonCode(ctx, g, rel, dirty); err != nil {
				errs = append(errs, err)
				// Best-effort: the next sync must retry the retraction even
				// though the passage step already advanced the file's hash
				// (review Q6).
				_ = g.MarkFileDirty(ctx, rel)
			}
			return
		}
		src, err := os.ReadFile(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				if rerr := retractDeletedFile(ctx, g, rel); rerr != nil {
					errs = append(errs, rerr)
				}
				return
			}
			errs = append(errs, fmt.Errorf("read %s: %w", absPath, err))
			failed[rel] = true
			_ = g.MarkFileDirty(ctx, rel) // best-effort, as for a failed write
			return
		}
		if err := applyFileGraphModules(ctx, g, exact.modules, module, root, absPath, string(src), batch, exact.resolverFor(rel)); err != nil {
			errs = append(errs, err)
			failed[rel] = true
			// Best-effort: the next sync must re-extract this file even
			// though its hash did not advance.
			_ = g.MarkFileDirty(ctx, rel)
			return
		}
		if exact.failed(rel) {
			// The file's package did not type-check, so the file was
			// extracted syntactically. Keep it dirty: the next syncs
			// re-resolve it exactly once the package type-checks again
			// (reviews Q4, G5). A file excluded by build tags is not
			// failed — its package type-checked without it — and must not
			// be retried, or the index would never be up to date.
			_ = g.MarkFileDirty(ctx, rel)
		}
	}

	// Deleted files are retracted before the changed ones are applied: a
	// rename within a package is a deletion plus an addition, and the added
	// file must not lose its declarations to the first-declaration-wins
	// dedupe against the deleted file's rows. The vector passage step does
	// not retract graph rows of deleted sources (the lexical one does), so
	// this pass is where the retraction happens on both paths.
	for _, absPath := range absent {
		applyOne(absPath, false)
	}
	for _, absPath := range existing {
		applyOne(absPath, false)
	}

	// Second pass: files whose references were broken by another file's
	// delta (graph_dirty). Files that failed this run are skipped so a
	// broken database is not hammered once per file; the next sync retries
	// them. A dirty file that disappeared is retracted.
	dirty, err := g.DirtyFiles()
	if err != nil {
		errs = append(errs, fmt.Errorf("listing dirty graph files: %w", err))
	}
	if len(dirty) > 0 {
		p.Printf("🔁 Re-applying %d dirty graph file(s)\n", len(dirty))
		// The dirty files of packages the first load did not cover (callers
		// in other packages of a declaration that appeared or went away).
		if err := exact.load(dirty); err != nil {
			errs = append(errs, err)
		}
	}
	for _, rel := range dirty {
		if failed[rel] {
			continue
		}
		absPath := filepath.Join(root, filepath.FromSlash(rel))
		if _, statErr := os.Stat(absPath); statErr != nil {
			if os.IsNotExist(statErr) {
				if rerr := retractDeletedFile(ctx, g, rel); rerr != nil {
					errs = append(errs, rerr)
				}
			}
			continue
		}
		applyOne(absPath, true)
	}
	if err := updateTreeSitterMeta(ctx, g); err != nil {
		errs = append(errs, fmt.Errorf("updating graph treesitter metadata: %w", err))
	}
	return errors.Join(errs...)
}

func isNonGoFile(rel string) bool {
	lang := chunking.DetectLanguage(rel)
	return lang != chunking.LangGo && lang != chunking.LangUnknown
}

// updateTreeSitterMeta converges the global graph_treesitter key after a sync
// pass. Each file delta already recomputed it inside its own transaction; this
// standalone call heals metadata written by an older version (or edited by
// hand) and runs in one transaction as well, so it cannot leave a half-written
// key beside a committed delta.
func updateTreeSitterMeta(ctx context.Context, g *graphstore.SQLite) error {
	return g.SyncTreeSitterMeta(ctx)
}

// applyFileGraph extracts one file and applies its delta. Edge targets are
// classified against the declarations already in the graph: a target declared
// in another file is internal, anything else (third-party, standard library)
// is external. A declaration already owned by another file is dropped together
// with its outgoing edges (first declaration wins, as in the full build).
//
// resolver is the sync's type-checked view of the file's package, or nil for
// the syntactic resolver. A resolver from a partial load re-derives the
// method-set IMPLEMENTS edges only against the interfaces it loaded; the
// file's stored edges to the others are carried over (carryImplements).
// applyFileGraph is applyFileGraphModules without a module index (the fallback
// module name prefixes every file). Kept for callers that only have a
// single-module tree.
func applyFileGraph(ctx context.Context, g *graphstore.SQLite, module, root, absPath, source string, batch map[string]string, resolver *indexer.ExactResolver) error {
	return applyFileGraphModules(ctx, g, nil, module, root, absPath, source, batch, resolver)
}

// applyFileGraphModules extracts one file and applies its delta, prefixing the
// file with the path of the module that owns it.
func applyFileGraphModules(ctx context.Context, g *graphstore.SQLite, modules *indexer.ModuleIndex, module, root, absPath, source string, batch map[string]string, resolver *indexer.ExactResolver) error {
	fg := indexer.ExtractFileExactInModules(modules, module, root, absPath, source, resolver)
	rel := fg.File.Path
	carried, err := carryImplements(g, rel, resolver)
	if err != nil {
		return err
	}
	fg.Implements = indexer.MergeImplements(fg.Implements, carried)

	var fqns []string
	seen := make(map[string]bool)
	add := func(fqn string) {
		if fqn != "" && !seen[fqn] {
			seen[fqn] = true
			fqns = append(fqns, fqn)
		}
	}
	for _, s := range fg.Symbols {
		if s.File != "" {
			add(s.FQN)
		}
	}
	for _, e := range fg.Calls {
		add(e.CalleeFQN)
		// The type a method callee belongs to: fileGraphToDelta binds a call
		// to an interface method (no symbol of its own) to the declared
		// interface, which the file need not name anywhere else.
		if i := strings.LastIndex(e.CalleeFQN, "."); i > 0 && inModuleOf(modules, module, e.CalleeFQN) {
			add(e.CalleeFQN[:i])
		}
	}
	for _, e := range fg.Implements {
		add(e.IfaceFQN)
	}
	for _, e := range fg.References {
		add(e.RefereeFQN)
	}
	existing, err := g.DeclaredFQNs(fqns)
	if err != nil {
		return fmt.Errorf("resolving graph targets for %s: %w", rel, err)
	}

	// Members for call resolution: this file's own declarations, the rest of
	// the sync batch and the package's members already in the graph.
	members := make(memberIndex)
	for _, s := range fg.Symbols {
		if s.File != "" {
			members.add(s.FQN, s.Name)
		}
	}
	for fqn, name := range batch {
		members.add(fqn, name)
	}
	if fg.File.Lang != "go" {
		stored, err := g.PackageMembers(indexer.PackagePrefix(module, rel))
		if err != nil {
			return fmt.Errorf("resolving member calls for %s: %w", rel, err)
		}
		for _, s := range stored {
			if s.File != rel {
				members.add(s.FQN, s.Name)
			}
		}
	}

	// The file's current declarations, read before the delta replaces them:
	// a method that goes away changes its receiver type's method set, and the
	// type's IMPLEMENTS edges may live in another file (markMethodOwnersDirty).
	previous, err := g.SymbolsInFile(rel)
	if err != nil {
		return fmt.Errorf("looking up graph rows of %s: %w", rel, err)
	}
	d := fileGraphToDeltaModules(fg, modules, module, contentHash([]byte(source)), func(fqn string) bool {
		owner, ok := existing[fqn]
		return !ok || owner == rel
	}, func(fqn string) bool {
		_, ok := existing[fqn]
		_, inBatch := batch[fqn]
		return ok || inBatch
	}, members)
	var metaMutations map[string]*string
	if isNonGoFile(rel) {
		if chunking.TreeSitterAvailable() {
			metaMutations = map[string]*string{"graph_nongo_lite:" + rel: nil}
		} else {
			v := "1"
			metaMutations = map[string]*string{"graph_nongo_lite:" + rel: &v}
		}
	}
	if err := g.ApplyFileDeltaWithMeta(ctx, d, metaMutations); err != nil {
		return fmt.Errorf("writing graph rows of %s: %w", rel, err)
	}

	// A method that appeared or went away changes its receiver type's method
	// set; when the type is declared in another file, that file must re-derive
	// its IMPLEMENTS edges (reviews Q3/G1/G7).
	if err := markMethodOwnersDirty(ctx, g, rel, changedMethods(previous, fg.Symbols)); err != nil {
		return err
	}

	// A declaration that is new to the graph can turn an external edge of an
	// unchanged file internal (a call to a function or method that did not
	// exist yet). Mark those files dirty; the dirty pass of the same sync
	// re-extracts them, so an incremental graph converges to the full build's.
	var appeared []string
	for _, s := range fg.Symbols {
		if s.File == "" {
			continue
		}
		if _, known := existing[s.FQN]; known {
			continue
		}
		appeared = append(appeared, s.FQN)
		if key, _, ok := memberKey(s.FQN, s.Name); ok {
			appeared = append(appeared, key)
		}
	}
	if len(appeared) > 0 {
		files, err := g.FilesWithExternalCalls(appeared)
		if err != nil {
			return fmt.Errorf("finding callers of new declarations of %s: %w", rel, err)
		}
		// A call to a name declared nowhere was dropped, not stored, so the
		// callers of a new declaration are found among the files of its
		// package (its directory).
		siblings, err := g.FilesInDir(path.Dir(rel))
		if err != nil {
			return fmt.Errorf("finding files of the package of %s: %w", rel, err)
		}
		files = append(files, siblings...)
		for _, f := range files {
			if f == rel {
				continue
			}
			if err := g.MarkFileDirty(ctx, f); err != nil {
				return fmt.Errorf("marking %s dirty after %s: %w", f, rel, err)
			}
		}
	}
	return nil
}

// retractNonCode removes the graph rows of a file that is not a code source
// file. Only a sync before this rule could have written any, so the common
// case is one read. A file flagged dirty is retracted in any case: that
// clears the flag.
func retractNonCode(ctx context.Context, g *graphstore.SQLite, rel string, dirty bool) error {
	if !dirty {
		syms, err := g.SymbolsInFile(rel)
		if err != nil {
			return fmt.Errorf("looking up graph rows of %s: %w", rel, err)
		}
		if len(syms) == 0 {
			return nil
		}
	}
	if err := g.RemoveFile(ctx, rel); err != nil {
		return fmt.Errorf("removing graph rows of %s: %w", rel, err)
	}
	return nil
}

// retractDeletedFile removes the graph rows of a deleted file and marks the
// declaration files of its methods' receiver types dirty: a type that lost a
// method may no longer satisfy an interface, and its IMPLEMENTS edges are
// bucketed under its declaration file, which is not otherwise re-extracted
// (review G7).
func retractDeletedFile(ctx context.Context, g *graphstore.SQLite, rel string) error {
	syms, err := g.SymbolsInFile(rel)
	if err != nil {
		return fmt.Errorf("looking up graph rows of %s: %w", rel, err)
	}
	metaMutations := map[string]*string{"graph_nongo_lite:" + rel: nil}
	if err := g.RemoveFileWithMeta(ctx, rel, metaMutations); err != nil {
		return fmt.Errorf("removing graph rows of %s: %w", rel, err)
	}
	return markMethodOwnersDirty(ctx, g, rel, syms)
}

// markMethodOwnersDirty marks the declaration files of the receiver types of
// syms (a Go file's removed or added methods, or all methods of a deleted
// file) dirty when the type is declared in another file. The method set of a
// type is spread over the files of its package; its IMPLEMENTS edges live
// with its declaration, so that file must be re-resolved when a methods-only
// sibling changes. Go methods share their receiver type's package, so the
// owner is a sibling of rel.
func markMethodOwnersDirty(ctx context.Context, g *graphstore.SQLite, rel string, syms []graphstore.Symbol) error {
	if !strings.HasSuffix(rel, ".go") {
		return nil
	}
	var types []string
	for _, s := range syms {
		if s.Kind != "method" {
			continue
		}
		if i := strings.LastIndex(s.FQN, "."); i > 0 {
			types = append(types, s.FQN[:i])
		}
	}
	if len(types) == 0 {
		return nil
	}
	owners, err := g.DeclaredFQNs(types)
	if err != nil {
		return fmt.Errorf("locating receiver types of %s: %w", rel, err)
	}
	for _, owner := range owners {
		if owner == rel {
			continue
		}
		if err := g.MarkFileDirty(ctx, owner); err != nil {
			return fmt.Errorf("marking %s dirty after %s: %w", owner, rel, err)
		}
	}
	return nil
}

// changedMethods returns the method declarations a delta removes (previous
// minus current) or adds (current minus previous): the ones whose receiver
// type's method set — and so its IMPLEMENTS edges — may have changed.
func changedMethods(previous []graphstore.Symbol, current []gtypes.SymbolNode) []graphstore.Symbol {
	now := make(map[string]bool, len(current))
	for _, s := range current {
		if s.File != "" {
			now[s.FQN] = true
		}
	}
	had := make(map[string]bool, len(previous))
	var out []graphstore.Symbol
	for _, s := range previous {
		had[s.FQN] = true
		if s.Kind == "method" && !now[s.FQN] {
			out = append(out, s)
		}
	}
	for _, s := range current {
		if s.File != "" && s.Kind == "method" && !had[s.FQN] {
			out = append(out, graphstore.Symbol{FQN: s.FQN, Kind: s.Kind})
		}
	}
	return out
}

// nonCodeMigrationMetaKey marks an index whose legacy non-code graph rows
// have been retracted (retractLegacyNonCode), so later syncs skip the scan.
const nonCodeMigrationMetaKey = "graph_noncode_retracted"

// retractLegacyNonCode retracts the graph rows an older gleann wrote for
// non-code files (the chunker's "block_N" pieces of README, LICENSE, go.mod
// ...): a full build holds no such rows, and the per-file rule of
// retractNonCode only reaches a file when it is synced, so unchanged legacy
// files would keep their rows forever (reviews Q5, G3). It runs once per
// index: a meta marker records the migration, later syncs only read it. A
// failed retraction leaves the marker unset, so the next sync retries.
func retractLegacyNonCode(ctx context.Context, st *store.Store, g *graphstore.SQLite, p *printer) error {
	done, err := st.Meta(ctx, nonCodeMigrationMetaKey)
	if err != nil {
		return fmt.Errorf("reading the non-code migration marker: %w", err)
	}
	if done != "" {
		return nil
	}
	files, err := g.SymbolFiles()
	if err != nil {
		return fmt.Errorf("listing symbol files for the non-code migration: %w", err)
	}
	var n int
	for _, f := range files {
		if chunking.IsCodeSourceFile(f) {
			continue
		}
		if err := g.RemoveFile(ctx, f); err != nil {
			return fmt.Errorf("removing legacy graph rows of %s: %w", f, err)
		}
		n++
	}
	if err := st.SetMeta(ctx, nonCodeMigrationMetaKey, "1"); err != nil {
		return fmt.Errorf("writing the non-code migration marker: %w", err)
	}
	if n > 0 {
		p.Printf("🧹 Retracted legacy graph rows of %d non-code file(s)\n", n)
	}
	return nil
}

// batchDeclarations extracts the declarations of every readable code file in
// a sync batch (FQN to symbol name), prefixing each file with its module path.
// Unreadable or deleted files contribute nothing.
func batchDeclarations(modules *indexer.ModuleIndex, module, root string, files []string) map[string]string {
	out := make(map[string]string)
	for _, f := range files {
		if !chunking.IsCodeSourceFile(f) {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, s := range indexer.ExtractFileInModules(modules, module, root, f, string(src)).Symbols {
			if s.File != "" {
				out[s.FQN] = s.Name
			}
		}
	}
	return out
}

// fileGraphToDelta is fileGraphToDeltaModules without a module index, for a
// single-module tree.
func fileGraphToDelta(fg indexer.FileGraph, module, hash string, own func(string) bool, internal func(string) bool, members memberIndex) graphstore.FileDelta {
	return fileGraphToDeltaModules(fg, nil, module, hash, own, internal, members)
}

// fileGraphToDeltaModules converts extractor output into a graphstore.FileDelta.
// modules supplies every module path of the indexed tree, so an unanchored
// target inside any of them is not mislabelled external.
// own reports whether this file owns a declaration FQN (cross-file dedupe);
// internal reports whether a target FQN is declared anywhere in the graph.
// Only owned declarations become symbol rows — external symbols live on edges
// as dst_external, never as rows (AGENTS.md section 2.2). Edges whose source
// this file does not own are dropped: the source declaration was deduped away
// or the extractor could not anchor it, and checkSources would refuse it.
//
// A target that is not declared anywhere in the graph is stored as an
// external edge only when it lies outside the indexed modules (an import-
// anchored standard-library or third-party symbol). One inside a module is a
// guess the extractor could not anchor (a builtin, a conversion, a local
// variable, a name declared nowhere) and the edge is dropped, so the graph
// holds neither phantom symbols nor in-repo targets mislabelled external. The
// exception is a call to a method of a declared type that has no symbol of its
// own (an interface method): it is bound to the type.
//
// members (may be nil) binds a CALLS edge of a non-Go file whose bare callee
// name is declared nowhere to the same-package method(s) of that name, see
// memberIndex.
func fileGraphToDeltaModules(fg indexer.FileGraph, modules *indexer.ModuleIndex, module, hash string, own func(string) bool, internal func(string) bool, members memberIndex) graphstore.FileDelta {
	d := graphstore.FileDelta{File: fg.File.Path, GraphHash: hash}
	kept := make(map[string]bool)
	for _, s := range fg.Symbols {
		if s.File == "" || !own(s.FQN) {
			continue
		}
		if kept[s.FQN] {
			continue
		}
		kept[s.FQN] = true
		d.Symbols = append(d.Symbols, graphstore.Symbol{
			FQN: s.FQN, Kind: s.Kind, File: s.File, Name: s.Name, Doc: s.Doc,
			Weight: s.Weight, IsTest: s.IsTest,
			StartLine: s.Line, EndLine: s.EndLine, StartCol: s.StartCol, ByteOffset: s.ByteOffset,
		})
	}
	addEdge := func(src, dst, kind, confidence string) {
		if !kept[src] {
			return
		}
		if !internal(dst) && inModuleOf(modules, module, dst) {
			// An interface method is not a declared symbol, but its
			// interface is: a call through it is bound to the interface, so
			// dispatch stays visible in callers and blast radius.
			owner := dst[:max(strings.LastIndex(dst, "."), 0)]
			if kind == "CALLS" && internal(owner) && owner != src {
				dst = owner
			} else {
				return // unanchored in-repo guess, not a third-party symbol
			}
		}
		d.Edges = append(d.Edges, graphstore.Edge{
			Src: src, Dst: dst, Kind: kind,
			DstExternal: !internal(dst),
			Confidence:  confidence,
		})
	}
	srcName := make(map[string]string)
	for _, s := range fg.Symbols {
		if s.File != "" {
			srcName[s.FQN] = s.Name
		}
	}
	for _, e := range fg.Calls {
		if members != nil && fg.File.Lang != "go" && !internal(e.CalleeFQN) {
			if targets, conf := members.resolve(srcName[e.CallerFQN], e.CalleeFQN); len(targets) > 0 {
				for _, t := range targets {
					addEdge(e.CallerFQN, t, "CALLS", conf)
				}
				continue
			}
		}
		addEdge(e.CallerFQN, e.CalleeFQN, "CALLS", e.Confidence)
	}
	for _, e := range fg.Implements {
		confidence := e.Confidence
		if confidence == "" {
			confidence = "extracted"
		}
		addEdge(e.ImplFQN, e.IfaceFQN, "IMPLEMENTS", confidence)
	}
	for _, e := range fg.References {
		addEdge(e.RefererFQN, e.RefereeFQN, "REFERENCES", e.Confidence)
	}
	return d
}

// applyPluginDocs writes the plugin-extracted document graph. A full build
// replaces doc_nodes/doc_explains wholesale; an incremental sync applies one
// DocDelta per re-extracted document and retracts the documents of deleted
// files (their SourcePath is relative to the docs dir, so deletions are
// matched by the relative form of the absolute deleted paths).
//
// A full build that read no documents leaves the document graph untouched:
// a code-only rebuild (or one whose documents were moved away) must not
// delete the documents an earlier build indexed.
func applyPluginDocs(ctx context.Context, g *graphstore.SQLite, docs []*PluginDoc, full bool, root string) error {
	if full {
		if len(docs) == 0 {
			return nil
		}
		deltas := make([]graphstore.DocDelta, 0, len(docs))
		for _, pd := range docs {
			deltas = append(deltas, pluginDocToDelta(pd))
		}
		if err := g.ReplaceDocs(ctx, deltas); err != nil {
			return fmt.Errorf("writing document graph: %w", err)
		}
		return nil
	}
	var errs []error
	for _, pd := range docs {
		if err := g.ApplyDocDelta(ctx, pluginDocToDelta(pd)); err != nil {
			errs = append(errs, fmt.Errorf("writing document graph of %s: %w", pd.SourcePath, err))
		}
	}
	return errors.Join(errs...)
}

// removeDeletedDocs retracts the document graph rows of deleted files. It is
// called from the incremental path with the absolute deleted paths.
func removeDeletedDocs(ctx context.Context, g *graphstore.SQLite, deletedFiles []string, root string) error {
	var errs []error
	for _, absPath := range deletedFiles {
		rel := relToRoot(root, absPath)
		if err := g.RemoveDoc(ctx, rel); err != nil {
			errs = append(errs, fmt.Errorf("removing document graph rows of %s: %w", rel, err))
		}
	}
	return errors.Join(errs...)
}

// pluginDocToDelta converts one plugin extraction result into a DocDelta.
// The document's SourcePath (relative to the docs dir) is the retraction
// owner and the stored rpath. Folders are derived from the vpath directory
// as the Kùzu extractor did; headings keep the plugin's section IDs, nested
// under their HAS_SUBSECTION parent or the document; chunks hang directly
// under the document (the read side's DocumentSymbols/FullDocument contract).
func pluginDocToDelta(pd *PluginDoc) graphstore.DocDelta {
	d := graphstore.DocDelta{RPath: pd.SourcePath}
	if pd.Result == nil {
		return d
	}
	parentOf := make(map[string]string)
	for _, e := range pd.Result.Edges {
		if e.Type == "HAS_SUBSECTION" {
			parentOf[e.To] = e.From
		}
	}
	var docID, folderID string
	for _, node := range pd.Result.Nodes {
		switch node.Type {
		case "Document":
			vpath := strValOr(node.Data, "vpath", pd.SourcePath)
			docID = "doc:" + vpath
			if strings.Contains(vpath, "/") {
				parts := strings.Split(vpath, "/")
				folderPath := strings.Join(parts[:len(parts)-1], "/")
				folderID = "folder:" + folderPath
				d.Nodes = append(d.Nodes, graphstore.DocNode{
					ID: folderID, Type: "folder", Name: parts[len(parts)-2],
				})
			}
			d.Nodes = append(d.Nodes, graphstore.DocNode{
				ID: docID, Type: "document", Parent: folderID,
				Name:    strValOr(node.Data, "title", ""),
				RPath:   pd.SourcePath,
				Hash:    strValOr(node.Data, "hash", ""),
				Summary: strValOr(node.Data, "summary", ""),
			})
		case "Section":
			id := strValOr(node.Data, "id", "")
			if id == "" {
				continue
			}
			parent := docID
			if p, ok := parentOf[id]; ok {
				parent = p
			}
			d.Nodes = append(d.Nodes, graphstore.DocNode{
				ID: id, Type: "heading", Parent: parent,
				Name:  strValOr(node.Data, "heading", ""),
				Level: intVal(node.Data, "level"),
				RPath: pd.SourcePath,
			})
		case "Chunk":
			id := strValOr(node.Data, "id", "")
			if id == "" {
				continue
			}
			d.Nodes = append(d.Nodes, graphstore.DocNode{
				ID: id, Type: "chunk", Parent: docID,
				Text:      strValOr(node.Data, "text", ""),
				StartChar: int64(intVal(node.Data, "start_char")),
				EndChar:   int64(intVal(node.Data, "end_char")),
				RPath:     pd.SourcePath,
			})
		}
	}
	for _, e := range pd.Result.Edges {
		if e.Type == "EXPLAINS" && e.From != "" && e.To != "" {
			d.Explains = append(d.Explains, graphstore.DocExplains{ChunkID: e.From, FQN: e.To})
		}
	}
	return d
}

func strValOr(m map[string]any, key, fallback string) string {
	if v := strVal(m, key); v != "" {
		return v
	}
	return fallback
}

func intVal(m map[string]any, key string) int {
	switch n := m[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// graphExactEnabled reports whether full builds and incremental syncs should
// attempt exact go/types resolution (WP-G3). It is on by default;
// GLEANN_GRAPH_EXACT=0|false|off|no forces the syntactic resolver (used by
// the precision benchmark and as an escape hatch).
func graphExactEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GLEANN_GRAPH_EXACT"))) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// contentHash is the graph hash of a file's content (files.graph_hash).
func contentHash(src []byte) string {
	sum := sha256.Sum256(src)
	return hex.EncodeToString(sum[:])
}

// relToRoot converts an absolute path to the slash-separated relative form
// the graph tables store.
//
// root is symlink-resolved (buildGraphIndexSQLite) while changed paths may
// not be (macOS /var -> /private/var, a symlinked checkout), so a path
// outside root is resolved too; a deleted file is resolved through its
// directory.
func relToRoot(root, absPath string) string {
	if rel, err := filepath.Rel(root, absPath); err == nil && !escapesRoot(rel) {
		return filepath.ToSlash(rel)
	}
	resolved := ""
	if r, err := filepath.EvalSymlinks(absPath); err == nil {
		resolved = r
	} else if d, err := filepath.EvalSymlinks(filepath.Dir(absPath)); err == nil {
		resolved = filepath.Join(d, filepath.Base(absPath))
	}
	if resolved != "" {
		if rel, err := filepath.Rel(root, resolved); err == nil && !escapesRoot(rel) {
			return filepath.ToSlash(rel)
		}
	}
	if rel, err := filepath.Rel(root, absPath); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(absPath)
}

func escapesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// convergeDirtyFormat2Graph re-applies the deltas of graph_dirty files when
// no passage changed, so a failed or partial graph write converges on the
// next sync instead of being masked by an "already up to date" result
// (RFC 003 section 6.5). It is a no-op for clean graphs.
func convergeDirtyFormat2Graph(indexDir, name, absDocsDir string, opts GraphOptions, p *printer) error {
	base := filepath.Join(indexDir, name, name)
	if gleann.StoreFormatOf(base) != 2 {
		return nil
	}
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		return fmt.Errorf("opening index database for dirty graph check: %w", err)
	}
	dirty, err := graphstore.New(st).DirtyFiles()
	st.Close()
	if err != nil {
		return fmt.Errorf("listing dirty graph files: %w", err)
	}
	if len(dirty) == 0 {
		return nil
	}
	changed := make([]string, len(dirty))
	for i, rel := range dirty {
		changed[i] = filepath.Join(absDocsDir, filepath.FromSlash(rel))
	}
	opts.ChangedFiles = changed
	opts.PluginDocs = nil
	return buildGraphIndex(opts, p)
}

// inModuleOf reports whether an FQN lies inside any module of the indexed
// tree, falling back to the single module name when no module index is
// available.
func inModuleOf(modules *indexer.ModuleIndex, module, fqn string) bool {
	if modules != nil {
		for _, m := range modules.Modules() {
			if fqnInModule(m.Path, fqn) {
				return true
			}
		}
	}
	return fqnInModule(module, fqn)
}

func fqnInModule(module, fqn string) bool {
	if module == "" {
		return false
	}
	return strings.HasPrefix(fqn, module+"/") || strings.HasPrefix(fqn, module+".")
}

// hasGoSource reports whether any walked source file is a Go file of a package
// the Go tool would build: .go files inside testdata, vendor, hidden or
// "_"-prefixed directories belong to no package of the tree and do not count
// (a non-Go repository with Go fixtures there is not a moduleless Go tree).
func hasGoSource(root string, files []string) bool {
	return countGoSource(root, files) > 0
}

// countGoSource counts the Go source files among the walked paths, skipping the
// directories the Go tool excludes from "./..." (the rule DiscoverModules
// applies, see indexer.GoPathIgnored).
func countGoSource(root string, files []string) int {
	n := 0
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		if rel, err := filepath.Rel(root, f); err == nil && indexer.GoPathIgnored(filepath.ToSlash(rel)) {
			continue
		}
		n++
	}
	return n
}
