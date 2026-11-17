// Package store implements the format-2 SQLite record store for one index
// (RFC 003, docs/plans/003-storage.md): passages, file manifest, metadata and
// the persisted lexical index in a single WAL database, with stable passage
// IDs and one transaction per source file. It is not wired into gleann yet.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	// Pure-Go SQLite driver (registers as "sqlite"); FTS5 is part of its
	// default build, so no build tags or CGo are needed (RFC 003 section 5).
	"modernc.org/sqlite"
)

// ErrUnsupportedFormat is returned (wrapped) by Open for a database whose
// store_format is newer than this build understands.
var ErrUnsupportedFormat = errors.New("unsupported store_format")

// Options controls Open. The zero value is usable.
type Options struct {
	// Log receives warnings, e.g. the network-file-system fallback.
	// Defaults to slog.Default().
	Log *slog.Logger

	// NetworkFS reports whether a path lives on a network file system.
	// It defaults to the platform detection (statfs on linux/darwin, UNC
	// prefix on windows). WAL relies on shared memory and does not work on
	// network file systems, so a true result switches the store to
	// journal_mode=DELETE with a warning (RFC 003 section 3.6). Exposed as a
	// function so tests can mock the file-system type.
	NetworkFS func(path string) bool
}

// Store is one index database: a single writer connection and a reader pool.
// It implements Passages.
type Store struct {
	path    string
	writer  *sql.DB
	reader  *sql.DB
	log     *slog.Logger
	lexRows atomic.Int64
}

var _ Passages = (*Store)(nil)

// Open opens (or creates) the index database at path, applies the pragmas
// from RFC 003 section 3.6 and runs any pending schema migrations. The parent
// directory is created when missing. Open does not take the per-index build
// lock: searches and small writes must not queue behind a sync (section 3.6);
// builds and syncs take AcquireLock separately.
func Open(path string, opts Options) (*Store, error) {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating index directory for %s: %w", path, err)
	}

	// WAL needs shared memory, which network file systems do not provide
	// reliably. Fall back to DELETE and warn that multi-process use there is
	// unsupported (RFC 003 section 3.6; the fallback is not measured).
	journalMode := "WAL"
	networkFS := opts.NetworkFS
	if networkFS == nil {
		networkFS = isNetworkFS
	}
	if networkFS(filepath.Dir(path)) {
		journalMode = "DELETE"
		log.Warn("index database is on a network file system; using journal_mode=DELETE instead of WAL",
			"path", path)
		log.Warn("concurrent access from several processes is not safe on a network file system; this location is unsupported for multi-process use",
			"path", path)
	}

	// The pragmas ride on the DSN so every pooled connection gets them:
	// busy_timeout and synchronous are per-connection settings. journal_mode
	// is set on the writer only: it persists in the database file, and
	// setting it from every fresh reader connection would widen the
	// first-open race between processes.
	dsn := path + "?_pragma=busy_timeout(10000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)"

	// One writer connection, so this process never contends with itself, and
	// BEGIN IMMEDIATE (_txlock=immediate) so a write transaction takes the
	// write lock up front instead of failing on a lock upgrade that
	// busy_timeout cannot retry.
	writer, err := sql.Open("sqlite", dsn+"&_pragma=journal_mode("+journalMode+")&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("opening writer for %s: %w", path, err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)

	reader, err := sql.Open("sqlite", dsn)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("opening reader for %s: %w", path, err)
	}

	s := &Store{path: path, writer: writer, reader: reader, log: log}
	if err := retryBusy(func() error { return writer.PingContext(context.Background()) }); err != nil {
		s.Close()
		return nil, fmt.Errorf("connecting writer to %s: %w", path, err)
	}
	if err := retryBusy(func() error { return migrate(writer) }); err != nil {
		s.Close()
		return nil, fmt.Errorf("migrating %s: %w", path, err)
	}
	return s, nil
}

// retryBusy retries fn while it fails with SQLITE_BUSY or SQLITE_LOCKED,
// for the same 10 s budget as busy_timeout. busy_timeout covers ordinary
// contention, but two processes initialising a brand-new WAL database at
// the same moment can hit a creation race the busy handler cannot resolve;
// retrying the open is the fix.
func retryBusy(fn func() error) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := fn()
		if err == nil || !isBusyErr(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func isBusyErr(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		// The driver enables extended result codes, so a busy error can be
		// SQLITE_BUSY_RECOVERY (261), SQLITE_BUSY_SNAPSHOT (517) and so on.
		// The primary code is the low byte.
		const sqliteBusy, sqliteLocked = 5, 6
		primary := se.Code() & 0xFF
		return primary == sqliteBusy || primary == sqliteLocked
	}
	return false
}

// Close closes both connection pools. It is safe to call more than once.
func (s *Store) Close() error {
	return errors.Join(s.writer.Close(), s.reader.Close())
}

// migration is one schema step, identified by its step name and the
// meta.store_format value it leaves behind. Migrations run in order, each in
// its own transaction. A step carries either static sql or an apply func for
// logic that must inspect the existing schema first (e.g. converting a table
// only when it is still in the old shape); never both.
type migration struct {
	name   string
	format string
	sql    string
	apply  func(tx *sql.Tx) error
}

// Meta keys of the graph row counters maintained by internal/graphstore on
// every write (ApplyFileDelta, RemoveFile, ReplaceAll) so Stats answers from
// point lookups instead of scanning the edges table. The graph_counts step
// seeds them from the current rows; graphstore keeps them exact afterwards.
const (
	MetaGraphSymbols     = "graph_count:symbols"
	MetaGraphEdgesPrefix = "graph_count:edges:"
)

const migrationLexVocab = `CREATE VIRTUAL TABLE IF NOT EXISTS lex_vocab USING fts5vocab(lex, 'row');`

// migrationDocNodesIndexes adds the document-lookup indexes to doc_nodes:
// rpath serves findDocument's physical-path lookup (without it every lookup
// scans the table) and type serves the type filters of findDocument and
// ListDocuments.
const migrationDocNodesIndexes = `CREATE INDEX IF NOT EXISTS doc_nodes_rpath ON doc_nodes(rpath);
CREATE INDEX IF NOT EXISTS doc_nodes_type ON doc_nodes(type);`

// migrations is the table-driven schema history. Format 2 creates the full
// RFC 003 section 3.4 schema, including the graph and document tables, which
// stay empty until the graph is ported (section 6.6), so a later release
// needs no schema migration to populate them. Format "2" stays across the
// migration steps of release 1 (RFC 003 section 4.2 step 3).
var migrations = []migration{
	{name: "format_2", format: "2", sql: schemaFormat2},
	{name: "lex_vocab", format: "2", sql: migrationLexVocab},
	{name: "doc_nodes_rpath", format: "2", sql: migrationDocNodesIndexes},
	{name: "symbols_fts", format: "2", apply: migrateSymbolsFTS},
	{name: "graph_counts", format: "2", apply: migrateGraphCounts},
	{name: "edges_file_drop", format: "2", sql: migrationEdgesFileDrop},
	{name: "graph_ids", format: "2", apply: migrateGraphIDs},
}

// migrationEdgesFileDrop removes the redundant edges_file index. The owner
// invariant (graphstore.checkSources: an edge's src must be declared in its
// owning file) makes edges.file functionally dependent on src, so every
// per-file edge lookup is served by `src IN (SELECT fqn FROM symbols WHERE
// file = ?)` over the edges primary key and symbols_file instead. The index
// repeated the whole primary key (file, src, dst, kind) per edge — roughly a
// fifth of the graph's disk — for no query the primary key cannot answer.
// The edges.file column stays (retraction owner, and callers filter on it).
const migrationEdgesFileDrop = `DROP INDEX IF EXISTS edges_file;`

// schemaSymbolsRowid is the symbols table in its rowid shape: an INTEGER
// PRIMARY KEY gives every declaration a stable integer handle (the FTS5
// external-content rowid and the suffix of the secondary indexes, which
// shrinks them from the full fqn to a varint), while fqn stays UNIQUE so
// every existing lookup and the one-anchor invariant are unchanged.
const schemaSymbolsRowid = `
CREATE TABLE symbols (
  id          INTEGER PRIMARY KEY,
  fqn         TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  kind        TEXT NOT NULL,
  file        TEXT NOT NULL,
  start_line  INTEGER NOT NULL,
  end_line    INTEGER NOT NULL,
  start_col   INTEGER NOT NULL,
  byte_offset INTEGER NOT NULL,
  is_test     INTEGER NOT NULL DEFAULT 0,
  weight      REAL NOT NULL DEFAULT 1.0,
  doc         TEXT NOT NULL DEFAULT ''
);`

// migrationSymbolsToRowid converts an existing WITHOUT ROWID symbols table
// (fqn PRIMARY KEY) to the rowid shape, preserving every row and ordering the
// copy by fqn so ids follow lexical order. The old secondary indexes are
// dropped with the table and recreated by migrateSymbolsFTS.
const migrationSymbolsToRowid = `
CREATE TABLE symbols_new (
  id          INTEGER PRIMARY KEY,
  fqn         TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  kind        TEXT NOT NULL,
  file        TEXT NOT NULL,
  start_line  INTEGER NOT NULL,
  end_line    INTEGER NOT NULL,
  start_col   INTEGER NOT NULL,
  byte_offset INTEGER NOT NULL,
  is_test     INTEGER NOT NULL DEFAULT 0,
  weight      REAL NOT NULL DEFAULT 1.0,
  doc         TEXT NOT NULL DEFAULT ''
);
INSERT INTO symbols_new (fqn, name, kind, file, start_line, end_line, start_col, byte_offset, is_test, weight, doc)
  SELECT fqn, name, kind, file, start_line, end_line, start_col, byte_offset, is_test, weight, doc
  FROM symbols ORDER BY fqn;
DROP TABLE symbols;
ALTER TABLE symbols_new RENAME TO symbols;`

// SymbolsFTSTriggers keeps symbols_fts in sync with symbols on every
// insert/delete/update. graphstore's bulk rebuild drops them while loading and
// recreates them from this same definition before running 'rebuild'.
const SymbolsFTSTriggers = `
CREATE TRIGGER IF NOT EXISTS symbols_fts_insert AFTER INSERT ON symbols BEGIN
  INSERT INTO symbols_fts(rowid, fqn, name) VALUES (new.id, new.fqn, new.name);
END;
CREATE TRIGGER IF NOT EXISTS symbols_fts_delete AFTER DELETE ON symbols BEGIN
  INSERT INTO symbols_fts(symbols_fts, rowid, fqn, name) VALUES('delete', old.id, old.fqn, old.name);
END;
CREATE TRIGGER IF NOT EXISTS symbols_fts_update AFTER UPDATE ON symbols BEGIN
  INSERT INTO symbols_fts(symbols_fts, rowid, fqn, name) VALUES('delete', old.id, old.fqn, old.name);
  INSERT INTO symbols_fts(rowid, fqn, name) VALUES (new.id, new.fqn, new.name);
END;`

// migrationSymbolsFTSCreate adds the trigram full-text index over
// symbols(fqn, name) for case-insensitive substring search (Symbols), as an
// external-content FTS5 table so the text is stored once, in symbols.
const migrationSymbolsFTSCreate = `
CREATE VIRTUAL TABLE IF NOT EXISTS symbols_fts USING fts5(
  fqn, name,
  content = 'symbols',
  content_rowid = 'id',
  tokenize = 'trigram'
);` + SymbolsFTSTriggers

// migrateSymbolsFTS brings symbols to the rowid shape (creating it fresh when
// a pre-graph format-2 database has no symbols table at all) and adds the
// trigram FTS index. It is idempotent: an already-converted table is left
// alone and the FTS objects use IF NOT EXISTS.
func migrateSymbolsFTS(tx *sql.Tx) error {
	exists, err := tableExists(tx, "symbols")
	if err != nil {
		return err
	}
	if !exists {
		if _, err := tx.Exec(schemaSymbolsRowid); err != nil {
			return fmt.Errorf("creating symbols: %w", err)
		}
	} else {
		hasID, err := tableHasColumn(tx, "symbols", "id")
		if err != nil {
			return err
		}
		if !hasID {
			if _, err := tx.Exec(migrationSymbolsToRowid); err != nil {
				return fmt.Errorf("converting symbols to rowid: %w", err)
			}
		}
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS symbols_file ON symbols(file);
CREATE INDEX IF NOT EXISTS symbols_name ON symbols(name);`); err != nil {
		return fmt.Errorf("creating symbols indexes: %w", err)
	}
	if _, err := tx.Exec(migrationSymbolsFTSCreate); err != nil {
		return fmt.Errorf("creating symbols_fts: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO symbols_fts(symbols_fts) VALUES('rebuild')`); err != nil {
		return fmt.Errorf("rebuilding symbols_fts: %w", err)
	}
	return nil
}

// migrateGraphCounts seeds the graph row counters from the current rows so
// Stats answers from meta point lookups. graphstore maintains them on every
// write afterwards; a database with no graph tables yet gets zero counters.
func migrateGraphCounts(tx *sql.Tx) error {
	symCount, err := countIfTableExists(tx, "symbols", "")
	if err != nil {
		return err
	}
	if err := upsertMeta(tx, MetaGraphSymbols, symCount); err != nil {
		return err
	}
	for _, kind := range []string{"CALLS", "IMPLEMENTS", "REFERENCES"} {
		n, err := countIfTableExists(tx, "edges", kind)
		if err != nil {
			return err
		}
		if err := upsertMeta(tx, MetaGraphEdgesPrefix+kind, n); err != nil {
			return err
		}
	}
	return nil
}

func tableExists(tx *sql.Tx, name string) (bool, error) {
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		return false, fmt.Errorf("checking table %s: %w", name, err)
	}
	return n > 0, nil
}

func tableHasColumn(tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("reading %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("reading %s columns: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// countIfTableExists counts rows of table (optionally filtered to one edge
// kind), returning 0 when the table does not exist yet.
func countIfTableExists(tx *sql.Tx, table, kind string) (int64, error) {
	exists, err := tableExists(tx, table)
	if err != nil || !exists {
		return 0, err
	}
	var n int64
	q := `SELECT count(*) FROM ` + table
	args := []any{}
	if kind != "" {
		q += ` WHERE kind = ?`
		args = append(args, kind)
	}
	if err := tx.QueryRow(q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting %s: %w", table, err)
	}
	return n, nil
}

func upsertMeta(tx *sql.Tx, key string, n int64) error {
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
  ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, strconv.FormatInt(n, 10)); err != nil {
		return fmt.Errorf("recording %s: %w", key, err)
	}
	return nil
}

// migrate brings the database up to the newest known format. An unknown
// store_format is refused rather than guessed at.
func migrate(w *sql.DB) error {
	// Bootstrap the meta table so the format can be read before any
	// migration has run; the migration itself uses IF NOT EXISTS.
	if _, err := w.Exec(`CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID`); err != nil {
		return fmt.Errorf("creating meta table: %w", err)
	}
	var format string
	err := w.QueryRow(`SELECT value FROM meta WHERE key = 'store_format'`).Scan(&format)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("reading store_format: %w", err)
	}

	if format != "" && format != "2" {
		return fmt.Errorf("%w %q; this binary understands formats up to %q", ErrUnsupportedFormat, format, "2")
	}

	for _, m := range migrations {
		// Check with a plain read first: a write transaction per step on
		// every open would queue each searcher behind a long rebuild that
		// holds the write lock. applyMigration re-checks under the lock.
		done, err := migrationDone(w, m, format)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		if err := applyMigration(w, m); err != nil {
			return fmt.Errorf("applying migration step %s to format %s: %w", m.name, m.format, err)
		}
	}
	return nil
}

// migrationDone reports, without taking the write lock, whether step m is
// already applied. Format 2 databases created before step tracking count as
// having run format_2.
func migrationDone(w *sql.DB, m migration, format string) (bool, error) {
	if m.name == "format_2" && format == "2" {
		return true, nil
	}
	var one int
	err := w.QueryRow(`SELECT 1 FROM meta WHERE key = ?`, "migration:"+m.name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking migration step %s: %w", m.name, err)
	}
	return true, nil
}

func applyMigration(w *sql.DB, m migration) error {
	tx, err := w.Begin() // BEGIN IMMEDIATE: holds the write lock from here
	if err != nil {
		return fmt.Errorf("beginning migration transaction: %w", err)
	}
	defer tx.Rollback()
	// Re-read the format under the write lock. Two processes opening a new
	// database at the same moment both read "no format" in migrate; the
	// second must see that the first has already applied this step instead
	// of failing on "table files already exists".
	var current string
	err = tx.QueryRow(`SELECT value FROM meta WHERE key = 'store_format'`).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("re-reading store_format: %w", err)
	}
	if current != "" && current != "2" {
		return fmt.Errorf("%w %q; this binary understands formats up to %q", ErrUnsupportedFormat, current, "2")
	}

	// For an existing database that already ran format_2 before step tracking,
	// format_2 is considered applied when store_format is "2".
	if m.name == "format_2" && current == "2" {
		if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('migration:format_2', '1')
  ON CONFLICT(key) DO NOTHING`); err != nil {
			return fmt.Errorf("recording migration step %s: %w", m.name, err)
		}
		return tx.Commit()
	}

	var stepDone int
	err = tx.QueryRow(`SELECT 1 FROM meta WHERE key = ?`, "migration:"+m.name).Scan(&stepDone)
	if err == nil && stepDone == 1 {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("checking migration step %s: %w", m.name, err)
	}

	if m.apply != nil {
		if err := m.apply(tx); err != nil {
			return fmt.Errorf("applying schema step: %w", err)
		}
	} else if _, err := tx.Exec(m.sql); err != nil {
		return fmt.Errorf("executing schema: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('store_format', ?)
  ON CONFLICT(key) DO UPDATE SET value = excluded.value`, m.format); err != nil {
		return fmt.Errorf("recording store_format: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, '1')
  ON CONFLICT(key) DO UPDATE SET value = '1'`, "migration:"+m.name); err != nil {
		return fmt.Errorf("recording migration step %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration: %w", err)
	}
	return nil
}

// schemaFormat2 is the complete index schema of RFC 003 section 3.4, created
// by the first format-2 release. Graph and document tables stay empty until
// the graph is ported; they are derived data and can be dropped and rebuilt
// if RFC 001 changes their columns before then.
const schemaFormat2 = `
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID;
-- keys: store_format ("2"), analyzer ("gleann-tok/1"), language, vector_generation, ...

-- one row per indexed source file (replaces <name>.manifest.json and the graph hash store)
CREATE TABLE files (
  path       TEXT PRIMARY KEY,
  size       INTEGER NOT NULL,
  mtime_ns   INTEGER NOT NULL,
  hash       TEXT NOT NULL,          -- same value as ManifestEntry.Hash
  graph_hash TEXT,                   -- hash the graph rows were built from
  graph_dirty INTEGER NOT NULL DEFAULT 0,
  indexed_at INTEGER NOT NULL
) WITHOUT ROWID;
-- files whose graph rows need re-extraction (6.5); partial, so the scan is cheap
CREATE INDEX files_dirty ON files(path) WHERE graph_dirty = 1;

CREATE TABLE passages (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,  -- internal, never reused; FTS rowid
  pid       TEXT NOT NULL UNIQUE,               -- stable ID (pid.go)
  source    TEXT NOT NULL,
  chunk_no  INTEGER NOT NULL,
  vec_slot  INTEGER,                            -- position in the vector file; NULL = lexical only
  kind      TEXT NOT NULL DEFAULT 'code',       -- code | prose; selects the analyzer rules (4.1)
  text      TEXT NOT NULL,
  meta      TEXT NOT NULL DEFAULT '{}'          -- remaining Passage.Metadata as JSON
);
CREATE INDEX passages_source ON passages(source, chunk_no);
CREATE UNIQUE INDEX passages_slot ON passages(vec_slot) WHERE vec_slot IS NOT NULL;

-- contentless: the text is stored once, in passages
CREATE VIRTUAL TABLE lex USING fts5(
  tokens,
  content = '',
  contentless_delete = 1,
  tokenize = "unicode61 remove_diacritics 0 tokenchars '_'"
);
-- lex.rowid = passages.id; tokens = space-joined output of the gleann analyzer

-- term vocabulary for document-frequency cut (RFC 003 section 4.2 step 3)
CREATE VIRTUAL TABLE IF NOT EXISTS lex_vocab USING fts5vocab(lex, 'row');


-- first-party declarations only; external symbols are never rows here
CREATE TABLE symbols (
  fqn         TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  kind        TEXT NOT NULL,
  file        TEXT NOT NULL,         -- declaration anchor (RFC 001: no phantom symbols)
  start_line  INTEGER NOT NULL,
  end_line    INTEGER NOT NULL,
  start_col   INTEGER NOT NULL,
  byte_offset INTEGER NOT NULL,
  is_test     INTEGER NOT NULL DEFAULT 0,
  weight      REAL NOT NULL DEFAULT 1.0,
  doc         TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
CREATE INDEX symbols_file ON symbols(file);
CREATE INDEX symbols_name ON symbols(name);

CREATE TABLE edges (
  src        TEXT NOT NULL,
  dst        TEXT NOT NULL,
  kind       TEXT NOT NULL,          -- CALLS | IMPLEMENTS | REFERENCES
  file       TEXT NOT NULL,          -- file that contains the reference (retraction owner)
  dst_external INTEGER NOT NULL DEFAULT 0, -- 1: dst is a third-party or standard-library symbol with no row in symbols
  confidence TEXT NOT NULL DEFAULT 'extracted',
  PRIMARY KEY (src, dst, kind, file)
) WITHOUT ROWID;
CREATE INDEX edges_dst  ON edges(dst, kind);
CREATE INDEX edges_file ON edges(file);

-- document hierarchy (hierarchical GraphRAG), plain tables
CREATE TABLE doc_nodes (
  id TEXT PRIMARY KEY, type TEXT NOT NULL,       -- folder | document | heading | chunk
  parent TEXT, name TEXT, level INTEGER,
  rpath TEXT, hash TEXT, summary TEXT,
  text TEXT, start_char INTEGER, end_char INTEGER
) WITHOUT ROWID;
CREATE INDEX doc_nodes_parent ON doc_nodes(parent);
-- document lookups by physical path and by node type (findDocument, ListDocuments)
CREATE INDEX doc_nodes_rpath ON doc_nodes(rpath);
CREATE INDEX doc_nodes_type ON doc_nodes(type);
CREATE TABLE doc_explains (chunk_id TEXT NOT NULL, fqn TEXT NOT NULL, PRIMARY KEY (chunk_id, fqn)) WITHOUT ROWID;
`
