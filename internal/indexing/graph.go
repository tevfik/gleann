package indexing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/tevfik/gleann/pkg/gleann"
)

// GraphOptions configures BuildGraphIndex.
type GraphOptions struct {
	Name     string
	DocsDir  string
	IndexDir string
	// PluginDocs are written as Document/Section nodes after AST indexing.
	PluginDocs []*PluginDoc
	// ChangedFiles selects incremental mode; empty means full re-index.
	ChangedFiles      []string
	IncludeSubmodules bool
	Stdout            io.Writer
	Stderr            io.Writer
}

// BuildGraphIndex builds (or incrementally updates) the AST code graph and
// document graph for an index. Failures are reported as warnings on
// Stderr and do not abort the caller; Build and Sync, which own the index's
// up-to-date state, treat them as errors instead.
//
// The graph lives in the index database (<name>/<name>.db) in every build,
// including CGO_ENABLED=0. An index in the old store format is refused with
// the migration hint.
//
// The build takes the index lock and, for a full rebuild whose caller did
// not read the documents (PluginDocs nil), reads them from DocsDir so the
// document graph is rebuilt instead of wiped; see buildGraphIndexStandalone.
func BuildGraphIndex(opts GraphOptions) {
	p := newPrinter(opts.Stdout, opts.Stderr)
	if err := buildGraphIndexStandalone(opts, p); err != nil {
		p.Warnf("warning: %v\n", err)
	}
}

// BuildGraphIndexErr is BuildGraphIndex for callers that must act on a
// failure (the REST graph index endpoint): it returns the error instead of
// printing it as a warning.
func BuildGraphIndexErr(opts GraphOptions) error {
	return buildGraphIndexStandalone(opts, newPrinter(opts.Stdout, opts.Stderr))
}

// buildGraphIndexStandalone runs a graph build that is not part of Build,
// Sync or Migrate (CLI `gleann graph build`, REST POST /api/graph/{name}/index):
// it takes the index lock (AGENTS.md section 2.4) and, for a full build whose
// caller did not read the documents, reads them from the source directory the
// way migrate does (rebuildGraphFromSources), so the document graph is rebuilt
// from the sources instead of replaced with an empty list. Build, Sync and
// Migrate hold the lock already and call buildGraphIndex directly with the
// documents they read; lockIndex must not be re-acquired while it is held.
func buildGraphIndexStandalone(opts GraphOptions, p *printer) error {
	if gleann.DetectStoreFormat(filepath.Join(opts.IndexDir, opts.Name, opts.Name)) != gleann.StoreV2 {
		// Not a format-2 index: buildGraphIndex reports the migration hint
		// or ErrIndexNotFound without taking the lock or reading anything.
		return buildGraphIndex(opts, p)
	}
	unlock, err := lockIndex(context.Background(), opts.IndexDir, opts.Name)
	if err != nil {
		return err
	}
	defer unlock()
	if len(opts.ChangedFiles) == 0 && opts.PluginDocs == nil && opts.DocsDir != "" {
		cc := gleann.DefaultChunkConfig()
		_, pluginDocs, err := readDocuments(context.Background(), opts.DocsDir, ReadOptions{
			ChunkSize:         cc.ChunkSize,
			ChunkOverlap:      cc.ChunkOverlap,
			Mode:              ModeAll,
			IncludeSubmodules: opts.IncludeSubmodules,
			Stdout:            opts.Stdout,
			Stderr:            opts.Stderr,
		}, p)
		if err != nil {
			return fmt.Errorf("reading documents: %w", err)
		}
		opts.PluginDocs = pluginDocs
	}
	return buildGraphIndex(opts, p)
}

// buildGraphIndex writes graphstore.FileDelta rows into <name>/<name>.db
// (RFC 003 section 8 step 7, every build).
func buildGraphIndex(opts GraphOptions, p *printer) error {
	switch gleann.DetectStoreFormat(filepath.Join(opts.IndexDir, opts.Name, opts.Name)) {
	case gleann.StoreV2:
		return buildGraphIndexSQLite(opts, p)
	case gleann.StoreOld:
		return &gleann.OldFormatError{Name: opts.Name}
	}
	return fmt.Errorf("building the graph of index %q: %w", opts.Name, gleann.ErrIndexNotFound)
}

// ErrGraphUpdate is returned by Build and Sync when the vector index was
// written but the code graph could not be. The index is not recorded as up to
// date, so the next sync retries instead of reporting "already up to date"
// over a stale graph.
var ErrGraphUpdate = errors.New("graph update failed")
