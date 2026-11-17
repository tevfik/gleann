package indexing

import (
	"errors"
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
// Format-2 indexes (meta.StoreFormat == 2) keep the graph in the index
// database and are built in every build, including CGO_ENABLED=0. A format-1
// index has no code graph (RFC 004 A3): the graph step prints a one-line
// notice and is skipped, so building or syncing its passages still works.
func BuildGraphIndex(opts GraphOptions) {
	p := newPrinter(opts.Stdout, opts.Stderr)
	if err := buildGraphIndex(opts, p); err != nil {
		p.Warnf("warning: %v\n", err)
	}
}

// BuildGraphIndexErr is BuildGraphIndex for callers that must act on a
// failure (the REST graph index endpoint): it returns the error instead of
// printing it as a warning.
func BuildGraphIndexErr(opts GraphOptions) error {
	return buildGraphIndex(opts, newPrinter(opts.Stdout, opts.Stderr))
}

// buildGraphIndex writes graphstore.FileDelta rows into <name>/<name>.db of a
// format-2 index (RFC 003 section 8 step 7, every build). A format-1 index
// has no code graph; the step is skipped with a notice.
func buildGraphIndex(opts GraphOptions, p *printer) error {
	if gleann.StoreFormatOf(filepath.Join(opts.IndexDir, opts.Name, opts.Name)) == 2 {
		return buildGraphIndexSQLite(opts, p)
	}
	p.Warnf("⚠️  Index %q uses the old store format, which has no code graph; skipping the graph. Convert it with `gleann index migrate %s` (or build new indexes with GLEANN_STORE_FORMAT=2) and build the graph again.\n", opts.Name, opts.Name)
	return nil
}

// ErrGraphUpdate is returned by Build and Sync when the vector index was
// written but the code graph could not be. The index is not recorded as up to
// date, so the next sync retries instead of reporting "already up to date"
// over a stale graph.
var ErrGraphUpdate = errors.New("graph update failed")
