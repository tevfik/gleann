package gleann

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// decodeFamilyMetas are meta texts that are valid JSON for SQLite (json_valid
// = 1) yet that encoding/json decodes differently from json_type and
// json_extract: a number outside float64 (Go leaves the key nil, SQLite reads
// a real or a long integer), a key that occurs twice (Go keeps the last
// value, SQLite reads the first), a string with bytes that are not UTF-8 (Go
// reads U+FFFD, SQLite keeps the bytes). The filter indexes must answer every
// filter on them like the filter engine does.
func decodeFamilyMetas() []string {
	long := strings.Repeat("x", 3<<20)
	bs := string(rune(92))
	return []string{
		`{"v":1e400}`, `{"v":-1e400}`, `{"v":1e-400}`, `{"v":-1e-400}`, `{"v":1E400}`, `{"v":1e+400}`, `{"v":0e999}`,
		`{"v":12345678901234567890123456789012345678901234567890}`, `{"v":-12345678901234567890123456789012345678901234567890}`,
		`{"v":1` + strings.Repeat("0", 400) + `}`, `{"v":1e308}`, `{"v":1.7976931348623157e308}`, `{"v":1.8e308}`,
		`{"v":9007199254740993}`, `{"v":9223372036854775808}`, `{"v":1E5}`, `{"v":1e5}`, `{"v":1E+5}`, `{"v":100000}`, `{"v":-0}`, `{"v":-0.0}`, `{"v":0e0}`,
		`{"v":"a","v":"b"}`, `{"v":"b","v":"a"}`, `{"v":1,"v":"x"}`, `{"v":"x","v":1}`, `{"v":null,"v":"x"}`, `{"v":"x","v":null}`,
		`{"v":true,"v":false}`, `{"v":{"a":1,"a":2}}`, `{"v":[1],"v":[2]}`, `{"a":1,"v":"a","w":2,"v":"b"}`, `{"v":"a","` + bs + `u0076":"b"}`,
		`{"` + bs + `u0076":"go"}`, `{"v` + bs + `u0000":1}`, `{"v":1,"v":1e400}`, `{"v":1e400,"v":1}`,
		"{\"v\":\"" + bs + "u00e9\"}", "{\"v\":\"a" + bs + "/b\"}", "{\"v\":\"\xff\"}", "{\"v\":\"a\xc3\"}", "{\"v\":\"\xe2\x82\"}", "{\"v\":\"\xef\xbf\xbd\"}", "{\"v\":\"\xc0\x80\"}",
		`{"v":"` + long + `"}`, `{"v":"é"}`, `{"v":"` + bs + `u00e9"}`, `{"v":"` + bs + `ud83d` + bs + `ude00"}`, `{"v":"😀"}`,
		`{"v" : "a" , "w" : 1}`, "{\"v\":\n\"a\"}", `{"v":"a"}  `, "  {\"v\":\"a\"}",
		`{"V":"a"}`, `{"v":"a","V":"b"}`, `{"v":{"x":1e400}}`, `{"v":[1e400]}`, `{"w":1e400,"v":"go"}`,
	}
}

// decodeFamilyNULMetas are texts with a raw NUL byte: SQLite's JSON functions
// stop reading at it (json_valid = 1), encoding/json rejects the whole text.
// Only the index path is compared for them: the table pass decides "key
// absent" from SQLite's reading of text Go rejects (a separate, older item).
func decodeFamilyNULMetas() []string {
	return []string{
		"{\"v\":\"go\"}\x00", "{\"v\":\"go\"}\x00x", "{\"v\":\"a\"}\x00{\"v\":\"b\"}", "\x00{\"v\":\"go\"}",
		"{\"v\":\"g\x00o\"}", "{\"v\":1}\x00garbage", "{\"v\":null}\x00", "{\"w\":1}\x00{\"v\":\"x\"}", "{\"v\":\"go\"}\x00{",
	}
}

func decodeFamilyNeedles() []any {
	return []any{
		nil, "<nil>", "0", 0, "-0", "1", 1, 100000, 99999, "1e+05", "100000", "1e+400", "+Inf", "-Inf", "NaN", "Inf", 5.0, 1e21, "1e+21",
		"1.2345678901234568e+49", "-1.2345678901234568e+49", "9.007199254740992e+15", "9.223372036854776e+18", 9007199254740993, "9007199254740993",
		"a", "b", "x", "go", "", "é", "😀", "\xff", "\ufffd", "a\xc3", "a\ufffd", "\xe2\x82", "\xc0\x80", "a/b", "true", true, false, "false",
		"[1]", "[2]", "map[a:1]", "map[a:2]", "[1e+400]", []any{nil, "x"}, []any{"a", "b", 1}, []any{"\xff", "\ufffd"},
	}
}

// TestPreselectIndexed_DecodeFamily runs the filters of the needle list, on
// field v and on the missing key w, over the metas SQLite and Go decode
// differently and compares the index answer to the engine's.
func TestPreselectIndexed_DecodeFamily(t *testing.T) {
	runDecodeFamily(t, decodeFamilyMetas(), true)
}

// TestPreselectIndexed_DecodeFamilyRawNUL: the same comparison, index path
// only, over the metas with a raw NUL byte (with the family's metas mixed in).
func TestPreselectIndexed_DecodeFamilyRawNUL(t *testing.T) {
	runDecodeFamily(t, append(decodeFamilyNULMetas(), decodeFamilyMetas()[:20]...), false)
}

func runDecodeFamily(t *testing.T, metas []string, withScan bool) {
	s := newWidenSearcher(t, []Item{{Text: "seed", Metadata: map[string]any{"v": "seed", "source": "s.go"}}})
	sp, ok := s.passages.(*storePassages)
	if !ok {
		t.Fatalf("expected a format-2 store, got %T", s.passages)
	}
	ctx := context.Background()
	for i, m := range metas {
		ps := []store.NewPassage{{Text: fmt.Sprintf("decode doc %d", i), Meta: m}}
		if _, err := sp.st.ApplySource(ctx, store.SourceDelta{Source: fmt.Sprintf("dec/%d.go", i), Passages: ps}); err != nil {
			t.Fatalf("ApplySource(%.60q): %v", m, err)
		}
	}
	all := s.passages.All()
	ids := func(engine *MetadataFilterEngine) []int64 {
		var want []int64
		for _, p := range all {
			if engine.Match(p.Metadata) {
				want = append(want, p.ID)
			}
		}
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
		return want
	}
	const limit = 1 << 20
	bad := 0
	report := func(format string, args ...any) {
		bad++
		if bad <= 40 {
			t.Errorf(format, args...)
		}
	}
	// Under the race detector the table pass (it reads every row, the 3 MB
	// ones included) is compared on every 4th filter only; the index path is
	// compared on all. GLEANN_TEST_FULL_DIFFERENTIAL=1 restores the full run.
	nChecks := 0
	check := func(f MetadataFilter) {
		nChecks++
		scanThis := withScan && (fullDifferential() || nChecks%4 == 0)
		engine := NewMetadataFilterEngine([]MetadataFilter{f})
		want := ids(engine)
		got, outcome := s.preselectIndexed(ctx, sp, engine, limit, true)
		if outcome == preselectDone {
			if g := sortedIDKeys(got); fmt.Sprint(g) != fmt.Sprint(want) {
				report("%+v:\n filter indexes %v\n engine         %v", f, g, want)
			}
		}
		if !scanThis {
			return
		}
		if scan, ran := s.preselectScan(ctx, sp, engine, limit); ran {
			if g := sortedIDKeys(scan); fmt.Sprint(g) != fmt.Sprint(want) {
				report("%+v:\n table pass %v\n engine     %v", f, g, want)
			}
		}
	}
	for _, field := range []string{"v", "w"} {
		for _, n := range decodeFamilyNeedles() {
			check(MetadataFilter{Field: field, Operator: OpEqual, Value: n})
			check(MetadataFilter{Field: field, Operator: OpIn, Value: []any{n, "zz-never"}})
		}
		for _, in := range decodeFamilyNeedles() {
			if l, ok := in.([]any); ok {
				check(MetadataFilter{Field: field, Operator: OpIn, Value: l})
			}
		}
		check(MetadataFilter{Field: field, Operator: OpExists, Value: true})
		// The string operators are answered by the pass over the table only.
		for _, op := range []FilterOperator{OpNotEqual, OpNotIn, OpContains, OpStartsWith, OpEndsWith} {
			for _, n := range decodeFamilyNeedles() {
				if _, isList := n.([]any); isList && op != OpNotIn {
					continue
				}
				check(MetadataFilter{Field: field, Operator: op, Value: n})
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d filters disagree with the engine", bad)
	}
}

// TestStorePassageToGleann_NullMeta: a meta text of JSON null (not what
// gleann writes, but valid JSON) must not crash the conversion.
func TestStorePassageToGleann_NullMeta(t *testing.T) {
	p := storePassageToGleann(store.Passage{ID: 1, Source: "a.go", Meta: "null"})
	if p.Metadata["source"] != "a.go" || p.Metadata["chunk_index"] != 0 {
		t.Fatalf("metadata = %v", p.Metadata)
	}
}

// TestCompileFilterIndex_OddNeedles: the text range of a needle that is not
// UTF-8 is left out (an empty value list would select every text value), a
// needle with U+FFFD verifies every text value, and every key range carries
// the "odd" type for verification.
func TestCompileFilterIndex_OddNeedles(t *testing.T) {
	texts := func(f MetadataFilter) (valued, bare, odd int) {
		c, ok := compileFilterIndex(f)
		if !ok {
			t.Fatalf("%+v not compiled", f)
		}
		for _, b := range c.branches {
			switch {
			case b.Kind == store.MetaByKey && b.Type == "text" && len(b.Values) > 0:
				valued++
				if !b.Sure {
					t.Fatalf("%+v: a text range with values must be sure", f)
				}
			case b.Kind == store.MetaByKey && b.Type == "text":
				bare++
				if b.Sure {
					t.Fatalf("%+v: the all-text range must be verified", f)
				}
			case b.Kind == store.MetaByKey && b.Type == store.MetaOddType:
				odd++
				if b.Sure {
					t.Fatalf("%+v: the odd range must be verified", f)
				}
			}
		}
		return
	}
	for _, tc := range []struct {
		f                 MetadataFilter
		valued, bare, odd int
	}{
		{MetadataFilter{Field: "v", Operator: OpEqual, Value: "go"}, 1, 0, 1},
		{MetadataFilter{Field: "v", Operator: OpEqual, Value: "\xff"}, 0, 0, 1},
		{MetadataFilter{Field: "v", Operator: OpIn, Value: []any{"\xff", "go"}}, 1, 0, 1},
		{MetadataFilter{Field: "v", Operator: OpEqual, Value: "a�b"}, 0, 1, 1},
		{MetadataFilter{Field: "v", Operator: OpEqual, Value: nil}, 1, 0, 1},
	} {
		if v, b, o := texts(tc.f); v != tc.valued || b != tc.bare || o != tc.odd {
			t.Errorf("%+v: valued/bare/odd text ranges = %d/%d/%d, want %d/%d/%d", tc.f, v, b, o, tc.valued, tc.bare, tc.odd)
		}
	}
}
