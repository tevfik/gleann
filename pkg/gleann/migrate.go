package gleann

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"go.etcd.io/bbolt"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/store"
)

// This file implements `gleann index migrate` for v1.20 (RFC 004 B3): it
// converts an index in the old store format (v1.9 format 1: bbolt passages,
// <name>.manifest.json, in-memory or .lex.db lexical index) to format 2
// without re-embedding. It is the only code that still reads the old format;
// it is kept for v1.20.x and removed in v1.21. The code graph is rebuilt from
// the sources by the caller (internal/indexing), not here.

// ErrAlreadyFormat2 is returned by MigrateIndex for an index that is already
// format 2 (a re-run of `migrate --all` skips it).
var ErrAlreadyFormat2 = errors.New("already store format 2")

// MigrateOptions controls a migration.
type MigrateOptions struct {
	// Config supplies the vector backend settings used when the passage IDs
	// have to be renumbered and the main vector file is rebuilt from the
	// stored vectors. Zero means DefaultConfig with the index's backend.
	Config *Config
	// crashHook injects a failure at a named step of the switch (tests only;
	// see the migrateAfter* constants). Unexported: only in-package tests can
	// set it.
	crashHook func(point string) error
}

// MigrateResult says what a migration did.
type MigrateResult struct {
	// Passages is how many passages were copied.
	Passages int
	// Renumbered is true when the old passage IDs had gaps: the passages got
	// dense vector slots and the main vector file was rebuilt from the vectors
	// it stored (no embedding).
	Renumbered bool
	// HadGraph is true when the old index had a Kùzu code graph
	// (<name>_graph); the caller rebuilds the graph from the sources.
	HadGraph bool
}

// Crash-injection points of the migration switch, for tests only, in the
// switch's execution order.
const (
	migrateAfterIndexAside = "after_index_aside" // renumbered: old vector file renamed to .index.v1
	migrateAfterIndexNew   = "after_index_new"   // renumbered: rebuilt vector file in place
	migrateAfterDBRename   = "after_db_rename"   // <name>.db in place, meta.json not yet updated
)

// MigrationLeftovers lists the files of the old format that a migrated index
// still has next to it (they are never deleted implicitly; `index migrate
// --cleanup` deletes them, `gleann doctor` lists them). Paths are absolute
// when indexDir is.
func MigrationLeftovers(indexDir, name string) []string {
	base := filepath.Join(indexDir, name, name)
	var out []string
	for _, p := range []string{
		base + ".passages.db",
		base + ".passages.db.v1",
		base + ".manifest.json",
		base + ".manifest.json.v1",
		base + ".index.v1",
		base + ".lex.db",
		base + ".lex.db-wal",
		base + ".lex.db-shm",
		filepath.Join(indexDir, name+".lex.lock"),
		filepath.Join(indexDir, name+"_graph"),
		filepath.Join(indexDir, name+"_graph.wal"),
		filepath.Join(indexDir, name+"_graph.shadow"),
		filepath.Join(indexDir, name+"_graph_file_hashes.db"),
	} {
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// HadOldCodeGraph reports whether the index had a v1.9 Kùzu code graph
// (<IndexDir>/<name>_graph, a directory or a single-file database).
func HadOldCodeGraph(indexDir, name string) bool {
	_, err := os.Lstat(filepath.Join(indexDir, name+"_graph"))
	return err == nil
}

// CleanupMigration deletes the old-format leftovers of a migrated (format-2)
// index and returns the deleted paths. It refuses an index that is not
// format 2, so the only copy of an unmigrated index is never deleted.
func CleanupMigration(indexDir, name string) ([]string, error) {
	if err := checkPathName(name); err != nil {
		return nil, err
	}
	basePath := filepath.Join(indexDir, name, name)
	if DetectStoreFormat(basePath) != StoreV2 {
		return nil, fmt.Errorf("index %q is not store format 2; migrate it first: gleann index migrate %s", name, name)
	}
	lock, err := store.AcquireLock(filepath.Join(indexDir, name+".lock"))
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	var removed []string
	for _, p := range MigrationLeftovers(indexDir, name) {
		if err := os.RemoveAll(p); err != nil {
			return removed, fmt.Errorf("removing %s: %w", p, err)
		}
		removed = append(removed, p)
	}
	return removed, nil
}

// MigrateIndex converts the old-format index at indexDir/name to format 2.
// It takes the exclusive index lock, copies every passage into <name>.db.tmp
// (vector slot = old passage ID when the IDs are dense, so the vector file
// stays valid; otherwise dense slots and a main vector file rebuilt from the
// stored vectors), builds the FTS5 lexical rows with the analyzer a fresh
// build uses, verifies the copy and switches: the database rename and the
// meta.json store_format field are the last actions, so an interrupted run is
// completed or redone by the next one. The old files stay as leftovers.
func MigrateIndex(indexDir, name string, opts MigrateOptions) (*MigrateResult, error) {
	if err := checkPathName(name); err != nil {
		return nil, err
	}
	basePath := filepath.Join(indexDir, name, name)

	lock, err := store.AcquireLock(filepath.Join(indexDir, name+".lock"))
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	switch DetectStoreFormat(basePath) {
	case StoreNone:
		return nil, fmt.Errorf("migrating index %q: %w", name, ErrIndexNotFound)
	case StoreV2:
		// A crash between the database rename and the meta flag leaves the
		// database in place: record the flag.
		m, err := GetIndexMeta(indexDir, name)
		if err == nil && m.StoreFormat != 2 {
			if err := UpdateIndexMeta(indexDir, name, func(m *IndexMeta) { m.StoreFormat = 2 }); err != nil {
				return nil, fmt.Errorf("recording store format: %w", err)
			}
			return &MigrateResult{HadGraph: HadOldCodeGraph(indexDir, name)}, nil
		}
		return nil, ErrAlreadyFormat2
	}
	if _, err := os.Stat(basePath + ".db"); err == nil {
		// A real bbolt store next to a <name>.db without the meta flag: the
		// previous run renamed the database but crashed before recording the
		// flag. The database was verified before the rename: complete it.
		if err := UpdateIndexMeta(indexDir, name, func(m *IndexMeta) { m.StoreFormat = 2 }); err != nil {
			return nil, fmt.Errorf("recording store format: %w", err)
		}
		return &MigrateResult{HadGraph: HadOldCodeGraph(indexDir, name)}, nil
	}
	// Undo the leftovers of an interrupted switch (of this version or of the
	// v1.9 migration) so this run starts from a clean old-format index.
	if err := undoMigrationSwitch(basePath); err != nil {
		return nil, err
	}
	meta, err := GetIndexMeta(indexDir, name)
	if err != nil {
		return nil, fmt.Errorf("reading index meta: %w", err)
	}
	passages, err := readOldPassages(basePath)
	if err != nil {
		return nil, err
	}
	n := len(passages)
	if n == 0 {
		return nil, fmt.Errorf("index %q has no passages to migrate; rebuild it: gleann index rebuild %s", name, name)
	}
	res := &MigrateResult{Passages: n, HadGraph: HadOldCodeGraph(indexDir, name)}

	// Vector slots: the old ID when the IDs are dense (the vector file
	// addresses a vector by it), otherwise the position (renumbered).
	dense := true
	for i, p := range passages {
		if p.ID != int64(i) {
			dense = false
			break
		}
	}
	_, statErr := os.Stat(basePath + suffixMain)
	hasVectors := statErr == nil && meta.VectorReady && !meta.LexicalOnly
	var newIndex []byte
	if !dense {
		res.Renumbered = true
		if hasVectors {
			newIndex, err = renumberVectors(context.Background(), indexDir, basePath, meta, passages, opts.Config)
			if err != nil {
				return nil, fmt.Errorf("index %q has non-contiguous passage IDs and its vectors cannot be renumbered (%v); rebuild it from its sources: gleann index rebuild %s", name, err, name)
			}
		}
	}

	// The analyzer of a fresh build (builder_format2.go), with the same
	// corpus language guess.
	analyzer := analysis.New(guessLanguage(len(passages), func(i int) (string, string) {
		src, _ := passages[i].Metadata["source"].(string)
		return src, passages[i].Text
	}))

	// Group by source, ordering each source's passages by chunk_index so
	// chunk_no reproduces the original chunk order.
	slotOf := make(map[int64]int64, n)
	for i, p := range passages {
		if dense {
			slotOf[p.ID] = p.ID
		} else {
			slotOf[p.ID] = int64(i)
		}
	}
	groups := make(map[string][]Passage)
	order := []string{}
	for _, p := range passages {
		src, _ := p.Metadata["source"].(string)
		if _, ok := groups[src]; !ok {
			order = append(order, src)
		}
		groups[src] = append(groups[src], p)
	}
	for _, src := range order {
		sort.SliceStable(groups[src], func(i, j int) bool {
			return chunkIndexOf(groups[src][i]) < chunkIndexOf(groups[src][j])
		})
	}

	oldManifest, _ := LoadManifest(basePath + ".manifest.json")

	ctx := context.Background()
	tmpPath := basePath + ".db.tmp"
	_ = removeDBFiles(tmpPath)
	st, err := store.Open(tmpPath, store.Options{})
	if err != nil {
		return nil, fmt.Errorf("creating format-2 store: %w", err)
	}
	fail := func(err error) (*MigrateResult, error) {
		st.Close()
		_ = removeDBFiles(tmpPath)
		return nil, err
	}
	for _, src := range order {
		delta := store.SourceDelta{Source: src, File: migrateFileRow(src, oldManifest, meta.SourceDir)}
		for _, p := range groups[src] {
			slot := slotOf[p.ID]
			id := slot + 1 // internal rowid, dense and positive
			kind := passageKind(src)
			_, metaJSON := splitItemMeta(p.Metadata)
			np := store.NewPassage{
				ID:     &id,
				Text:   p.Text,
				Kind:   kind,
				Meta:   metaJSON,
				Tokens: analyzer.TokenString(p.Text, analysis.Kind(kind)),
			}
			if hasVectors {
				np.VecSlot = &slot
			}
			delta.Passages = append(delta.Passages, np)
		}
		if _, err := st.ApplySource(ctx, delta); err != nil {
			return fail(fmt.Errorf("migrating source %q: %w", src, err))
		}
	}
	// Manifest entries without passages still get a files row.
	if oldManifest != nil {
		for path, e := range oldManifest.Entries {
			if _, ok := groups[path]; ok {
				continue
			}
			fr := store.FileRow{Path: e.Path, Size: e.Size, MtimeNS: e.ModTime, Hash: e.Hash}
			if _, err := st.ApplySource(ctx, store.SourceDelta{Source: path, File: fr}); err != nil {
				return fail(fmt.Errorf("migrating file row %q: %w", path, err))
			}
		}
	}
	for _, kv := range [][2]string{{"analyzer", analyzer.Identity()}, {"language", analyzer.Language}} {
		if err := st.SetMeta(ctx, kv[0], kv[1]); err != nil {
			return fail(fmt.Errorf("setting store meta %q: %w", kv[0], err))
		}
	}

	// Verify the copy before switching. No embedding happens.
	if err := verifyMigration(ctx, st, analyzer, passages, slotOf, n); err != nil {
		return fail(err)
	}
	if hasVectors && newIndex == nil {
		if err := verifyVectorFile(ctx, indexDir, basePath, meta); err != nil {
			return fail(err)
		}
	}
	// Close every handle so the renames below are safe on Windows.
	if err := st.Close(); err != nil {
		_ = removeDBFiles(tmpPath)
		return nil, fmt.Errorf("closing format-2 store: %w", err)
	}

	hook := func(point string) error {
		if opts.crashHook == nil {
			return nil
		}
		return opts.crashHook(point)
	}
	// The switch. A renumbered index first puts its rebuilt vector file in
	// place (the old one is kept as .index.v1, which a re-run restores); the
	// database rename and the meta flag are the last actions.
	if newIndex != nil {
		staged := basePath + suffixMain + ".migrate"
		if err := os.WriteFile(staged, newIndex, 0o644); err != nil {
			_ = removeDBFiles(tmpPath)
			return nil, fmt.Errorf("staging the renumbered vector file: %w", err)
		}
		if err := os.Rename(basePath+suffixMain, basePath+suffixMain+".v1"); err != nil {
			_ = os.Remove(staged)
			_ = removeDBFiles(tmpPath)
			return nil, fmt.Errorf("preserving the old vector file: %w", err)
		}
		if err := hook(migrateAfterIndexAside); err != nil {
			return nil, err
		}
		if err := os.Rename(staged, basePath+suffixMain); err != nil {
			return nil, fmt.Errorf("publishing the renumbered vector file: %w", err)
		}
		if err := hook(migrateAfterIndexNew); err != nil {
			return nil, err
		}
	}
	if err := os.Rename(tmpPath, basePath+".db"); err != nil {
		return nil, fmt.Errorf("switching to format 2: %w", err)
	}
	if err := hook(migrateAfterDBRename); err != nil {
		return nil, err
	}
	if err := UpdateIndexMeta(indexDir, name, func(m *IndexMeta) {
		m.StoreFormat = 2
		m.NumPassages = n
		m.VectorGeneration, m.VectorMainGeneration, m.VectorSlots = 0, 0, 0
		if !hasVectors {
			m.VectorReady, m.LexicalOnly = false, true
		}
		m.UpdatedAt = time.Now()
	}); err != nil {
		return nil, fmt.Errorf("recording store format: %w", err)
	}
	return res, nil
}

// readOldPassages reads every passage of the old-format bbolt store
// (<name>.passages.db, bucket "passages", 8-byte big-endian ID keys, JSON
// values) read-only, in ID order.
func readOldPassages(basePath string) ([]Passage, error) {
	path := basePath + ".passages.db"
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("index %q has no old-format passage store to migrate (%s); rebuild it: gleann index rebuild %s",
			filepath.Base(basePath), filepath.Base(path), filepath.Base(basePath))
	}
	db, err := bbolt.Open(path, 0o644, &bbolt.Options{Timeout: 5 * time.Second, ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("opening old-format passage store: %w", err)
	}
	defer db.Close()
	var out []Passage
	err = db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("passages"))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var p Passage
			if err := json.Unmarshal(v, &p); err != nil {
				return fmt.Errorf("passage key %x: %w", k, err)
			}
			if len(k) == 8 {
				p.ID = int64(binary.BigEndian.Uint64(k))
			}
			out = append(out, p)
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("reading old-format passages: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// renumberVectors reads the stored vector of every passage (by its old ID)
// from the old vector file and builds a new main file in which passage i of
// the ID-ordered list is at slot i. A backend that pruned its vectors (or
// cannot return them) fails; the caller then points at a rebuild.
func renumberVectors(ctx context.Context, indexDir, basePath string, meta *IndexMeta, passages []Passage, cfgIn *Config) ([]byte, error) {
	cfg := DefaultConfig()
	if cfgIn != nil {
		cfg = *cfgIn
	}
	cfg.IndexDir = indexDir
	if meta.Backend != "" {
		cfg.Backend = meta.Backend
	}
	if meta.VectorDType != "" {
		cfg.HNSWConfig.VectorDType = meta.VectorDType
	}
	factory, err := GetBackend(cfg.Backend)
	if err != nil {
		return nil, fmt.Errorf("getting backend: %w", err)
	}
	data, err := os.ReadFile(basePath + suffixMain)
	if err != nil {
		return nil, fmt.Errorf("reading the vector file: %w", err)
	}
	noMmap := false
	scfg := cfg
	scfg.UseMmap = &noMmap
	searcher := factory.NewSearcher(scfg)
	defer searcher.Close()
	extractor, ok := searcher.(VectorExtractor)
	if !ok {
		return nil, fmt.Errorf("the %s backend cannot return its stored vectors", cfg.Backend)
	}
	if err := searcher.Load(ctx, data, *meta); err != nil {
		return nil, fmt.Errorf("loading the vector file: %w", err)
	}
	ids := make([]int64, len(passages))
	for i, p := range passages {
		ids[i] = p.ID
	}
	vecs, err := extractor.ExtractVectors(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(ids) {
		return nil, fmt.Errorf("the vector file returned %d vectors for %d passages", len(vecs), len(ids))
	}
	out, err := factory.NewBuilder(cfg).Build(ctx, vecs)
	if err != nil {
		return nil, fmt.Errorf("building the renumbered vector file: %w", err)
	}
	return out, nil
}

// undoMigrationSwitch restores the old-format files after an interrupted
// switch, of this version (.index.v1 next to a rebuilt vector file) or of
// the v1.9 migration (.v1 copies and guard files): it removes guard files
// (never a real store), renames the preserved originals back and deletes
// stale staging files.
func undoMigrationSwitch(basePath string) error {
	for _, suffix := range []string{".passages.db", ".manifest.json"} {
		if isGuardFile(basePath + suffix) {
			if err := os.Remove(basePath + suffix); err != nil {
				return fmt.Errorf("removing guard %s: %w", suffix, err)
			}
		}
	}
	for _, suffix := range []string{".passages.db", ".manifest.json", suffixMain} {
		if _, err := os.Stat(basePath + suffix + ".v1"); err == nil {
			if err := os.Rename(basePath+suffix+".v1", basePath+suffix); err != nil {
				return fmt.Errorf("restoring old %s: %w", suffix, err)
			}
		}
	}
	_ = os.Remove(basePath + suffixMain + ".migrate")
	_ = removeDBFiles(basePath + ".db.tmp")
	return nil
}

// migrateFileRow builds the files row for a source from the old manifest when
// it has an entry, else from the file on disk (same hash as the manifest).
func migrateFileRow(source string, m *Manifest, sourceDir string) store.FileRow {
	if m != nil {
		if e, ok := m.Entries[source]; ok {
			return store.FileRow{Path: e.Path, Size: e.Size, MtimeNS: e.ModTime, Hash: e.Hash}
		}
	}
	return fileRowFor(source, sourceDir)
}

// chunkIndexOf reads a passage's chunk_index metadata (JSON numbers arrive as
// float64); missing defaults to 0, and a stable sort then keeps ID order.
func chunkIndexOf(p Passage) int {
	switch v := p.Metadata["chunk_index"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// verifyMigration checks the copied store: the passage count matches and
// sampled passages are their own lexical top-10 hit (RFC 003 section 6.4
// step 3). A passage outranked on its own words by ten or more near-copies
// (a PDF's chart tick labels: "1 2 3 ... 64 Layer 9", "... Layer 10", ...)
// is copied right but not in its top 10; it passes when copiedIntact holds.
func verifyMigration(ctx context.Context, st *store.Store, analyzer analysis.Analyzer, passages []Passage, slotOf map[int64]int64, n int) error {
	cnt, err := st.Count(ctx)
	if err != nil {
		return fmt.Errorf("verify count: %w", err)
	}
	if cnt != n {
		return fmt.Errorf("verify: copied %d passages, expected %d", cnt, n)
	}
	for _, p := range samplePassages(passages, analyzer, 20) {
		q := store.LexQuery{Text: p.Text, Analyzer: analyzer}
		hits, err := st.Search(ctx, q, 10)
		if err != nil {
			return fmt.Errorf("verify lexical search: %w", err)
		}
		want := slotOf[p.ID] + 1 // the internal rowid
		found := false
		for _, h := range hits {
			if h.ID == want {
				found = true
				break
			}
		}
		if !found {
			ok, err := copiedIntact(ctx, st, q, want, p.Text)
			if err != nil {
				return fmt.Errorf("verify passage %d: %w", p.ID, err)
			}
			if !ok {
				return fmt.Errorf("verify: passage %d is not its own top-10 lexical hit", p.ID)
			}
		}
	}
	return nil
}

// copiedIntact reports whether row id of the store holds text and its
// lexical row holds every query term that text yields: the passage went to
// the right row and can be found by all of its words, whatever its rank
// among similar passages. Requiring every term (not just one, as an OR query
// would) refuses a lexical row that belongs to another passage sharing a
// few words.
func copiedIntact(ctx context.Context, st *store.Store, q store.LexQuery, id int64, text string) (bool, error) {
	row, err := st.Get(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading row %d: %w", id, err)
	}
	if row.Text != text {
		return false, nil
	}
	terms, err := st.ExplainTerms(ctx, q, id)
	if err != nil {
		return false, fmt.Errorf("scoring row %d: %w", id, err)
	}
	matched := 0
	for _, t := range terms {
		if t.TF == 0 {
			continue // a query form the passage's own tokens do not contain
		}
		if t.Score <= 0 {
			return false, nil
		}
		matched++
	}
	return matched > 0, nil
}

// verifyVectorFile loads the vector backend to confirm the file the migrated
// vec_slots address is present and well-formed.
func verifyVectorFile(ctx context.Context, indexDir, basePath string, meta *IndexMeta) error {
	indexPath := basePath + suffixMain
	factory, err := GetBackend(meta.Backend)
	if err != nil {
		return fmt.Errorf("verify vectors: getting backend: %w", err)
	}
	bs := factory.NewSearcher(Config{IndexDir: indexDir, Backend: meta.Backend})
	defer bs.Close()
	if mmap, ok := bs.(MmapBackendSearcher); ok {
		if err := mmap.LoadFromFile(ctx, indexPath); err != nil {
			return fmt.Errorf("verify vectors: loading %s: %w", indexPath, err)
		}
		return nil
	}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("verify vectors: reading %s: %w", indexPath, err)
	}
	if err := bs.Load(ctx, data, *meta); err != nil {
		return fmt.Errorf("verify vectors: loading %s: %w", indexPath, err)
	}
	return nil
}

// samplePassages picks up to k passages spread evenly across the corpus,
// skipping ones that tokenize to nothing (they cannot be a lexical self-hit).
func samplePassages(passages []Passage, an analysis.Analyzer, k int) []Passage {
	var usable []Passage
	for _, p := range passages {
		src, _ := p.Metadata["source"].(string)
		if len(an.Tokens(p.Text, analysis.Kind(passageKind(src)))) > 0 {
			usable = append(usable, p)
		}
	}
	if len(usable) <= k {
		return usable
	}
	out := make([]Passage, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, usable[i*len(usable)/k])
	}
	return out
}
