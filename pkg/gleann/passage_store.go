package gleann

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/store"
)

// PassageStore is the record-store abstraction the searcher, builder helpers
// and MCP read passages through (RFC 003 section 8 step 4), backed by the
// SQLite record store (internal/store). The ID space a caller sees is the
// vector slot (vec_slot): for a vector hit it is the position the backend
// returned, and the lexical leg maps its rowids to the same slots, so both
// legs address the same passages and can be fused. A passage WITHOUT a slot
// (vec_slot NULL: the lexical-only staging of a build, RFC 003 section 3.6)
// has no vector position; its visible ID is the NEGATIVE internal rowid
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

	// idSpan is package-internal so the cheap health check does not widen
	// the public surface.
	idSpan() (count int, maxID int64, err error)
}

var _ PassageStore = (*storePassages)(nil)

// storeGuardPrefix marks the non-empty, non-bbolt guard files that the v1.9
// migration left at the old names (<name>.passages.db, <name>.manifest.json).
// They are leftovers of a migrated index now; the detection below and
// `index migrate --cleanup` recognise them.
const storeGuardPrefix = "gleann store format 2"

// isGuardFile reports whether path is one of the v1.9 format-2 guard files.
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

// StoreFormat is the three-way result of DetectStoreFormat.
type StoreFormat int

const (
	// StoreNone: no index exists at the path.
	StoreNone StoreFormat = iota
	// StoreOld: an index in the old (v1.9 format-1) layout - bbolt passages,
	// a .lex.db sidecar or a Kùzu <name>_graph directory - without a
	// format-2 database. Only `gleann index migrate` reads it.
	StoreOld
	// StoreV2: an index in the single-SQLite-file (format-2) layout.
	StoreV2
)

// ErrIndexNotFound is returned where an operation needs an existing index and
// there is none. It wraps fs.ErrNotExist.
var ErrIndexNotFound = fmt.Errorf("index not found: %w", fs.ErrNotExist)

// ErrOldStoreFormat matches (errors.Is) the refusal of every operation on an
// index in the old store format (RFC 004 B2).
var ErrOldStoreFormat = errors.New("old store format")

// OldFormatError is the refusal for an index in the old store format: every
// command except `gleann index migrate` (and `index remove` / `index list`)
// stops with it.
type OldFormatError struct{ Name string }

func (e *OldFormatError) Error() string {
	return fmt.Sprintf("index %s uses the old store format; run: gleann index migrate %s", e.Name, e.Name)
}

// Is makes errors.Is(err, ErrOldStoreFormat) true.
func (e *OldFormatError) Is(target error) bool { return target == ErrOldStoreFormat }

// CheckStoreFormat returns an *OldFormatError when the index called name
// under indexDir is in the old store format, nil otherwise (format 2 or no
// index at all).
func CheckStoreFormat(indexDir, name string) error {
	if DetectStoreFormat(filepath.Join(indexDir, name, name)) == StoreOld {
		return &OldFormatError{Name: name}
	}
	return nil
}

// DetectStoreFormat detects the on-disk format of the index at basePath
// (<IndexDir>/<name>/<name>): the meta.json store_format field 2 wins;
// otherwise a present <name>.db with an absent or guarded <name>.passages.db
// means format 2; any other trace of an index - a meta.json, a bbolt
// <name>.passages.db, a <name>.lex.db sidecar, a vector file or a Kùzu
// <name>_graph directory - is the old format; no trace at all is StoreNone.
func DetectStoreFormat(basePath string) StoreFormat {
	hasMeta := false
	if data, err := os.ReadFile(basePath + ".meta.json"); err == nil {
		hasMeta = true
		var m struct {
			StoreFormat int `json:"store_format"`
		}
		if json.Unmarshal(data, &m) == nil && m.StoreFormat == 2 {
			return StoreV2
		}
	}
	if _, err := os.Stat(basePath + ".db"); err == nil {
		if _, err := os.Stat(basePath + ".passages.db"); os.IsNotExist(err) {
			return StoreV2
		}
		if isGuardFile(basePath + ".passages.db") {
			return StoreV2
		}
		return StoreOld
	}
	if hasMeta {
		return StoreOld
	}
	for _, ext := range []string{".passages.db", ".lex.db", ".index"} {
		if _, err := os.Stat(basePath + ext); err == nil {
			return StoreOld
		}
	}
	name := filepath.Base(basePath)
	if fi, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(basePath)), name+"_graph")); err == nil && fi.IsDir() {
		return StoreOld
	}
	return StoreNone
}

// StoreFormatOf is the two-way form of DetectStoreFormat: 2 for format 2,
// otherwise 1 - which includes an index that does not exist. Callers only ask
// "is it format 2?", for which a missing index correctly answers no.
func StoreFormatOf(basePath string) int {
	if DetectStoreFormat(basePath) == StoreV2 {
		return 2
	}
	return 1
}

// OpenPassageStore opens the record store of the format-2 index at basePath.
// Read-only instances are used by searchers. An index in the old store
// format is refused with an *OldFormatError, a missing one with
// ErrIndexNotFound.
func OpenPassageStore(basePath string, readOnly bool) (PassageStore, error) {
	switch DetectStoreFormat(basePath) {
	case StoreV2:
		return openStorePassages(basePath, readOnly)
	case StoreOld:
		return nil, &OldFormatError{Name: filepath.Base(basePath)}
	}
	return nil, ErrIndexNotFound
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
		return nil, fmt.Errorf("index %q is store format 2 but its record store %s.db is missing; "+
			"rebuild the index from its sources: gleann index rebuild %s",
			name, name, name)
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

// itemSource is the source path of an item's metadata: the first result of
// splitItemMeta without marshalling the rest. Grouping items by source and
// guessing the corpus language need only this; calling splitItemMeta there
// marshalled every item's metadata to JSON and threw the result away.
func itemSource(md map[string]any) string {
	source, _ := md["source"].(string)
	return source
}

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
// lexical leg: the FTS scorer reads <name>.db directly (RFC 003 section 8
// step 4) with the fixed query options (AND first, document-frequency cut
// 0.2; RFC 004 B1). It shares the passage store's database handle and maps
// between lex rowids and vec_slots (slotIDs). The write path keeps the lex
// rows fresh; if the analyzer identity is missing or differs, the table is
// rebuilt once from the stored texts.
func (s *LeannSearcher) openFormat2Scorer(ctx context.Context) (*FTSScorer, error) {
	sp, ok := s.passages.(*storePassages)
	if !ok {
		return nil, fmt.Errorf("index %q has no format-2 passage store", s.meta.Name)
	}
	if an, err := sp.st.Meta(ctx, "analyzer"); err != nil || an != sp.analyzer.Identity() {
		if err := sp.st.Rebuild(ctx, sp.analyzer); err != nil {
			return nil, fmt.Errorf("rebuilding the lexical table of %q: %w", s.meta.Name, err)
		}
	}
	return &FTSScorer{store: sp.st, analyzer: sp.analyzer, slotIDs: true, opts: LexicalQueryOptions()}, nil
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
	if md == nil {
		// A meta text of "null" resets the map to nil.
		md = make(map[string]any)
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
	// After incremental updates slots are sparse (dead slots are never
	// reused): the rule is that every passage has a slot below the high-water
	// mark. Without a recorded mark the slots must be dense.
	if vslots, _ := currentVectorSlots(ctx, sp.st); vslots > 0 {
		return slotless == 0 && maxSlot < vslots, nil
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
	_ = removeDBFiles(sp.basePath + ".db")
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
		src := itemSource(it.Metadata)
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
			kind := passageKind(src)
			s := slot
			slot++
			ids[i] = s
			delta.Passages = append(delta.Passages, store.NewPassage{
				Text:    it.Text,
				Kind:    kind,
				Meta:    metaJSON,
				VecSlot: &s,
				Tokens:  sp.analyzer.TokenString(it.Text, analysis.Kind(kind)),
			})
		}
		if _, err := sp.st.ApplySource(ctx, delta); err != nil {
			return nil, fmt.Errorf("adding source %q: %w", src, err)
		}
	}
	sp.cached = nil
	return ids, nil
}
