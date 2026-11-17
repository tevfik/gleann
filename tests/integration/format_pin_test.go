package integration

import "testing"

// pinFormat1 runs a test against format-1 indexes. These tests read the
// format-1 files directly (<name>.passages.db through PassageManager, the
// in-memory BM25 scorer), so a run with GLEANN_STORE_FORMAT=2 or
// GLEANN_LEXICAL=fts5 in the environment would test the wrong store.
func pinFormat1(t *testing.T) {
	t.Helper()
	t.Setenv("GLEANN_STORE_FORMAT", "")
	t.Setenv("GLEANN_LEXICAL", "")
	t.Setenv("GLEANN_LEXICAL_OPTS", "")
}
