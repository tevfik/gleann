package store

import (
	"context"
	"fmt"
)

// Page bounds of the read-only listing queries (ListPassages, ListSections).
// ListPassages serves the REST passage/section listing (RFC 004 G8) and
// `?limit=N` must never read the whole passages table: a page is one
// `ORDER BY id LIMIT ? OFFSET ?` over the integer primary key.
const (
	// DefaultListLimit is the page size when a caller passes limit 0.
	DefaultListLimit = 100
	// MaxListLimit is the largest page size a caller may ask for.
	MaxListLimit = 1000
)

// normalizePage validates a (limit, offset) page: a negative value is an
// error (never a silent clamp), limit 0 means DefaultListLimit and limit above
// MaxListLimit is refused so one request cannot stream the whole index.
func normalizePage(limit, offset int) (int, error) {
	if offset < 0 {
		return 0, fmt.Errorf("offset must not be negative, got %d", offset)
	}
	if limit < 0 {
		return 0, fmt.Errorf("limit must not be negative, got %d", limit)
	}
	if limit == 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		return 0, fmt.Errorf("limit %d exceeds the maximum of %d", limit, MaxListLimit)
	}
	return limit, nil
}

// listPassagesQuery reads one page in internal id order. The id column is the
// table's integer primary key, so SQLite walks the table in id order and stops
// after LIMIT rows; it never sorts (no temporary b-tree) and never touches the
// vector files.
const listPassagesQuery = `SELECT ` + passageCols + ` FROM passages ORDER BY id LIMIT ? OFFSET ?`

// ListPassages returns up to limit passages in ascending internal id order,
// starting at offset. It reads the passages table only — no vector file, no
// lexical table, no document graph — so a metadata listing is cheap and works
// while a sync is writing (WAL: readers do not block the writer).
//
// limit 0 means DefaultListLimit; a negative limit/offset or a limit above
// MaxListLimit is an error. An empty store returns an empty slice, not an
// error.
func (s *Store) ListPassages(ctx context.Context, limit, offset int) ([]Passage, error) {
	limit, err := normalizePage(limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing passages: %w", err)
	}
	rows, err := s.reader.QueryContext(ctx, listPassagesQuery, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing passages: %w", err)
	}
	defer rows.Close()
	out := make([]Passage, 0, limit)
	for rows.Next() {
		p, err := scanPassage(rows)
		if err != nil {
			return nil, fmt.Errorf("listing passages: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing passages: %w", err)
	}
	return out, nil
}

// Section is one heading-delimited group of passages, aggregated from the
// section metadata chunkers store in passages.meta (RFC 004 G8). See
// ListSections for how the group key and the fallbacks are defined.
type Section struct {
	// Source is the document the section belongs to (passages.source).
	Source string
	// SectionID is the chunker's section ID (e.g. "s0.1"); empty when the
	// chunker wrote only a heading/title.
	SectionID string
	// Title is section_title, falling back to the "heading" key of the
	// document extractors and then to "title".
	Title string
	// Path is the heading breadcrumb ("Introduction > Details") when known.
	Path string
	// Summary is the section summary when the chunker wrote one.
	Summary string
	// PassageCount is how many passages this section contributed.
	PassageCount int
	// FirstChunk and LastChunk bound the section's chunk_no range within its
	// source, so a caller can tell where the section sits in the document.
	FirstChunk int
	LastChunk  int
	// FirstPassage and LastPassage bound the internal passage IDs, so a
	// listing page can be extended with ListPassages.
	FirstPassage PassageID
	LastPassage  PassageID
}

// sectionsQuery groups the stored passages by their heading metadata. The
// group key is (source, section_id, title, path) so two sections that share a
// title but have distinct IDs stay apart, while the chunks of one section
// (identical metadata, as modules/chunking writes them) collapse into one row.
//
// title falls back from section_title to "heading" (internal/indexing and
// pkg/gleann document extractors) and then to "title". A passage with none of
// section_id, section_title, heading or title belongs to no section and is
// excluded: the grouping never returns a bogus group with an empty heading for
// every section-less document. json_valid guards damaged metadata rows (the
// repair tooling also reads those) instead of failing the whole listing.
//
// The scalar subquery extracts each metadata key once per row (json_extract
// parses the JSON, so repeating it in WHERE and SELECT would double the cost);
// the outer query then filters, groups and orders. The summary is picked
// deterministically with MIN over the non-empty values (MIN ignores NULLs)
// rather than as a bare column. ORDER BY source, first id keeps paging stable;
// both are aggregates computed by the GROUP BY.
const sectionsQuery = `
SELECT
  source,
  section_id,
  title,
  path,
  CAST(COALESCE(MIN(NULLIF(summary, '')), '') AS TEXT) AS summary,
  count(*) AS passage_count,
  min(chunk_no) AS first_chunk,
  max(chunk_no) AS last_chunk,
  min(id) AS first_passage,
  max(id) AS last_passage
FROM (
  SELECT
    id,
    source,
    chunk_no,
    CAST(COALESCE(json_extract(meta, '$.section_id'), '') AS TEXT) AS section_id,
    CAST(COALESCE(json_extract(meta, '$.section_title'),
                  json_extract(meta, '$.heading'),
                  json_extract(meta, '$.title'), '') AS TEXT) AS title,
    CAST(COALESCE(json_extract(meta, '$.section_path'), '') AS TEXT) AS path,
    CAST(COALESCE(json_extract(meta, '$.section_summary'), '') AS TEXT) AS summary
  FROM passages
  WHERE json_valid(meta)
) AS section_rows
WHERE section_id <> '' OR title <> ''
GROUP BY source, section_id, title, path
ORDER BY source, first_passage
LIMIT ? OFFSET ?`

// ListSections returns up to limit sections in (source, first passage id)
// order, starting at offset. The grouping runs in SQLite over the meta column
// only (no passage text and no vector access), so a listing does not load the
// index.
//
// Passages without any section metadata are not sections and are left out; an
// empty store therefore returns an empty slice. limit 0 means
// DefaultListLimit, and the same bounds as ListPassages apply.
func (s *Store) ListSections(ctx context.Context, limit, offset int) ([]Section, error) {
	limit, err := normalizePage(limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing sections: %w", err)
	}
	rows, err := s.reader.QueryContext(ctx, sectionsQuery, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing sections: %w", err)
	}
	defer rows.Close()
	out := make([]Section, 0, limit)
	for rows.Next() {
		var sec Section
		if err := rows.Scan(&sec.Source, &sec.SectionID, &sec.Title, &sec.Path, &sec.Summary,
			&sec.PassageCount, &sec.FirstChunk, &sec.LastChunk, &sec.FirstPassage, &sec.LastPassage); err != nil {
			return nil, fmt.Errorf("listing sections: %w", err)
		}
		out = append(out, sec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing sections: %w", err)
	}
	return out, nil
}
