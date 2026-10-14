// Package indexing implements gleann's index build and sync pipeline:
// file walking and filtering, plugin / native / multimodal extraction,
// chunking, embedding, manifest bookkeeping and (when built with the
// treesitter tag) AST + document graph indexing.
//
// It is the single in-process implementation shared by the CLI
// (cmd/gleann), the REST server (internal/server) and the MCP server
// (internal/mcp). The package never exits the process, never parses CLI
// flags and never writes to os.Stdout / os.Stderr directly: human-readable
// messages go to the Stdout / Stderr writers supplied in Options (discarded
// when nil), machine-readable progress goes to the Progress callback, and
// failures are returned as errors. Cancellation is driven by the context
// passed to Build / Sync.
//
// Build-tag specific behaviour lives in graph_treesitter.go (KuzuDB graph
// indexing, `-tags treesitter`) and graph_stub.go (all other builds).
package indexing
