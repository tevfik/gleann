package indexing

import "io"

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
// Stderr and never abort the surrounding build, matching the CLI.
//
// Only functional when built with -tags treesitter (CGo + KuzuDB); other
// builds print a one-line warning. See GraphSupported.
func BuildGraphIndex(opts GraphOptions) {
	buildGraphIndex(opts, newPrinter(opts.Stdout, opts.Stderr))
}
