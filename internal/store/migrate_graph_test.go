package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// mockOldGraphDB writes a format-2 database as it existed before the
// symbols_fts / graph_counts / edges_file_drop steps: symbols is WITHOUT ROWID
// keyed by fqn, edges carries the redundant edges_file index, and there are no
// FTS or counter objects. It holds real rows so the migration must preserve
// them.
func mockOldGraphDB(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	initSQL := `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;
INSERT INTO meta (key, value) VALUES ('store_format', '2');
CREATE TABLE files (path TEXT PRIMARY KEY, size INT, mtime_ns INT, hash TEXT, graph_hash TEXT, graph_dirty INT, indexed_at INT) WITHOUT ROWID;
CREATE TABLE passages (id INTEGER PRIMARY KEY, pid TEXT NOT NULL, source TEXT, chunk_no INT, vec_slot INT, kind TEXT, text TEXT, meta TEXT);
CREATE VIRTUAL TABLE lex USING fts5(tokens, content='', contentless_delete=1, tokenize="unicode61 remove_diacritics 0 tokenchars '_'");
CREATE VIRTUAL TABLE lex_vocab USING fts5vocab(lex, 'row');
CREATE TABLE symbols (fqn TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, file TEXT NOT NULL, start_line INT, end_line INT, start_col INT, byte_offset INT, is_test INT, weight REAL, doc TEXT) WITHOUT ROWID;
CREATE INDEX symbols_file ON symbols(file);
CREATE INDEX symbols_name ON symbols(name);
CREATE TABLE edges (src TEXT NOT NULL, dst TEXT NOT NULL, kind TEXT NOT NULL, file TEXT NOT NULL, dst_external INT, confidence TEXT, PRIMARY KEY (src, dst, kind, file)) WITHOUT ROWID;
CREATE INDEX edges_dst ON edges(dst, kind);
CREATE INDEX edges_file ON edges(file);
CREATE TABLE doc_nodes (id TEXT PRIMARY KEY, type TEXT NOT NULL, parent TEXT, name TEXT, level INTEGER, rpath TEXT, hash TEXT, summary TEXT, text TEXT, start_char INTEGER, end_char INTEGER) WITHOUT ROWID;
CREATE INDEX doc_nodes_parent ON doc_nodes(parent);
CREATE TABLE doc_explains (chunk_id TEXT NOT NULL, fqn TEXT NOT NULL, PRIMARY KEY (chunk_id, fqn)) WITHOUT ROWID;
INSERT INTO symbols (fqn, name, kind, file, start_line, end_line, start_col, byte_offset, is_test, weight, doc) VALUES
  ('pkg.Alpha', 'Alpha', 'function', 'a.go', 1, 5, 1, 0, 0, 1.0, ''),
  ('pkg.alphabet', 'alphabet', 'function', 'a.go', 6, 9, 1, 40, 0, 1.0, ''),
  ('pkg.Beta', 'Beta', 'type', 'b.go', 1, 3, 1, 0, 0, 1.0, '');
INSERT INTO edges (src, dst, kind, file, dst_external, confidence) VALUES
  ('pkg.Alpha', 'pkg.Beta', 'CALLS', 'a.go', 0, 'extracted'),
  ('pkg.Beta', 'pkg.Alpha', 'IMPLEMENTS', 'b.go', 0, 'extracted'),
  ('pkg.Alpha', 'ext.Lib', 'CALLS', 'a.go', 1, 'extracted');
`
	if _, err := db.Exec(initSQL); err != nil {
		t.Fatalf("initializing mock old graph db: %v", err)
	}
}

// TestMigrateGraphSymbolsFTSAndCounts checks the three new format-2 steps on a
// pre-existing database: symbols is converted to the rowid shape with its rows
// and UNIQUE(fqn) intact, the trigram FTS index is built and searchable, the
// graph counters are seeded from the current rows, and the redundant edges_file
// index is dropped. It fails on the old code, which has none of these objects.
func TestMigrateGraphSymbolsFTSAndCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	mockOldGraphDB(t, path)

	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open old graph db: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if format, err := s.Meta(ctx, "store_format"); err != nil || format != "2" {
		t.Fatalf("store_format = %q, %v; want 2", format, err)
	}
	for _, step := range []string{"symbols_fts", "graph_counts", "edges_file_drop"} {
		if v, err := s.Meta(ctx, "migration:"+step); err != nil || v != "1" {
			t.Fatalf("migration:%s = %q, %v; want 1", step, v, err)
		}
	}

	// symbols gained an integer id and kept fqn UNIQUE.
	rows, err := s.reader.QueryContext(ctx, `PRAGMA table_info(symbols)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		cols[name] = true
	}
	rows.Close()
	if !cols["id"] {
		t.Fatalf("symbols has no id column after migration; cols=%v", cols)
	}
	var n int
	if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM symbols`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("symbols count = %d, %v; want 3 (rows preserved)", n, err)
	}
	// UNIQUE(fqn) still rejects a duplicate declaration.
	if _, err := s.writer.ExecContext(ctx, `INSERT INTO symbols (fqn,name,kind,file,start_line,end_line,start_col,byte_offset,is_test,weight,doc)
VALUES ('pkg.Alpha','dup','function','a.go',1,1,1,0,0,1.0,'')`); err == nil {
		t.Fatalf("duplicate fqn insert succeeded; UNIQUE(fqn) lost in conversion")
	}

	// FTS index exists and answers a case-insensitive substring search.
	if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name='symbols_fts'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("symbols_fts missing (n=%d err=%v)", n, err)
	}
	var fqn string
	if err := s.reader.QueryRowContext(ctx, `SELECT s.fqn FROM symbols_fts f JOIN symbols s ON s.id=f.rowid
WHERE symbols_fts MATCH '"ALPH"' ORDER BY s.fqn LIMIT 1`).Scan(&fqn); err != nil || fqn != "pkg.Alpha" {
		t.Fatalf("FTS substring ALPH = %q, %v; want pkg.Alpha", fqn, err)
	}

	// Counters seeded from the current rows.
	for key, want := range map[string]string{
		MetaGraphSymbols:                    "3",
		MetaGraphEdgesPrefix + "CALLS":      "2",
		MetaGraphEdgesPrefix + "IMPLEMENTS": "1",
		MetaGraphEdgesPrefix + "REFERENCES": "0",
	} {
		if v, err := s.Meta(ctx, key); err != nil || v != want {
			t.Fatalf("counter %s = %q, %v; want %s", key, v, err, want)
		}
	}

	// edges_file dropped; edges_dst and the edges rows remain.
	if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='edges_file'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("edges_file still present (n=%d err=%v); want dropped", n, err)
	}
	if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM edges`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("edges count = %d, %v; want 3", n, err)
	}

	// Reopen is idempotent: rows and counters intact, format stays 2.
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if err := s2.reader.QueryRowContext(ctx, `SELECT count(*) FROM symbols`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("symbols after reopen = %d, %v; want 3", n, err)
	}
	if v, err := s2.Meta(ctx, MetaGraphSymbols); err != nil || v != "3" {
		t.Fatalf("counter after reopen = %q, %v; want 3", v, err)
	}
}
