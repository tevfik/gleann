package gleann

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/store"
	_ "modernc.org/sqlite"
)

// Differential of the filter pre-selection against the Go filter engine over
// metadata that SQLite and encoding/json read differently (JSON5 forms, a BOM,
// a trailing \f, escaped and raw NUL, bare keys, invalid UTF-8, duplicate keys,
// out-of-range numbers). The pre-selected set, by the filter indexes and by the
// table pass alike, must equal the engine's. Seeded and bounded: it runs in a
// few seconds. Under the race detector it runs one seed with fewer filter sets
// (the full run took over 200 s there); GLEANN_TEST_FULL_DIFFERENTIAL=1 runs
// the full set under race too.

// fullDifferential reports whether the differential tests run at full size.
func fullDifferential() bool {
	return !raceEnabled || os.Getenv("GLEANN_TEST_FULL_DIFFERENTIAL") == "1"
}

// ---- generator ---------------------------------------------------------

var tbpStrTokens = []string{
	`"a"`, `"b"`, `"go"`, `"x"`, `""`, `"1"`, `"0"`, `"-0"`, `"true"`, `"false"`, `"null"`, `"<nil>"`, `"1e+06"`, `"100000"`,
	`"é"`, `"%u00e9"`, "\"\xff\"", "\"a\xc3\"", "\"\xe2\x82\"", "\"\xc0\x80\"", "\"\xed\xa0\x80\"", "\"\xef\xbf\xbd\"", `"%ufffd"`, "\"a\xffb\"", "\"\xff\xfe\"",
	`"a\u0000b"`, `"\ud83d"`, `"%ud83d%ude00"`, `"😀"`, `"a\/b"`, `"a/b"`, `"a\"b"`, `"a\tb"`, `"a\nb"`, "\"a\x7fb\"", `"%u0061"`, `"[1]"`, `"map[a:1]"`,
	`"Go"`, `"go "`, `" go"`, `"a\\b"`,
}

var tbpNumTokens = []string{
	`0`, `-0`, `0.0`, `-0.0`, `0e0`, `1`, `-1`, `1.0`, `1e0`, `1E0`, `1e+0`, `10`, `999999`, `-999999`, `1000000`, `1e6`, `1E+6`, `1.5`, `0.1`, `100000`, `1e5`, `1E5`,
	`9007199254740992`, `9007199254740993`, `-9007199254740993`, `9223372036854775807`, `9223372036854775808`, `-9223372036854775808`, `-9223372036854775809`, `18446744073709551616`,
	`1e400`, `-1e400`, `1E400`, `1e+400`, `1e-400`, `-1e-400`, `2e-324`, `4.9e-324`, `1.7976931348623157e308`, `1.7976931348623158e308`, `1.797693134862315808e308`, `-1.797693134862315808e308`,
	`1` + strings.Repeat("0", 400), `-1` + strings.Repeat("0", 400), `1` + strings.Repeat("0", 308), `0.` + strings.Repeat("0", 400) + `1`, `123456789012345678901234567890`,
	`1e21`, `1e20`, `123456`, `1234567`, `5`, `5.0`, `5e0`, `50e-1`,
}

var tbpLitTokens = []string{`true`, `false`, `null`, `[]`, `{}`, `[1]`, `["a"]`, `[null]`, `{"b":"y"}`, `{"b":1e400}`, `[1e400]`, `{"a":1,"a":2}`, `[1,"a",null]`, `{"b":"x","b":"y"}`}

var tbpKeys = []string{`v`, `v`, `v`, `w`, `kind`, `type`, `source`, `file`, `a.b`, `a`, `a-b`, `%u0076`, "v\xff", `V`, `v\u0000`, `%u006bind`, `a%u002eb`, `vv`}

func tbpWS(r *rand.Rand) string {
	switch r.Intn(10) {
	case 0:
		return " "
	case 1:
		return "\n"
	case 2:
		return "\t"
	case 3:
		return "\r\n"
	}
	return ""
}

func tbpValue(r *rand.Rand, depth int) string {
	switch k := r.Intn(10); {
	case k < 4:
		return tbpStrTokens[r.Intn(len(tbpStrTokens))]
	case k < 7:
		return tbpNumTokens[r.Intn(len(tbpNumTokens))]
	case k < 9 || depth > 1:
		return tbpLitTokens[r.Intn(len(tbpLitTokens))]
	default:
		return tbpObject(r, depth+1)
	}
}

func tbpObject(r *rand.Rand, depth int) string {
	n := r.Intn(5)
	var used []string
	var b strings.Builder
	b.WriteString("{" + tbpWS(r))
	for i := 0; i < n; i++ {
		var k string
		if len(used) > 0 && r.Intn(5) == 0 {
			k = used[r.Intn(len(used))] // duplicate
		} else {
			k = tbpKeys[r.Intn(len(tbpKeys))]
		}
		if depth > 0 && r.Intn(2) == 0 {
			k = "b"
		}
		used = append(used, k)
		if i > 0 {
			b.WriteString("," + tbpWS(r))
		}
		b.WriteString(`"` + k + `"` + tbpWS(r) + ":" + tbpWS(r) + tbpValue(r, depth) + tbpWS(r))
	}
	b.WriteString("}")
	return b.String()
}

func tbpMeta(r *rand.Rand) string {
	o := tbpObject(r, 0)
	switch r.Intn(40) {
	case 0:
		return "[]"
	case 1:
		return "5"
	case 2:
		return `"s"`
	case 3:
		return "null"
	case 4:
		return ""
	case 5:
		return "\ufeff" + o
	case 6:
		return o + "\x00garbage"
	case 7:
		return o + "\x00"
	case 8:
		return strings.Replace(o, `"`, ``, 2) // JSON5-ish bare key
	case 9:
		return o + "\f"
	case 10:
		return strings.TrimSuffix(o, "}") + ",}"
	case 11:
		return "  " + o + "  "
	case 12:
		return "{not json"
	case 13:
		return strings.Repeat("[", 1100) + strings.Repeat("]", 1100)
	}
	return o
}

// handwritten adversarial metas
func tbpFixedMetas() []string {
	bs := `\`
	return []string{
		`{"v":1e400}`, `{"v":-1e400}`, `{"v":1E400}`, `{"v":1e-400}`, `{"v":1.797693134862315808e308}`, `{"v":1` + strings.Repeat("0", 400) + `}`,
		`{"v":"a","v":"b"}`, `{"v":"a","v":"b","v":"c"}`, `{"v":"a","v":"b","v":"a"}`, `{"v":"a","` + bs + `u0076":"b"}`, `{"` + bs + `u0076":"a","v":"b"}`,
		`{"a.b":"x","a":{"b":"y"}}`, `{"a":{"b":"y"},"a.b":"x"}`, `{"a":{"b":"y","b":"z"}}`, `{"a.b":"x","a.b":"y"}`, `{"a` + bs + `u002eb":"x","a.b":"y"}`,
		"{\"v\":\"\xff\"}", "{\"v\":\"\xef\xbf\xbd\"}", "{\"v\":\"" + bs + "ufffd\"}", "{\"v\xff\":\"a\",\"v\":\"b\"}", "{\"v\xff\":\"a\",\"v" + bs + "ufffd\":\"b\"}",
		"{\"v\":\"a\"}\x00garbage", "{\"v\":\"a\"}\x00", "{\"kind\":\"go\"}\x00{\"kind\":\"x\"}", "{\"source\":\"s.go\"}\x00", "\ufeff{\"v\":\"a\"}", `{v:"a"}`, `{"v":"a",}`,
		`{"v":-0}`, `{"v":1.0}`, `{"v":1}`, `{"v":"1"}`, `{"v":true}`, `{"v":"true"}`, `{"v":1,"v":true}`, `{"v":{}}`, `{"v":[]}`, `null`, `[]`, `5`, `"v"`, `{}`, ``,
		`{"v":9007199254740993}`, `{"v":9223372036854775808}`, `{"kind":"go","type":"x"}`, `{"type":"go"}`, `{"kind":"go","kind":"x"}`, `{"type":"x","type":"go"}`,
		`{"kind":1e400,"type":"go"}`, `{"source":"m.go"}`, `{"file":"f.go"}`, `{"source":"a","source":"b"}`, `{"v":null}`, `{"v":null,"v":null}`, `{"v":"a","v":null}`,
	}
}

// ---- comparison --------------------------------------------------------

func tbpEngineIDs(all []Passage, e *MetadataFilterEngine) []int64 {
	var want []int64
	for _, p := range all {
		if e.Match(p.Metadata) {
			want = append(want, p.ID)
		}
	}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	return want
}

type tbpStats struct {
	filters, idxDone, scanRan, idxBad, scanBad int
	samples                                    []string
}

func (st *tbpStats) bad(msg string) {
	if len(st.samples) < 20 {
		st.samples = append(st.samples, msg)
	}
}

func tbpSeedStore(t *testing.T, metas []string) (*LeannSearcher, *storePassages, []Passage) {
	t.Helper()
	s := newWidenSearcher(t, []Item{{Text: "seed", Metadata: map[string]any{"v": "seed", "source": "s.go"}}})
	sp := s.passages.(*storePassages)
	ctx := context.Background()
	// several passages per source; some sources are the same
	for i := 0; i < len(metas); i += 3 {
		var ps []store.NewPassage
		for j := i; j < i+3 && j < len(metas); j++ {
			ps = append(ps, store.NewPassage{Text: fmt.Sprintf("doc %d", j), Meta: metas[j]})
		}
		src := fmt.Sprintf("src/%d.go", i)
		if i%7 == 0 {
			src = "m.go"
		}
		if _, err := sp.st.ApplySource(ctx, store.SourceDelta{Source: src, Passages: ps}); err != nil {
			t.Fatalf("ApplySource: %v", err)
		}
	}
	var all []Passage
	if err := sp.st.ForEach(ctx, func(p store.Passage) error {
		all = append(all, storePassageToGleann(p))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return s, sp, all
}

func tbpCheck(ctx context.Context, s *LeannSearcher, sp *storePassages, all []Passage, st *tbpStats, logic string, fs []MetadataFilter) {
	e := &MetadataFilterEngine{Filters: fs, Logic: logic}
	want := fmt.Sprint(tbpEngineIDs(all, e))
	st.filters++
	const limit = 1 << 20
	if got, outcome := s.preselectIndexed(ctx, sp, e, limit, true); outcome == preselectDone {
		st.idxDone++
		if g := fmt.Sprint(sortedIDKeys(got)); g != want {
			st.idxBad++
			st.bad(fmt.Sprintf("INDEX %s %+v\n  got  %s\n  want %s", logic, fs, g, want))
		}
	}
	if scan, ran := s.preselectScan(ctx, sp, e, limit); ran {
		st.scanRan++
		if g := fmt.Sprint(sortedIDKeys(scan)); g != want {
			st.scanBad++
			st.bad(fmt.Sprintf("TABLE %s %+v\n  got  %s\n  want %s", logic, fs, g, want))
		}
	}
}

func TestTablePassDifferential(t *testing.T) {
	for _, c := range []struct {
		seed       int64
		sqliteOnly bool
	}{{20261124, false}, {7, false}, {20261124, true}, {7, true}} {
		if !fullDifferential() && c.seed != 20261124 {
			continue
		}
		seed, sqliteOnly := c.seed, c.sqliteOnly
		t.Run(fmt.Sprintf("seed%d/sqliteReadable=%v", seed, sqliteOnly), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			nRows, nSets, nPer := 500, 1200, 12
			if !fullDifferential() {
				nRows, nSets, nPer = 150, 150, 2
			}
			metas := tbpFixedMetas()
			for i := 0; i < nRows; i++ {
				metas = append(metas, tbpMeta(r))
			}
			if sqliteOnly {
				// Only text SQLite's JSON functions accept (JSON5 included), so
				// the table pass runs on every filter set and the rows Go
				// rejects but SQLite reads are the ones under test.
				db, err := sql.Open("sqlite", ":memory:")
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				kept := metas[:0]
				for _, m := range metas {
					var ok int
					if err := db.QueryRow(`SELECT json_valid(?, 2)`, m).Scan(&ok); err == nil && ok == 1 {
						kept = append(kept, m)
					}
				}
				metas = kept
			}
			s, sp, all := tbpSeedStore(t, metas)
			ctx := context.Background()
			fields := []string{"v", "w", "kind", "type", "source", "a.b", "a", "a-b", "missing", "V", "vv", "file"}
			needlePool := []any{nil, "<nil>", "a", "b", "c", "go", "x", "", "1", 1, 1.0, 0, "0", "-0", 5, "5", true, false, "true", "false",
				"\xff", "�", "a�", "a\xff", "é", "😀", "a/b", "a\"b", "[1]", "map[b:y]", "[]", "1e+06", 1e6, 999999, "seed", "s.go", "m.go", "f.go", "y", "z"}
			for _, p := range all {
				for _, v := range p.Metadata {
					if k := fmt.Sprint(v); len(k) < 100 && r.Intn(4) == 0 {
						needlePool = append(needlePool, k)
					}
				}
			}
			needle := func() any { return needlePool[r.Intn(len(needlePool))] }
			var st tbpStats
			for _, f := range fields {
				tbpCheck(ctx, s, sp, all, &st, "and", []MetadataFilter{{Field: f, Operator: OpExists, Value: true}})
				tbpCheck(ctx, s, sp, all, &st, "and", []MetadataFilter{{Field: f, Operator: OpExists, Value: false}})
				for k := 0; k < nPer; k++ {
					tbpCheck(ctx, s, sp, all, &st, "and", []MetadataFilter{{Field: f, Operator: OpEqual, Value: needle()}})
					tbpCheck(ctx, s, sp, all, &st, "and", []MetadataFilter{{Field: f, Operator: OpIn, Value: []any{needle(), needle()}}})
				}
			}
			ops := []FilterOperator{OpEqual, OpEqual, OpIn, OpIn, OpExists, OpNotEqual, OpNotIn, OpContains, OpStartsWith, OpEndsWith}
			for i := 0; i < nSets; i++ {
				var fs []MetadataFilter
				for j := 1 + r.Intn(3); j > 0; j-- {
					f := MetadataFilter{Field: fields[r.Intn(len(fields))], Operator: ops[r.Intn(len(ops))]}
					switch f.Operator {
					case OpExists:
						f.Value = r.Intn(4) != 0
					case OpIn, OpNotIn:
						var l []any
						for k := r.Intn(4) + 1; k > 0; k-- {
							l = append(l, needle())
						}
						f.Value = l
					default:
						f.Value = needle()
					}
					fs = append(fs, f)
				}
				logic := "and"
				if r.Intn(2) == 0 {
					logic = "or"
				}
				tbpCheck(ctx, s, sp, all, &st, logic, fs)
			}
			t.Logf("rows %d, filter sets %d, index answered %d, table pass ran %d; mismatches: index %d, table %d",
				len(all), st.filters, st.idxDone, st.scanRan, st.idxBad, st.scanBad)
			for _, m := range st.samples {
				t.Log(m)
			}
			if sqliteOnly && st.scanRan < st.filters*9/10 {
				t.Errorf("table pass ran on only %d of %d filter sets: the comparison is not exercising it", st.scanRan, st.filters)
			}
			if st.idxBad+st.scanBad > 0 {
				t.Fail()
			}
		})
	}
}

// TestTablePassUndecidableMeta: one targeted case per form of meta text that
// SQLite's JSON functions read as an object with keys and encoding/json
// rejects (or reads without the key). The engine sees no keys for such a row,
// so exists:false matches it and the file filter falls back to the source.
func TestTablePassUndecidableMeta(t *testing.T) {
	forms := map[string]string{
		"trailing comma": `{"v":"a",}`,
		"trailing FF":    "{\"v\":\"a\"}\f",
		"BOM":            "\ufeff{\"v\":\"a\"}",
		"bare key":       `{v:"a"}`,
		"escaped NUL":    `{"v\u0000":"a"}`,
		"raw NUL":        "{\"v\":\"a\"}\x00",
		"NUL, garbage":   "{\"v\":\"a\"}\x00garbage",
		"single quotes":  `{'v':'a'}`,
		"comment":        `{"v":"a"} // c`,
		"hex number":     `{"v":0x10}`,
		"clean":          `{"v":"a"}`,
		"clean other":    `{"w":"a"}`,
	}
	names := make([]string, 0, len(forms))
	for n := range forms {
		names = append(names, n)
	}
	sort.Strings(names)
	var metas []string
	for _, n := range names {
		metas = append(metas, forms[n])
	}
	s, sp, all := tbpSeedStore(t, metas)
	ctx := context.Background()
	for _, fs := range [][]MetadataFilter{
		{{Field: "v", Operator: OpExists, Value: false}},
		{{Field: "v", Operator: OpExists, Value: true}},
		{{Field: "file", Operator: OpEqual, Value: "m.go"}},
		{{Field: "file", Operator: OpNotEqual, Value: "m.go"}},
		{{Field: "file", Operator: OpExists, Value: false}},
		{{Field: "v", Operator: OpNotEqual, Value: "a"}},
		{{Field: "v", Operator: OpEqual, Value: "a"}},
	} {
		e := &MetadataFilterEngine{Filters: fs}
		want := fmt.Sprint(tbpEngineIDs(all, e))
		got, ran := s.preselectScan(ctx, sp, e, 1<<20)
		if !ran {
			t.Errorf("%+v: table pass did not run", fs)
			continue
		}
		if g := fmt.Sprint(sortedIDKeys(got)); g != want {
			var rows []string
			for _, p := range all {
				rows = append(rows, fmt.Sprintf("%d:%q", p.ID, strings.TrimSpace(fmt.Sprint(p.Metadata))))
			}
			t.Errorf("%+v: table pass %s, engine %s\nrows %v", fs, g, want, rows)
		}
	}
}

// TestTablePassFieldNamesDecodedApart: Go reads a key with an invalid UTF-8
// byte or an escaped lone surrogate as the key with U+FFFD, SQLite keeps the
// bytes, so a JSON path built from such a field name finds nothing (or the
// wrong key) in SQL. The compiler declines such fields; the engine decides.
func TestTablePassFieldNamesDecodedApart(t *testing.T) {
	metas := []string{
		"{\"k\xff\":\"x\"}", // raw invalid byte in the key
		`{"k\ud83d":"x"}`,   // escaped lone surrogate in the key
		"{\"k�\":\"x\"}",    // the replacement character itself
		`{"k�":"y"}`,        // the same, escaped
		`{"k":"x"}`,
		`{"other":"x"}`,
	}
	s, sp, all := tbpSeedStore(t, metas)
	ctx := context.Background()
	for _, field := range []string{"k�", "k\xff", "�"} {
		for _, f := range []MetadataFilter{
			{Field: field, Operator: OpExists, Value: true},
			{Field: field, Operator: OpExists, Value: false},
			{Field: field, Operator: OpEqual, Value: "x"},
			{Field: field, Operator: OpNotEqual, Value: "x"},
			{Field: field, Operator: OpContains, Value: "x"},
		} {
			e := &MetadataFilterEngine{Filters: []MetadataFilter{f}}
			want := fmt.Sprint(tbpEngineIDs(all, e))
			got, ran := s.preselectScan(ctx, sp, e, 1<<20)
			if !ran {
				continue // declined: the caller keeps the engine's loop
			}
			if g := fmt.Sprint(sortedIDKeys(got)); g != want {
				t.Errorf("%+v (%q): table pass %s, engine %s", f, field, g, want)
			}
		}
	}
}
