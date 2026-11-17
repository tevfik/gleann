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
		return nil
	}

	if err := fullGraphSQLite(ctx, g, module, absDocsDir, opts.IncludeSubmodules); err != nil {
		return fmt.Errorf("graph indexing failed: %w", err)
	}
	if err := applyPluginDocs(ctx, g, opts.PluginDocs, true, absDocsDir); err != nil {
		return err
	}
	p.Printf("✅ Code Graph Index built in %s\n", time.Since(graphStart).Round(time.Millisecond))
	return nil
}

// fullGraphSQLite extracts every code source file under root and rebuilds the
// whole graph in one transaction. Files are processed in sorted path order so
// the cross-file declaration dedupe (first declaration wins, the rule kuzu's
// dedupeSymbols applied) is deterministic.
func fullGraphSQLite(ctx context.Context, g *graphstore.SQLite, module, root string, includeSubmodules bool) error {
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

	// Exact go/types resolution (WP-G3): load and type-check the module once.
	// A nil resolver (not a module, load failure, nothing type-checked) means
	// every file keeps the syntactic resolver; per-package type errors leave
	// just those files syntactic. No failure here aborts the build.
	var resolver *indexer.ExactResolver
	if graphExactEnabled() {
		resolver, _ = indexer.LoadExact(module, root)
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
			graphs[i] = indexer.ExtractFileExact(module, root, path, string(src), resolver)
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
		deltas = append(deltas, fileGraphToDelta(fg, module, hashes[i], func(fqn string) bool {
			return owner[fqn] == fg.File.Path
		}, func(fqn string) bool {
			_, ok := owner[fqn]
			return ok
		}, members))
	}
	if err := g.ReplaceAll(ctx, deltas); err != nil {
		return fmt.Errorf("writing code graph: %w", err)
	}
	return nil
}

// incrementalGraphSQLite applies one delta per changed file, RemoveFile per
// deleted file, then a second pass over the graph_dirty files. A failed file
// write keeps its old graph_hash (the delta transaction rolled back), marks
// the file dirty best-effort and joins the error; the remaining files still
// apply, and the next sync retries the failed ones.
//
// Resolution here is syntactic (ExtractFile), not exact go/types (WP-G3):
// re-type-checking per changed file is not required, and a per-file delta is
// self-contained and atomic, so the graph stays referentially consistent. A
// synced file's edges revert to "extracted" until the next full build
// re-resolves them exactly. This is the documented incremental fallback.
func incrementalGraphSQLite(ctx context.Context, g *graphstore.SQLite, changedFiles []string, module, root string, p *printer) error {
	var errs []error
	failed := make(map[string]bool)
	// Declarations of the whole batch, so a call into a file applied later in
	// this sync is classified internal (dropped as unresolved with the caller
	// marked dirty until that file lands, then re-applied by the dirty pass)
	// instead of being stored as an external edge for good.
	batch := batchDeclarations(module, root, changedFiles)
	applyOne := func(absPath string) {
		rel := relToRoot(root, absPath)
		src, err := os.ReadFile(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				if rerr := g.RemoveFile(ctx, rel); rerr != nil {
					errs = append(errs, fmt.Errorf("removing graph rows of %s: %w", rel, rerr))
				}
				return
			}
			errs = append(errs, fmt.Errorf("read %s: %w", absPath, err))
			failed[rel] = true
			_ = g.MarkFileDirty(ctx, rel) // best-effort, as for a failed write
			return
		}
		if err := applyFileGraph(ctx, g, module, root, absPath, string(src), batch); err != nil {
			errs = append(errs, err)
			failed[rel] = true
			// Best-effort: the next sync must re-extract this file even
			// though its hash did not advance.
			_ = g.MarkFileDirty(ctx, rel)
		}
	}

	for _, absPath := range changedFiles {
		applyOne(absPath)
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
	}
	for _, rel := range dirty {
		if failed[rel] {
			continue
		}
		absPath := filepath.Join(root, filepath.FromSlash(rel))
		if _, statErr := os.Stat(absPath); statErr != nil {
			if os.IsNotExist(statErr) {
				if rerr := g.RemoveFile(ctx, rel); rerr != nil {
					errs = append(errs, fmt.Errorf("removing graph rows of %s: %w", rel, rerr))
				}
			}
			continue
		}
		applyOne(absPath)
	}
	return errors.Join(errs...)
}

// applyFileGraph extracts one file and applies its delta. Edge targets are
// classified against the declarations already in the graph: a target declared
// in another file is internal, anything else (third-party, standard library)
// is external. A declaration already owned by another file is dropped together
// with its outgoing edges (first declaration wins, as in the full build).
func applyFileGraph(ctx context.Context, g *graphstore.SQLite, module, root, absPath, source string, batch map[string]string) error {
	fg := indexer.ExtractFile(module, root, absPath, source)
	rel := fg.File.Path

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

	d := fileGraphToDelta(fg, module, contentHash([]byte(source)), func(fqn string) bool {
		owner, ok := existing[fqn]
		return !ok || owner == rel
	}, func(fqn string) bool {
		_, ok := existing[fqn]
		_, inBatch := batch[fqn]
		return ok || inBatch
	}, members)
	if err := g.ApplyFileDelta(ctx, d); err != nil {
		return fmt.Errorf("writing graph rows of %s: %w", rel, err)
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

// batchDeclarations extracts the declarations of every readable file in a
// sync batch (FQN to symbol name). Unreadable or deleted files contribute
// nothing.
func batchDeclarations(module, root string, files []string) map[string]string {
	out := make(map[string]string)
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, s := range indexer.ExtractFile(module, root, f, string(src)).Symbols {
			if s.File != "" {
				out[s.FQN] = s.Name
			}
		}
	}
	return out
}

// fileGraphToDelta converts extractor output into a graphstore.FileDelta.
// own reports whether this file owns a declaration FQN (cross-file dedupe);
// internal reports whether a target FQN is declared anywhere in the graph.
// Only owned declarations become symbol rows — external symbols live on edges
// as dst_external, never as rows (AGENTS.md section 2.2). Edges whose source
// this file does not own are dropped: the source declaration was deduped away
// or the extractor could not anchor it, and checkSources would refuse it.
//
// A target that is not declared anywhere in the graph is stored as an
// external edge only when it lies outside the indexed module (an import-
// anchored standard-library or third-party symbol). One inside the module is a
// guess the extractor could not anchor (a builtin, a conversion, a local
// variable, a name declared nowhere) and the edge is dropped, so the graph
// holds neither phantom symbols nor in-repo targets mislabelled external. The
// exception is a call to a method of a declared type that has no symbol of its
// own (an interface method): it is bound to the type.
//
// members (may be nil) binds a CALLS edge of a non-Go file whose bare callee
// name is declared nowhere to the same-package method(s) of that name, see
// memberIndex.
func fileGraphToDelta(fg indexer.FileGraph, module, hash string, own func(string) bool, internal func(string) bool, members memberIndex) graphstore.FileDelta {
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
		if !internal(dst) && inModule(module, dst) {
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
func applyPluginDocs(ctx context.Context, g *graphstore.SQLite, docs []*PluginDoc, full bool, root string) error {
	if full {
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

// graphExactEnabled reports whether full builds should attempt exact go/types
// resolution (WP-G3). It is on by default; GLEANN_GRAPH_EXACT=0|false|off|no
// forces the syntactic resolver (used by the precision benchmark and as an
// escape hatch). Incremental syncs always use the syntactic resolver.
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

// inModule reports whether an FQN lies inside the indexed module: FQNs are
// "<module>/<dir>.<Name>" or "<module>.<Name>" for the root directory.
func inModule(module, fqn string) bool {
	if module == "" {
		return false
	}
	return strings.HasPrefix(fqn, module+"/") || strings.HasPrefix(fqn, module+".")
}
