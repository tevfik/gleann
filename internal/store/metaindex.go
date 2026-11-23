package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Metadata filter indexes.
//
// A metadata filter is evaluated against the meta JSON column of passages.
// Without an index that is a pass over every row (json_type/json_extract per
// row: about 0.6 µs per passage on small rows, 2.5 µs on a real code index),
// so its cost follows the size of the index, not the number of matches.
//
// A filter index is a plain SQLite expression index over one meta key:
//
//	CREATE INDEX passages_mf2_<hex(key)> ON passages(
//	    <type>(<meta>, '$."<key>"'), json_extract(<meta>, '$."<key>"'))
//	  WHERE <type>(<meta>, '$."<key>"') IS NOT NULL
//
// where <meta> is the meta column guarded against text that is not JSON (see
// metaJSONExpr) and <type> is json_type, except that a value SQLite and Go
// read differently has the type "odd" (see metaTypeExpr). It holds one entry
// per passage that has the key: the type of the value and the value itself,
// so "key = value" is an index range.
//
// Properties that make it safe to add to an existing index database:
//
//   - It is not part of the schema history: no migration step, no change of
//     store_format. An index exists for the keys somebody filtered on
//     (EnsureMetaIndexes) and for no other key, so an index database that is
//     never filtered carries none and stays byte-for-byte what it was.
//   - SQLite maintains it on every write to passages, in the writer's own
//     transaction, whatever binary does the write: an older gleann that knows
//     nothing about filter indexes keeps them exact, and one that rebuilds
//     the database file from scratch simply loses them (the next filtered
//     query creates them again).
//   - Dropping one is always safe: readers check which exist per query
//     (MetaIndexes) and fall back to the pass over the table.
//
// The companion index passages_mfbad1 lists the passages whose meta SQLite
// and Go may read differently (see metaDamagedPredicate); readers verify
// those rows with the Go filter engine instead of trusting the index.

// MaxMetaIndexes bounds the number of filter indexes of one database. Every
// index costs one json_type and one json_extract per written passage and
// about 19 bytes per passage that has the key, and field names reach
// EnsureMetaIndexes from search requests, so the number must be bounded.
const MaxMetaIndexes = 16

// maxMetaKeyLen bounds the length in bytes of an indexable meta key.
const maxMetaKeyLen = 64

// metaIndexPrefix names the filter indexes: the prefix, then the key in hex.
// The 2 is the version of the index definition: a later change of the indexed
// expressions takes a new prefix, so an index is never used with a query it
// was not built for. (Version 1, plain json_type, never reached a release.)
const metaIndexPrefix = "passages_mf2_"

// metaOddType is the type the filter indexes give a value that SQLite and Go
// read differently, so its range is verified by the filter engine whatever the
// needle: see metaTypeExpr.
const metaOddType = "odd"

// MetaOddType is metaOddType for the readers of MetaBranch.Type.
const MetaOddType = metaOddType

// metaDamagedIndex is the partial index of the passages whose meta the index
// path must not trust.
const metaDamagedIndex = "passages_mfbad1"

// metaJSONExpr is the meta column as the filter indexes read it: text that is
// not valid JSON counts as an empty object, as it does for the Go filter
// engine (json.Unmarshal fails and the passage has no metadata keys). Without
// the guard json_type would raise "malformed JSON" and, once an index exists,
// fail every insert of such a row.
const metaJSONExpr = `CASE WHEN json_valid(meta) THEN meta ELSE '{}' END`

// metaDamagedPredicate selects the passages whose meta SQLite and Go may
// decode differently: text SQLite does not accept as JSON (Go accepts deeper
// nesting), a raw NUL byte (SQLite's JSON functions stop reading at it, so
// `{"lang":"go"}` + NUL + garbage is valid and has lang=go for SQLite, while
// encoding/json rejects the whole text), and strings with an escaped NUL or an
// escaped UTF-16 surrogate (Go replaces a lone surrogate, SQLite keeps it).
// encoding/json writes none of these, so the index is empty for metadata
// gleann wrote itself.
const metaDamagedPredicate = `NOT json_valid(meta) OR instr(CAST(meta AS BLOB), X'00') > 0 OR meta GLOB '*\u0000*' OR meta GLOB '*\u[dD][89a-fA-F]*'`

// MetaIndexable reports whether a meta key can have a filter index. The key
// becomes part of the index definition and of the query text (an expression
// index is only used by a query that spells the same expression, so the JSON
// path cannot be a bound parameter). Only letters, digits, '_', '-' and '.'
// are accepted: none of them can end the SQL string literal or the quoted
// JSON path label the key is written into.
func MetaIndexable(key string) bool {
	if key == "" || len(key) > maxMetaKeyLen || !utf8.ValidString(key) {
		return false
	}
	for _, r := range key {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.' {
			return false
		}
	}
	return true
}

func metaIndexName(key string) string {
	return metaIndexPrefix + hex.EncodeToString([]byte(key))
}

// metaTypeExpr and metaValueExpr are the two indexed expressions of a key.
// The key must have passed MetaIndexable.
//
// The type is json_type, except for a value of the key that SQLite and
// encoding/json decode differently, which gets the type metaOddType:
//
//   - a number outside the float64 range (1e400, or 400 digits): Go leaves the
//     key nil, SQLite reads +-Inf (json_extract gives 9e999 for it);
//   - a key that occurs more than once in the object: Go keeps the last value,
//     SQLite the first (json_remove deletes the first occurrence only, so a
//     second one is still found afterwards; this also matches a label spelled
//     with a \u escape).
//
// A key spelled with an escaped NUL (`{"v\u0000":"a"}`) is read by SQLite as
// the key `v` (its JSON path matching stops at the NUL), so it gets the plain
// type, not odd. Only the damaged predicate's `\u0000` check (and the raw-NUL
// check) keeps that correct: the row is in the damaged range and is verified
// by the engine whatever its key index says. Do not narrow that predicate.
//
// Bytes that are not UTF-8 inside a string cannot be detected in SQL (Go
// reads U+FFFD, SQLite keeps the bytes); readers handle them by the needle
// (see keyIndexBranches).
func metaTypeExpr(key string) string {
	m, p := metaJSONExpr, `'$."`+key+`"'`
	return `CASE WHEN json_type(` + m + `, ` + p + `) IS NULL THEN NULL` +
		` WHEN json_extract(` + m + `, ` + p + `) IN (9e999, -9e999)` +
		` OR json_type(json_remove(` + m + `, ` + p + `), ` + p + `) IS NOT NULL THEN '` + metaOddType + `'` +
		` ELSE json_type(` + m + `, ` + p + `) END`
}

func metaValueExpr(key string) string {
	return `json_extract(` + metaJSONExpr + `, '$."` + key + `"')`
}

// MetaIndexes reports which of keys have a filter index. It reads the schema
// table only, so it is cheap enough to run per query, and a query therefore
// never trusts an index another process dropped. A key index counts only
// together with the damaged-rows index its readers need (EnsureMetaIndexes
// creates both, and creates that one again should it be gone).
func (s *Store) MetaIndexes(ctx context.Context, keys []string) (map[string]bool, error) {
	out := make(map[string]bool, len(keys))
	byName := make(map[string]string, len(keys))
	args := []any{metaDamagedIndex}
	for _, k := range keys {
		if !MetaIndexable(k) {
			continue
		}
		if _, dup := byName[metaIndexName(k)]; !dup {
			byName[metaIndexName(k)] = k
			args = append(args, metaIndexName(k))
		}
	}
	if len(byName) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
	rows, err := s.reader.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name IN (`+ph+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("listing filter indexes: %w", err)
	}
	defer rows.Close()
	damaged := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("listing filter indexes: %w", err)
		}
		if name == metaDamagedIndex {
			damaged = true
			continue
		}
		out[byName[name]] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing filter indexes: %w", err)
	}
	if !damaged {
		return make(map[string]bool), nil
	}
	return out, nil
}

// EnsureMetaIndexes creates the filter indexes of keys that do not exist yet,
// in one transaction. Creating an index reads every passage once (about the
// cost of one unindexed filter query); it is a no-op, without any write, when
// all of them exist.
//
// It never waits for another writer: the transaction runs on a connection of
// its own with busy_timeout 0, so while a build or sync holds the write lock
// it fails at once with SQLITE_BUSY and the caller answers its query without
// the index. It also fails when a key is not indexable (MetaIndexable) or the
// database already has MaxMetaIndexes filter indexes.
func (s *Store) EnsureMetaIndexes(ctx context.Context, keys []string) error {
	for _, k := range keys {
		if !MetaIndexable(k) {
			return fmt.Errorf("creating filter indexes: meta key %q cannot be indexed", k)
		}
	}
	// Read first, without the write lock: nothing to do when every index
	// exists, and a database that is full of filter indexes refuses here, so
	// a search on one more field does not take the write lock per query.
	create, damaged, err := missingMetaIndexes(ctx, s.reader, keys)
	if err != nil || (len(create) == 0 && damaged) {
		return err
	}

	db, err := sql.Open("sqlite", s.path+"?_pragma=busy_timeout(0)&_pragma=synchronous(NORMAL)&_txlock=immediate")
	if err != nil {
		return fmt.Errorf("creating filter indexes: opening %s: %w", s.path, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, nil) // BEGIN IMMEDIATE: fails at once while another writer is active
	if err != nil {
		return fmt.Errorf("creating filter indexes: beginning transaction: %w", err)
	}
	defer tx.Rollback()
	// Again under the write lock, so two processes cannot both add the last
	// allowed index.
	if create, damaged, err = missingMetaIndexes(ctx, tx, keys); err != nil || (len(create) == 0 && damaged) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS `+metaDamagedIndex+` ON passages(id) WHERE `+metaDamagedPredicate); err != nil {
		return fmt.Errorf("creating filter indexes: %s: %w", metaDamagedIndex, err)
	}
	for _, k := range create {
		t := metaTypeExpr(k)
		if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS `+metaIndexName(k)+` ON passages(`+t+`, `+metaValueExpr(k)+`) WHERE `+t+` IS NOT NULL`); err != nil {
			return fmt.Errorf("creating filter indexes: key %q: %w", k, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("creating filter indexes: committing: %w", err)
	}
	return nil
}

// metaIndexQuerier is the part of *sql.DB and *sql.Tx missingMetaIndexes needs.
type metaIndexQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// missingMetaIndexes returns the keys that have no filter index yet and
// whether the damaged-rows index exists, or an error when creating the
// missing ones would exceed MaxMetaIndexes.
func missingMetaIndexes(ctx context.Context, q metaIndexQuerier, keys []string) (missing []string, damaged bool, err error) {
	existing := make(map[string]bool)
	rows, err := q.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND (name GLOB '`+metaIndexPrefix+`*' OR name = '`+metaDamagedIndex+`')`)
	if err != nil {
		return nil, false, fmt.Errorf("creating filter indexes: listing: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, false, fmt.Errorf("creating filter indexes: listing: %w", err)
		}
		if name == metaDamagedIndex {
			damaged = true
			continue
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("creating filter indexes: listing: %w", err)
	}
	have := len(existing)
	for _, k := range keys {
		if name := metaIndexName(k); !existing[name] {
			existing[name] = true
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 && len(existing) > MaxMetaIndexes {
		return nil, damaged, fmt.Errorf("creating filter indexes: the index database has %d of at most %d filter indexes and %d more were asked for",
			have, MaxMetaIndexes, len(missing))
	}
	return missing, damaged, nil
}

// MetaBranchKind says what a MetaBranch selects by.
type MetaBranchKind int

const (
	// MetaByKey selects passages by one meta key, from its filter index.
	MetaByKey MetaBranchKind = iota
	// MetaBySource selects the passages of one source, from the
	// passages_source index every database has.
	MetaBySource
	// MetaDamaged selects the passages whose meta the filter indexes must
	// not be trusted for (metaDamagedPredicate). Their matches come back
	// with Damaged set; the caller verifies them whatever another branch
	// said about the same passage.
	MetaDamaged
)

// MetaBranch is one index range of a metadata pre-selection.
type MetaBranch struct {
	Kind MetaBranchKind

	// MetaByKey: the key (it must have a filter index), the JSON type of the
	// value as json_type names it (text, integer, real, true, false, null,
	// array, object; "" selects every passage that has the key) and the
	// values: strings for text, int64 for integer; none selects every value
	// of the type.
	Key    string
	Type   string
	Values []any

	// MetaBySource: the source; "" selects the passages without one.
	Source string

	// Sure says the caller takes this branch's passages as matches without
	// looking at their metadata. Otherwise they come back with Source and
	// Meta, for verification.
	Sure bool
}

// MetaMatch is one passage selected by SelectByMetaIndex.
type MetaMatch struct {
	ID      int64
	VecSlot *int64
	Sure    bool   // from a branch with Sure
	Damaged bool   // from the MetaDamaged branch
	Source  string // empty when Sure
	Meta    string // empty when Sure
}

// SelectByMetaIndex returns the passages selected by any of branches, each
// branch read from an index (INDEXED BY: the statement fails instead of
// falling back to a pass over the table when an index is missing), so its cost
// follows the number of selected passages. limit caps the rows read (0 means
// unlimited). A passage selected by several branches comes back once per
// branch; the order is unspecified. Like SelectByPredicate it reads id,
// vec_slot, source and meta only, and source and meta only for the branches
// that are not Sure.
func (s *Store) SelectByMetaIndex(ctx context.Context, branches []MetaBranch, limit int) ([]MetaMatch, error) {
	if len(branches) == 0 {
		return nil, nil
	}
	query, args, err := metaIndexQuery(branches, limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("selecting passages by filter index: %w", err)
	}
	defer rows.Close()
	var out []MetaMatch
	for rows.Next() {
		var m MetaMatch
		var slot sql.NullInt64
		var state int
		var source, meta sql.NullString
		if err := rows.Scan(&m.ID, &slot, &state, &source, &meta); err != nil {
			return nil, fmt.Errorf("selecting passages by filter index: %w", err)
		}
		if slot.Valid {
			v := slot.Int64
			m.VecSlot = &v
		}
		m.Sure, m.Damaged = state == 1, state == 2
		m.Source, m.Meta = source.String, meta.String
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("selecting passages by filter index: %w", err)
	}
	return out, nil
}

// metaIndexQuery builds the statement of SelectByMetaIndex: one SELECT per
// branch, each bound to its index, joined by UNION ALL. The third column is 1
// for a sure branch, 2 for the damaged one and 0 otherwise.
func metaIndexQuery(branches []MetaBranch, limit int) (string, []any, error) {
	var q strings.Builder
	var args []any
	for i, b := range branches {
		if i > 0 {
			q.WriteString(" UNION ALL ")
		}
		switch {
		case b.Kind == MetaDamaged:
			q.WriteString(`SELECT id, vec_slot, 2, source, meta FROM passages INDEXED BY ` + metaDamagedIndex + ` WHERE ` + metaDamagedPredicate)
			continue
		case b.Sure:
			q.WriteString(`SELECT id, vec_slot, 1, NULL, NULL FROM passages`)
		default:
			q.WriteString(`SELECT id, vec_slot, 0, source, meta FROM passages`)
		}
		switch b.Kind {
		case MetaBySource:
			q.WriteString(` INDEXED BY passages_source WHERE source = ?`)
			args = append(args, b.Source)
		case MetaByKey:
			if !MetaIndexable(b.Key) {
				return "", nil, fmt.Errorf("selecting passages by filter index: meta key %q cannot be indexed", b.Key)
			}
			q.WriteString(` INDEXED BY ` + metaIndexName(b.Key) + ` WHERE ` + metaTypeExpr(b.Key))
			if b.Type == "" {
				q.WriteString(` IS NOT NULL`)
				break
			}
			q.WriteString(` = ?`)
			args = append(args, b.Type)
			if len(b.Values) > 0 {
				q.WriteString(` AND ` + metaValueExpr(b.Key) + ` IN (` + strings.TrimSuffix(strings.Repeat("?,", len(b.Values)), ",") + `)`)
				args = append(args, b.Values...)
			}
		default:
			return "", nil, fmt.Errorf("selecting passages by filter index: unknown branch kind %d", b.Kind)
		}
	}
	if limit > 0 {
		fmt.Fprintf(&q, ` LIMIT %d`, limit)
	}
	return q.String(), args, nil
}
