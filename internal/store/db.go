package store

import (
	"context"
	"database/sql"
	"fmt"
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
