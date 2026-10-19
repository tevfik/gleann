package indexing

import (
	"errors"
	"io"
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
// Only functional when built with -tags treesitter (CGo + KuzuDB); other
// builds print a one-line warning. See GraphSupported.
func BuildGraphIndex(opts GraphOptions) {
	p := newPrinter(opts.Stdout, opts.Stderr)
	if err := buildGraphIndex(opts, p); err != nil {
		p.Warnf("warning: %v\n", err)
	}
}

// ErrGraphUpdate is returned by Build and Sync when the vector index was
// written but the code graph could not be. The index is not recorded as up to
// date, so the next sync retries instead of reporting "already up to date"
// over a stale graph.
var ErrGraphUpdate = errors.New("graph update failed")
