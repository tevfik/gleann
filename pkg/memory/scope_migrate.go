package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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

// scopeCollisionMigration is the memory_meta ledger key recording that the
// store has been inventoried for the v1.22 collision-safe scope scheme
// (finding H7), and scopeLegacyNamesKey holds the JSON list it recorded.
const (
	scopeCollisionMigration = "migration:scope_collision_prefix_v122"
	scopeLegacyNamesKey     = "migration:scope_collision_prefix_v122:legacy_names"
)

// migrateLegacyScopeNames is the one-time, non-destructive companion of the
// collision-safe DetectRepoScope change (finding H7).
//
// Blocks stored before v1.22 under a bare folder name ("app") cannot be
// rewritten to a prefixed scope: the store does not know which directory the
// name came from, and guessing would silently move other users' memory. So the
// migration never touches a block row. It records the set of bare names that
// existed at the moment of the switch and caches it in the store; filterScope
// then keeps those pre-existing blocks readable from the prefixed scope of the
// directory they came from (see legacyAliasScope). Writes always use the new
// collision-free scope, so no new sharing can occur, and scopes created after
// the switch (an explicit --scope app) are never widened.
//
// Like migrateStoredScopes it re-checks the ledger under the write lock, so
// concurrent openers run the scan at most once.
func (s *Store) migrateLegacyScopeNames() error {
	if err := s.loadLegacyScopeNames(); err != nil {
		return err
	}
	if len(s.legacyScopes) > 0 {
		return nil
	}
	done, err := s.metaValue(scopeCollisionMigration)
	if err != nil {
		return err
	}
	if done == "1" {
		// The inventory may have been committed by a concurrent opener
		// after the load above; re-read so this store aliases the same names.
		return s.loadLegacyScopeNames()
	}
	return s.writeTx(func(tx *sql.Tx) error {
		var v string
		err := tx.QueryRow(`SELECT value FROM memory_meta WHERE key = ?`, scopeCollisionMigration).Scan(&v)
		if err == nil && v == "1" {
			// A concurrent opener completed the inventory first; adopt its
			// recorded names instead of leaving this store's cache empty.
			var raw string
			err := tx.QueryRow(`SELECT value FROM memory_meta WHERE key = ?`, scopeLegacyNamesKey).Scan(&raw)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("reading recorded legacy scope names: %w", err)
			}
			return s.setLegacyScopeNames(raw)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("checking scope collision ledger: %w", err)
		}

		rows, err := tx.Query(`SELECT DISTINCT scope FROM blocks`)
		if err != nil {
			return fmt.Errorf("scanning legacy block scopes: %w", err)
		}
		var names []string
		for rows.Next() {
			var scope string
			if err := rows.Scan(&scope); err != nil {
				rows.Close()
				return fmt.Errorf("scanning legacy block scope row: %w", err)
			}
			if isLegacyBareScope(scope) {
				names = append(names, scope)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterating legacy block scope rows: %w", err)
		}
		rows.Close()
		sort.Strings(names)

		data, err := json.Marshal(names)
		if err != nil {
			return fmt.Errorf("encoding legacy scope names: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO memory_meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, scopeLegacyNamesKey, string(data)); err != nil {
			return fmt.Errorf("recording legacy scope names: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO memory_meta (key, value) VALUES (?, '1')
			ON CONFLICT(key) DO UPDATE SET value = '1'`, scopeCollisionMigration); err != nil {
			return fmt.Errorf("recording scope collision migration: %w", err)
		}
		s.legacyScopes = toNameSet(names)
		return nil
	})
}

// loadLegacyScopeNames fills the cached set of pre-switch bare scope names.
func (s *Store) loadLegacyScopeNames() error {
	raw, err := s.metaValue(scopeLegacyNamesKey)
	if err != nil {
		return err
	}
	return s.setLegacyScopeNames(raw)
}

// setLegacyScopeNames decodes a recorded name list into the cache. An empty
// record leaves the cache alone.
func (s *Store) setLegacyScopeNames(raw string) error {
	if raw == "" {
		return nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return fmt.Errorf("decoding recorded legacy scope names: %w", err)
	}
	s.legacyScopes = toNameSet(names)
	return nil
}

// isLegacyBareScope reports whether scope is the pre-v1.22 form: a single
// folder name with no path separator and no collision-safe prefix.
func isLegacyBareScope(scope string) bool {
	if scope == "" || strings.ContainsAny(scope, "/:") {
		return false
	}
	return true
}

func toNameSet(names []string) map[string]bool {
	if len(names) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// LegacyScopeNames returns the bare-basename scopes that were already stored
// when the store switched to collision-safe scopes. They stay readable from the
// matching "local:"/"dir:" scope but never receive new blocks.
func (s *Store) LegacyScopeNames() []string {
	var out []string
	for name := range s.legacyScopes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
