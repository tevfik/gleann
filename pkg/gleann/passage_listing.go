package gleann

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/tevfik/gleann/internal/store"
)

// Page bounds of the passage/section listing endpoints. They are the store's
// bounds, re-exported so the HTTP layer and its OpenAPI description cannot
// drift from the query layer.
const (
	// DefaultPassageListLimit is the page size when ?limit= is absent.
	DefaultPassageListLimit = store.DefaultListLimit
	// MaxPassageListLimit is the largest page size ?limit= may ask for.
	MaxPassageListLimit = store.MaxListLimit
)

// IndexPassage is one stored passage as the read-only listing returns it
// (RFC 004 G8, GET /api/indexes/{name}/passages). ID is the internal passage
// row ID — the listing's sort key, not a search result ID; VecSlot is the
// position in the vector file (the ID search results carry) and is nil for a
// lexical-only passage. ChunkNo is the passage's index within its source.
type IndexPassage struct {
	ID       int64          `json:"id"`
	PID      string         `json:"pid"`
	Source   string         `json:"source"`
	ChunkNo  int            `json:"chunk_index"`
	VecSlot  *int64         `json:"vec_slot"`
	Kind     string         `json:"kind"`
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata"`
}

// IndexSection is one heading-delimited group of stored passages
// (GET /api/indexes/{name}/sections). The fields mirror store.Section: the
// section metadata the chunkers write into passage metadata, the number of
// passages and where they sit in the source (chunk range) and in the store
// (internal passage ID range).
type IndexSection struct {
	Source         string `json:"source"`
	SectionID      string `json:"section_id"`
	Title          string `json:"title"`
	Path           string `json:"path"`
	Summary        string `json:"summary"`
	PassageCount   int    `json:"passage_count"`
	FirstChunk     int    `json:"first_chunk"`
	LastChunk      int    `json:"last_chunk"`
	FirstPassageID int64  `json:"first_passage_id"`
	LastPassageID  int64  `json:"last_passage_id"`
}

// ListStoredPassages lists the passages of the format-2 index name in internal
// id order, starting at offset. It opens only the index's SQLite record store:
// no vector file, no embedder and no searcher is loaded, so listing metadata
// costs one `ORDER BY id LIMIT ? OFFSET ?` page (RFC 004 G8).
//
// A missing index returns an error wrapping ErrIndexNotFound and creates no
// files; an index in the old store format returns *OldFormatError.
func ListStoredPassages(ctx context.Context, indexDir, name string, limit, offset int) ([]IndexPassage, error) {
	var out []IndexPassage
	err := withListingStore(indexDir, name, "passages", func(st *store.Store) error {
		ps, err := st.ListPassages(ctx, limit, offset)
		if err != nil {
			return err
		}
		out = make([]IndexPassage, len(ps))
		for i, p := range ps {
			out[i] = indexPassage(p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListStoredSections lists the heading groups of the format-2 index name in
// (source, first passage id) order, starting at offset. The grouping is the
// store's SQL aggregation over passage metadata, so it reads the meta column
// only. Passages without section metadata belong to no section and are left
// out; the same index-format errors as ListStoredPassages apply.
func ListStoredSections(ctx context.Context, indexDir, name string, limit, offset int) ([]IndexSection, error) {
	var out []IndexSection
	err := withListingStore(indexDir, name, "sections", func(st *store.Store) error {
		secs, err := st.ListSections(ctx, limit, offset)
		if err != nil {
			return err
		}
		out = make([]IndexSection, len(secs))
		for i, s := range secs {
			out[i] = IndexSection{
				Source:         s.Source,
				SectionID:      s.SectionID,
				Title:          s.Title,
				Path:           s.Path,
				Summary:        s.Summary,
				PassageCount:   s.PassageCount,
				FirstChunk:     s.FirstChunk,
				LastChunk:      s.LastChunk,
				FirstPassageID: s.FirstPassage,
				LastPassageID:  s.LastPassage,
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// indexPassage converts a store row to the listing shape. The stored meta
// column is decoded as the passage's metadata object (an unreadable value
// yields an empty object rather than failing the whole page).
func indexPassage(p store.Passage) IndexPassage {
	md := make(map[string]any)
	if p.Meta != "" && p.Meta != "{}" {
		_ = json.Unmarshal([]byte(p.Meta), &md)
	}
	if md == nil {
		// Unmarshaling JSON scalar "null" resets a non-nil map to nil; restore {}.
		md = make(map[string]any)
	}
	ip := IndexPassage{
		ID:       p.ID,
		PID:      p.PID,
		Source:   p.Source,
		ChunkNo:  p.ChunkNo,
		Kind:     p.Kind,
		Text:     p.Text,
		Metadata: md,
	}
	if p.VecSlot != nil {
		v := *p.VecSlot
		ip.VecSlot = &v
	}
	return ip
}

// withListingStore resolves the store format of the index first and only then
// opens the database, so a missing index is a clean ErrIndexNotFound and never
// an empty database created by store.Open. The database is closed when fn
// returns (a listing is a short, bounded read; no handle is cached).
func withListingStore(indexDir, name, what string, fn func(*store.Store) error) error {
	if err := checkPathName(name); err != nil {
		return err
	}
	base := filepath.Join(indexDir, name, name)
	switch DetectStoreFormat(base) {
	case StoreOld:
		return &OldFormatError{Name: name}
	case StoreV2:
		// fall through to the open below
	default:
		return fmt.Errorf("listing %s of index %q: %w", what, name, ErrIndexNotFound)
	}
	// openFormat2Store refuses a format-2 meta.json whose database is missing
	// instead of letting store.Open create an empty one (the same rule as the
	// searchers, RFC 004 B2).
	st, err := openFormat2Store(base)
	if err != nil {
		return fmt.Errorf("listing %s of index %q: %w", what, name, err)
	}
	defer st.Close()
	if err := fn(st); err != nil {
		return fmt.Errorf("listing %s of index %q: %w", what, name, err)
	}
	return nil
}
