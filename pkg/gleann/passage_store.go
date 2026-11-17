package gleann

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/store"
)

// PassageStore is the record-store abstraction the searcher, builder helpers
// and MCP read passages through (RFC 003 section 8 step 4). Today's bbolt
// PassageManager satisfies it unchanged; the format-2 implementation below is
// backed by the SQLite record store (internal/store). The ID space a caller
// sees is the vector slot (vec_slot): for a vector hit it is the position the
// backend returned, and the lexical leg maps its rowids to the same slots, so
// both legs address the same passages and can be fused. A passage WITHOUT a
// slot (vec_slot NULL: the lexical-only staging of a build, RFC 003 section
// 3.6) has no vector position; its visible ID is the NEGATIVE internal rowid
// (-rowid), which cannot collide with a vec_slot (always >= 0). Get/GetBatch
// and the FTS scorer map negative IDs back to rowids.
type PassageStore interface {
	Add(items []Item) ([]int64, error)
	Get(id int64) (Passage, error)
	GetBatch(ids []int64) ([]Passage, error)
	GetTexts(ids []int64) ([]string, error)
	All() []Passage
	Count() int
	Load() error
	LoadAll() error
	LoadAllWithLimit(limit int) error
	ForEachPassage(fn func(Passage) error) error
	Close() error
	RemoveBySource(sources []string) ([]int64, error)
	HasSources(sources []string) (bool, error)
	IsDense() (bool, error)
	RollbackAdd(ids []int64) error
	Delete() error

	// idSpan and storeFormat are package-internal so both implementations can
	// share the cheap health check and the format dispatch without widening
	// the public surface.
	idSpan() (count int, maxID int64, err error)
	storeFormat() int
}

var (
	_ PassageStore = (*PassageManager)(nil)
	_ PassageStore = (*storePassages)(nil)
)

// storeFormat reports the on-disk format of a bbolt PassageManager (always 1).
func (pm *PassageManager) storeFormat() int { return 1 }

// storeGuardPrefix marks the non-empty, non-bbolt guard files left at the old
// names after a migration (RFC 003 sections 3.3 and 6.4 step 4). An old
// binary's bbolt.Open on a non-empty non-bbolt file fails instead of silently
// creating a fresh empty store, which is exactly what protects the index.
const storeGuardPrefix = "gleann store format 2"

// storeGuardText is the content written to the guard files. It is deliberately
// not valid bbolt and not empty, so an old reader errors out loudly.
func storeGuardText() string {
	return storeGuardPrefix + ": this index uses the SQLite record store. " +
		"Read it with gleann " + GetGleannVersion() + " or newer (gleann index migrate --rollback restores the old files). " +
		"Do not open this file with bbolt.\n"
}

// isGuardFile reports whether path is one of the format-2 guard files.
func isGuardFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len(storeGuardPrefix))
	n, err := io.ReadFull(f, buf)
	return err == nil && n == len(storeGuardPrefix) && string(buf) == storeGuardPrefix
}

// StoreFormatOf detects the on-disk format of the index at basePath
// (<IndexDir>/<name>/<name>) per RFC 003 section 6.3: the meta.json
// store_format field wins; otherwise a present <name>.db with an absent or
// guarded <name>.passages.db means format 2. Everything else is format 1.
// Existing format-2 indexes are always read as format 2 regardless of the
// GLEANN_STORE_FORMAT switch, which only governs NEW indexes.
func StoreFormatOf(basePath string) int {
	if data, err := os.ReadFile(basePath + ".meta.json"); err == nil {
		var m struct {
			StoreFormat int `json:"store_format"`
		}
		if json.Unmarshal(data, &m) == nil && m.StoreFormat == 2 {
			return 2
		}
	}
	if _, err := os.Stat(basePath + ".db"); err == nil {
		if _, err := os.Stat(basePath + ".passages.db"); os.IsNotExist(err) {
			return 2
		}
		if isGuardFile(basePath + ".passages.db") {
			return 2
		}
	}
	return 1
}

// storeFormatSwitch reads GLEANN_STORE_FORMAT once, where a builder is
// created, and returns the format for NEW indexes (no package-level mutable
// state). Unset or "1" keeps today's bbolt format; "2" selects the SQLite
// record store. Any other value falls back to the default so a typo cannot
// silently change the on-disk format (RFC 003 section 8 step 4).
func storeFormatSwitch() int {
	if strings.TrimSpace(os.Getenv("GLEANN_STORE_FORMAT")) == "2" {
		return 2
	}
	return 1
}

// OpenPassageStore opens the record store for the index at basePath in the
// format detected on disk. Read-only instances are used by searchers; the
// builder opens its own writable handle for the write path.
func OpenPassageStore(basePath string, readOnly bool) (PassageStore, error) {
	if StoreFormatOf(basePath) == 2 {
		return openStorePassages(basePath, readOnly)
	}
	if readOnly {
		return NewReadOnlyPassageManager(basePath), nil
	}
	return NewPassageManager(basePath), nil
}

// storePassages is the format-2 PassageStore over internal/store. The ID space
// exposed to callers is vec_slot (see PassageStore). The meta column holds the
// passage metadata minus source and chunk_index, which live in their own
// columns and are re-attached on read.
type storePassages struct {
	st       *store.Store
	basePath string
	readOnly bool
	analyzer analysis.Analyzer
	cached   []Passage
}

// openFormat2Store opens the existing format-2 database of the index at
// basePath. It refuses a missing <name>.db instead of letting store.Open
// create an empty one: meta.json recording format 2 without the file is a
// crash state (an interrupted rollback, or a lost file), and an empty store
// would silently serve no passages and let the next sync overwrite the index.
func openFormat2Store(basePath string) (*store.Store, error) {
	if _, err := os.Stat(basePath + ".db"); err != nil {
		name := filepath.Base(basePath)
		return nil, fmt.Errorf("index %q is store format 2 but its record store %s.db is missing (a crash may have interrupted a rollback); "+
			"restore the format-1 files with: gleann index migrate --rollback %s, or repair the index: gleann index repair %s",
			name, name, name, name)
	}
	st, err := store.Open(basePath+".db", store.Options{})
	if err != nil {
		return nil, fmt.Errorf("opening format-2 store: %w", err)
	}
	return st, nil
}

func openStorePassages(basePath string, readOnly bool) (PassageStore, error) {
	st, err := openFormat2Store(basePath)
	if err != nil {
		return nil, err
	}
	// Finish or discard an interrupted vector publication before any reader
	// trusts the slots (RFC 003 section 3.4). This may write even on a
	// read-only open: it only completes a publication the builder already
	// committed to the database, never new data.
	if err := recoverVectorPublication(context.Background(), st, basePath); err != nil {
		st.Close()
		return nil, fmt.Errorf("recovering vector publication: %w", err)
	}
	lang, _ := st.Meta(context.Background(), "language")
	return &storePassages{
		st:       st,
		basePath: basePath,
		readOnly: readOnly,
		analyzer: analysis.New(lang),
	}, nil
}

func (sp *storePassages) storeFormat() int { return 2 }

// splitItemMeta separates the source path from the rest of an item's metadata
// and marshals the rest for the meta column. chunk_index is dropped here: it
// is recomputed as chunk_no (the passage's position within its source).
func splitItemMeta(md map[string]any) (source, metaJSON string) {
	rest := make(map[string]any, len(md))
	for k, v := range md {
		switch k {
		case "source":
			if s, ok := v.(string); ok {
				source = s
			}
		case "chunk_index":
			// stored as chunk_no
		default:
			rest[k] = v
		}
	}
	if len(rest) == 0 {
		return source, "{}"
	}
	b, err := json.Marshal(rest)
	if err != nil {
		return source, "{}"
	}
	return source, string(b)
}

// openFormat2Scorer wraps a format-2 index's own lex table as the persisted
// lexical leg: the FTS scorer reads <name>.db directly, no sidecar (RFC 003
// section 8 step 4). It shares the passage store's database handle and maps
// between lex rowids and vec_slots (slotIDs). The write path keeps the lex
// rows fresh; if the analyzer identity is missing or differs, the table is
// rebuilt once from the stored texts. On any failure it returns nil and Load
// falls back to the in-RAM BM25 build, so a search never fails.
func (s *LeannSearcher) openFormat2Scorer(ctx context.Context) *FTSScorer {
	sp, ok := s.passages.(*storePassages)
	if !ok {
		return nil
	}
	if an, err := sp.st.Meta(ctx, "analyzer"); err != nil || an != analysis.ID {
		if err := sp.st.Rebuild(ctx, sp.analyzer); err != nil {
			log.Printf("ℹ️  Rebuilding lexical table for %q failed (%v); using in-memory BM25 for this load", s.meta.Name, err)
			return nil
		}
	}
	return &FTSScorer{store: sp.st, analyzer: sp.analyzer, slotIDs: true, opts: parseLexicalOpts(os.Getenv("GLEANN_LEXICAL_OPTS"))}
}

// storePassageToGleann converts a store row to the searcher's Passage, using
// vec_slot as the visible ID and re-attaching source and chunk_index. A row
// without a slot (lexical-only staging) is visible as the negative internal
// rowid: vec_slots are >= 0, so the two ID ranges cannot collide, and
// Get/GetBatch and the FTS scorer map negative IDs back to rowids.
func storePassageToGleann(p store.Passage) Passage {
	gp := Passage{Text: p.Text, PID: p.PID}
	if p.VecSlot != nil {
		gp.ID = *p.VecSlot
	} else {
		gp.ID = -p.ID
	}
	md := make(map[string]any)
	if p.Meta != "" && p.Meta != "{}" {
		_ = json.Unmarshal([]byte(p.Meta), &md)
	}
	if p.Source != "" {
		md["source"] = p.Source
	}
	md["chunk_index"] = p.ChunkNo
	gp.Metadata = md
	return gp
}

func (sp *storePassages) Load() error { return nil } // opened in the constructor

// Get looks a passage up by its visible ID: a non-negative ID is a vec_slot,
// a negative ID is the negated internal rowid of a slotless passage (see
// PassageStore).
func (sp *storePassages) Get(id int64) (Passage, error) {
	ctx := context.Background()
	if id < 0 {
		p, err := sp.st.Get(ctx, -id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Passage{}, fmt.Errorf("passage %d not found", id)
			}
			return Passage{}, fmt.Errorf("getting passage %d: %w", id, err)
		}
		return storePassageToGleann(p), nil
	}
	ps, err := sp.st.BySlot(ctx, []int64{id})
	if err != nil {
		return Passage{}, fmt.Errorf("getting passage %d: %w", id, err)
	}
	if len(ps) == 0 {
		return Passage{}, fmt.Errorf("passage %d not found", id)
	}
	return storePassageToGleann(ps[0]), nil
}

func (sp *storePassages) GetBatch(ids []int64) ([]Passage, error) {
	ctx := context.Background()
	// Split the visible IDs into vec_slots (>= 0) and negated rowids (< 0),
	// look each group up in its own space, then reassemble in caller order.
	var slots, rowids []int64
	for _, id := range ids {
		if id < 0 {
			rowids = append(rowids, -id)
		} else {
			slots = append(slots, id)
		}
	}
	byID := make(map[int64]store.Passage, len(ids))
	if len(slots) > 0 {
		ps, err := sp.st.BySlot(ctx, slots)
		if err != nil {
			return nil, fmt.Errorf("getting passages: %w", err)
		}
		for _, p := range ps {
			if p.VecSlot != nil {
				byID[*p.VecSlot] = p
			}
		}
	}
	if len(rowids) > 0 {
		ps, err := sp.st.GetMany(ctx, rowids)
		if err != nil {
			return nil, fmt.Errorf("getting passages: %w", err)
		}
		for _, p := range ps {
			byID[-p.ID] = p
		}
	}
	// The lookups skip missing IDs; GetBatch must return one entry per
	// requested ID, so error on any that is absent.
	out := make([]Passage, len(ids))
	for i, id := range ids {
		p, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("passage %d not found", id)
		}
		out[i] = storePassageToGleann(p)
	}
	return out, nil
}

func (sp *storePassages) GetTexts(ids []int64) ([]string, error) {
	ps, err := sp.GetBatch(ids)
	if err != nil {
		return nil, err
	}
	texts := make([]string, len(ps))
	for i, p := range ps {
		texts[i] = p.Text
	}
	return texts, nil
}

func (sp *storePassages) All() []Passage {
	if sp.cached != nil {
		return sp.cached
	}
	var out []Passage
	_ = sp.st.ForEach(context.Background(), func(p store.Passage) error {
		out = append(out, storePassageToGleann(p))
		return nil
	})
	return out
}

func (sp *storePassages) Count() int {
	n, err := sp.st.Count(context.Background())
	if err != nil {
		return 0
	}
	return n
}

func (sp *storePassages) LoadAll() error { return sp.LoadAllWithLimit(0) }

func (sp *storePassages) LoadAllWithLimit(limit int) error {
	if sp.cached != nil {
		return nil
	}
	var out []Passage
	err := sp.st.ForEach(context.Background(), func(p store.Passage) error {
		if limit > 0 && len(out) >= limit {
			return nil
		}
		out = append(out, storePassageToGleann(p))
		return nil
	})
	if err != nil {
		return fmt.Errorf("loading passages: %w", err)
	}
	sp.cached = out
	return nil
}

func (sp *storePassages) ForEachPassage(fn func(Passage) error) error {
	return sp.st.ForEach(context.Background(), func(p store.Passage) error {
		return fn(storePassageToGleann(p))
	})
}

func (sp *storePassages) Close() error {
	sp.cached = nil
	return sp.st.Close()
}

func (sp *storePassages) HasSources(sources []string) (bool, error) {
	set := make(map[string]bool, len(sources))
	for _, s := range sources {
		set[s] = true
	}
	errStop := errors.New("stop")
	found := false
	err := sp.st.ForEach(context.Background(), func(p store.Passage) error {
		if set[p.Source] {
			found = true
			return errStop
		}
		return nil
	})
	// store.ForEach wraps the callback error with %w, so the sentinel survives.
	if err != nil && !errors.Is(err, errStop) {
		return false, err
	}
	return found, nil
}

func (sp *storePassages) idSpan() (count int, maxID int64, err error) {
	count, maxID, _, err = sp.st.SlotSpan(context.Background())
	if err != nil {
		return 0, -1, err
	}
	return count, maxID, nil
}

func (sp *storePassages) IsDense() (bool, error) {
	ctx := context.Background()
	count, maxSlot, slotless, err := sp.st.SlotSpan(ctx)
	if err != nil {
		return false, err
	}
	return slotless == 0 && int64(count) == maxSlot+1, nil
}

func (sp *storePassages) RemoveBySource(sources []string) ([]int64, error) {
	if sp.readOnly {
		return nil, fmt.Errorf("passage store is read-only")
	}
	ctx := context.Background()
	set := make(map[string]bool, len(sources))
	for _, s := range sources {
		set[s] = true
	}
	var removed []int64
	err := sp.st.ForEach(ctx, func(p store.Passage) error {
		if set[p.Source] && p.VecSlot != nil {
			removed = append(removed, *p.VecSlot)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, s := range sources {
		if _, err := sp.st.RemoveSource(ctx, s); err != nil {
			return nil, fmt.Errorf("removing source %q: %w", s, err)
		}
	}
	sp.cached = nil
	return removed, nil
}

// RollbackAdd is not supported in format 2: there is no in-place append whose
// ID sequence could be rewound (the write path always compacts). It is never
// called on a format-2 store; returning a clear error keeps that invariant.
func (sp *storePassages) RollbackAdd(ids []int64) error {
	return fmt.Errorf("rollback add is not supported on a format-2 index")
}

func (sp *storePassages) Delete() error {
	if sp.readOnly {
		return fmt.Errorf("passage store is read-only")
	}
	if err := sp.st.Close(); err != nil {
		return err
	}
	sp.cached = nil
	removeLexicalFiles(sp.basePath + ".db")
	return nil
}

// Add appends items, grouping them by source and assigning dense vector slots
// after the current last slot. The format-2 builder uses ApplySource directly
// for its per-file transactions; this satisfies the interface for callers that
// add through the abstraction.
func (sp *storePassages) Add(items []Item) ([]int64, error) {
	if sp.readOnly {
		return nil, fmt.Errorf("passage store is read-only")
	}
	ctx := context.Background()
	n, err := sp.st.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("counting passages: %w", err)
	}
	slot := int64(n)
	ids := make([]int64, len(items))
	// Group by source, preserving first-appearance order and chunk order.
	order := []string{}
	groups := make(map[string][]int)
	for i, it := range items {
		src, _ := splitItemMeta(it.Metadata)
		if _, ok := groups[src]; !ok {
			order = append(order, src)
		}
		groups[src] = append(groups[src], i)
	}
	for _, src := range order {
		delta := store.SourceDelta{Source: src}
		for _, i := range groups[src] {
			it := items[i]
			_, metaJSON := splitItemMeta(it.Metadata)
			kind := sidecarKind(src)
			s := slot
			slot++
			ids[i] = s
			delta.Passages = append(delta.Passages, store.NewPassage{
				Text:    it.Text,
				Kind:    kind,
				Meta:    metaJSON,
				VecSlot: &s,
				Tokens:  strings.Join(sp.analyzer.Tokens(it.Text, analysis.Kind(kind)), " "),
			})
		}
		if _, err := sp.st.ApplySource(ctx, delta); err != nil {
			return nil, fmt.Errorf("adding source %q: %w", src, err)
		}
	}
	sp.cached = nil
	return ids, nil
}
