package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// applyMetas applies one source whose passages carry the given meta JSON.
func applyMetas(t *testing.T, s *Store, source string, metas ...string) []int64 {
	t.Helper()
	ps := make([]NewPassage, len(metas))
	for i, m := range metas {
		ps[i] = NewPassage{Text: fmt.Sprintf("%s chunk %d", source, i), Meta: m}
	}
	ids, err := s.ApplySource(context.Background(), SourceDelta{Source: source, File: FileRow{Hash: "h-" + source}, Passages: ps})
	if err != nil {
		t.Fatalf("ApplySource(%s): %v", source, err)
	}
	return ids
}

// indexedIDs returns the sorted passage IDs of one text range of key.
func indexedIDs(t *testing.T, s *Store, key, value string) []int64 {
	t.Helper()
	ms, err := s.SelectByMetaIndex(context.Background(), []MetaBranch{{Kind: MetaByKey, Key: key, Type: "text", Values: []any{value}, Sure: true}}, 0)
	if err != nil {
		t.Fatalf("SelectByMetaIndex(%s=%s): %v", key, value, err)
	}
	ids := make([]int64, len(ms))
	for i, m := range ms {
		ids[i] = m.ID
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// scannedIDs is the same selection without any index: the reference.
func scannedIDs(t *testing.T, s *Store, key, value string) []int64 {
	t.Helper()
	rows, err := s.reader.Query(`SELECT id FROM passages NOT INDEXED
  WHERE json_valid(meta) AND json_type(meta, '$."`+key+`"') = 'text' AND json_extract(meta, '$."`+key+`"') = ? ORDER BY id`, value)
	if err != nil {
		t.Fatalf("reference scan: %v", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("reference scan: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func filterIndexNames(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.reader.Query(`SELECT name FROM sqlite_master WHERE type = 'index' AND name GLOB 'passages_mf*' ORDER BY name`)
	if err != nil {
		t.Fatalf("sqlite_master: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("sqlite_master: %v", err)
		}
		names = append(names, n)
	}
	return names
}

func TestMetaIndexable(t *testing.T) {
	for key, want := range map[string]bool{
		"lang": true, "node_type": true, "doc.title": true, "a-b": true, "t10": true, "tür": true, "日本語": true,
		"":                      false,
		"a'b":                   false,
		`a"b`:                   false,
		`a\b`:                   false,
		"a b":                   false,
		"x')) AND 1=0 --":       false,
		"a\x00b":                false,
		"a\nb":                  false,
		"\xff":                  false,
		strings.Repeat("k", 64): true,
		strings.Repeat("k", 65): false,
	} {
		if got := MetaIndexable(key); got != want {
			t.Errorf("MetaIndexable(%q) = %v, want %v", key, got, want)
		}
	}
}

// TestOpenCreatesNoFilterIndex: filter indexes are not part of the schema
// history. A database that is opened, written and reopened has none, and its
// store_format and migration steps are what they were.
func TestOpenCreatesNoFilterIndex(t *testing.T) {
	s := openTestStore(t)
	applyMetas(t, s, "a.go", `{"lang":"go"}`, `{"lang":"rust"}`)
	if names := filterIndexNames(t, s); len(names) != 0 {
		t.Fatalf("filter indexes without a request: %v", names)
	}
	if have, err := s.MetaIndexes(context.Background(), []string{"lang", "a'b"}); err != nil || have["lang"] || have["a'b"] {
		t.Fatalf("MetaIndexes = %v, %v; want none", have, err)
	}
	// Without the index the statement fails; it never falls back to a scan.
	if _, err := s.SelectByMetaIndex(context.Background(), []MetaBranch{{Kind: MetaByKey, Key: "lang", Type: "text", Values: []any{"go"}}}, 0); err == nil {
		t.Fatal("SelectByMetaIndex without the index succeeded; INDEXED BY must refuse")
	}
	var format string
	if err := s.reader.QueryRow(`SELECT value FROM meta WHERE key = 'store_format'`).Scan(&format); err != nil || format != "2" {
		t.Fatalf("store_format = %q, %v; want 2", format, err)
	}
}

// TestMetaIndexMaintainedByEveryWriter: once created, a filter index follows
// every write path of the store, and writes made by plain SQL through another
// connection (what a binary that knows nothing about filter indexes does).
func TestMetaIndexMaintainedByEveryWriter(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applyMetas(t, s, "a.go", `{"lang":"go","n":1}`, `{"lang":"rust"}`, `{}`)
	applyMetas(t, s, "b.go", `{"lang":"go"}`, `{"lang":"go","x":[1]}`)
	if err := s.EnsureMetaIndexes(ctx, []string{"lang"}); err != nil {
		t.Fatalf("EnsureMetaIndexes: %v", err)
	}
	if have, err := s.MetaIndexes(ctx, []string{"lang", "n"}); err != nil || !have["lang"] || have["n"] {
		t.Fatalf("MetaIndexes = %v, %v; want lang only", have, err)
	}
	check := func(step string, wantGo int) {
		t.Helper()
		for _, v := range []string{"go", "rust", "zig"} {
			got, want := indexedIDs(t, s, "lang", v), scannedIDs(t, s, "lang", v)
			if !sameIDs(got, want) {
				t.Fatalf("%s: lang=%s from the index %v, from the table %v", step, v, got, want)
			}
			if v == "go" && len(got) != wantGo {
				t.Fatalf("%s: %d passages with lang=go, want %d", step, len(got), wantGo)
			}
		}
	}
	check("after create", 3)

	applyMetas(t, s, "a.go", `{"lang":"zig"}`, `{"lang":"go"}`) // replace a source
	check("after ApplySource", 3)
	if _, err := s.RemoveSource(ctx, "b.go"); err != nil {
		t.Fatalf("RemoveSource: %v", err)
	}
	check("after RemoveSource", 1)

	// Slot changes do not touch the indexed expressions.
	ids := applyMetas(t, s, "c.go", `{"lang":"go"}`, `{"lang":"go"}`)
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	if err := s.AssignSlotsTx(ctx, tx, map[int64]int64{ids[0]: 7, ids[1]: 8}); err != nil {
		t.Fatalf("AssignSlotsTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := s.ReassignSlots(ctx, map[int64]int64{7: 8, 8: 7}); err != nil {
		t.Fatalf("ReassignSlots: %v", err)
	}
	check("after slot changes", 3)
	ms, err := s.SelectByMetaIndex(ctx, []MetaBranch{{Kind: MetaByKey, Key: "lang", Type: "text", Values: []any{"go"}, Sure: true}}, 0)
	if err != nil {
		t.Fatalf("SelectByMetaIndex: %v", err)
	}
	slots := map[int64]int64{}
	for _, m := range ms {
		if !m.Sure || m.Meta != "" || m.Source != "" {
			t.Fatalf("a sure match carries metadata: %+v", m)
		}
		if m.VecSlot != nil {
			slots[m.ID] = *m.VecSlot
		}
	}
	if slots[ids[0]] != 8 || slots[ids[1]] != 7 || len(slots) != 2 {
		t.Fatalf("slots from the index path = %v, want %d:8 %d:7", slots, ids[0], ids[1])
	}

	// A writer that knows nothing about filter indexes: plain SQL on its own
	// connection, as v1.20.x issues it.
	old, err := sql.Open("sqlite", s.path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("opening second connection: %v", err)
	}
	defer old.Close()
	if _, err := old.Exec(`INSERT INTO passages (pid, source, chunk_no, vec_slot, kind, text, meta) VALUES ('old-1', 'd.go', 0, NULL, 'code', 't', '{"lang":"go"}')`); err != nil {
		t.Fatalf("plain insert: %v", err)
	}
	check("after a plain SQL insert", 4)
	if _, err := old.Exec(`UPDATE passages SET meta = '{"lang":"rust"}' WHERE pid = 'old-1'`); err != nil {
		t.Fatalf("plain update: %v", err)
	}
	check("after a plain SQL update", 3)
	if _, err := old.Exec(`DELETE FROM passages WHERE source = 'c.go'`); err != nil {
		t.Fatalf("plain delete: %v", err)
	}
	check("after a plain SQL delete", 1)

	// A whole-index rebuild in one transaction.
	err = s.ReplaceAll(ctx, func(add func(SourcedPassage) error) error {
		for i := 0; i < 5; i++ {
			lang := "go"
			if i%2 == 1 {
				lang = "rust"
			}
			if err := add(SourcedPassage{Source: "r.go", NewPassage: NewPassage{Text: fmt.Sprint("r", i), Meta: `{"lang":"` + lang + `"}`}}); err != nil {
				return err
			}
		}
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	check("after ReplaceAll", 3)

	// A key index counts only together with the damaged-rows index its
	// readers need; should that one be dropped, the next request restores it.
	if _, err := old.Exec(`DROP INDEX ` + metaDamagedIndex); err != nil {
		t.Fatalf("dropping %s: %v", metaDamagedIndex, err)
	}
	if have, err := s.MetaIndexes(ctx, []string{"lang"}); err != nil || have["lang"] {
		t.Fatalf("MetaIndexes without %s = %v, %v; want none", metaDamagedIndex, have, err)
	}
	if err := s.EnsureMetaIndexes(ctx, []string{"lang"}); err != nil {
		t.Fatalf("EnsureMetaIndexes restoring %s: %v", metaDamagedIndex, err)
	}
	if have, err := s.MetaIndexes(ctx, []string{"lang"}); err != nil || !have["lang"] {
		t.Fatalf("MetaIndexes after the restore = %v, %v; want lang", have, err)
	}
	check("after dropping and restoring the damaged-rows index", 3)

	// Creating again writes nothing: it succeeds while another connection
	// holds the write lock.
	wtx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	defer wtx.Rollback()
	if err := s.EnsureMetaIndexes(ctx, []string{"lang"}); err != nil {
		t.Fatalf("EnsureMetaIndexes for an existing index needed the write lock: %v", err)
	}
}

// TestSelectByMetaIndexReadsIndexesOnly: no branch of the statement scans the
// passages table; that is what makes the cost follow the matches.
func TestSelectByMetaIndexReadsIndexesOnly(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applyMetas(t, s, "a.go", `{"lang":"go","kind":"code","n":5}`, `{"lang":"rust","type":"x"}`)
	if err := s.EnsureMetaIndexes(ctx, []string{"lang", "kind", "type", "n"}); err != nil {
		t.Fatalf("EnsureMetaIndexes: %v", err)
	}
	branches := []MetaBranch{
		{Kind: MetaByKey, Key: "lang", Type: "text", Values: []any{"go", "rust"}, Sure: true},
		{Kind: MetaByKey, Key: "n", Type: "integer", Values: []any{int64(5)}, Sure: true},
		{Kind: MetaByKey, Key: "n", Type: "real"},
		{Kind: MetaByKey, Key: "kind", Sure: true},
		{Kind: MetaByKey, Key: "type", Type: "true"},
		{Kind: MetaBySource, Source: "a.go", Sure: true},
		{Kind: MetaBySource, Source: ""},
		{Kind: MetaDamaged},
	}
	query, args, err := metaIndexQuery(branches, 10)
	if err != nil {
		t.Fatalf("metaIndexQuery: %v", err)
	}
	rows, err := s.reader.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()
	steps := 0
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scanning the plan: %v", err)
		}
		if !strings.Contains(detail, "passages") {
			continue // COMPOUND QUERY, UNION ALL, ...
		}
		steps++
		if !strings.Contains(detail, "USING INDEX passages_") && !strings.Contains(detail, "USING COVERING INDEX passages_") {
			t.Errorf("plan step reads the table without an index: %s", detail)
		}
	}
	if steps != len(branches) {
		t.Errorf("%d plan steps over passages, want one per branch (%d)", steps, len(branches))
	}
	ms, err := s.SelectByMetaIndex(ctx, branches, 0)
	if err != nil {
		t.Fatalf("SelectByMetaIndex: %v", err)
	}
	// lang: 2 rows, n=5: 1, kind present: 1, source a.go: 2; the others none.
	if len(ms) != 6 {
		t.Fatalf("got %d rows, want 6: %+v", len(ms), ms)
	}
	if got, err := s.SelectByMetaIndex(ctx, branches, 3); err != nil || len(got) != 3 {
		t.Fatalf("limit 3: %d rows, %v", len(got), err)
	}
}

// TestMetaIndexDamagedMeta: with filter indexes in place a passage whose meta
// is not JSON can still be written (the indexed expressions must not raise),
// it is absent from every key index like a passage without metadata, and the
// damaged range hands it, and the strings SQLite and Go decode differently,
// to the caller for verification.
func TestMetaIndexDamagedMeta(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.EnsureMetaIndexes(ctx, []string{"lang"}); err != nil {
		t.Fatalf("EnsureMetaIndexes: %v", err)
	}
	ids := applyMetas(t, s, "a.go",
		`{"lang":"go"}`,
		`{not json`,
		`[1,2]`,
		`"lang"`,
		`{"lang":"g\u0000o"}`,
		`{"lang":"\ud83d"}`,
		`{"lang":"\uD83D`+`\uDE00"}`, // an escaped surrogate pair (a raw emoji is not damaged)
		`{"lang":"a<b"}`,             // encoding/json writes < like this: not damaged
		"{\"lang\":\"go\"}\x00x",     // 8: a raw NUL after valid JSON: SQLite stops there, Go rejects the text
		"{\"lang\":\"go\"}\x00",      // 9
	)
	// The key index reads the rows with a raw NUL as SQLite does (it stops at
	// the NUL); the damaged range below is what makes callers verify them.
	if got, want := indexedIDs(t, s, "lang", "go"), []int64{ids[0], ids[8], ids[9]}; !sameIDs(got, want) {
		t.Fatalf("lang=go from the index = %v, want %v", got, want)
	}
	if got := indexedIDs(t, s, "lang", "a<b"); !sameIDs(got, ids[7:8]) {
		t.Fatalf("lang=a<b from the index = %v, want %v", got, ids[7:8])
	}
	ms, err := s.SelectByMetaIndex(ctx, []MetaBranch{{Kind: MetaDamaged}}, 0)
	if err != nil {
		t.Fatalf("SelectByMetaIndex(damaged): %v", err)
	}
	got := map[int64]string{}
	for _, m := range ms {
		if !m.Damaged || m.Sure {
			t.Fatalf("damaged row flags: %+v", m)
		}
		got[m.ID] = m.Meta
	}
	want := map[int64]bool{ids[1]: true, ids[4]: true, ids[5]: true, ids[6]: true, ids[8]: true, ids[9]: true}
	if len(got) != len(want) {
		t.Fatalf("damaged rows = %v, want ids %v", got, want)
	}
	for id := range want {
		if got[id] == "" {
			t.Fatalf("damaged rows = %v: passage %d missing or without its meta", got, id)
		}
	}
	// Creating an index over rows that are already damaged works too.
	if err := s.EnsureMetaIndexes(ctx, []string{"other"}); err != nil {
		t.Fatalf("EnsureMetaIndexes over damaged rows: %v", err)
	}
}

func TestEnsureMetaIndexesLimitAndKeys(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applyMetas(t, s, "a.go", `{"k0":"v"}`)
	if err := s.EnsureMetaIndexes(ctx, []string{"ok", "a'b"}); err == nil {
		t.Fatal("EnsureMetaIndexes accepted a key that cannot be indexed")
	}
	if names := filterIndexNames(t, s); len(names) != 0 {
		t.Fatalf("a refused request left indexes behind: %v", names)
	}
	var keys []string
	for i := 0; i < MaxMetaIndexes; i++ {
		keys = append(keys, fmt.Sprintf("k%d", i))
	}
	if err := s.EnsureMetaIndexes(ctx, keys[:3]); err != nil {
		t.Fatalf("EnsureMetaIndexes(3): %v", err)
	}
	if err := s.EnsureMetaIndexes(ctx, append([]string{"one-too-many"}, keys...)); err == nil {
		t.Fatalf("EnsureMetaIndexes created more than %d filter indexes", MaxMetaIndexes)
	}
	if names := filterIndexNames(t, s); len(names) != 3+1 { // three keys and the damaged index
		t.Fatalf("after a refused request: %v, want the 3 key indexes and %s", names, metaDamagedIndex)
	}
	if err := s.EnsureMetaIndexes(ctx, keys); err != nil {
		t.Fatalf("EnsureMetaIndexes(%d): %v", MaxMetaIndexes, err)
	}
	if err := s.EnsureMetaIndexes(ctx, []string{"one-too-many"}); err == nil {
		t.Fatalf("EnsureMetaIndexes created filter index %d", MaxMetaIndexes+1)
	}
	if got := indexedIDs(t, s, "k0", "v"); len(got) != 1 {
		t.Fatalf("k0=v from the index = %v, want one passage", got)
	}
}

// TestEnsureMetaIndexesDoesNotWaitForAWriter: a search must not queue behind
// a build that holds the write lock (busy_timeout is 10 s on the store's own
// connections); creating a filter index fails at once instead.
func TestEnsureMetaIndexesDoesNotWaitForAWriter(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applyMetas(t, s, "a.go", `{"lang":"go"}`)
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	start := time.Now()
	err = s.EnsureMetaIndexes(ctx, []string{"lang"})
	if err == nil {
		t.Fatal("EnsureMetaIndexes succeeded while another transaction held the write lock")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("EnsureMetaIndexes waited %s for the write lock; it must fail at once", d)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if err := s.EnsureMetaIndexes(ctx, []string{"lang"}); err != nil {
		t.Fatalf("EnsureMetaIndexes after the writer finished: %v", err)
	}
	if got := indexedIDs(t, s, "lang", "go"); len(got) != 1 {
		t.Fatalf("lang=go from the index = %v, want one passage", got)
	}
}

// BenchmarkApplySourceMetaIndexes measures what filter indexes cost a write:
// 200 files of 20 passages with the metadata shape of a code index, without
// filter indexes and with 1, 4 and 16 of them.
func BenchmarkApplySourceMetaIndexes(b *testing.B) {
	keys := []string{"lang", "node_type", "is_test", "ext", "k4", "k5", "k6", "k7", "k8", "k9", "k10", "k11", "k12", "k13", "k14", "k15"}
	for _, n := range []int{0, 1, 4, 16} {
		b.Run(fmt.Sprintf("indexes=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				s, err := Open(b.TempDir()+"/idx.db", Options{})
				if err != nil {
					b.Fatalf("Open: %v", err)
				}
				if n > 0 {
					if err := s.EnsureMetaIndexes(context.Background(), keys[:n]); err != nil {
						b.Fatalf("EnsureMetaIndexes: %v", err)
					}
				}
				b.StartTimer()
				for f := 0; f < 200; f++ {
					ps := make([]NewPassage, 20)
					for c := range ps {
						ps[c] = NewPassage{
							Text: fmt.Sprintf("func F%d_%d() { /* %s */ }", f, c, strings.Repeat("body ", 60)),
							Meta: fmt.Sprintf(`{"end_line":%d,"ext":".go","file":"pkg/dir%d/file%d.go","fqn":"example.com/mod/pkg/dir%d.F%d_%d","hash":"%064d","is_test":%v,"is_vendor":false,"kind":"code","lang":"go","language":"go","name":"F%d_%d","node_type":"function","signature":"func F%d_%d(ctx context.Context) error","start_line":%d,"total_chunks":20}`,
								c*10+9, f%10, f, f%10, f, c, f*100+c, f%4 == 0, f, c, f, c, c*10),
						}
					}
					if _, err := s.ApplySource(context.Background(), SourceDelta{Source: fmt.Sprintf("pkg/dir%d/file%d.go", f%10, f), Passages: ps}); err != nil {
						b.Fatalf("ApplySource: %v", err)
					}
				}
				b.StopTimer()
				s.Close()
			}
		})
	}
}

// TestMetaIndexOddValues: a value SQLite and Go decode differently has the
// type "odd" in the filter index: a number outside float64 and a key that
// occurs twice (also spelled with an escape). A clean value keeps its JSON
// type, including a string that looks like the overflow marker.
func TestMetaIndexOddValues(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	bs := string(rune(92))
	ids := applyMetas(t, s, "a.go",
		`{"v":1e400}`,  // 0 odd
		`{"v":-1e400}`, // 1 odd
		`{"v":1`+strings.Repeat("0", 400)+`}`, // 2 odd (integer text, +Inf as a real)
		`{"v":"a","v":"b"}`,                   // 3 odd
		`{"v":"a","`+bs+`u0076":"b"}`,         // 4 odd
		`{"v":"9e999"}`,                       // 5 text
		`{"v":1e-400}`,                        // 6 real
		`{"v":12345678901234567890123456789012345678901234567890}`, // 7 integer
		`{"v":"a","w":"v"}`, // 8 text
		`{"w":"a","w":"b"}`, // 9 odd for w only
		`{"v":{"v":1}}`,     // 10 object
	)
	if err := s.EnsureMetaIndexes(ctx, []string{"v", "w"}); err != nil {
		t.Fatalf("EnsureMetaIndexes: %v", err)
	}
	typeOf := func(key, typ string) []int64 {
		ms, err := s.SelectByMetaIndex(ctx, []MetaBranch{{Kind: MetaByKey, Key: key, Type: typ}}, 0)
		if err != nil {
			t.Fatalf("SelectByMetaIndex(%s %s): %v", key, typ, err)
		}
		var out []int64
		for _, m := range ms {
			out = append(out, m.ID)
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	for _, tc := range []struct {
		key, typ string
		want     []int64
	}{
		{"v", MetaOddType, []int64{ids[0], ids[1], ids[2], ids[3], ids[4]}},
		{"v", "text", []int64{ids[5], ids[8]}},
		{"v", "real", []int64{ids[6]}},
		{"v", "integer", []int64{ids[7]}},
		{"v", "object", []int64{ids[10]}},
		{"w", MetaOddType, []int64{ids[9]}},
		{"w", "text", []int64{ids[8]}},
	} {
		if got := typeOf(tc.key, tc.typ); !sameIDs(got, tc.want) {
			t.Errorf("%s of type %s = %v, want %v", tc.key, tc.typ, got, tc.want)
		}
	}
}
