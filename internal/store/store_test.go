package store

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openTestStore opens a fresh store in a temp directory and closes it on
// cleanup.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenPragmasAndSchema(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// RFC 003 section 3.6: WAL, busy_timeout 10000, synchronous NORMAL (1),
	// foreign_keys ON, on both pools.
	for name, db := range map[string]*sql.DB{"writer": s.writer, "reader": s.reader} {
		var mode string
		if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
			t.Fatalf("%s journal_mode: %v", name, err)
		}
		if mode != "wal" {
			t.Errorf("%s journal_mode = %q, want wal", name, mode)
		}
		var busy, sync, fk int
		if err := db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busy); err != nil {
			t.Fatalf("%s busy_timeout: %v", name, err)
		}
		if busy != 10000 {
			t.Errorf("%s busy_timeout = %d, want 10000", name, busy)
		}
		if err := db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&sync); err != nil {
			t.Fatalf("%s synchronous: %v", name, err)
		}
		if sync != 1 {
			t.Errorf("%s synchronous = %d, want 1 (NORMAL)", name, sync)
		}
		if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
			t.Fatalf("%s foreign_keys: %v", name, err)
		}
		if fk != 1 {
			t.Errorf("%s foreign_keys = %d, want 1", name, fk)
		}
	}

	format, err := s.Meta(ctx, "store_format")
	if err != nil {
		t.Fatalf("Meta(store_format): %v", err)
	}
	if format != "2" {
		t.Errorf("store_format = %q, want 2", format)
	}

	// The first migration creates the full RFC 003 section 3.4 schema,
	// including the graph and document tables that stay empty until R2.
	rows, err := s.reader.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type IN ('table', 'index', 'view')`)
	if err != nil {
		t.Fatalf("sqlite_master: %v", err)
	}
	defer rows.Close()
	have := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		have[name] = true
	}
	for _, want := range []string{
		"meta", "files", "passages", "lex", "lex_vocab", "symbols", "edges", "doc_nodes", "doc_explains",
		"files_dirty", "passages_source", "passages_slot",
		"symbols_file", "symbols_name", "symbols_fts", "doc_nodes_parent",
		"graph_nodes", "graph_labels", "graph_edges", "graph_edges_dst",
		"doc_nodes_rpath", "doc_nodes_type",
	} {
		if !have[want] {
			t.Errorf("schema object %q missing", want)
		}
	}
	// edges_file is dropped by the edges_file_drop step: it is redundant with
	// the edges primary key under the owner invariant (edges.file == src's file).
	if have["edges_file"] {
		t.Errorf("schema object %q should have been dropped", "edges_file")
	}

	// Reopening is idempotent and keeps the data.
	if _, err := s.ApplySource(ctx, SourceDelta{
		Source:   "a.go",
		File:     FileRow{Hash: "h1"},
		Passages: []NewPassage{{Text: "hello"}},
	}); err != nil {
		t.Fatalf("ApplySource: %v", err)
	}
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	n, err := s2.Count(ctx)
	if err != nil || n != 1 {
		t.Fatalf("after reopen Count = %d, %v; want 1", n, err)
	}
}

// TestFTS5ContentlessDelete is the step-0 regression test: the driver's FTS5
// must support contentless_delete=1 with DELETE by rowid and INSERT OR
// REPLACE (measured once by hand in the spike, RFC 003 section 1.1).
func TestFTS5ContentlessDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fts.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE VIRTUAL TABLE lex USING fts5(
  tokens, content = '', contentless_delete = 1,
  tokenize = "unicode61 remove_diacritics 0 tokenchars '_'"
)`); err != nil {
		t.Fatalf("create fts5 table: %v", err)
	}
	match := func(q string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM lex WHERE lex MATCH ?`, q).Scan(&n); err != nil {
			t.Fatalf("MATCH %q: %v", q, err)
		}
		return n
	}
	if _, err := db.Exec(`INSERT INTO lex(rowid, tokens) VALUES (1, 'alpha beta')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if n := match("alpha"); n != 1 {
		t.Fatalf("match alpha = %d, want 1", n)
	}
	if _, err := db.Exec(`DELETE FROM lex WHERE rowid = 1`); err != nil {
		t.Fatalf("delete by rowid: %v", err)
	}
	if n := match("alpha"); n != 0 {
		t.Fatalf("match alpha after delete = %d, want 0", n)
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO lex(rowid, tokens) VALUES (2, 'gamma delta')`); err != nil {
		t.Fatalf("insert or replace: %v", err)
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO lex(rowid, tokens) VALUES (2, 'epsilon zeta')`); err != nil {
		t.Fatalf("insert or replace over existing: %v", err)
	}
	if n := match("epsilon"); n != 1 {
		t.Fatalf("match epsilon = %d, want 1", n)
	}
	if n := match("gamma"); n != 0 {
		t.Fatalf("match gamma after replace = %d, want 0", n)
	}
}

func TestNetworkFSFallback(t *testing.T) {
	var buf bytes.Buffer
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	s, err := Open(path, Options{
		Log:       slog.New(slog.NewTextHandler(&buf, nil)),
		NetworkFS: func(string) bool { return true }, // mocked file-system type (RFC 003 7.1)
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	for name, db := range map[string]*sql.DB{"writer": s.writer, "reader": s.reader} {
		var mode string
		if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
			t.Fatalf("%s journal_mode: %v", name, err)
		}
		if mode != "delete" {
			t.Errorf("%s journal_mode = %q, want delete on a network file system", name, mode)
		}
	}
	if !strings.Contains(buf.String(), "network file system") {
		t.Errorf("expected a warning about the network file system, got %q", buf.String())
	}
}

func TestMeta(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if v, err := s.Meta(ctx, "language"); err != nil || v != "" {
		t.Fatalf("missing key = %q, %v; want empty, nil", v, err)
	}
	if err := s.SetMeta(ctx, "language", "tr"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := s.SetMeta(ctx, "language", "en"); err != nil {
		t.Fatalf("SetMeta overwrite: %v", err)
	}
	if v, err := s.Meta(ctx, "language"); err != nil || v != "en" {
		t.Fatalf("Meta = %q, %v; want en", v, err)
	}
}

func TestOpenUnsupportedFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.SetMeta(context.Background(), "store_format", "99"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	s.Close()
	if _, err := Open(path, Options{}); err == nil || !strings.Contains(err.Error(), "unsupported store_format") {
		t.Fatalf("Open with unknown format: err = %v, want unsupported store_format", err)
	}
}

// TestMigrateExistingFormat2LexVocab tests that an existing format-2 database
// created before the lex_vocab migration step is cleanly and idempotently
// migrated: lex_vocab virtual table is created, and store_format stays "2"
// (RFC 003 section 4.2 step 3).
func TestMigrateExistingFormat2LexVocab(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Simulate an older format-2 database that has store_format="2", lex table,
	// but no lex_vocab and no migration:* meta keys.
	initSQL := `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;
INSERT INTO meta (key, value) VALUES ('store_format', '2');
CREATE TABLE passages (id INTEGER PRIMARY KEY, pid TEXT NOT NULL, source TEXT, chunk_no INT, vec_slot INT, kind TEXT, text TEXT, meta TEXT);
CREATE TABLE files (path TEXT PRIMARY KEY, size INT, mtime_ns INT, hash TEXT, graph_hash TEXT, graph_dirty INT, indexed_at INT) WITHOUT ROWID;
CREATE VIRTUAL TABLE lex USING fts5(tokens, content='', contentless_delete=1, tokenize="unicode61 remove_diacritics 0 tokenchars '_'");
CREATE TABLE doc_nodes (id TEXT PRIMARY KEY, type TEXT NOT NULL, parent TEXT, name TEXT, level INTEGER, rpath TEXT, hash TEXT, summary TEXT, text TEXT, start_char INTEGER, end_char INTEGER) WITHOUT ROWID;
CREATE INDEX doc_nodes_parent ON doc_nodes(parent);
CREATE TABLE doc_explains (chunk_id TEXT NOT NULL, fqn TEXT NOT NULL, PRIMARY KEY (chunk_id, fqn)) WITHOUT ROWID;
`
	if _, err := db.Exec(initSQL); err != nil {
		t.Fatalf("initializing mock format-2 db: %v", err)
	}
	db.Close()

	// Open with Store: should apply the lex_vocab migration step while format "2" stays.
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open existing format-2 db: %v", err)
	}
	ctx := context.Background()
	format, err := s.Meta(ctx, "store_format")
	if err != nil || format != "2" {
		t.Fatalf("store_format = %q, %v; want 2", format, err)
	}

	// Verify lex_vocab table exists and is queryable.
	var count int
	if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'lex_vocab'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("lex_vocab in sqlite_master = %d, err = %v; want 1", count, err)
	}

	// Reopen to verify idempotency.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("reopening migrated format-2 db: %v", err)
	}
	defer s2.Close()
	format2, err := s2.Meta(ctx, "store_format")
	if err != nil || format2 != "2" {
		t.Fatalf("store_format after reopen = %q, %v; want 2", format2, err)
	}
}

// TestMigrateExistingFormat2DocNodes tests that an existing format-2 database
// created before the doc_nodes_rpath step gets the document-lookup indexes on
// open, the step is recorded in meta, and store_format stays "2".
func TestMigrateExistingFormat2DocNodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// A format-2 database as the first release created it: the full section
	// 3.4 schema, but no doc_nodes_rpath/doc_nodes_type indexes and no
	// migration:* meta keys.
	initSQL := `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;
INSERT INTO meta (key, value) VALUES ('store_format', '2');
CREATE TABLE files (path TEXT PRIMARY KEY, size INT, mtime_ns INT, hash TEXT, graph_hash TEXT, graph_dirty INT, indexed_at INT) WITHOUT ROWID;
CREATE TABLE passages (id INTEGER PRIMARY KEY, pid TEXT NOT NULL, source TEXT, chunk_no INT, vec_slot INT, kind TEXT, text TEXT, meta TEXT);
CREATE VIRTUAL TABLE lex USING fts5(tokens, content='', contentless_delete=1, tokenize="unicode61 remove_diacritics 0 tokenchars '_'");
CREATE VIRTUAL TABLE lex_vocab USING fts5vocab(lex, 'row');
CREATE TABLE symbols (fqn TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, file TEXT NOT NULL, start_line INT, end_line INT, start_col INT, byte_offset INT, is_test INT, weight REAL, doc TEXT) WITHOUT ROWID;
CREATE TABLE edges (src TEXT NOT NULL, dst TEXT NOT NULL, kind TEXT NOT NULL, file TEXT NOT NULL, dst_external INT, confidence TEXT, PRIMARY KEY (src, dst, kind, file)) WITHOUT ROWID;
CREATE TABLE doc_nodes (id TEXT PRIMARY KEY, type TEXT NOT NULL, parent TEXT, name TEXT, level INTEGER, rpath TEXT, hash TEXT, summary TEXT, text TEXT, start_char INTEGER, end_char INTEGER) WITHOUT ROWID;
CREATE INDEX doc_nodes_parent ON doc_nodes(parent);
CREATE TABLE doc_explains (chunk_id TEXT NOT NULL, fqn TEXT NOT NULL, PRIMARY KEY (chunk_id, fqn)) WITHOUT ROWID;
INSERT INTO doc_nodes (id, type, rpath) VALUES ('doc1', 'document', 'docs/guide.md');
`
	if _, err := db.Exec(initSQL); err != nil {
		t.Fatalf("initializing mock format-2 db: %v", err)
	}
	db.Close()

	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open existing format-2 db: %v", err)
	}
	ctx := context.Background()
	format, err := s.Meta(ctx, "store_format")
	if err != nil || format != "2" {
		t.Fatalf("store_format = %q, %v; want 2", format, err)
	}
	step, err := s.Meta(ctx, "migration:doc_nodes_rpath")
	if err != nil || step != "1" {
		t.Fatalf("migration:doc_nodes_rpath = %q, %v; want 1", step, err)
	}
	for _, idx := range []string{"doc_nodes_rpath", "doc_nodes_type"} {
		var n int
		if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&n); err != nil || n != 1 {
			t.Fatalf("index %s in sqlite_master = %d, err = %v; want 1", idx, n, err)
		}
	}
	// The indexed lookup finds the pre-existing row.
	var id string
	if err := s.reader.QueryRowContext(ctx, `SELECT id FROM doc_nodes WHERE rpath = ?`, "docs/guide.md").Scan(&id); err != nil || id != "doc1" {
		t.Fatalf("rpath lookup = %q, %v; want doc1", id, err)
	}

	// Reopen: the step is recorded, so it is not applied again.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("reopening migrated format-2 db: %v", err)
	}
	defer s2.Close()
	format2, err := s2.Meta(ctx, "store_format")
	if err != nil || format2 != "2" {
		t.Fatalf("store_format after reopen = %q, %v; want 2", format2, err)
	}
}
