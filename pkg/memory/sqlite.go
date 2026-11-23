package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/store"

	// Pure-Go SQLite driver (registers as "sqlite"); FTS5 is part of its
	// default build, so no build tags or CGo are needed (RFC 003 section 5).
	"modernc.org/sqlite"
)

// memorySchema is the memory database schema of RFC 003 section 3.5.
// blocks keeps an explicit INTEGER PRIMARY KEY because blocks_lex needs a
// stable integer rowid (implicit rowids may change on VACUUM).
const memorySchema = `
CREATE TABLE IF NOT EXISTS blocks (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE, tier TEXT NOT NULL, label TEXT NOT NULL, content TEXT NOT NULL,
  source TEXT NOT NULL, tags TEXT NOT NULL DEFAULT '[]', metadata TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, expires_at INTEGER,
  char_limit INTEGER NOT NULL DEFAULT 0, scope TEXT NOT NULL DEFAULT '',
  repo TEXT NOT NULL DEFAULT '', paths TEXT NOT NULL DEFAULT '[]', symbols TEXT NOT NULL DEFAULT '[]',
  commit_hash TEXT NOT NULL DEFAULT '', suspect INTEGER NOT NULL DEFAULT 0, stale_reason TEXT NOT NULL DEFAULT '',
  confirms INTEGER NOT NULL DEFAULT 0, conflicts INTEGER NOT NULL DEFAULT 0,
  access_count INTEGER NOT NULL DEFAULT 0, last_accessed_at INTEGER
);
CREATE INDEX IF NOT EXISTS blocks_tier ON blocks(tier);
CREATE INDEX IF NOT EXISTS blocks_scope_repo ON blocks(scope, repo);
CREATE VIRTUAL TABLE IF NOT EXISTS blocks_lex USING fts5(tokens, content = '', contentless_delete = 1,
  tokenize = "unicode61 remove_diacritics 0 tokenchars '_'");   -- rowid = blocks.seq

CREATE TABLE IF NOT EXISTS summaries (key TEXT PRIMARY KEY, data TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS memory_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;
`

// migrationEntityGraph adds the entity graph tables of RFC 003 section 3.5:
// entities, relations and entity_archive. ns is the index namespace.
const migrationEntityGraph = `
CREATE TABLE IF NOT EXISTS entities (
  ns TEXT NOT NULL,
  id TEXT NOT NULL,
  type TEXT NOT NULL,
  content TEXT NOT NULL,
  attributes TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (ns, id)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS relations (
  ns TEXT NOT NULL,
  src TEXT NOT NULL,
  dst TEXT NOT NULL,
  relation_type TEXT NOT NULL,
  weight REAL NOT NULL DEFAULT 1.0,
  attributes TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (ns, src, dst, relation_type)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS relations_dst ON relations(ns, dst);

CREATE TABLE IF NOT EXISTS entity_archive (
  archive_id TEXT PRIMARY KEY,
  ns TEXT NOT NULL,
  id TEXT NOT NULL,
  version_id TEXT NOT NULL,
  type TEXT,
  content TEXT,
  attributes TEXT,
  valid_to TEXT
) WITHOUT ROWID;
`

type memoryMigration struct {
	name   string
	format string
	sql    string
}

// migrationReflectionRuns records each sleep-time reflection run as a change
// set, so `gleann memory reflect --revert <run-id>` can remove what it added.
const migrationReflectionRuns = `
CREATE TABLE IF NOT EXISTS reflection_runs (
  run_id     TEXT PRIMARY KEY,
  created_at INTEGER NOT NULL,
  data       TEXT NOT NULL
) WITHOUT ROWID;
`

var memoryMigrations = []memoryMigration{
	{name: "format_2", format: "2", sql: memorySchema},
	{name: "entity_graph", format: "2", sql: migrationEntityGraph},
	{name: "reflection_runs", format: "2", sql: migrationReflectionRuns},
}

// blockCols is the column list shared by every block read and write, in the
// order scanBlock and blockArgs use. seq is internal and handled separately.
const blockCols = `id, tier, label, content, source, tags, metadata,
  created_at, updated_at, expires_at, char_limit, scope, repo, paths, symbols,
  commit_hash, suspect, stale_reason, confirms, conflicts, access_count, last_accessed_at`

// openSQLite opens (or creates) the memory database at path with the pragmas
// of RFC 003 section 3.6: WAL (DELETE with a warning on network file systems),
// busy_timeout 10 s, synchronous NORMAL, one writer connection with BEGIN
// IMMEDIATE (_txlock=immediate) and a reader pool. The schema is created in
// one idempotent transaction, so several processes opening a brand-new
// database at the same moment all succeed.
func openSQLite(path string) (*Store, error) {
	// WAL needs shared memory, which network file systems do not provide
	// reliably. Fall back to DELETE and warn that multi-process use there is
	// unsupported (RFC 003 section 3.6; the fallback is not measured).
	journalMode := "WAL"
	if store.IsNetworkFS(filepath.Dir(path)) {
		journalMode = "DELETE"
		slog.Warn("memory database is on a network file system; using journal_mode=DELETE instead of WAL",
			"path", path)
		slog.Warn("concurrent access from several processes is not safe on a network file system; this location is unsupported for multi-process use",
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
	// BEGIN IMMEDIATE so a write transaction takes the write lock up front
	// instead of failing on a lock upgrade that busy_timeout cannot retry.
	writer, err := sql.Open("sqlite", dsn+"&_pragma=journal_mode("+journalMode+")&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("opening memory writer for %s: %w", path, err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)

	reader, err := sql.Open("sqlite", dsn)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("opening memory reader for %s: %w", path, err)
	}

	s := &Store{sqlitePath: path, writer: writer, reader: reader, analyzer: analysis.New("")}
	if err := retryBusy(func() error { return writer.PingContext(context.Background()) }); err != nil {
		s.Close()
		return nil, fmt.Errorf("connecting memory writer to %s: %w", path, err)
	}
	if err := retryBusy(func() error { return s.createSchema() }); err != nil {
		s.Close()
		return nil, fmt.Errorf("creating memory schema in %s: %w", path, err)
	}
	if err := retryBusy(func() error { return s.migrateStoredScopes() }); err != nil {
		s.Close()
		return nil, fmt.Errorf("normalizing stored memory scopes in %s: %w", path, err)
	}
	return s, nil
}

// createSchema applies memoryMigrations and records each named step in memory_meta.
// Every statement is idempotent (IF NOT EXISTS / ON CONFLICT), so concurrent first
// opens do not need to re-check the format under the write lock.
func (s *Store) createSchema() error {
	// Bootstrap the memory_meta table so migrations can be checked.
	if _, err := s.writer.Exec(`CREATE TABLE IF NOT EXISTS memory_meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID`); err != nil {
		return fmt.Errorf("creating memory_meta table: %w", err)
	}

	var format string
	err := s.writer.QueryRow(`SELECT value FROM memory_meta WHERE key = 'store_format'`).Scan(&format)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("reading store_format: %w", err)
	}
	if format != "" && format != "2" {
		return fmt.Errorf("unsupported store_format %q; this binary understands formats up to %q", format, "2")
	}

	for _, m := range memoryMigrations {
		done, err := s.memoryMigrationDone(m, format)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		if err := s.applyMemoryMigration(m); err != nil {
			return fmt.Errorf("applying memory migration step %s: %w", m.name, err)
		}
	}
	return nil
}

func (s *Store) memoryMigrationDone(m memoryMigration, format string) (bool, error) {
	if m.name == "format_2" && format == "2" {
		return true, nil
	}
	var one int
	err := s.writer.QueryRow(`SELECT 1 FROM memory_meta WHERE key = ?`, "migration:"+m.name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking memory migration step %s: %w", m.name, err)
	}
	return true, nil
}

func (s *Store) applyMemoryMigration(m memoryMigration) error {
	tx, err := s.writer.Begin() // BEGIN IMMEDIATE via _txlock
	if err != nil {
		return fmt.Errorf("beginning memory migration transaction: %w", err)
	}
	defer tx.Rollback()

	var current string
	err = tx.QueryRow(`SELECT value FROM memory_meta WHERE key = 'store_format'`).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("re-reading store_format: %w", err)
	}
	if current != "" && current != "2" {
		return fmt.Errorf("unsupported store_format %q; this binary understands formats up to %q", current, "2")
	}

	if m.name == "format_2" && current == "2" {
		if _, err := tx.Exec(`INSERT INTO memory_meta (key, value) VALUES ('migration:format_2', '1')
  ON CONFLICT(key) DO NOTHING`); err != nil {
			return fmt.Errorf("recording memory migration step %s: %w", m.name, err)
		}
		return tx.Commit()
	}

	var stepDone int
	err = tx.QueryRow(`SELECT 1 FROM memory_meta WHERE key = ?`, "migration:"+m.name).Scan(&stepDone)
	if err == nil && stepDone == 1 {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("checking memory migration step %s: %w", m.name, err)
	}

	if _, err := tx.Exec(m.sql); err != nil {
		return fmt.Errorf("executing memory migration %s: %w", m.name, err)
	}
	if _, err := tx.Exec(`INSERT INTO memory_meta (key, value) VALUES ('store_format', ?)
  ON CONFLICT(key) DO UPDATE SET value = excluded.value`, m.format); err != nil {
		return fmt.Errorf("recording store_format: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO memory_meta (key, value) VALUES (?, '1')
  ON CONFLICT(key) DO UPDATE SET value = '1'`, "migration:"+m.name); err != nil {
		return fmt.Errorf("recording memory migration step %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing memory migration %s: %w", m.name, err)
	}
	return nil
}

// retryBusy retries fn while it fails with SQLITE_BUSY or SQLITE_LOCKED, for
// the same 10 s budget as busy_timeout. busy_timeout covers ordinary
// contention, but two processes initialising a brand-new WAL database at the
// same moment can hit a creation race the busy handler cannot resolve;
// retrying the open is the fix (same pattern as internal/store).
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

// writeTx runs fn inside one BEGIN IMMEDIATE transaction (the writer DSN
// sets _txlock=immediate). Reads and writes of one operation — notably Add's
// deduplication — stay in the same transaction so concurrent agent processes
// cannot interleave them (RFC 003 section 3.5, last bullet).
func (s *Store) writeTx(fn func(tx *sql.Tx) error) error {
	var tx *sql.Tx
	err := retryBusy(func() error {
		var bErr error
		tx, bErr = s.writer.Begin()
		return bErr
	})
	if err != nil {
		return fmt.Errorf("beginning memory write: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing memory write: %w", err)
	}
	return nil
}

// EntityNamespaceEmpty reports whether the given index namespace has no rows in entities.
func (s *Store) EntityNamespaceEmpty(ns string) (bool, error) {
	var count int
	err := s.reader.QueryRow(`SELECT count(*) FROM entities WHERE ns = ?`, ns).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking entity namespace %s: %w", ns, err)
	}
	return count == 0, nil
}

// ── Row mapping ───────────────────────────────────────────────────

// Timestamps are stored as Unix nanoseconds (RFC 003 section 3.5 INTEGER
// columns). nsToTime restores the zero time.Time exactly, so a zero
// UpdatedAt keeps IsZero() true after a round trip, as it did with the bbolt
// JSON encoding.
var zeroTimeNS = time.Time{}.UnixNano()

func nsToTime(ns int64) time.Time {
	if ns == zeroTimeNS {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

func timeToNS(t time.Time) int64 {
	return t.UnixNano()
}

type rowScanner interface {
	Scan(dest ...any) error
}

// blockScan holds the raw column values of one blocks row (blockCols order)
// and decodes them into a Block. The JSON columns round-trip nil slices and
// maps exactly as the old whole-block JSON did: "null" decodes back to nil.
type blockScan struct {
	id, tier, label, content, source string
	tags, metadata, paths, symbols   string
	createdAt, updatedAt             int64
	expiresAt, lastAccessedAt        sql.NullInt64
	charLimit                        int
	scope, repo, commit, staleReason string
	suspect                          int
	confirms, conflicts, accessCount int
}

func (sc *blockScan) dest() []any {
	return []any{&sc.id, &sc.tier, &sc.label, &sc.content, &sc.source, &sc.tags, &sc.metadata,
		&sc.createdAt, &sc.updatedAt, &sc.expiresAt, &sc.charLimit, &sc.scope, &sc.repo,
		&sc.paths, &sc.symbols, &sc.commit, &sc.suspect, &sc.staleReason,
		&sc.confirms, &sc.conflicts, &sc.accessCount, &sc.lastAccessedAt}
}

func (sc *blockScan) decode() (Block, error) {
	b := Block{
		ID: sc.id, Tier: Tier(sc.tier), Label: sc.label, Content: sc.content, Source: sc.source,
		CharLimit: sc.charLimit, Scope: sc.scope, Repo: sc.repo, Commit: sc.commit,
		StaleReason: sc.staleReason, Suspect: sc.suspect != 0,
		Confirms: sc.confirms, Conflicts: sc.conflicts, AccessCount: sc.accessCount,
	}
	for _, field := range []struct {
		name string
		raw  string
		dst  any
	}{
		{"tags", sc.tags, &b.Tags},
		{"metadata", sc.metadata, &b.Metadata},
		{"paths", sc.paths, &b.Paths},
		{"symbols", sc.symbols, &b.Symbols},
	} {
		if err := json.Unmarshal([]byte(field.raw), field.dst); err != nil {
			return b, fmt.Errorf("decoding block %s %s: %w", b.ID, field.name, err)
		}
	}
	b.CreatedAt = nsToTime(sc.createdAt)
	b.UpdatedAt = nsToTime(sc.updatedAt)
	if sc.expiresAt.Valid {
		t := nsToTime(sc.expiresAt.Int64)
		b.ExpiresAt = &t
	}
	if sc.lastAccessedAt.Valid {
		t := nsToTime(sc.lastAccessedAt.Int64)
		b.LastAccessedAt = &t
	}
	return b, nil
}

// scanBlock reads one blocks row (blockCols order) into a Block.
func scanBlock(r rowScanner) (Block, error) {
	var sc blockScan
	if err := r.Scan(sc.dest()...); err != nil {
		return Block{}, err
	}
	return sc.decode()
}

// blockArgs returns the blockCols values for b, in the same order scanBlock
// reads them.
func blockArgs(b *Block) ([]any, error) {
	tags, err := json.Marshal(b.Tags)
	if err != nil {
		return nil, fmt.Errorf("encoding block %s tags: %w", b.ID, err)
	}
	metadata, err := json.Marshal(b.Metadata)
	if err != nil {
		return nil, fmt.Errorf("encoding block %s metadata: %w", b.ID, err)
	}
	paths, err := json.Marshal(b.Paths)
	if err != nil {
		return nil, fmt.Errorf("encoding block %s paths: %w", b.ID, err)
	}
	symbols, err := json.Marshal(b.Symbols)
	if err != nil {
		return nil, fmt.Errorf("encoding block %s symbols: %w", b.ID, err)
	}
	var expiresAt, lastAccessedAt any
	if b.ExpiresAt != nil {
		expiresAt = timeToNS(*b.ExpiresAt)
	}
	if b.LastAccessedAt != nil {
		lastAccessedAt = timeToNS(*b.LastAccessedAt)
	}
	suspect := 0
	if b.Suspect {
		suspect = 1
	}
	return []any{
		b.ID, string(b.Tier), b.Label, b.Content, b.Source, string(tags), string(metadata),
		timeToNS(b.CreatedAt), timeToNS(b.UpdatedAt), expiresAt, b.CharLimit, b.Scope, b.Repo,
		string(paths), string(symbols), b.Commit, suspect, b.StaleReason,
		b.Confirms, b.Conflicts, b.AccessCount, lastAccessedAt,
	}, nil
}

const insertBlockSQL = `INSERT INTO blocks (` + blockCols + `)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

const updateBlockSQL = `UPDATE blocks SET
  tier = ?, label = ?, content = ?, source = ?, tags = ?, metadata = ?,
  created_at = ?, updated_at = ?, expires_at = ?, char_limit = ?, scope = ?, repo = ?,
  paths = ?, symbols = ?, commit_hash = ?, suspect = ?, stale_reason = ?,
  confirms = ?, conflicts = ?, access_count = ?, last_accessed_at = ?
WHERE id = ?`

// insertBlockTx inserts b and returns its new seq.
func insertBlockTx(tx *sql.Tx, b *Block) (int64, error) {
	args, err := blockArgs(b)
	if err != nil {
		return 0, err
	}
	res, err := tx.Exec(insertBlockSQL, args...)
	if err != nil {
		return 0, fmt.Errorf("inserting block %s: %w", b.ID, err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("inserting block %s: reading seq: %w", b.ID, err)
	}
	return seq, nil
}

// updateBlockTx overwrites the row with b.ID; the caller has checked that it
// exists.
func updateBlockTx(tx *sql.Tx, b *Block) error {
	args, err := blockArgs(b)
	if err != nil {
		return err
	}
	// The UPDATE takes every column except id, then id as the WHERE argument.
	if _, err := tx.Exec(updateBlockSQL, append(args[1:], args[0])...); err != nil {
		return fmt.Errorf("updating block %s: %w", b.ID, err)
	}
	return nil
}

// blockSeqTx returns the seq of a block, or sql.ErrNoRows when it is absent.
func blockSeqTx(tx *sql.Tx, id string) (int64, error) {
	var seq int64
	err := tx.QueryRow(`SELECT seq FROM blocks WHERE id = ?`, id).Scan(&seq)
	return seq, err
}

// getBlockTx reads a block inside a write transaction, so Add's deduplication
// sees the same snapshot it writes (RFC 003 section 3.5). It returns nil when
// the block is absent.
func getBlockTx(tx rowQuerier, id string) (*Block, int64, error) {
	var sc blockScan
	var seq int64
	err := tx.QueryRow(`SELECT seq, `+blockCols+` FROM blocks WHERE id = ?`, id).
		Scan(append([]any{&seq}, sc.dest()...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("reading block %s: %w", id, err)
	}
	b, err := sc.decode()
	if err != nil {
		return nil, 0, err
	}
	return &b, seq, nil
}

// lexTokens is the text a block is indexed under in blocks_lex: the same
// searchText the in-RAM BM25 leg ranks over, tokenised by the gleann analyzer
// (RFC 003 section 4). Memory is natural language, so the prose kind applies.
// Reads still rank with modules/bm25 in this step; FTS recall is WP-4.1.
func (s *Store) lexTokens(b *Block) string {
	return strings.Join(s.analyzer.Tokens(searchText(b), analysis.KindProse), " ")
}

func insertLexTx(tx *sql.Tx, seq int64, tokens string) error {
	if _, err := tx.Exec(`INSERT INTO blocks_lex (rowid, tokens) VALUES (?, ?)`, seq, tokens); err != nil {
		return fmt.Errorf("inserting lexical row %d: %w", seq, err)
	}
	return nil
}

// refreshLexTx replaces the lexical row of an updated block (contentless
// FTS5 with contentless_delete=1 supports DELETE by rowid).
func refreshLexTx(tx *sql.Tx, seq int64, tokens string) error {
	if _, err := tx.Exec(`DELETE FROM blocks_lex WHERE rowid = ?`, seq); err != nil {
		return fmt.Errorf("deleting lexical row %d: %w", seq, err)
	}
	return insertLexTx(tx, seq, tokens)
}

// metaValue reads one memory_meta key; "" when the key is absent.
func (s *Store) metaValue(key string) (string, error) {
	var v string
	err := s.reader.QueryRow(`SELECT value FROM memory_meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading memory_meta %s: %w", key, err)
	}
	return v, nil
}

// Meta returns the value associated with key from memory_meta, or "" if not found.
func (s *Store) Meta(key string) (string, error) {
	return s.metaValue(key)
}

// SetMeta sets or updates a key-value pair in memory_meta.
func (s *Store) SetMeta(key, value string) error {
	return s.writeTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO memory_meta (key, value) VALUES (?, ?)
  ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
		return err
	})
}

// SaveChangeSet persists a reflection change set in the memory database.
func (s *Store) SaveChangeSet(cs *ChangeSet) error {
	data, err := json.Marshal(cs)
	if err != nil {
		return fmt.Errorf("encoding change set %s: %w", cs.RunID, err)
	}
	return s.writeTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO reflection_runs (run_id, created_at, data) VALUES (?, ?, ?)
  ON CONFLICT(run_id) DO UPDATE SET created_at = excluded.created_at, data = excluded.data`,
			cs.RunID, timeToNS(cs.Timestamp), string(data))
		return err
	})
}

// GetChangeSet loads a reflection change set by run ID.
func (s *Store) GetChangeSet(runID string) (*ChangeSet, error) {
	var data string
	err := s.reader.QueryRow(`SELECT data FROM reflection_runs WHERE run_id = ?`, runID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("reflection run not found: %s", runID)
	}
	if err != nil {
		return nil, fmt.Errorf("reading reflection run %s: %w", runID, err)
	}
	var cs ChangeSet
	if err := json.Unmarshal([]byte(data), &cs); err != nil {
		return nil, fmt.Errorf("decoding reflection run %s: %w", runID, err)
	}
	return &cs, nil
}

// DeleteChangeSet removes a change set record by run ID.
func (s *Store) DeleteChangeSet(runID string) error {
	return s.writeTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM reflection_runs WHERE run_id = ?`, runID)
		return err
	})
}

// removeSQLiteFiles deletes a database and its WAL companions. Used only for
// the stale migration .tmp, which by definition holds no authoritative data.
func removeSQLiteFiles(path string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
}
