package gleann

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// filterIndexFixture builds a searcher over passages whose metadata covers
// every JSON type the filter engine can meet, then adds passages with meta
// text encoding/json would never write (raw SQL through the record store):
// numbers in other spellings, text that is not JSON, escaped NUL and
// surrogates. It returns the searcher and every passage as the engine sees it.
func filterIndexFixture(t *testing.T) (*LeannSearcher, *storePassages, []Passage) {
	t.Helper()
	values := []any{
		"go", "rust", "", "5", "true", "false", "<nil>", "0", "-0", "1e+21", "5.5", "[1 2]", "[a b]", "map[a:1]", "a<b", "éü",
		5, 0, -7, 999999, 1000000, 1234567, 123456789012345, 1234567890123456, 5.5, 1e21, 1e-7, 2.5e10,
		true, false, nil,
		[]any{1, 2}, []any{"a", "b"}, []any{}, map[string]any{"a": 1}, map[string]any{},
	}
	var items []Item
	for i, v := range values {
		items = append(items,
			Item{Text: fmt.Sprintf("value doc %d", i), Metadata: map[string]any{"source": fmt.Sprintf("dir/f%d.go", i%7), "f": v, "lang": []string{"go", "rust", "zig"}[i%3]}},
			Item{Text: fmt.Sprintf("kind doc %d", i), Metadata: map[string]any{"source": fmt.Sprintf("dir/f%d.go", i%7), "kind": v, "type": values[(i+3)%len(values)]}},
			Item{Text: fmt.Sprintf("type doc %d", i), Metadata: map[string]any{"type": v, "g": values[(i+5)%len(values)]}},
		)
	}
	items = append(items,
		Item{Text: "no metadata", Metadata: map[string]any{}},
		Item{Text: "source only", Metadata: map[string]any{"source": "dir/f1.go"}},
		Item{Text: "file only", Metadata: map[string]any{"file": "dir/f1.go", "f": "go"}},
	)
	s := newWidenSearcher(t, items)
	sp, ok := s.passages.(*storePassages)
	if !ok {
		t.Fatalf("expected a format-2 store, got %T", s.passages)
	}
	bs := string(rune(92)) // a backslash, kept out of the literals below
	raw := []string{
		`{"f":5.0}`, `{"f":1e2}`, `{"f":-0}`, `{"f":0.0}`, `{"f":12345678901234567890}`, `{"f":100}`, `{"f":"100"}`,
		`{"kind":5.0,"type":"go"}`, `{"type":-0}`, `{"kind":null,"type":"go"}`,
		`{not json`, `[1,2]`, `"f"`, `7`, ``,
		`{"f":"g` + bs + `u0000o"}`, `{"f":"` + bs + `ud83d"}`, `{"f":"` + bs + `uD83D` + bs + `uDE00"}`, `{"f":"go","pad":"` + bs + `u0000"}`,
		`{"f":"a` + bs + `u003cb"}`,
	}
	for i, m := range raw {
		// One source with a path and one without: the sourceless bucket is
		// where "source" resolves through the meta JSON.
		for _, src := range []string{fmt.Sprintf("raw/%d.go", i), ""} {
			if src == "" && i > 0 {
				continue
			}
			ps := []store.NewPassage{{Text: fmt.Sprintf("raw doc %d %q", i, src), Meta: m}}
			if src == "" {
				ps = append(ps,
					store.NewPassage{Text: "sourceless file", Meta: `{"file":"dir/f1.go","f":"go"}`},
					store.NewPassage{Text: "sourceless source key", Meta: `{"source":"dir/f2.go"}`},
					store.NewPassage{Text: "sourceless plain", Meta: `{"f":"rust"}`},
				)
			}
			if _, err := sp.st.ApplySource(context.Background(), store.SourceDelta{Source: src, Passages: ps}); err != nil {
				t.Fatalf("ApplySource(%q): %v", m, err)
			}
		}
	}
	return s, sp, s.passages.All()
}

func sortedIDKeys(m map[int64]int64) []int64 {
	ids := make([]int64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// TestPreselectIndexed_MatchesEngineAndScan: for every operator of the filter
// language, on fields of every resolution kind and values of every JSON type,
// the pre-selection from the filter indexes returns exactly the passages the
// filter engine matches, and exactly what the pass over the table returns.
// Filters the indexes do not answer must say so instead of answering.
func TestPreselectIndexed_MatchesEngineAndScan(t *testing.T) {
	s, sp, all := filterIndexFixture(t)
	ctx := context.Background()
	const limit = 1 << 20

	fields := []string{"f", "g", "lang", "kind", "type", "source", "file", "missing", "ext", "chunk_index", "a'b"}
	needles := []any{
		"go", "rust", "", "5", 5, 5.0, 5.5, "5.5", "0", 0, "-0", "100", 100, 999999, "999999", 1000000, "1000000", "1e+06", 1234567, "1.234567e+06", "1e+21", 1e21, "1e-07", "2.5e+10", "1.2345678901234567e+19",
		"123456789012345", "1234567890123456", 1234567890123456, -7, "true", true, "false", false, nil, "<nil>",
		"[1 2]", "[a b]", "[]", "map[a:1]", "map[]", "a<b", "éü", "dir/f1.go", "dir/f2.go", "raw/0.go", "g\x00o", "\ufffd", "\U0001F600",
		[]any{"go", 5, true}, []any{"rust", "0", nil, "[1 2]"}, []string{"go", "dir/f1.go", "100"}, []any{}, []string{},
	}
	ops := []FilterOperator{OpEqual, OpNotEqual, OpGreaterThan, OpGreaterEqual, OpLessThan, OpLessEqual, OpIn, OpNotIn, OpContains, OpStartsWith, OpEndsWith, OpExists, FilterOperator("bogus")}

	indexed, scanned := 0, 0
	check := func(filters []MetadataFilter, logic string) {
		t.Helper()
		engine := NewMetadataFilterEngine(filters)
		engine.Logic = logic
		name := fmt.Sprintf("%+v logic=%s", filters, logic)
		var want []int64
		for _, p := range all {
			if engine.Match(p.Metadata) {
				want = append(want, p.ID)
			}
		}
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })

		answerable := true
		for _, f := range filters {
			if _, ok := compileFilterIndex(f); !ok {
				answerable = false
			}
		}
		got, outcome := s.preselectIndexed(ctx, sp, engine, limit, true)
		if answerable != (outcome == preselectDone) {
			t.Fatalf("%s: outcome %d, but compileFilterIndex says answerable=%v", name, outcome, answerable)
		}
		if outcome == preselectDone {
			indexed++
			if ids := sortedIDKeys(got); fmt.Sprint(ids) != fmt.Sprint(want) {
				t.Fatalf("%s:\n filter indexes %v\n engine         %v", name, ids, want)
			}
			for id, rowid := range got {
				if rowid <= 0 || (id >= 0) == (id == -rowid) {
					t.Fatalf("%s: passage %d mapped to rowid %d", name, id, rowid)
				}
			}
		}
		if scan, ran := s.preselectScan(ctx, sp, engine, limit); ran {
			scanned++
			// The fixture holds text SQLite cannot evaluate a JSON path on,
			// so the scan path errors out (ran=false) whenever a meta key is
			// involved; where it does run, it must agree.
			if ids := sortedIDKeys(scan); fmt.Sprint(ids) != fmt.Sprint(want) {
				t.Fatalf("%s:\n table pass %v\n engine     %v", name, ids, want)
			}
		}
	}
	for _, field := range fields {
		for _, op := range ops {
			for _, v := range needles {
				check([]MetadataFilter{{Field: field, Operator: op, Value: v}}, "and")
			}
			for _, v := range []any{true, false, "true", 1, nil} {
				check([]MetadataFilter{{Field: field, Operator: OpExists, Value: v}}, "and")
			}
		}
	}
	// Two filters, both logics: every pairing of the answerable shapes.
	pairs := []MetadataFilter{
		{Field: "f", Operator: OpEqual, Value: "go"},
		{Field: "f", Operator: OpEqual, Value: 5},
		{Field: "f", Operator: OpIn, Value: []any{"go", 5, true, nil, "0"}},
		{Field: "f", Operator: OpExists, Value: true},
		{Field: "lang", Operator: OpEqual, Value: "go"},
		{Field: "kind", Operator: OpEqual, Value: "go"},
		{Field: "type", Operator: OpIn, Value: []any{"go", "rust", 5}},
		{Field: "kind", Operator: OpExists, Value: true},
		{Field: "source", Operator: OpEqual, Value: "dir/f1.go"},
		{Field: "source", Operator: OpIn, Value: []string{"dir/f1.go", "dir/f2.go", "raw/0.go"}},
		{Field: "missing", Operator: OpEqual, Value: "x"},
		{Field: "f", Operator: OpNotEqual, Value: "go"}, // not answerable: the pair is not either
	}
	for _, a := range pairs {
		for _, b := range pairs {
			for _, logic := range []string{"and", "or"} {
				check([]MetadataFilter{a, b}, logic)
			}
		}
	}
	check([]MetadataFilter{pairs[0], pairs[4], pairs[8]}, "and")
	check([]MetadataFilter{pairs[1], pairs[5], pairs[9]}, "or")
	if indexed < 500 {
		t.Fatalf("only %d filters were answered from the filter indexes; the test proves nothing", indexed)
	}
	t.Logf("%d filters answered from the filter indexes, %d by the pass over the table", indexed, scanned)
}

// TestPreselectIndexed_ScanIdentityOnCleanMetadata: on metadata the pass over
// the table can evaluate (everything gleann writes), both pre-selections run
// for every filter the indexes answer and return the same passages.
func TestPreselectIndexed_ScanIdentityOnCleanMetadata(t *testing.T) {
	var items []Item
	values := []any{"go", "rust", "5", 5, 5.5, 0, true, false, nil, []any{1, 2}, map[string]any{"a": 1}, "", "a<b"}
	for i := 0; i < 260; i++ {
		md := map[string]any{"source": fmt.Sprintf("d%d/f%d.go", i%4, i%13), "f": values[i%len(values)], "kind": values[(i/2)%len(values)]}
		if i%3 == 0 {
			md["type"] = values[(i/3)%len(values)]
			delete(md, "kind")
		}
		if i%5 == 0 {
			delete(md, "source")
		}
		items = append(items, Item{Text: fmt.Sprintf("clean doc %d", i), Metadata: md})
	}
	s := newWidenSearcher(t, items)
	sp := s.passages.(*storePassages)
	ctx := context.Background()
	n := 0
	for _, field := range []string{"f", "kind", "type", "source", "nokey"} {
		for _, op := range []FilterOperator{OpEqual, OpIn, OpExists} {
			for _, v := range []any{"go", "5", 5, "5.5", 0, "0", true, "true", nil, "<nil>", "[1 2]", "map[a:1]", "", "a<b", "d1/f1.go"} {
				for _, logic := range []string{"and", "or"} {
					value := v
					switch op {
					case OpIn:
						value = []any{v, "rust"}
					case OpExists:
						value = true
					}
					engine := NewMetadataFilterEngine([]MetadataFilter{{Field: field, Operator: op, Value: value}, {Field: "f", Operator: OpEqual, Value: "go"}})
					engine.Logic = logic
					got, outcome := s.preselectIndexed(ctx, sp, engine, 1<<20, true)
					if outcome != preselectDone {
						if field == "source" && op == OpExists {
							continue // exists on source is left to the table pass
						}
						t.Fatalf("field=%s op=%s value=%v: outcome %d, want an answer from the filter indexes", field, op, value, outcome)
					}
					scan, ran := s.preselectScan(ctx, sp, engine, 1<<20)
					if !ran {
						t.Fatalf("field=%s op=%s value=%v: the pass over the table did not run", field, op, value)
					}
					if fmt.Sprint(sortedIDKeys(got)) != fmt.Sprint(sortedIDKeys(scan)) {
						t.Fatalf("field=%s op=%s value=%v logic=%s:\n filter indexes %v\n table pass     %v", field, op, value, logic, sortedIDKeys(got), sortedIDKeys(scan))
					}
					for id, rowid := range got {
						if scan[id] != rowid {
							t.Fatalf("passage %d: rowid %d from the filter indexes, %d from the table pass", id, rowid, scan[id])
						}
					}
					n++
				}
			}
		}
	}
	t.Logf("%d filters: identical passages and rowids on both paths", n)
}

// TestPreselect_LargeIndexUsesExistingFilterIndexOnly: without leave for a
// pass over the table (an index above filterExactScanMax) the pre-selection
// answers from the filter indexes that exist and creates none; once the
// index is there (EnsureFilterIndexes) it answers, with the same passages.
func TestPreselect_LargeIndexUsesExistingFilterIndexOnly(t *testing.T) {
	items := make([]Item, 120)
	for i := range items {
		lang := "rust"
		if i%12 == 0 {
			lang = "go"
		}
		items[i] = Item{Text: fmt.Sprintf("doc %d", i), Metadata: map[string]any{"source": fmt.Sprintf("f%d.md", i), "lang": lang}}
	}
	s := newLinearSearcher(t, items, lossyIDScorer{linearIDScorer{linearBackend{n: 120}}, map[int64]bool{0: true}})
	sp := s.passages.(*storePassages)
	ctx := context.Background()
	engine := NewMetadataFilterEngine([]MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}})

	if _, ran := s.preselectFilterIDsLimit(ctx, engine, 1000, false); ran {
		t.Fatal("pre-selection ran without a filter index and without leave for a table pass")
	}
	if have, err := sp.st.MetaIndexes(ctx, []string{"lang"}); err != nil || have["lang"] {
		t.Fatalf("a filter index was created without leave for a table pass (%v, %v)", have, err)
	}
	if _, _, ok := s.searchPreselectedLimit(ctx, "doc", "doc", []float32{1, 0, 0, 0, 0, 0, 0, 0}, SearchConfig{HybridAlpha: 1}, 3, engine, 0.5, 1000, true, false); ok {
		t.Fatal("exact scoring ran without a filter index and without leave for a table pass")
	}
	// A source filter needs no filter index: the passages_source index every
	// database has answers it at any size.
	bySource := NewMetadataFilterEngine([]MetadataFilter{{Field: "source", Operator: OpIn, Value: []string{"f0.md", "f5.md"}}})
	if got, ran := s.preselectFilterIDsLimit(ctx, bySource, 1000, false); !ran || len(got) != 2 {
		t.Fatalf("source filter without a table pass: %v, ran=%v; want 2 passages", got, ran)
	}

	if err := s.EnsureFilterIndexes(ctx, "lang", "source"); err != nil {
		t.Fatalf("EnsureFilterIndexes: %v", err)
	}
	if err := s.EnsureFilterIndexes(ctx, "ext"); err == nil {
		t.Fatal("EnsureFilterIndexes accepted a field the filter indexes cannot serve")
	}
	got, ran := s.preselectFilterIDsLimit(ctx, engine, 1000, false)
	if !ran || len(got) != 10 {
		t.Fatalf("with the filter index: %d passages, ran=%v; want 10", len(got), ran)
	}
	want, ran := s.preselectScan(ctx, sp, engine, 1000)
	if !ran || fmt.Sprint(sortedIDKeys(want)) != fmt.Sprint(sortedIDKeys(got)) {
		t.Fatalf("filter index %v, table pass %v (ran=%v)", sortedIDKeys(got), sortedIDKeys(want), ran)
	}
	rs, _, ok := s.searchPreselectedLimit(ctx, "doc", "doc", []float32{1, 0, 0, 0, 0, 0, 0, 0}, SearchConfig{HybridAlpha: 1}, 3, engine, 0.5, 1000, true, false)
	if !ok || len(rs) < 3 {
		t.Fatalf("exact scoring from the filter index: %d results, ok=%v", len(rs), ok)
	}
	for i, wantText := range []string{"doc 0", "doc 12", "doc 24"} {
		if rs[i].Text != wantText {
			t.Errorf("result %d = %q, want %q (the true top-3 among the matches)", i, rs[i].Text, wantText)
		}
	}

	// A filter that provably matches more than the limit is reported as not
	// selective without a pass over the table, as the scan path reports it.
	rust := NewMetadataFilterEngine([]MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "rust"}})
	if got, outcome := s.preselectIndexed(ctx, sp, rust, 50, false); outcome != preselectNotSelective || got != nil {
		t.Fatalf("110 matches with limit 50: outcome %d, %d passages; want not selective", outcome, len(got))
	}
	if _, ran := s.preselectFilterIDsLimit(ctx, rust, 50, true); ran {
		t.Fatal("a filter over the limit must report ran=false")
	}
}

// TestCompileFilterIndex_SureRanges pins which index ranges are taken without
// verification. A sure range the engine would not match is a wrong result, so
// the rule is spelled out here value by value: the JSON text is decoded as
// the engine decodes it and compared with the needle.
func TestCompileFilterIndex_SureRanges(t *testing.T) {
	jsonValues := []string{
		`"go"`, `"5"`, `"true"`, `"<nil>"`, `"0"`, `""`, `"[1 2]"`, `"map[a:1]"`, `"1e+21"`,
		`5`, `0`, `-0`, `-7`, `100`, `999999`, `-999999`, `1000000`, `1234567`, `123456789012345`, `999999999999999`, `1000000000000000`, `1234567890123456`, `9007199254740993`,
		`12345678901234567890`, `5.0`, `5.5`, `1e2`, `1e21`, `0.0`, `-0.0`,
		`true`, `false`, `null`, `[1,2]`, `["a","b"]`, `[]`, `{"a":1}`, `{}`,
	}
	jsonType := func(raw string) string {
		switch {
		case strings.HasPrefix(raw, `"`):
			return "text"
		case raw == "true" || raw == "false" || raw == "null":
			return raw
		case strings.HasPrefix(raw, "["):
			return "array"
		case strings.HasPrefix(raw, "{"):
			return "object"
		case strings.ContainsAny(raw, ".eE"):
			return "real"
		}
		return "integer"
	}
	needles := []string{"go", "5", "true", "false", "<nil>", "0", "-0", "", "[1 2]", "[a b]", "[]", "map[a:1]", "map[]", "1e+21", "100", "-7",
		"999999", "-999999", "1000000", "1e+06", "1.234567e+06", "1234567", "1.23456789012345e+14",
		"123456789012345", "999999999999999", "1e+15", "1.234567890123456e+15", "9.007199254740992e+15", "1.2345678901234567e+19", "5.5", "+5", "05", "5e0"}
	for _, raw := range jsonValues {
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			t.Fatalf("fixture %s: %v", raw, err)
		}
		printed := fmt.Sprint(decoded)
		typ := jsonType(raw)
		for _, needle := range needles {
			c, ok := compileFilterIndex(MetadataFilter{Field: "f", Operator: OpEqual, Value: needle})
			if !ok {
				t.Fatalf("eq %q is not answerable", needle)
			}
			selected, sure := false, false
			for _, b := range c.branches {
				if b.Kind != store.MetaByKey || b.Type != typ {
					continue
				}
				in := len(b.Values) == 0
				for _, v := range b.Values {
					switch typ {
					case "text":
						in = in || v == decoded
					case "integer":
						in = in || fmt.Sprint(v) == raw
					}
				}
				if in {
					selected, sure = true, sure || b.Sure
				}
			}
			matches := printed == needle
			if matches && !selected {
				t.Errorf("JSON %s (prints %q) matches needle %q but no index range selects it", raw, printed, needle)
			}
			if sure && !matches {
				t.Errorf("JSON %s (prints %q) is in a SURE range for needle %q, which it does not match", raw, printed, needle)
			}
		}
	}
}

// TestPreselect_ConcurrentOnDemandCreation: searches that arrive together on
// a field without a filter index race to create it. Whoever loses the write
// lock answers by the pass over the table; every one of them returns the
// engine's matches, and the index exists afterwards.
func TestPreselect_ConcurrentOnDemandCreation(t *testing.T) {
	items := make([]Item, 300)
	for i := range items {
		items[i] = Item{Text: fmt.Sprintf("doc %d", i), Metadata: map[string]any{
			"source": fmt.Sprintf("f%d.md", i), "a": fmt.Sprint(i % 10), "b": i % 7, "c": i%5 == 0,
		}}
	}
	s := newWidenSearcher(t, items)
	sp := s.passages.(*storePassages)
	all := s.passages.All()
	ctx := context.Background()
	filters := []MetadataFilter{
		{Field: "a", Operator: OpEqual, Value: "3"},
		{Field: "b", Operator: OpEqual, Value: 4},
		{Field: "c", Operator: OpEqual, Value: true},
		{Field: "a", Operator: OpIn, Value: []any{"1", "2"}},
	}
	const workers = 12
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			for round := 0; round < 4; round++ {
				f := filters[(w+round)%len(filters)]
				engine := NewMetadataFilterEngine([]MetadataFilter{f})
				want := 0
				for _, p := range all {
					if engine.Match(p.Metadata) {
						want++
					}
				}
				got, ran := s.preselectFilterIDs(ctx, engine)
				if !ran || len(got) != want {
					errs <- fmt.Errorf("worker %d, %+v: %d passages (ran=%v), engine %d", w, f, len(got), ran, want)
					return
				}
				for id := range got {
					p, err := s.passages.Get(id)
					if err != nil || !engine.Match(p.Metadata) {
						errs <- fmt.Errorf("worker %d, %+v: passage %d does not match (%v)", w, f, id, err)
						return
					}
				}
			}
			errs <- nil
		}(w)
	}
	for w := 0; w < workers; w++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	// One more query per field, alone: nothing holds the write lock now, so
	// an index a racing search could not create is created here.
	for _, f := range filters {
		s.preselectFilterIDs(ctx, NewMetadataFilterEngine([]MetadataFilter{f}))
	}
	have, err := sp.st.MetaIndexes(ctx, []string{"a", "b", "c"})
	if err != nil || !have["a"] || !have["b"] || !have["c"] {
		t.Fatalf("filter indexes after the searches: %v, %v; want a, b and c", have, err)
	}
}
