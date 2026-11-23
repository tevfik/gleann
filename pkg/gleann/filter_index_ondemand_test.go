package gleann

import (
	"context"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// filterIndexCount counts the filter indexes of the record store by name
// (passages_mf2_<hex of the meta key>), straight from the schema table.
func filterIndexCount(t *testing.T, sp *storePassages, key string) int {
	t.Helper()
	var n int
	if err := sp.st.ReaderDB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`,
		"passages_mf2_"+hex.EncodeToString([]byte(key))).Scan(&n); err != nil {
		t.Fatalf("sqlite_master: %v", err)
	}
	return n
}

// TestFilteredSearch_CreatesFilterIndexOnDemand: the first search with a
// selective eq filter leaves a filter index for its field in the record
// store, so the next one is answered from the index instead of a pass over
// every passage. An unfiltered search, a filter the indexes do not answer and
// a filter on source (served by the passages_source index) create none.
func TestFilteredSearch_CreatesFilterIndexOnDemand(t *testing.T) {
	items := make([]Item, 120)
	for i := range items {
		lang := "rust"
		if i%12 == 0 {
			lang = "go"
		}
		items[i] = Item{Text: fmt.Sprintf("doc %d", i), Metadata: map[string]any{"source": fmt.Sprintf("f%d.md", i), "lang": lang, "tag": fmt.Sprintf("t%d", i%40)}}
	}
	s := newLinearSearcher(t, items, lossyIDScorer{linearIDScorer{linearBackend{n: 120}}, map[int64]bool{0: true}})
	sp, ok := s.passages.(*storePassages)
	if !ok {
		t.Fatalf("expected a format-2 store, got %T", s.passages)
	}
	ctx := context.Background()
	search := func(filters ...MetadataFilter) []SearchResult {
		t.Helper()
		opts := []SearchOption{WithTopK(3), WithHybridAlpha(1.0)}
		if len(filters) > 0 {
			opts = append(opts, WithMetadataFilters(filters))
		}
		rs, err := s.Search(ctx, "doc", opts...)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		return rs
	}

	search()
	search(MetadataFilter{Field: "tag", Operator: OpContains, Value: "t1"})
	search(MetadataFilter{Field: "source", Operator: OpEqual, Value: "f12.md"})
	for _, key := range []string{"lang", "tag", "source", "file"} {
		if n := filterIndexCount(t, sp, key); n != 0 {
			t.Fatalf("filter index for %q exists before any eq filter on it", key)
		}
	}

	first := search(MetadataFilter{Field: "lang", Operator: OpEqual, Value: "go"})
	if n := filterIndexCount(t, sp, "lang"); n != 1 {
		t.Fatalf("after a filtered search: %d filter indexes for lang, want 1", n)
	}
	if n := filterIndexCount(t, sp, "tag"); n != 0 {
		t.Fatalf("a filter on lang created an index for tag")
	}
	again := search(MetadataFilter{Field: "lang", Operator: OpEqual, Value: "go"})
	want := []string{"doc 0", "doc 12", "doc 24"} // the true top-3 among the matches
	for i, w := range want {
		if len(first) != len(want) || len(again) != len(want) || first[i].Text != w || again[i].Text != w || first[i].Score != again[i].Score {
			t.Fatalf("first search %v, second %v; want %v with equal scores", texts(first), texts(again), want)
		}
	}
}

func texts(rs []SearchResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Text
	}
	return out
}

// TestPreselectFilterIDs_SourceKeyOfSourcelessPassage: a passage without a
// source column keeps a "source" key of its meta JSON as metadata["source"],
// and the filter engine matches on it. The pre-selection must select it too:
// the compiled chain went from the column straight to the file key and lost
// the passage.
func TestPreselectFilterIDs_SourceKeyOfSourcelessPassage(t *testing.T) {
	s := newWidenSearcher(t, []Item{
		{Text: "with a source", Metadata: map[string]any{"source": "a.go"}},
		{Text: "another", Metadata: map[string]any{"source": "b.go"}},
	})
	sp := s.passages.(*storePassages)
	ctx := context.Background()
	if _, err := sp.st.ApplySource(ctx, store.SourceDelta{Source: "", Passages: []store.NewPassage{
		{Text: "sourceless, source key in meta", Meta: `{"source":"b.go"}`},
		{Text: "sourceless, file key in meta", Meta: `{"file":"b.go"}`},
		{Text: "sourceless, both", Meta: `{"source":"c.go","file":"b.go"}`},
	}}); err != nil {
		t.Fatalf("ApplySource: %v", err)
	}
	all := s.passages.All()
	for _, f := range []MetadataFilter{
		{Field: "source", Operator: OpEqual, Value: "b.go"},
		{Field: "source", Operator: OpExists, Value: true},
		{Field: "source", Operator: OpStartsWith, Value: "c."},
		{Field: "file", Operator: OpEqual, Value: "b.go"},
		{Field: "file", Operator: OpEqual, Value: "c.go"},
		{Field: "file", Operator: OpExists, Value: false},
	} {
		engine := NewMetadataFilterEngine([]MetadataFilter{f})
		want := map[int64]bool{}
		for _, p := range all {
			if engine.Match(p.Metadata) {
				want[p.ID] = true
			}
		}
		got, ran := s.preselectFilterIDs(ctx, engine)
		if !ran {
			t.Fatalf("%+v: pre-selection did not run", f)
		}
		if len(got) != len(want) {
			t.Errorf("%+v: pre-selection %d passages, engine %d", f, len(got), len(want))
		}
		for id := range want {
			if _, ok := got[id]; !ok {
				t.Errorf("%+v: pre-selection missed passage %d", f, id)
			}
		}
	}
}

// TestFilteredSearch_LargeIndexCreatesNoFilterIndex: on an index of more than
// filterExactScanMax passages a filtered search creates no filter index and
// scores nothing exactly (a pass over the table at that size is the caller's
// decision); once EnsureFilterIndexes has created the index, the same search
// scores the few matches exactly. The first 120 passages carry vectors (the
// fake backend knows only those), the rest of the table is bulk rows.
func TestFilteredSearch_LargeIndexCreatesNoFilterIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("inserts 200,000+ passages")
	}
	items := make([]Item, 120)
	for i := range items {
		lang := "rust"
		if i%50 == 0 { // 3 of 120: few enough for the exact path at 200K+ rows
			lang = "go"
		}
		items[i] = Item{Text: fmt.Sprintf("doc %d", i), Metadata: map[string]any{"source": fmt.Sprintf("f%d.md", i), "lang": lang}}
	}
	s := newLinearSearcher(t, items, lossyIDScorer{linearIDScorer{linearBackend{n: 120}}, map[int64]bool{0: true}})
	sp := s.passages.(*storePassages)
	ctx := context.Background()
	const batch = 20_000
	for b := 0; s.passages.Count() <= filterExactScanMax; b++ {
		ps := make([]store.NewPassage, batch)
		for i := range ps {
			ps[i] = store.NewPassage{Text: fmt.Sprintf("bulk %d %d", b, i), Meta: `{"lang":"zig"}`}
		}
		if _, err := sp.st.ApplySource(ctx, store.SourceDelta{Source: fmt.Sprintf("bulk/%d.go", b), File: store.FileRow{Hash: fmt.Sprint(b)}, Passages: ps}); err != nil {
			t.Fatalf("ApplySource: %v", err)
		}
	}
	if n := s.passages.Count(); n <= filterExactScanMax {
		t.Fatalf("%d passages, want more than %d", n, filterExactScanMax)
	}
	search := func() []string {
		t.Helper()
		rs, err := s.Search(ctx, "doc", WithTopK(3), WithHybridAlpha(1.0),
			WithMetadataFilters([]MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}}))
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		return texts(rs)
	}
	before := search()
	if n := filterIndexCount(t, sp, "lang"); n != 0 {
		t.Fatalf("a search on a %d-passage index created %d filter indexes", s.passages.Count(), n)
	}
	for _, w := range before {
		if w == "doc 0" {
			t.Fatalf("without a filter index the large index scored exactly: %v", before)
		}
	}
	if err := s.EnsureFilterIndexes(ctx, "lang"); err != nil {
		t.Fatalf("EnsureFilterIndexes: %v", err)
	}
	if got, want := search(), []string{"doc 0", "doc 50", "doc 100"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("with the filter index: %v, want the exact top-3 %v (without: %v)", got, want, before)
	}
}

// TestFilteredSearch_RawNULMeta: a meta text of valid JSON followed by a raw
// NUL byte is read by SQLite up to the NUL (lang=go) but rejected by
// encoding/json, so the engine sees a passage without keys. A filtered
// search must not return it, with or without a filter index.
func TestFilteredSearch_RawNULMeta(t *testing.T) {
	items := make([]Item, 120)
	for i := range items {
		lang := "rust"
		if i%12 == 0 {
			lang = "go"
		}
		items[i] = Item{Text: fmt.Sprintf("doc %d", i), Metadata: map[string]any{"source": fmt.Sprintf("f%d.md", i), "lang": lang}}
	}
	s := newLinearSearcher(t, items, linearIDScorer{linearBackend{n: 120}})
	sp := s.passages.(*storePassages)
	ctx := context.Background()
	// Passage "doc 1" gets the damaged meta: raw SQL, as a foreign writer would.
	ids, err := sp.st.ApplySource(ctx, store.SourceDelta{Source: "nul.md", File: store.FileRow{Hash: "n"},
		Passages: []store.NewPassage{{Text: "doc nul", Meta: "{\"lang\":\"go\"}\x00x"}}})
	if err != nil || len(ids) != 1 {
		t.Fatalf("ApplySource: %v %v", ids, err)
	}
	engine := NewMetadataFilterEngine([]MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}})
	for _, withIndex := range []bool{true, false} {
		if withIndex {
			if err := s.EnsureFilterIndexes(ctx, "lang"); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range s.passages.All() {
			if p.Text == "doc nul" && engine.Match(p.Metadata) {
				t.Fatal("the engine matches the NUL passage; the premise of the test is wrong")
			}
		}
		got, ran := s.preselectFilterIDsLimit(ctx, engine, 1000, true)
		if !ran || len(got) != 10 {
			t.Fatalf("pre-selection (index=%v): %d passages, ran=%v; want the 10 true matches", withIndex, len(got), ran)
		}
		rs, err := s.Search(ctx, "doc", WithTopK(30), WithHybridAlpha(1.0), WithMetadataFilters(engine.Filters))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			if r.Text == "doc nul" {
				t.Fatalf("Search returned the passage whose meta Go rejects (index=%v): %v", withIndex, texts(rs))
			}
		}
	}
}
