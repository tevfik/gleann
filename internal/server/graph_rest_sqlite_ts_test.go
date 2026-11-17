//go:build treesitter

package server

import "testing"

// checkSQLiteGolden is a no-op with tree-sitter: that build extracts more CALLS
// edges than the lite build the sqlite_lite goldens describe (A1 changes them
// again), so only the structural assertions of the tests apply.
func checkSQLiteGolden(t *testing.T, name, got string) {}
