package indexing

import (
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
