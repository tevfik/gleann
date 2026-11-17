package gleann

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// This file covers the three WP-2.5 audit defects: SQL injection through the
// filter field name, pool-dependent vector score normalization, and the
// pre-selection lexical fallback skipping the buildBoostedResults tail. Each
// test fails on the pre-fix code.

// linearBackend returns ids 0..min(topK,n)-1 with distance float32(i)*0.01,
// so the first retrieveK=50 candidates have a known max distance
// (float32(49)*0.01) and every score is exactly predictable. It does NOT
// implement VectorIDScorer, like the mmap HNSW searcher.
type linearBackend struct{ n int }

func (linearBackend) Load(ctx context.Context, indexData []byte, meta IndexMeta) error {
	return nil
}

func (b linearBackend) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	k := min(topK, b.n)
	ids := make([]int64, k)
	dists := make([]float32, k)
	for i := range ids {
		ids[i] = int64(i)
		dists[i] = float32(i) * 0.01
	}
	return ids, dists, nil
}

func (b linearBackend) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error) {
	return b.Search(ctx, query, topK)
}

func (linearBackend) Close() error { return nil }

// linearIDScorer is linearBackend plus VectorIDScorer, like the RAM HNSW
// searcher: pre-selection tier 1 can score explicit slots.
type linearIDScorer struct{ linearBackend }

func (b linearIDScorer) VectorDistances(ctx context.Context, query []float32, ids []int64) ([]float32, error) {
	dists := make([]float32, len(ids))
	for i, id := range ids {
		dists[i] = float32(id) * 0.01
	}
	return dists, nil
}

// newLinearSearcher builds a format-2 index through the real builder, then
// swaps in a deterministic fake backend so scores are exactly predictable.
func newLinearSearcher(t *testing.T, items []Item, backend BackendSearcher) *LeannSearcher {
	t.Helper()
	s := newWidenSearcher(t, items)
	old := s.backend
	s.backend = backend
	t.Cleanup(func() { old.Close() })
	return s
}

// TestPreselectFilterIDs_HostileFieldNames proves a field name can never
// change the SQL statement: hostile names (single quotes, an injected
// ') AND 1=0 -- clause, unicode) travel as bound JSON-path parameters, and
// the verified pre-selection set equals the engine's matches exactly. On the
// pre-fix code the quote escaped the path literal: the store errored (ran=false)
// or the injected AND 1=0 returned zero rows, silently losing every match.
func TestPreselectFilterIDs_HostileFieldNames(t *testing.T) {
	fields := []string{
		"x'",
		"x')) AND 1=0 --",
		"x' OR '1'='1",
		"uni'日本語",
		"a'b",
	}
	metas := make([]map[string]any, 0, len(fields)+2)
	for _, f := range fields {
		metas = append(metas, map[string]any{f: "go", "lang": "go"})
	}
	metas = append(metas, map[string]any{"lang": "go"}, map[string]any{"lang": "rust"})
	items := make([]Item, len(metas))
	for i, md := range metas {
		items[i] = Item{Text: fmt.Sprintf("hostile doc %d", i), Metadata: md}
	}
	s := newWidenSearcher(t, items)
	all := s.passages.All()
	ctx := context.Background()

	compilable := 0
	for _, field := range fields {
		for _, op := range []FilterOperator{OpEqual, OpContains, OpExists, OpIn} {
			for _, logic := range []string{"and", "or"} {
				value := any("go")
				switch op {
				case OpExists:
					value = true
				case OpIn:
					value = []any{"go", "rust"}
				}
				filters := []MetadataFilter{
					{Field: field, Operator: op, Value: value},
					{Field: "lang", Operator: OpEqual, Value: "go"},
				}
				engine := NewMetadataFilterEngine(filters)
				engine.Logic = logic
				want := make(map[int64]bool)
				for _, p := range all {
					if engine.Match(p.Metadata) {
						want[p.ID] = true
					}
				}
				if len(want) == 0 {
					t.Fatalf("field=%q op=%s logic=%s: fixture matches nothing; the test cannot detect under-selection", field, op, logic)
				}
				got, ran := s.preselectFilterIDs(ctx, engine)
				if !ran {
					t.Fatalf("field=%q op=%s logic=%s: pre-selection did not run (the field name broke the SQL)", field, op, logic)
				}
				compilable++
				if len(got) != len(want) {
					t.Fatalf("field=%q op=%s logic=%s: preselect %d matches, engine %d", field, op, logic, len(got), len(want))
				}
				for id := range want {
					if _, ok := got[id]; !ok {
						t.Fatalf("field=%q op=%s logic=%s: preselect missed passage %d", field, op, logic, id)
					}
				}
				// The field name must not appear in the SQL text at all.
				where, _, ok := compileFilterSQL(filters, logic)
				if !ok {
					t.Fatalf("field=%q op=%s: preselect ran but compileFilterSQL reports not compilable", field, op)
				}
				if strings.Contains(where, field) {
					t.Fatalf("field=%q op=%s: field name interpolated into the SQL text: %s", field, op, where)
				}
			}
		}
	}
	if compilable == 0 {
		t.Fatal("no hostile field combination was compilable")
	}

	// End to end: a search with a hostile field filter returns exactly the
	// engine's matches.
	filters := []MetadataFilter{{Field: "x')) AND 1=0 --", Operator: OpEqual, Value: "go"}}
	engine := NewMetadataFilterEngine(filters)
	rs, err := s.Search(ctx, "hostile doc", WithTopK(10), WithHybridAlpha(0.5), WithMetadataFilters(filters))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := make(map[int64]bool)
	for _, p := range all {
		if engine.Match(p.Metadata) {
			want[p.ID] = true
		}
	}
	if len(rs) != len(want) {
		t.Fatalf("hostile-filter search returned %d results, engine matches %d", len(rs), len(want))
	}
	for _, r := range rs {
		if !want[r.ID] {
			t.Fatalf("hostile-filter search returned passage %d the engine does not match", r.ID)
		}
	}
}

// TestFilteredSearch_ScoreIndependentOfPool proves a passage's vector score
// does not depend on the widened pool: the normalizer is the max distance
// among the first retrieveK candidates (what the unfiltered search uses),
// computed once. On the pre-fix code the second round normalized over the
// wider pool, so the same passage scored higher filtered than unfiltered and
// MinScore gated differently.
func TestFilteredSearch_ScoreIndependentOfPool(t *testing.T) {
	items := make([]Item, 100)
	for i := range items {
		lang := "rust"
		if i%20 == 0 {
			lang = "go"
		}
		items[i] = Item{
			Text:     "common query term " + strings.Repeat("x", i),
			Metadata: map[string]any{"source": fmt.Sprintf("f%03d.md", i), "lang": lang},
		}
	}
	s := newLinearSearcher(t, items, linearBackend{n: 100})
	ctx := context.Background()
	filters := []MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}}
	// retrieveK is 50 on both searches below (topK*2 unfiltered, max(4*topK,50)
	// filtered), so the fixed normalizer is the distance of candidate 49.
	norm := float32(49) * 0.01
	scoreOf := func(id int64) float32 { return 1.0 - (float32(id)*0.01)/norm }
	find := func(rs []SearchResult, id int64) *SearchResult {
		for i := range rs {
			if rs[i].ID == id {
				return &rs[i]
			}
		}
		return nil
	}

	// Unfiltered with a topK wide enough to include passage 20.
	unf, err := s.Search(ctx, "common query term", WithTopK(25), WithHybridAlpha(1.0))
	if err != nil {
		t.Fatalf("unfiltered Search: %v", err)
	}
	u20 := find(unf, 20)
	if u20 == nil {
		t.Fatalf("unfiltered search did not return passage 20")
	}
	if u20.Score != scoreOf(20) {
		t.Fatalf("unfiltered passage 20 score = %v, want %v", u20.Score, scoreOf(20))
	}

	// Filtered: 3 of the 5 matches sit in the first pool, so the loop widens
	// (50 -> 125) and finishes on the second round. Passage 20 must carry
	// exactly the unfiltered score.
	fil, err := s.Search(ctx, "common query term", WithTopK(5), WithHybridAlpha(1.0), WithMetadataFilters(filters))
	if err != nil {
		t.Fatalf("filtered Search: %v", err)
	}
	if len(fil) != 5 {
		t.Fatalf("filtered search returned %d results, want 5", len(fil))
	}
	f20 := find(fil, 20)
	if f20 == nil {
		t.Fatalf("filtered search did not return passage 20")
	}
	if f20.Score != u20.Score {
		t.Errorf("passage 20 scores %v filtered but %v unfiltered; the score must not depend on the pool size", f20.Score, u20.Score)
	}

	// MinScore gates identically with and without the filter: 0.5 keeps
	// passage 20 on both paths, 0.7 drops it on both.
	for _, ms := range []float32{0.5, 0.7} {
		unf, err := s.Search(ctx, "common query term", WithTopK(25), WithHybridAlpha(1.0), WithMinScore(ms))
		if err != nil {
			t.Fatalf("unfiltered Search (MinScore %v): %v", ms, err)
		}
		fil, err := s.Search(ctx, "common query term", WithTopK(5), WithHybridAlpha(1.0), WithMetadataFilters(filters), WithMinScore(ms))
		if err != nil {
			t.Fatalf("filtered Search (MinScore %v): %v", ms, err)
		}
		u, f := find(unf, 20), find(fil, 20)
		if (u == nil) != (f == nil) {
			t.Errorf("MinScore %v: passage 20 kept=%v unfiltered but kept=%v filtered", ms, u != nil, f != nil)
		}
		if u != nil && f != nil && u.Score != f.Score {
			t.Errorf("MinScore %v: passage 20 scores %v filtered but %v unfiltered", ms, f.Score, u.Score)
		}
	}

	// Deep matches only: under the fixed normalizer they score below 0, and
	// with MinScore unset they must NOT be silently dropped (an unfiltered
	// search of a larger topK would keep them).
	deepItems := make([]Item, 100)
	for i := range deepItems {
		lang := "rust"
		if i >= 60 {
			lang = "go"
		}
		deepItems[i] = Item{
			Text:     "common query term " + strings.Repeat("x", i),
			Metadata: map[string]any{"source": fmt.Sprintf("f%03d.md", i), "lang": lang},
		}
	}
	sd := newLinearSearcher(t, deepItems, linearBackend{n: 100})
	rs, err := sd.Search(ctx, "common query term", WithTopK(5), WithHybridAlpha(1.0), WithMetadataFilters(filters))
	if err != nil {
		t.Fatalf("deep-match Search: %v", err)
	}
	if len(rs) != 5 {
		t.Fatalf("deep-match search returned %d results, want 5 (negative scores must not be dropped when MinScore is unset)", len(rs))
	}
	for i, r := range rs {
		if r.ID != int64(60+i) {
			t.Errorf("deep-match result %d: ID=%d, want %d", i, r.ID, 60+i)
		}
		if want := scoreOf(r.ID); r.Score != want {
			t.Errorf("deep-match passage %d score = %v, want %v (fixed normalizer)", r.ID, r.Score, want)
		}
	}
}

// TestPreselected_VectorScoresUseLoopNormalizer proves pre-selection tier 1
// scores with the loop's fixed normalizer, not the max distance over the
// matched set: the deep matches carry exactly 1 - d/norm even though they sit
// far beyond the first retrieveK candidates. On the pre-fix code tier 1
// normalized over the matched set, giving the same passages different scores.
func TestPreselected_VectorScoresUseLoopNormalizer(t *testing.T) {
	s := newLinearSearcher(t, widenDeepMatchItems(5000, 10), linearIDScorer{linearBackend{n: 5000}})
	filters := []MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}}
	rs, err := s.Search(context.Background(), "common query term",
		WithTopK(10), WithHybridAlpha(1.0), WithMetadataFilters(filters))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rs) != 10 {
		t.Fatalf("deep-match preselected search returned %d results, want 10", len(rs))
	}
	norm := float32(49) * 0.01 // max distance among the first retrieveK=50 candidates
	for _, r := range rs {
		if r.Metadata["lang"] != "go" {
			t.Errorf("result %d has lang=%v, want go", r.ID, r.Metadata["lang"])
		}
		d := float32(r.ID) * 0.01
		if want := 1.0 - d/norm; r.Score != want {
			t.Errorf("preselected passage %d score = %v, want %v (loop normalizer)", r.ID, r.Score, want)
		}
		if r.VectorDistance == nil || *r.VectorDistance != d {
			t.Errorf("preselected passage %d VectorDistance = %v, want %v", r.ID, r.VectorDistance, d)
		}
	}
}

// TestPreselected_LexicalFallbackAppliesBoostedTail proves the tier-2 lexical
// fallback (backend without VectorIDScorer, format 2, deep-match filter)
// applies the same buildBoostedResults tail as every other path: Kind,
// MinScore, the symbol boost and the pid tiebreak. On the pre-fix code it
// built raw results and ignored all of them.
func TestPreselected_LexicalFallbackAppliesBoostedTail(t *testing.T) {
	items := widenDeepMatchItems(5000, 10)
	// One deep match carries the exact symbol name of the query: the +0.5
	// boost must rank it first even though its raw BM25 is not the best.
	items[4995].Metadata["name"] = "common query term"
	s := newLinearSearcher(t, items, linearBackend{n: 5000})
	ctx := context.Background()
	filters := []MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}}

	for _, alpha := range []float32{1.0, 0.5} {
		t.Run(fmt.Sprintf("alpha%.1f", alpha), func(t *testing.T) {
			// Kind gate: every source is a .md file, so Kind=code keeps none.
			rs, err := s.Search(ctx, "common query term",
				WithTopK(10), WithHybridAlpha(alpha), WithMetadataFilters(filters), WithKind("code"))
			if err != nil {
				t.Fatalf("Search Kind=code: %v", err)
			}
			if len(rs) != 0 {
				t.Errorf("Kind=code returned %d results, want 0 (all matches are docs)", len(rs))
			}

			// MinScore gate: an unreachable threshold keeps none.
			rs, err = s.Search(ctx, "common query term",
				WithTopK(10), WithHybridAlpha(alpha), WithMetadataFilters(filters), WithMinScore(1e9))
			if err != nil {
				t.Fatalf("Search MinScore: %v", err)
			}
			if len(rs) != 0 {
				t.Errorf("MinScore=1e9 returned %d results, want 0", len(rs))
			}

			// Symbol boost: the named passage ranks first; Kind=docs keeps all.
			rs, err = s.Search(ctx, "common query term",
				WithTopK(10), WithHybridAlpha(alpha), WithMetadataFilters(filters), WithKind("docs"))
			if err != nil {
				t.Fatalf("Search Kind=docs: %v", err)
			}
			if len(rs) != 10 {
				t.Fatalf("Kind=docs returned %d results, want 10", len(rs))
			}
			if rs[0].ID != 4995 {
				t.Errorf("boosted passage 4995 ranks %d, want first (symbol boost skipped)", rs[0].ID)
			}
			for _, r := range rs {
				if r.Metadata["lang"] != "go" {
					t.Errorf("result %d has lang=%v, want go", r.ID, r.Metadata["lang"])
				}
				if r.BM25Score == nil {
					t.Errorf("result %d lost its raw BM25 score", r.ID)
				}
			}
			// Equal scores order by the stable pid, not by searcher ID.
			for i := 1; i < len(rs); i++ {
				if rs[i-1].Score == rs[i].Score && rs[i-1].pid > rs[i].pid {
					t.Errorf("equal-score results %d,%d ordered by id, not pid (%q > %q)", rs[i-1].ID, rs[i].ID, rs[i-1].pid, rs[i].pid)
				}
			}
		})
	}
}

// TestFilteredSearch_DeepMatchesFallbackNoIDScorer is the reachability case of
// audit finding 3: a backend without VectorIDScorer (mmap HNSW, diskann,
// faiss, or pruned embeddings) must still return k deep matches through the
// lexical tier, on every leg.
func TestFilteredSearch_DeepMatchesFallbackNoIDScorer(t *testing.T) {
	s := newLinearSearcher(t, widenDeepMatchItems(5000, 10), linearBackend{n: 5000})
	ctx := context.Background()
	filters := []MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}}
	for _, alpha := range []float32{1.0, 0.5, 0.0} {
		t.Run(fmt.Sprintf("alpha%.1f", alpha), func(t *testing.T) {
			rs, err := s.Search(ctx, "common query term",
				WithTopK(10), WithHybridAlpha(alpha), WithMetadataFilters(filters))
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(rs) != 10 {
				t.Fatalf("deep-match filtered search returned %d results, want 10", len(rs))
			}
			// Deterministic order: score desc, then pid.
			if !sort.SliceIsSorted(rs, func(i, j int) bool {
				if rs[i].Score != rs[j].Score {
					return rs[i].Score > rs[j].Score
				}
				return rs[i].pid < rs[j].pid
			}) {
				t.Errorf("results not ordered by score desc / pid asc")
			}
		})
	}
}

// Audit (Gemini) finding 1: on a backend without VectorIDScorer the lexical
// pre-selection tier must not replace the vector matches the widen loop found.
// 3 matches rank inside the pool cap, 7 beyond it, and the query shares no
// term with any passage, so the lexical tier scores nothing: the 3 loop
// matches must survive (the pre-fix code returned 0 results).
func TestFilteredSearch_LexicalFallbackKeepsLoopMatches(t *testing.T) {
	const n = 5000
	items := make([]Item, n)
	for i := range items {
		lang := "rust"
		if i == 10 || i == 20 || i == 30 || i >= n-7 {
			lang = "go"
		}
		items[i] = Item{
			Text:     "alpha beta " + strings.Repeat("x", i),
			Metadata: map[string]any{"source": fmt.Sprintf("f%05d.md", i), "lang": lang},
		}
	}
	s := newLinearSearcher(t, items, linearBackend{n: n})
	filters := []MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}}
	for _, alpha := range []float32{1.0, 0.5} {
		rs, err := s.Search(context.Background(), "zzqx unmatched", WithTopK(10), WithHybridAlpha(alpha), WithMetadataFilters(filters))
		if err != nil {
			t.Fatalf("alpha %.1f: Search: %v", alpha, err)
		}
		if len(rs) != 3 {
			t.Fatalf("alpha %.1f: got %d results, want the 3 loop matches", alpha, len(rs))
		}
		for _, r := range rs {
			if r.VectorDistance == nil {
				t.Errorf("alpha %.1f: loop match %d lost its vector distance", alpha, r.ID)
			}
		}
	}
}

// Audit (Gemini) finding 2: in pre-selection tier 1 a hybrid search whose
// query matches no pre-selected passage lexically keeps the vector scores
// (as the main loop does), instead of fusing with an empty lexical ranking,
// which capped every score at alpha.
func TestPreselected_HybridWithoutLexicalMatchKeepsVectorScores(t *testing.T) {
	s := newLinearSearcher(t, widenDeepMatchItems(5000, 10), linearIDScorer{linearBackend{n: 5000}})
	filters := []MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}}
	ctx := context.Background()
	vec, err := s.Search(ctx, "zzqx unmatched", WithTopK(10), WithHybridAlpha(1.0), WithMetadataFilters(filters))
	if err != nil {
		t.Fatalf("vector: %v", err)
	}
	hyb, err := s.Search(ctx, "zzqx unmatched", WithTopK(10), WithHybridAlpha(0.5), WithMetadataFilters(filters))
	if err != nil {
		t.Fatalf("hybrid: %v", err)
	}
	if len(vec) != 10 || len(hyb) != 10 {
		t.Fatalf("got %d vector / %d hybrid results, want 10 each", len(vec), len(hyb))
	}
	for i := range vec {
		if vec[i].ID != hyb[i].ID || vec[i].Score != hyb[i].Score {
			t.Fatalf("rank %d: vector (%d, %.4f) != hybrid (%d, %.4f)", i, vec[i].ID, vec[i].Score, hyb[i].ID, hyb[i].Score)
		}
	}
}

func TestMergeLexicalFill(t *testing.T) {
	loop := []SearchResult{{ID: 1, Score: 0.9}, {ID: 2, Score: 0.6}}
	lex := []SearchResult{{ID: 2, Score: 12}, {ID: 3, Score: 9}, {ID: 4, Score: 7}}
	got := mergeLexicalFill(loop, lex, 4, 0)
	if len(got) != 4 || got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 3 || got[3].ID != 4 {
		t.Fatalf("merge order: %+v", got)
	}
	if !(got[2].Score < 0.6 && got[3].Score < got[2].Score && got[3].Score > 0) {
		t.Errorf("fill scores not strictly below the loop floor: %.3f %.3f", got[2].Score, got[3].Score)
	}
	if got := mergeLexicalFill(loop, nil, 4, 0); len(got) != 2 {
		t.Errorf("empty lexical tier changed the loop result: %+v", got)
	}
	if got := mergeLexicalFill(loop, lex, 4, 0.5); len(got) != 2 {
		t.Errorf("fill below MinScore kept: %+v", got)
	}
}
