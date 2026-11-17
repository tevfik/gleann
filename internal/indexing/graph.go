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
// database and are built in every build, including CGO_ENABLED=0. Format-1
// indexes use the Kùzu path, which requires -tags treesitter; other builds
// print a one-line warning for them. See GraphSupported.
func BuildGraphIndex(opts GraphOptions) {
	p := newPrinter(opts.Stdout, opts.Stderr)
	if err := buildGraphIndex(opts, p); err != nil {
		p.Warnf("warning: %v\n", err)
	}
}

// buildGraphIndex dispatches on the index's store format: format-2 writes
// graphstore.FileDelta rows into <name>/<name>.db (RFC 003 section 8 step 7,
// every build); format-1 keeps the Kùzu path and its behaviour unchanged.
func buildGraphIndex(opts GraphOptions, p *printer) error {
	if gleann.StoreFormatOf(filepath.Join(opts.IndexDir, opts.Name, opts.Name)) == 2 {
		return buildGraphIndexSQLite(opts, p)
	}
	return buildGraphIndexKuzu(opts, p)
}

// ErrGraphUpdate is returned by Build and Sync when the vector index was
// written but the code graph could not be. The index is not recorded as up to
// date, so the next sync retries instead of reporting "already up to date"
// over a stale graph.
var ErrGraphUpdate = errors.New("graph update failed")
