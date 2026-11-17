package store

import (
	"context"
	"crypto/sha256"
	"fmt"
)

// SourcedPassage is one passage of a ReplaceAll stream.
type SourcedPassage struct {
	Source string
	NewPassage
}

// ReplaceAll replaces every passage, lex row and file row with the passages
// that fill yields, and sets the given meta keys, all in ONE transaction.
//
// It exists for whole-index rebuilds that must not swap files: a reader in
// another process (gleann serve, an MCP server) keeps reading the previous
// committed state through WAL until the commit, then sees the new one. A
// file swap instead fails on Windows while a reader has the database open,
// and on unix leaves the reader on a deleted file. The transaction holds the
// write lock for the whole rebuild, so callers serialise rebuilds with a lock
// of their own instead of queueing on busy_timeout.
//
// Passages are streamed: memory holds only per-source counters, never the
// texts. Within a source, chunk_no follows the order in which passages are
// yielded and dup_index counts identical texts, as in ApplySource.
func (s *Store) ReplaceAll(ctx context.Context, fill func(add func(SourcedPassage) error) error, meta map[string]string) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replacing all passages: beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// contentless_delete=1 lets a plain DELETE clear the FTS5 table.
	for _, q := range []string{`DELETE FROM lex`, `DELETE FROM passages`, `DELETE FROM files`} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("replacing all passages: %s: %w", q, err)
		}
	}
	insAuto, err := tx.PrepareContext(ctx, `INSERT INTO passages (pid, source, chunk_no, vec_slot, kind, text, meta) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("replacing all passages: preparing insert: %w", err)
	}
	defer insAuto.Close()
	insID, err := tx.PrepareContext(ctx, `INSERT INTO passages (id, pid, source, chunk_no, vec_slot, kind, text, meta) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("replacing all passages: preparing explicit-id insert: %w", err)
	}
	defer insID.Close()
	lexIns, err := tx.PrepareContext(ctx, `INSERT INTO lex(rowid, tokens) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("replacing all passages: preparing lex insert: %w", err)
	}
	defer lexIns.Close()

	type dupKey struct {
		source string
		text   [sha256.Size]byte
	}
	chunkNo := make(map[string]int)
	dups := make(map[dupKey]int)
	n := 0
	add := func(p SourcedPassage) error {
		if n%1000 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		n++
		k := dupKey{source: p.Source, text: sha256.Sum256([]byte(p.Text))}
		pid := PassagePID(p.Source, p.Text, dups[k])
		dups[k]++
		no := chunkNo[p.Source]
		chunkNo[p.Source]++
		kind := p.Kind
		if kind == "" {
			kind = "code"
		}
		m := p.Meta
		if m == "" {
			m = "{}"
		}
		var slot any
		if p.VecSlot != nil {
			slot = *p.VecSlot
		}
		var id int64
		if p.ID != nil {
			id = *p.ID
			if _, err := insID.ExecContext(ctx, id, pid, p.Source, no, slot, kind, p.Text, m); err != nil {
				return fmt.Errorf("inserting passage %d: %w", id, err)
			}
		} else {
			res, err := insAuto.ExecContext(ctx, pid, p.Source, no, slot, kind, p.Text, m)
			if err != nil {
				return fmt.Errorf("inserting passage of %q: %w", p.Source, err)
			}
			if id, err = res.LastInsertId(); err != nil {
				return fmt.Errorf("reading passage id: %w", err)
			}
		}
		if p.Tokens != "" {
			if _, err := lexIns.ExecContext(ctx, id, p.Tokens); err != nil {
				return fmt.Errorf("inserting lex row %d: %w", id, err)
			}
		}
		return nil
	}
	if err := fill(add); err != nil {
		return fmt.Errorf("replacing all passages: %w", err)
	}
	for k, v := range meta {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
  ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return fmt.Errorf("replacing all passages: writing meta %q: %w", k, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replacing all passages: committing: %w", err)
	}
	s.lexRows.Store(0)
	return nil
}
