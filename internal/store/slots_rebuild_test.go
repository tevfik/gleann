package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/analysis"
)

func slot(v int64) *int64 { return &v }

// applySlotted applies one source whose passages get the given slots (a
// negative slot means slotless).
func applySlotted(t *testing.T, s *Store, source, hash string, slots ...int64) []int64 {
	t.Helper()
	ps := make([]NewPassage, len(slots))
	for i, v := range slots {
		ps[i] = NewPassage{Text: source + " passage", Tokens: "passage"}
		if v >= 0 {
			ps[i].VecSlot = slot(v)
		}
	}
	ids, err := s.ApplySource(context.Background(), SourceDelta{Source: source, File: FileRow{Hash: hash}, Passages: ps})
	if err != nil {
		t.Fatalf("ApplySource(%s): %v", source, err)
	}
	return ids
}

func TestSlotQueries(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applySlotted(t, s, "a.go", "h1", 0, 1, -1)
	applySlotted(t, s, "b.go", "h2", 5)

	live, err := s.LiveSlots(ctx, []int64{0, 1, 2, 5, 9})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int64]bool{0: true, 1: true, 5: true}; !reflect.DeepEqual(live, want) {
		t.Fatalf("LiveSlots = %v, want %v", live, want)
	}

	all, err := s.AllSlots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{0, 1, 5}; !reflect.DeepEqual(all, want) {
		t.Fatalf("AllSlots = %v, want %v", all, want)
	}

	slots, n, err := s.SourceSlots(ctx, []string{"b.go", "a.go", "missing.go"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{0, 1, 5}; !reflect.DeepEqual(slots, want) || n != 4 {
		t.Fatalf("SourceSlots = %v, %d passages; want %v, 4 (the slotless one counts)", slots, n, want)
	}

	count, maxSlot, slotless, err := s.SlotSpan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 || maxSlot != 5 || slotless != 1 {
		t.Fatalf("SlotSpan = %d, %d, %d; want 4, 5, 1", count, maxSlot, slotless)
	}
	if c, err := s.Count(ctx); err != nil || c != 4 {
		t.Fatalf("Count = %d, %v", c, err)
	}
}

func TestSlotQueriesOnEmptyStore(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if all, err := s.AllSlots(ctx); err != nil || len(all) != 0 {
		t.Fatalf("AllSlots = %v, %v", all, err)
	}
	if live, err := s.LiveSlots(ctx, nil); err != nil || len(live) != 0 {
		t.Fatalf("LiveSlots(nil) = %v, %v", live, err)
	}
	count, maxSlot, slotless, err := s.SlotSpan(ctx)
	if err != nil || count != 0 || maxSlot != -1 || slotless != 0 {
		t.Fatalf("SlotSpan = %d, %d, %d, %v; want 0, -1, 0", count, maxSlot, slotless, err)
	}
}

// LiveSlots and SourceSlots split their IN lists at maxInParams; the result
// must not depend on the split.
func TestSlotQueriesAcrossChunks(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const n = maxInParams + 37
	slots := make([]int64, n)
	sources := make([]string, n)
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		slots[i] = int64(i)
		sources[i] = "dir/f" + strconv.Itoa(i) + ".go"
		if _, err := s.ApplySourceTx(ctx, tx, SourceDelta{Source: sources[i], Passages: []NewPassage{{Text: "t", VecSlot: slot(int64(i))}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	query := append(append([]int64(nil), slots...), n+1, n+2)
	live, err := s.LiveSlots(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != n || live[n+1] {
		t.Fatalf("LiveSlots found %d of %d", len(live), n)
	}
	got, passages, err := s.SourceSlots(ctx, sources)
	if err != nil {
		t.Fatal(err)
	}
	if passages != n || !reflect.DeepEqual(got, slots) {
		t.Fatalf("SourceSlots: %d passages, %d slots; want %d sorted", passages, len(got), n)
	}
}

func TestMetaSnapshotAndDocNodes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.SetMeta(ctx, "k1", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta(ctx, "k2", "v2"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.MetaSnapshot(ctx, "k1", "k2", "absent")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"k1": "v1", "k2": "v2"}; !reflect.DeepEqual(snap, want) {
		t.Fatalf("MetaSnapshot = %v, want %v", snap, want)
	}
	if empty, err := s.MetaSnapshot(ctx); err != nil || len(empty) != 0 {
		t.Fatalf("MetaSnapshot() = %v, %v", empty, err)
	}

	// DeleteMetaTx removes a key in the caller's transaction.
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMetaTx(ctx, tx, "k1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Meta(ctx, "k1"); err != nil || v != "" {
		t.Fatalf("k1 after delete = %q, %v", v, err)
	}

	has, err := s.HasDocNodes(ctx)
	if err != nil || has {
		t.Fatalf("HasDocNodes on a fresh store = %v, %v", has, err)
	}
	if _, err := s.WriterDB().Exec(`INSERT INTO doc_nodes (id, type) VALUES ('d1', 'document')`); err != nil {
		t.Fatal(err)
	}
	has, err = s.HasDocNodes(ctx)
	if err != nil || !has {
		t.Fatalf("HasDocNodes after insert = %v, %v", has, err)
	}
	// The reader pool sees the writer's commit.
	var n int
	if err := s.ReaderDB().QueryRow(`SELECT count(*) FROM doc_nodes`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("reader sees %d doc nodes, %v", n, err)
	}
}

func TestSelectByPredicate(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	_, err := s.ApplySource(ctx, SourceDelta{Source: "a.md", Passages: []NewPassage{
		{Text: "one", Kind: "prose", Meta: `{"lang":"en"}`, VecSlot: slot(3)},
		{Text: "two", Kind: "prose", Meta: `{"lang":"tr"}`},
		{Text: "three", Meta: `{"lang":"en"}`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.SelectByPredicate(ctx, `json_extract(meta, '$.lang') = ?`, []any{"en"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != "prose" || got[0].VecSlot == nil || *got[0].VecSlot != 3 || got[1].VecSlot != nil || got[1].Kind != "code" {
		t.Fatalf("SelectByPredicate = %+v", got)
	}
	if got[0].Text != "" || got[0].Source != "a.md" {
		t.Fatalf("a pre-selection row carries Source but no text: %+v", got[0])
	}
	limited, err := s.SelectByPredicate(ctx, `1 = 1`, nil, 1)
	if err != nil || len(limited) != 1 {
		t.Fatalf("limit 1 returned %d rows, %v", len(limited), err)
	}
	if _, err := s.SelectByPredicate(ctx, `no_such_column = 1`, nil, 0); err == nil || !strings.Contains(err.Error(), "selecting passages by predicate") {
		t.Fatalf("bad predicate: err = %v", err)
	}
}

func TestExplainTerms(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	ids := applyTexts(t, s, "a.txt", []string{
		"apple apple apple zebra",
		"cherry banana",
	})
	terms, err := s.ExplainTerms(ctx, lexQuery("zebra apple mango"), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != 3 {
		t.Fatalf("ExplainTerms = %+v, want 3 terms", terms)
	}
	if terms[0].Term != "apple" || terms[0].TF != 3 || terms[0].Score <= 0 {
		t.Fatalf("first term = %+v, want apple with TF 3 and a positive score", terms[0])
	}
	if terms[1].Term != "zebra" || terms[1].TF != 1 || terms[1].Score <= 0 || terms[1].Score >= terms[0].Score {
		t.Fatalf("second term = %+v, want zebra scored below apple", terms[1])
	}
	if terms[2].Term != "mango" || terms[2].TF != 0 || terms[2].Score != 0 {
		t.Fatalf("absent term = %+v, want mango last with score 0", terms[2])
	}

	// The scores add up to the passage's ScoreIDs score (bm25 is a sum over
	// the query's phrases).
	scores, err := s.ScoreIDs(ctx, lexQuery("zebra apple mango"), ids[:1])
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for _, ts := range terms {
		sum += ts.Score
	}
	if d := sum - float64(scores[ids[0]]); d > 1e-3 || d < -1e-3 {
		t.Fatalf("term scores sum to %v, ScoreIDs gives %v", sum, scores[ids[0]])
	}

	if got, err := s.ExplainTerms(ctx, lexQuery("apple"), 999999); err != nil || got != nil {
		t.Fatalf("missing passage: %v, %v", got, err)
	}
	cut := lexQuery("apple cherry")
	cut.Options.DocFreqCut = 0.9
	if got, err := s.ExplainTerms(ctx, cut, ids[1]); err != nil || len(got) == 0 {
		t.Fatalf("with a doc-freq cut: %v, %v", got, err)
	}
}

// fileRow reads one manifest row through the reader pool.
func fileRow(t *testing.T, s *Store, path string) (FileRow, bool) {
	t.Helper()
	files, err := s.Files(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f, ok := files[path]
	return f, ok
}

// markGraphBuilt records that the graph rows of path were built, as the graph
// store does.
func markGraphBuilt(t *testing.T, s *Store, path, hash string) {
	t.Helper()
	if _, err := s.WriterDB().Exec(`UPDATE files SET graph_hash = ? WHERE path = ?`, hash, path); err != nil {
		t.Fatal(err)
	}
}

func TestReplacePassagesTxKeepsGraphAndDecidesFates(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, src := range []string{"gone.go", "kept.go", "unknown.go", "changed.go", "same.go"} {
		applySlotted(t, s, src, "h-"+src, -1)
		markGraphBuilt(t, s, src, "h-"+src)
	}
	if _, err := s.WriterDB().Exec(`INSERT INTO symbols (fqn, name, kind, file, start_line, end_line, start_col, byte_offset) VALUES ('p.F', 'F', 'func', 'kept.go', 1, 2, 0, 0)`); err != nil {
		t.Fatal(err)
	}

	fates := map[string]SourceFate{"gone.go": SourceGone, "kept.go": SourceKept, "unknown.go": SourceUnknown}
	var retracted []string
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := s.ReplacePassagesTx(ctx, tx, []SourceDelta{
		{Source: "changed.go", File: FileRow{Hash: "h2-changed"}, Passages: []NewPassage{{Text: "new", Tokens: "new"}}},
		{Source: "same.go", File: FileRow{Hash: "h-same.go"}, Passages: []NewPassage{{Text: "same", Tokens: "same"}, {Text: "more", Tokens: "more"}}},
		{Source: "new.go", File: FileRow{Hash: "h-new"}, Passages: []NewPassage{{Text: "fresh", Tokens: "fresh"}}},
	}, RebuildHooks{
		Fate: func(src string) SourceFate { return fates[src] },
		Retract: func(ctx context.Context, tx *sql.Tx, src string) error {
			retracted = append(retracted, src)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || len(ids[0]) != 1 || len(ids[1]) != 2 || len(ids[2]) != 1 {
		t.Fatalf("ids = %v, want one list per delta in delta order", ids)
	}
	if !reflect.DeepEqual(retracted, []string{"gone.go"}) {
		t.Fatalf("retracted %v, want only gone.go", retracted)
	}

	if _, ok := fileRow(t, s, "gone.go"); ok {
		t.Fatal("the files row of a deleted source survived")
	}
	kept, ok := fileRow(t, s, "kept.go")
	if !ok || kept.Hash != "" || kept.GraphHash != "h-kept.go" || kept.GraphDirty {
		t.Fatalf("kept.go = %+v, ok %v; want manifest cleared, graph_hash kept, not dirty", kept, ok)
	}
	unknown, ok := fileRow(t, s, "unknown.go")
	if !ok || unknown.Hash != "" || unknown.GraphHash == "" || !unknown.GraphDirty {
		t.Fatalf("unknown.go = %+v; want manifest cleared and graph_dirty", unknown)
	}
	changed, _ := fileRow(t, s, "changed.go")
	if changed.Hash != "h2-changed" || !changed.GraphDirty {
		t.Fatalf("changed.go = %+v; want the new hash and graph_dirty", changed)
	}
	same, _ := fileRow(t, s, "same.go")
	if same.GraphDirty {
		t.Fatalf("same.go = %+v; an unchanged hash must not be marked dirty", same)
	}
	if fresh, ok := fileRow(t, s, "new.go"); !ok || fresh.GraphDirty {
		t.Fatalf("new.go = %+v, %v", fresh, ok)
	}

	// The graph tables are untouched by the passage rebuild.
	var symbols int
	if err := s.ReaderDB().QueryRow(`SELECT count(*) FROM symbols`).Scan(&symbols); err != nil || symbols != 1 {
		t.Fatalf("symbols = %d, %v", symbols, err)
	}
	// Old passages are gone from the passages and lex tables.
	if c, _ := s.Count(ctx); c != 4 {
		t.Fatalf("Count = %d, want 4", c)
	}
	if n := lexCount(t, s, "passage"); n != 0 {
		t.Fatalf("%d lex rows of dropped passages remain", n)
	}
}

// Without hooks every source that lost its passages is treated as deleted.
func TestReplacePassagesTxNilHooks(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applySlotted(t, s, "old.go", "h", 0)
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplacePassagesTx(ctx, tx, nil, RebuildHooks{}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fileRow(t, s, "old.go"); ok {
		t.Fatal("files row survived")
	}
	if c, _ := s.Count(ctx); c != 0 {
		t.Fatalf("Count = %d", c)
	}
}

// A failing Retract aborts the rebuild; rolling the transaction back leaves
// the previous passages and manifest intact.
func TestReplacePassagesTxRetractErrorRollsBack(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applySlotted(t, s, "old.go", "h", 0)
	boom := errors.New("graph store down")
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ReplacePassagesTx(ctx, tx, []SourceDelta{{Source: "new.go", Passages: []NewPassage{{Text: "x"}}}}, RebuildHooks{
		Retract: func(context.Context, *sql.Tx, string) error { return boom },
	})
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "old.go") {
		t.Fatalf("err = %v, want the retract error naming old.go", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fileRow(t, s, "old.go"); !ok {
		t.Fatal("rollback lost the old files row")
	}
	if all, _ := s.AllSlots(ctx); !reflect.DeepEqual(all, []int64{0}) {
		t.Fatalf("slots after rollback = %v", all)
	}
}

// A delta whose passages collide (two passages in one slot) fails inside the
// rebuild and reports the error.
func TestReplacePassagesTxDeltaError(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = s.ReplacePassagesTx(ctx, tx, []SourceDelta{{Source: "a.go", Passages: []NewPassage{
		{Text: "x", VecSlot: slot(1)}, {Text: "y", VecSlot: slot(1)},
	}}}, RebuildHooks{})
	if err == nil {
		t.Fatal("two passages in one slot were accepted")
	}
}

func TestIsCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(path, []byte(strings.Repeat("this is not a database file ", 400)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path, Options{})
	if err == nil {
		t.Fatal("opened a file that is not a database")
	}
	if !IsCorrupt(err) {
		t.Fatalf("IsCorrupt(%v) = false", err)
	}
	for _, other := range []error{nil, errors.New("disk full"), sql.ErrNoRows} {
		if IsCorrupt(other) {
			t.Fatalf("IsCorrupt(%v) = true", other)
		}
	}
}

func TestIsNetworkFSOnLocalDir(t *testing.T) {
	if IsNetworkFS(t.TempDir()) {
		t.Fatal("a temp directory was reported as a network file system")
	}
	// Detection is best effort: an unreadable path counts as local.
	if IsNetworkFS(filepath.Join(t.TempDir(), "does", "not", "exist")) {
		t.Fatal("a missing path was reported as a network file system")
	}
}

// Every read and write on a closed store fails with an error that says what
// it was doing, never a panic.
func TestClosedStoreReportsErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	applySlotted(t, s, "a.go", "h", 0)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	checks := map[string]error{}
	_, checks["LiveSlots"] = s.LiveSlots(ctx, []int64{0})
	_, checks["AllSlots"] = s.AllSlots(ctx)
	_, _, checks["SourceSlots"] = s.SourceSlots(ctx, []string{"a.go"})
	_, checks["MetaSnapshot"] = s.MetaSnapshot(ctx, "k")
	_, checks["HasDocNodes"] = s.HasDocNodes(ctx)
	_, checks["SelectByPredicate"] = s.SelectByPredicate(ctx, "1=1", nil, 0)
	_, checks["Count"] = s.Count(ctx)
	_, _, _, checks["SlotSpan"] = s.SlotSpan(ctx)
	_, checks["Meta"] = s.Meta(ctx, "k")
	checks["SetMeta"] = s.SetMeta(ctx, "k", "v")
	_, checks["Files"] = s.Files(ctx)
	_, checks["BeginWrite"] = s.BeginWrite(ctx)
	_, checks["RemoveSource"] = s.RemoveSource(ctx, "a.go")
	checks["ReassignSlots"] = s.ReassignSlots(ctx, map[int64]int64{0: 1})
	_, checks["BySlot"] = s.BySlot(ctx, []int64{0})
	checks["ForEach"] = s.ForEach(ctx, func(Passage) error { return nil })
	_, checks["ApplySource"] = s.ApplySource(ctx, SourceDelta{Source: "b.go"})
	_, checks["ExplainTerms"] = s.ExplainTerms(ctx, lexQuery("x"), 1)
	_, checks["Search"] = s.Search(ctx, lexQuery("xylophone"), 5)
	cut := lexQuery("xylophone yodel")
	cut.Options.DocFreqCut = 0.5
	_, checks["Search with a doc-freq cut"] = s.Search(ctx, cut, 5)
	_, checks["ScoreIDs with a doc-freq cut"] = s.ScoreIDs(ctx, cut, []int64{1})
	and := lexQuery("xylophone yodel")
	and.Options.ANDFirst = true
	_, checks["Search AND-first"] = s.Search(ctx, and, 5)
	_, checks["ScoreIDs"] = s.ScoreIDs(ctx, lexQuery("xylophone"), []int64{1})
	_, checks["Stats"] = s.Stats(ctx)
	checks["Rebuild"] = s.Rebuild(ctx, analysis.New(""))
	_, checks["Get"] = s.Get(ctx, 1)
	checks["AcquireLock in a file"] = func() error {
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, nil, 0o644); err != nil {
			return nil
		}
		_, err := AcquireLock(filepath.Join(blocker, "idx.lock"))
		return err
	}()
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s on a closed store returned no error", name)
		}
	}
}

func TestLexicalEdgeCases(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// An empty index: a doc-freq cut has nothing to measure against.
	cut := lexQuery("apple")
	cut.Options.DocFreqCut = 0.5
	if hits, err := s.Search(ctx, cut, 5); err != nil || len(hits) != 0 {
		t.Fatalf("Search on an empty index = %v, %v", hits, err)
	}

	ids := applyTexts(t, s, "a.txt", []string{"apple banana", "apple cherry", "apple durian"})
	if _, err := s.ApplySource(ctx, SourceDelta{Source: "empty.txt", Passages: []NewPassage{{Text: "..."}}}); err != nil {
		t.Fatal(err)
	}
	if hits, err := s.Search(ctx, lexQuery("apple"), 0); err != nil || hits != nil {
		t.Fatalf("k = 0: %v, %v", hits, err)
	}

	// ScoreIDs applies the same cut as Search: apple is in every passage,
	// so only banana scores.
	cut = lexQuery("apple banana")
	cut.Options.DocFreqCut = 0.5
	if _, err := s.Stats(ctx); err != nil { // refreshes the cached row count
		t.Fatal(err)
	}
	scores, err := s.ScoreIDs(ctx, cut, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 1 || scores[ids[0]] <= 0 {
		t.Fatalf("ScoreIDs with the cut = %v, want only the banana passage", scores)
	}
	// A query of only stop words or punctuation scores nothing.
	if scores, err := s.ScoreIDs(ctx, lexQuery("..."), ids); err != nil || len(scores) != 0 {
		t.Fatalf("punctuation query = %v, %v", scores, err)
	}

	// The lexical index can be rebuilt from the stored text; a passage
	// whose text yields no token gets no lex row.
	if err := s.Rebuild(ctx, analysis.New("")); err != nil {
		t.Fatal(err)
	}
	st, err := s.Stats(ctx)
	if err != nil || st.Rows != 3 {
		t.Fatalf("Stats after Rebuild = %+v, %v; want 3 rows (the punctuation passage has none)", st, err)
	}
	if hits, err := s.Search(ctx, lexQuery("cherry"), 5); err != nil || len(hits) != 1 || hits[0].ID != ids[1] {
		t.Fatalf("Search after Rebuild = %v, %v", hits, err)
	}
}
