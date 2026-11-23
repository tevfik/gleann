package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// PassageID is the internal integer ID of a passage (passages.id, the FTS
// rowid). It is an alias of int64 because the RFC 003 section 3.2 interface
// uses both spellings. IDs come from AUTOINCREMENT and are never reused;
// callers that need a stable identity across rebuilds use Passage.PID.
type PassageID = int64

// Passage is one stored chunk of a source file.
type Passage struct {
	ID      int64
	PID     string // stable ID, see PassagePID
	Source  string
	ChunkNo int
	VecSlot *int64 // position in the vector file; nil = lexical only
	Kind    string // "code" or "prose"
	Text    string
	Meta    string // JSON object
}

// NewPassage is one passage of a SourceDelta, in chunk order.
type NewPassage struct {
	Text    string
	Kind    string // defaults to "code" when empty
	Meta    string // JSON object; defaults to "{}" when empty
	VecSlot *int64 // nil = lexical only

	// ID, when non-nil, is the passage's internal ID (passages.id and the lex
	// rowid). Used by migration and the sidecar: id = old passage ID, so the
	// IDs a format-1 searcher already understands are preserved (RFC 003
	// section 6.4 step 2). When nil, ApplySource assigns an AUTOINCREMENT ID.
	ID *int64

	// Tokens is the space-joined output of the gleann analyzer. When
	// non-empty, ApplySource inserts it into lex(rowid, tokens). This is the
	// lexical hook of RFC 003 step 1: ApplySource never tokenizes text and
	// never touches lex beyond deleting the lex rows of removed passages and
	// inserting these tokens; the lexical package (step 2) fills tokens
	// later, and Lexical.Rebuild can refill them from passages.text.
	Tokens string
}

// SourceDelta is the complete new state of one source file. ApplySource
// replaces everything belonging to Source with it in one transaction,
// whatever the number of passages (RFC 003 section 3.6 batch-commit rule).
type SourceDelta struct {
	Source   string
	File     FileRow      // manifest row committed with the passages; Path defaults to Source when empty
	Passages []NewPassage // chunk order; chunk_no is the index in this slice
}

// Passages is the record store for one index (RFC 003 section 3.2).
type Passages interface {
	// ApplySource replaces everything belonging to one source in one
	// transaction, whatever the number of passages: deletes its old passages,
	// inserts the new ones, updates the lexical rows and the file manifest
	// row. It returns the internal IDs of the new passages in chunk order
	// (they are the lex rowids); the stable ID of each is its PID column,
	// PassagePID(source, text, dup_index), which callers can read back.
	ApplySource(ctx context.Context, d SourceDelta) (ids []PassageID, err error)
	RemoveSource(ctx context.Context, source string) (removed int, err error)

	// ReassignSlots rewrites vec_slot for many passages in one transaction.
	// The vector builder calls it when compaction renumbers the vector file,
	// so passage rows and vector positions change together.
	ReassignSlots(ctx context.Context, oldToNew map[int64]int64) error

	Get(ctx context.Context, id int64) (Passage, error)
	GetMany(ctx context.Context, ids []int64) ([]Passage, error)
	BySlot(ctx context.Context, slots []int64) ([]Passage, error)
	ForEach(ctx context.Context, fn func(Passage) error) error
	Count(ctx context.Context) (int, error)

	// ListPassages and ListSections are the bounded, read-only listing of the
	// stored rows behind GET /api/indexes/{name}/passages and /sections (RFC
	// 004 G8). They page with `ORDER BY id LIMIT ? OFFSET ?`, never read the
	// whole table or the vector files, and leave the section metadata
	// aggregation in SQLite (see their documentation).
	ListPassages(ctx context.Context, limit, offset int) ([]Passage, error)
	ListSections(ctx context.Context, limit, offset int) ([]Section, error)

	Files(ctx context.Context) (map[string]FileRow, error)
	Meta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error
	Close() error
}

const passageCols = `id, pid, source, chunk_no, vec_slot, kind, text, meta`

// scanner abstracts *sql.Row and *sql.Rows for scanPassage.
type scanner interface {
	Scan(dest ...any) error
}

func scanPassage(sc scanner) (Passage, error) {
	var p Passage
	var slot sql.NullInt64
	if err := sc.Scan(&p.ID, &p.PID, &p.Source, &p.ChunkNo, &slot, &p.Kind, &p.Text, &p.Meta); err != nil {
		return Passage{}, err
	}
	if slot.Valid {
		v := slot.Int64
		p.VecSlot = &v
	}
	return p, nil
}

// ApplySource replaces one source atomically: old passages and their lex
// rows are deleted, the new passages (with their optional tokens) and the
// manifest row are inserted, and the transaction commits. A failure at any
// point rolls everything back, so the old state of the source stays intact
// and files.hash does not advance (AGENTS.md section 2.3, per file).
//
// It is BeginWrite + ApplySourceTx + Commit. A sync that also writes the
// file's graph rows calls those two directly instead, so the passages and
// the graph delta share one transaction (RFC 003 section 6.5).
func (s *Store) ApplySource(ctx context.Context, d SourceDelta) ([]PassageID, error) {
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		return nil, fmt.Errorf("applying source %q: %w", d.Source, err)
	}
	defer tx.Rollback()
	ids, err := s.ApplySourceTx(ctx, tx, d)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("applying source %q: committing: %w", d.Source, err)
	}
	return ids, nil
}

// ApplySourceTx is ApplySource inside a caller-owned transaction (from
// BeginWrite): it deletes the source's old passages and lex rows, inserts
// the new ones and upserts the manifest row, but does not commit. The caller
// commits or rolls back, so a graph delta (graphstore.ApplyFileDeltaTx) can
// run in the same transaction and files.hash advances only when both the
// passages and the graph rows succeed (RFC 003 section 6.5: "in one
// transaction together with the file's passages and its files row").
//
// The cached lex row count is reset here, before the caller commits. That is
// the safe option: 0 means "unknown, recount" to filterDocFreq, so an early
// reset can only cost a recount of the committed rows, never serve a stale
// count, and a rollback leaves it equally safe. Resetting after the commit
// instead would need every ApplySourceTx caller to remember a second call;
// a missed one would silently skew the document-frequency cut. ApplySource
// inherits the reset through this function.
func (s *Store) ApplySourceTx(ctx context.Context, tx *sql.Tx, d SourceDelta) ([]PassageID, error) {
	// An empty source is the sourceless bucket of RFC 003 section 6.4 step 2
	// (passages without a Metadata["source"], e.g. migration and the sidecar).
	// It is a real bucket: its passages and manifest row are keyed by "".
	if d.File.Path != "" && d.File.Path != d.Source {
		// RemoveSource deletes the manifest row by source, so a different
		// path would leave an orphaned row behind.
		return nil, fmt.Errorf("applying source %q: manifest path %q differs from the source", d.Source, d.File.Path)
	}

	if err := deleteSourcePassages(ctx, tx, d.Source); err != nil {
		return nil, fmt.Errorf("applying source %q: %w", d.Source, err)
	}

	// dup_index counts identical texts within the source so repeated chunks
	// still get distinct stable IDs (RFC 003 "Stable passage IDs").
	dup := make(map[string]int)
	ins, err := tx.PrepareContext(ctx, `INSERT INTO passages (pid, source, chunk_no, vec_slot, kind, text, meta)
  VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("applying source %q: preparing insert: %w", d.Source, err)
	}
	defer ins.Close()
	// Explicit-ID insert for migration and the sidecar (NewPassage.ID): the
	// rowid must equal the old passage ID so a format-1 searcher's IDs stay
	// valid. AUTOINCREMENT semantics are kept for the nil-ID path above.
	insID, err := tx.PrepareContext(ctx, `INSERT INTO passages (id, pid, source, chunk_no, vec_slot, kind, text, meta)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("applying source %q: preparing explicit-id insert: %w", d.Source, err)
	}
	defer insID.Close()
	lexIns, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO lex(rowid, tokens) VALUES (?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("applying source %q: preparing lex insert: %w", d.Source, err)
	}
	defer lexIns.Close()

	ids := make([]PassageID, 0, len(d.Passages))
	for i, p := range d.Passages {
		pid := PassagePID(d.Source, p.Text, dup[p.Text])
		dup[p.Text]++
		kind := p.Kind
		if kind == "" {
			kind = "code"
		}
		meta := p.Meta
		if meta == "" {
			meta = "{}"
		}
		var slot any
		if p.VecSlot != nil {
			slot = *p.VecSlot
		}
		var id int64
		if p.ID != nil {
			id = *p.ID
			if _, err := insID.ExecContext(ctx, id, pid, d.Source, i, slot, kind, p.Text, meta); err != nil {
				return nil, fmt.Errorf("applying source %q: inserting passage %d with id %d: %w", d.Source, i, id, err)
			}
		} else {
			res, err := ins.ExecContext(ctx, pid, d.Source, i, slot, kind, p.Text, meta)
			if err != nil {
				return nil, fmt.Errorf("applying source %q: inserting passage %d: %w", d.Source, i, err)
			}
			id, err = res.LastInsertId()
			if err != nil {
				return nil, fmt.Errorf("applying source %q: reading id of passage %d: %w", d.Source, i, err)
			}
		}
		if p.Tokens != "" {
			if _, err := lexIns.ExecContext(ctx, id, p.Tokens); err != nil {
				return nil, fmt.Errorf("applying source %q: inserting lex row for passage %d: %w", d.Source, i, err)
			}
		}
		ids = append(ids, id)
	}

	f := d.File
	if f.Path == "" {
		f.Path = d.Source
	}
	if f.IndexedAt == 0 {
		f.IndexedAt = time.Now().UnixNano()
	}
	if err := upsertFile(ctx, tx, f); err != nil {
		return nil, fmt.Errorf("applying source %q: %w", d.Source, err)
	}
	s.lexRows.Store(0)
	return ids, nil
}

// deleteSourcePassages removes the lex rows of a source's passages and then
// the passages themselves, inside the caller's transaction. Deleting a lex
// rowid that was never inserted is a no-op in a contentless_delete FTS5
// table, so passages without tokens need no special case.
func deleteSourcePassages(ctx context.Context, tx *sql.Tx, source string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM passages WHERE source = ? ORDER BY id`, source)
	if err != nil {
		return fmt.Errorf("listing old passages: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scanning old passage id: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("listing old passages: %w", err)
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM lex WHERE rowid = ?`, id); err != nil {
			return fmt.Errorf("deleting lex row %d: %w", id, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM passages WHERE source = ?`, source); err != nil {
		return fmt.Errorf("deleting old passages: %w", err)
	}
	return nil
}

// RemoveSource deletes a source's passages, lex rows and manifest row in one
// transaction and reports how many passages were removed.
//
// It is BeginWrite + RemoveSourceTx + Commit. A vector publication removes
// several sources in the same transaction as the slot renumbering, so it
// calls RemoveSourceTx directly (RFC 003 section 3.4).
func (s *Store) RemoveSource(ctx context.Context, source string) (int, error) {
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		return 0, fmt.Errorf("removing source %q: %w", source, err)
	}
	defer tx.Rollback()
	removed, err := s.RemoveSourceTx(ctx, tx, source)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("removing source %q: committing: %w", source, err)
	}
	return removed, nil
}

// RemoveSourceTx is RemoveSource inside a caller-owned transaction (from
// BeginWrite): it deletes the source's passages, lex rows and manifest row
// but does not commit. The cached lex row count is reset here, before the
// caller commits, for the same reason as in ApplySourceTx: 0 means "unknown,
// recount", so an early reset can only cost a recount, never serve a stale
// count, and a rollback leaves it equally safe.
func (s *Store) RemoveSourceTx(ctx context.Context, tx *sql.Tx, source string) (int, error) {
	var removed int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM passages WHERE source = ?`, source).Scan(&removed); err != nil {
		return 0, fmt.Errorf("removing source %q: counting passages: %w", source, err)
	}
	if err := deleteSourcePassages(ctx, tx, source); err != nil {
		return 0, fmt.Errorf("removing source %q: %w", source, err)
	}
	if err := deleteFile(ctx, tx, source); err != nil {
		return 0, fmt.Errorf("removing source %q: %w", source, err)
	}
	s.lexRows.Store(0)
	return removed, nil
}

// ReassignSlots rewrites vec_slot values in one transaction, keyed by the
// old slot. It is called after a vector compaction renumbers the vector file
// (RFC 003 section 3.4). Slots are first cleared to NULL and then set to
// their new values, so a renumbering that permutes slots (e.g. 0->1, 1->0)
// never trips the unique index mid-update.
//
// It is BeginWrite + ReassignSlotsTx + Commit. A vector publication runs the
// renumbering in the same transaction as the source removals and the pending
// publication keys, so it calls ReassignSlotsTx directly.
func (s *Store) ReassignSlots(ctx context.Context, oldToNew map[int64]int64) error {
	if len(oldToNew) == 0 {
		return nil
	}
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		return fmt.Errorf("reassigning slots: %w", err)
	}
	defer tx.Rollback()
	if err := s.ReassignSlotsTx(ctx, tx, oldToNew); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("reassigning slots: committing: %w", err)
	}
	return nil
}

// ReassignSlotsTx is ReassignSlots inside a caller-owned transaction (from
// BeginWrite): it validates the mapping, looks up the passages by old slot
// and rewrites their vec_slot, but does not commit.
func (s *Store) ReassignSlotsTx(ctx context.Context, tx *sql.Tx, oldToNew map[int64]int64) error {
	if len(oldToNew) == 0 {
		return nil
	}
	// Deterministic order, and reject duplicate targets early with a clear
	// message rather than a bare constraint error.
	olds := make([]int64, 0, len(oldToNew))
	seenNew := make(map[int64]int64, len(oldToNew))
	for old, new := range oldToNew {
		olds = append(olds, old)
		if prev, ok := seenNew[new]; ok {
			return fmt.Errorf("reassigning slots: slots %d and %d both map to %d", prev, old, new)
		}
		seenNew[new] = old
	}
	sort.Slice(olds, func(i, j int) bool { return olds[i] < olds[j] })

	type change struct{ id, newSlot int64 }
	changes := make([]change, 0, len(olds))
	for _, old := range olds {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM passages WHERE vec_slot = ?`, old).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("reassigning slots: no passage with vec_slot %d", old)
		}
		if err != nil {
			return fmt.Errorf("reassigning slots: looking up vec_slot %d: %w", old, err)
		}
		changes = append(changes, change{id: id, newSlot: oldToNew[old]})
	}
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, `UPDATE passages SET vec_slot = NULL WHERE id = ?`, c.id); err != nil {
			return fmt.Errorf("reassigning slots: clearing slot of passage %d: %w", c.id, err)
		}
	}
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, `UPDATE passages SET vec_slot = ? WHERE id = ?`, c.newSlot, c.id); err != nil {
			return fmt.Errorf("reassigning slots: setting slot %d on passage %d: %w", c.newSlot, c.id, err)
		}
	}
	return nil
}

// AssignSlotsTx sets vec_slot by internal passage ID inside a caller-owned
// transaction (from BeginWrite). The vector builder uses it when the first
// vector file is published for a staged index: BuildPassages stores the
// passages slotless and returns their internal IDs in item order, and slot i
// belongs to item i. Like ReassignSlotsTx, slots are cleared to NULL first so
// a renumbering that permutes slots never trips the unique index mid-update.
func (s *Store) AssignSlotsTx(ctx context.Context, tx *sql.Tx, idToSlot map[int64]int64) error {
	if len(idToSlot) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(idToSlot))
	seenSlot := make(map[int64]int64, len(idToSlot))
	for id, slot := range idToSlot {
		ids = append(ids, id)
		if prev, ok := seenSlot[slot]; ok {
			return fmt.Errorf("assigning slots: passages %d and %d both map to slot %d", prev, id, slot)
		}
		seenSlot[slot] = id
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		var found int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM passages WHERE id = ?`, id).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("assigning slots: no passage with id %d", id)
		}
		if err != nil {
			return fmt.Errorf("assigning slots: looking up passage %d: %w", id, err)
		}
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE passages SET vec_slot = NULL WHERE id = ?`, id); err != nil {
			return fmt.Errorf("assigning slots: clearing slot of passage %d: %w", id, err)
		}
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE passages SET vec_slot = ? WHERE id = ?`, idToSlot[id], id); err != nil {
			return fmt.Errorf("assigning slots: setting slot %d on passage %d: %w", idToSlot[id], id, err)
		}
	}
	return nil
}

// Get returns one passage by internal ID. A missing ID returns an error
// wrapping sql.ErrNoRows.
func (s *Store) Get(ctx context.Context, id int64) (Passage, error) {
	p, err := scanPassage(s.reader.QueryRowContext(ctx, `SELECT `+passageCols+` FROM passages WHERE id = ?`, id))
	if err != nil {
		return Passage{}, fmt.Errorf("getting passage %d: %w", id, err)
	}
	return p, nil
}

// GetMany returns the passages for the given IDs in the order of ids, so a
// ranked list stays ranked. Missing IDs are skipped.
func (s *Store) GetMany(ctx context.Context, ids []int64) ([]Passage, error) {
	return s.queryByInts(ctx, `SELECT `+passageCols+` FROM passages WHERE id IN (%s)`, ids,
		func(p Passage) (int64, bool) { return p.ID, true }, "getting passages")
}

// BySlot returns the passages occupying the given vector slots in the order
// of slots, so the vector search ranking is kept. Slots without a passage are
// skipped.
func (s *Store) BySlot(ctx context.Context, slots []int64) ([]Passage, error) {
	return s.queryByInts(ctx, `SELECT `+passageCols+` FROM passages WHERE vec_slot IN (%s)`, slots,
		func(p Passage) (int64, bool) {
			if p.VecSlot == nil {
				return 0, false
			}
			return *p.VecSlot, true
		}, "getting passages by slot")
}

// maxInParams bounds the placeholders of one IN list, well below SQLite's
// host-parameter limit, so a large lookup is split into several queries.
const maxInParams = 500

// queryByInts runs query (one %s for the IN list) over vals in chunks and
// returns the rows in the order of vals; key extracts the value a row was
// selected by.
func (s *Store) queryByInts(ctx context.Context, query string, vals []int64, key func(Passage) (int64, bool), doing string) ([]Passage, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	found := make(map[int64]Passage, len(vals))
	for start := 0; start < len(vals); start += maxInParams {
		chunk := vals[start:min(start+maxInParams, len(vals))]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, v := range chunk {
			args[i] = v
		}
		if err := func() error {
			rows, err := s.reader.QueryContext(ctx, fmt.Sprintf(query, placeholders), args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				p, err := scanPassage(rows)
				if err != nil {
					return err
				}
				if k, ok := key(p); ok {
					found[k] = p
				}
			}
			return rows.Err()
		}(); err != nil {
			return nil, fmt.Errorf("%s: %w", doing, err)
		}
	}
	out := make([]Passage, 0, len(found))
	for _, v := range vals {
		if p, ok := found[v]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// ForEach calls fn for every passage, ordered by id, and stops at the first
// error from fn or from the iteration.
func (s *Store) ForEach(ctx context.Context, fn func(Passage) error) error {
	rows, err := s.reader.QueryContext(ctx, `SELECT `+passageCols+` FROM passages ORDER BY id`)
	if err != nil {
		return fmt.Errorf("iterating passages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanPassage(rows)
		if err != nil {
			return fmt.Errorf("iterating passages: %w", err)
		}
		if err := fn(p); err != nil {
			return fmt.Errorf("iterating passages: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating passages: %w", err)
	}
	return nil
}

// SelectByPredicate returns the passages matching a caller-supplied SQL WHERE
// clause over the passages table, ordered by id, reading only the columns a
// metadata-filter pre-selection needs to verify a match (id, vec_slot, source,
// kind, meta — never text, pid or chunk_no). limit caps the rows read (0 means
// unlimited); a pre-selection passes limit+1 so it can tell that a filter is
// not selective without scanning the whole table. It is the cheap pre-selection
// seam for metadata-filtered search: the caller compiles a predicate that is a
// SUPERSET of the true matches and verifies every returned passage with the
// authoritative filter engine, so a loose predicate costs extra verification
// but never drops a match. The clause is built by trusted code, never from user
// input directly; values go through args placeholders. The returned Passage
// values carry empty Text/PID and ChunkNo 0: they are for filter verification,
// not for serving results.
func (s *Store) SelectByPredicate(ctx context.Context, where string, args []any, limit int) ([]Passage, error) {
	q := `SELECT id, vec_slot, source, kind, meta FROM passages WHERE ` + where + ` ORDER BY id`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := s.reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("selecting passages by predicate: %w", err)
	}
	defer rows.Close()
	var out []Passage
	for rows.Next() {
		var p Passage
		var slot sql.NullInt64
		if err := rows.Scan(&p.ID, &slot, &p.Source, &p.Kind, &p.Meta); err != nil {
			return nil, fmt.Errorf("selecting passages by predicate: %w", err)
		}
		if slot.Valid {
			v := slot.Int64
			p.VecSlot = &v
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("selecting passages by predicate: %w", err)
	}
	return out, nil
}

// Count returns the number of stored passages.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM passages`).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting passages: %w", err)
	}
	return n, nil
}

// SlotSpan returns how many passages the store holds, the highest occupied
// vec_slot (-1 when none) and how many passages have no slot (lexical only).
// It reads aggregates only, no passage text, so index health checks can run
// it on every load (the format-2 counterpart of the bbolt idSpan).
func (s *Store) SlotSpan(ctx context.Context) (count int, maxSlot int64, slotless int, err error) {
	err = s.reader.QueryRowContext(ctx,
		`SELECT count(*), coalesce(max(vec_slot), -1), count(*) - count(vec_slot) FROM passages`).
		Scan(&count, &maxSlot, &slotless)
	if err != nil {
		return 0, -1, 0, fmt.Errorf("reading slot span: %w", err)
	}
	return count, maxSlot, slotless, nil
}

// Meta returns the value of a metadata key, or "" when the key is absent.
func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.reader.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading meta key %q: %w", key, err)
	}
	return v, nil
}

// SetMeta writes a metadata key in its own transaction.
//
// It is BeginWrite + SetMetaTx + Commit. A vector publication writes its
// pending keys in the same transaction as the slot changes, so it calls
// SetMetaTx directly (RFC 003 section 3.4).
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		return fmt.Errorf("writing meta key %q: %w", key, err)
	}
	defer tx.Rollback()
	if err := s.SetMetaTx(ctx, tx, key, value); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("writing meta key %q: committing: %w", key, err)
	}
	return nil
}

// SetMetaTx writes a metadata key inside a caller-owned transaction (from
// BeginWrite), so it commits atomically with the data it describes.
func (s *Store) SetMetaTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
  ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("writing meta key %q: %w", key, err)
	}
	return nil
}

// DeleteMetaTx removes a metadata key inside a caller-owned transaction (from
// BeginWrite). The vector publication's finish step deletes the pending keys
// in the same transaction that records the new vector_generation.
func (s *Store) DeleteMetaTx(ctx context.Context, tx *sql.Tx, key string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM meta WHERE key = ?`, key); err != nil {
		return fmt.Errorf("deleting meta key %q: %w", key, err)
	}
	return nil
}
