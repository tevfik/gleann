package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// WriterDB exposes the single writer connection pool so another store over
// the same index database (internal/graphstore) runs its writes through the
// same connection and the same BEGIN IMMEDIATE discipline (RFC 003 sections
// 3.3 and 3.6: one SQLite file per index, one writer). The pool stays owned
// by Store: callers must not close it or change its connection limits, and
// Store.Close closes it for everyone.
func (s *Store) WriterDB() *sql.DB { return s.writer }

// ReaderDB exposes the reader pool for the same database. In WAL mode reads
// never block the writer, so graph queries can run here while a sync commits
// on WriterDB. The pool stays owned by Store (see WriterDB).
func (s *Store) ReaderDB() *sql.DB { return s.reader }

// BeginWrite starts a write transaction on the single writer connection.
// The DSN's _txlock=immediate makes it BEGIN IMMEDIATE, so the write lock is
// taken up front instead of failing later on a lock upgrade that
// busy_timeout cannot retry (RFC 003 section 3.6). The caller owns the
// transaction: commit or roll it back, and run no other write on the writer
// pool until then (it has a single connection).
//
// A sync that writes a file's passages and its graph rows together uses
// BeginWrite + ApplySourceTx + graphstore.ApplyFileDeltaTx + one commit, so
// files.hash advances only when both succeed (RFC 003 section 6.5).
func (s *Store) BeginWrite(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.writer.BeginTx(ctx, nil) // _txlock=immediate makes this BEGIN IMMEDIATE
	if err != nil {
		return nil, fmt.Errorf("beginning write transaction: %w", err)
	}
	return tx, nil
}

// OpenReadOnly opens an existing index database for diagnostics: both pools
// are mode=ro with query_only, no migration runs and no pragma is written, so
// a doctor or info command never modifies the main .db file (its content and
// mtime stay as they were). SQLite may still create the -wal and -shm side
// files of a WAL database. A missing file is an error. A database written by
// an older schema is opened as it is; queries against a table it lacks fail
// instead of triggering a migration.
func OpenReadOnly(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("opening %s read-only: %w", path, err)
	}
	slash := filepath.ToSlash(path)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash // file:/C:/x on Windows
	}
	dsn := "file:" + slash + "?mode=ro&_pragma=busy_timeout(10000)&_pragma=query_only(1)&_pragma=foreign_keys(ON)"
	writer, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening %s read-only: %w", path, err)
	}
	writer.SetMaxOpenConns(1)
	reader, err := sql.Open("sqlite", dsn)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("opening %s read-only: %w", path, err)
	}
	s := &Store{path: path, writer: writer, reader: reader, log: slog.Default()}
	if err := retryBusy(func() error { return reader.PingContext(context.Background()) }); err != nil {
		s.Close()
		return nil, fmt.Errorf("opening %s read-only: %w", path, err)
	}
	return s, nil
}
