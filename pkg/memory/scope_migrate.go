package memory

import (
	"database/sql"
	"errors"
	"fmt"
)

// scopeNormalizeMigration is the memory_meta ledger key recording that stored
// block scopes have been canonicalized (v3 repair, finding 1).
const scopeNormalizeMigration = "migration:scope_normalize_v3"

// migrateStoredScopes canonicalizes the scope/repo columns of rows written by
// older releases, which stored raw slash-padded scopes ("repo-a/", "/repo-a/")
// and even the literal sentinel "all". Reads normalize their query scope, so
// such rows were orphaned. This one-time migration heals them in place.
//
// A migration was chosen over matching both forms on every read because the
// SQL dedup scan (findDuplicateQ: WHERE tier = ? AND scope = ?) and every Go
// scope comparator stay exact-match: one canonical stored form keeps all of
// them correct without dual-form logic at each call site.
//
// Only the scope/repo columns change. Block IDs are never re-keyed (the
// content-scan dedup path still finds migrated rows), confirms/conflicts and
// timestamps are untouched, and the lexical index needs no refresh because
// searchText does not include scope or repo.
func (s *Store) migrateStoredScopes() error {
	done, err := s.metaValue(scopeNormalizeMigration)
	if err != nil {
		return err
	}
	if done == "1" {
		return nil
	}
	return s.writeTx(func(tx *sql.Tx) error {
		// Re-check under the write lock: a concurrent opener may have
		// finished the migration between our read and this transaction.
		var v string
		err := tx.QueryRow(`SELECT value FROM memory_meta WHERE key = ?`, scopeNormalizeMigration).Scan(&v)
		if err == nil && v == "1" {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("checking scope migration ledger: %w", err)
		}

		rows, err := tx.Query(`SELECT id, scope, repo FROM blocks`)
		if err != nil {
			return fmt.Errorf("scanning block scopes: %w", err)
		}
		type scopeFix struct{ id, scope, repo string }
		var fixes []scopeFix
		for rows.Next() {
			var id, scope, repo string
			if err := rows.Scan(&id, &scope, &repo); err != nil {
				rows.Close()
				return fmt.Errorf("scanning block scope row: %w", err)
			}
			b := Block{Scope: scope, Repo: repo}
			normalizeBlockScopes(&b)
			if b.Scope != scope || b.Repo != repo {
				fixes = append(fixes, scopeFix{id, b.Scope, b.Repo})
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterating block scope rows: %w", err)
		}
		rows.Close()

		for _, f := range fixes {
			if _, err := tx.Exec(`UPDATE blocks SET scope = ?, repo = ? WHERE id = ?`, f.scope, f.repo, f.id); err != nil {
				return fmt.Errorf("normalizing scope of block %s: %w", f.id, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO memory_meta (key, value) VALUES (?, '1')
			ON CONFLICT(key) DO UPDATE SET value = '1'`, scopeNormalizeMigration); err != nil {
			return fmt.Errorf("recording scope migration: %w", err)
		}
		return nil
	})
}
