package store

import (
	"context"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/analysis"
)

// lexQuery builds a base-language query for the tests.
func lexQuery(text string) LexQuery {
	return LexQuery{Text: text, Analyzer: analysis.New("")}
}

func TestLexicalSearchRanking(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Equal-length documents so term frequency, not length normalisation,
	// decides the order: p1 has apple three times, p2 once, p3 not at all.
	ids := applyTexts(t, s, "a.txt", []string{
		"apple apple apple zebra",
		"apple zebra zebra zebra",
		"cherry cherry cherry cherry",
	})

	hits, err := s.Search(ctx, lexQuery("apple"), 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2 (the two apple passages)", len(hits))
	}
	if hits[0].ID != ids[0] || hits[1].ID != ids[1] {
		t.Fatalf("ranking = %v, want [%d %d] (higher term frequency first)", hits, ids[0], ids[1])
	}
	// Higher is better: the negated bm25 must descend, and be positive for a
	// matching term in a small corpus.
	if hits[0].Score <= hits[1].Score {
		t.Errorf("scores not descending: %v", hits)
	}
	if hits[0].Score <= 0 {
		t.Errorf("best score = %v, want > 0 (score is -bm25)", hits[0].Score)
	}

	// k caps the result list.
	one, err := s.Search(ctx, lexQuery("apple"), 1)
	if err != nil || len(one) != 1 || one[0].ID != ids[0] {
		t.Fatalf("Search k=1 = %v, %v; want just the best hit", one, err)
	}

	stats, err := s.Stats(ctx)
	if err != nil || stats.Rows != 3 {
		t.Fatalf("Stats = %+v, %v; want 3 rows", stats, err)
	}
}

func TestLexicalEmptyAndStopWordQuery(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applyTexts(t, s, "a.txt", []string{"apple banana"})

	// A query that tokenises to nothing must return no rows and no error,
	// without running SQL (FTS5 rejects MATCH ''). RFC 003 section 4.2 step 1.
	for _, q := range []string{"", "   ", "the and of", "a is on", "!!! ??? ..."} {
		hits, err := s.Search(ctx, lexQuery(q), 10)
		if err != nil {
			t.Errorf("Search(%q): unexpected error %v", q, err)
		}
		if len(hits) != 0 {
			t.Errorf("Search(%q) = %v, want no rows", q, hits)
		}
		scores, err := s.ScoreIDs(ctx, lexQuery(q), []int64{1, 2})
		if err != nil || len(scores) != 0 {
			t.Errorf("ScoreIDs(%q) = %v, %v; want empty map, no error", q, scores, err)
		}
	}
}

func TestLexicalQuoteInQuery(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	applyTexts(t, s, "a.txt", []string{"apple banana", "cherry date"})

	// A double quote in the query text must not break the MATCH expression.
	// The analyzer splits on it, and MatchExpr doubles any embedded quote.
	hits, err := s.Search(ctx, lexQuery(`apple"banana`), 10)
	if err != nil {
		t.Fatalf("Search with quote: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1 (the apple/banana passage)", len(hits))
	}

	// The store must not depend on the analyzer never emitting a quote:
	// MatchExpr escapes an embedded quote by doubling it (FTS5 string syntax).
	if got := analysis.MatchExpr([][]string{{`a"b`}}); got != `"a""b"` {
		t.Errorf("MatchExpr quote escaping = %q, want %q", got, `"a""b"`)
	}
}

func TestLexicalRebuildAfterLanguageChange(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	base := analysis.New("")

	// A Turkish prose passage. ApplySource stores the text and kind; Rebuild
	// recomputes the tokens from them.
	if _, err := s.ApplySource(ctx, SourceDelta{
		Source: "doc.md",
		Passages: []NewPassage{{
			Text:   "ışıkları",
			Kind:   "prose",
			Tokens: strings.Join(base.Tokens("ışıkları", analysis.KindProse), " "),
		}},
	}); err != nil {
		t.Fatalf("ApplySource: %v", err)
	}

	// Base language: the ASCII query does not match the Turkish tokens.
	if err := s.Rebuild(ctx, base); err != nil {
		t.Fatalf("Rebuild(base): %v", err)
	}
	if hits, err := s.Search(ctx, lexQuery("isiklari"), 10); err != nil || len(hits) != 0 {
		t.Fatalf("base Search(isiklari) = %v, %v; want no match", hits, err)
	}
	if lang, err := s.Meta(ctx, "language"); err != nil || lang != "" {
		t.Fatalf("meta language = %q, %v; want empty", lang, err)
	}

	// Switch to Turkish: prose folding adds the ASCII variant, so the ASCII
	// query now matches. Rebuild needs no embedding, only the stored text.
	tr := analysis.New("tr")
	if err := s.Rebuild(ctx, tr); err != nil {
		t.Fatalf("Rebuild(tr): %v", err)
	}
	hits, err := s.Search(ctx, LexQuery{Text: "isiklari", Analyzer: tr}, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("tr Search(isiklari) = %v, %v; want one match", hits, err)
	}
	if an, err := s.Meta(ctx, "analyzer"); err != nil || an != analysis.ID {
		t.Fatalf("meta analyzer = %q, %v; want %q", an, err, analysis.ID)
	}
	if lang, err := s.Meta(ctx, "language"); err != nil || lang != "tr" {
		t.Fatalf("meta language = %q, %v; want tr", lang, err)
	}
}

func TestLexicalScoreIDs(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	ids := applyTexts(t, s, "a.txt", []string{
		"apple apple apple zebra",
		"apple zebra zebra zebra",
		"cherry cherry cherry cherry",
	})

	scores, err := s.ScoreIDs(ctx, lexQuery("apple"), ids)
	if err != nil {
		t.Fatalf("ScoreIDs: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("got %d scores, want 2 (the cherry passage does not match)", len(scores))
	}
	if _, ok := scores[ids[2]]; ok {
		t.Errorf("non-matching passage %d got a score", ids[2])
	}
	if scores[ids[0]] <= scores[ids[1]] {
		t.Errorf("scores not ordered by relevance: %v", scores)
	}

	// An empty ID list returns an empty map without SQL.
	if empty, err := s.ScoreIDs(ctx, lexQuery("apple"), nil); err != nil || len(empty) != 0 {
		t.Fatalf("ScoreIDs(nil) = %v, %v; want empty", empty, err)
	}
}

func TestLexicalScoreIDsChunked(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// More than maxInParams (500) passages so the IN list is split into
	// several queries; every one must still be scored.
	const n = 550
	texts := make([]string, n)
	for i := range texts {
		texts[i] = "apple zebra"
	}
	ids := applyTexts(t, s, "big.txt", texts)

	scores, err := s.ScoreIDs(ctx, lexQuery("apple"), ids)
	if err != nil {
		t.Fatalf("ScoreIDs: %v", err)
	}
	if len(scores) != n {
		t.Fatalf("got %d scores, want %d (chunked IN list lost rows)", len(scores), n)
	}
}

func TestApplySourceExplicitID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// The sidecar/migration path: id = old passage ID, vec_slot = ID.
	id100, id200 := int64(100), int64(200)
	ids, err := s.ApplySource(ctx, SourceDelta{
		Source: "a.go",
		Passages: []NewPassage{
			{ID: &id100, VecSlot: &id100, Text: "alpha", Tokens: "alpha"},
			{ID: &id200, VecSlot: &id200, Text: "beta", Tokens: "beta"},
		},
	})
	if err != nil {
		t.Fatalf("ApplySource explicit ids: %v", err)
	}
	if len(ids) != 2 || ids[0] != 100 || ids[1] != 200 {
		t.Fatalf("ids = %v, want [100 200]", ids)
	}
	p, err := s.Get(ctx, 100)
	if err != nil || p.Text != "alpha" || p.VecSlot == nil || *p.VecSlot != 100 {
		t.Fatalf("Get(100) = %+v, %v; want alpha at slot 100", p, err)
	}
	// The lex rowid equals the explicit ID, so Search returns IDs the
	// format-1 searcher understands.
	hits, err := s.Search(ctx, lexQuery("beta"), 10)
	if err != nil || len(hits) != 1 || hits[0].ID != 200 {
		t.Fatalf("Search(beta) = %v, %v; want id 200", hits, err)
	}

	// AUTOINCREMENT semantics are kept for nil-ID passages: the next ID
	// continues after the highest explicit one.
	auto, err := s.ApplySource(ctx, SourceDelta{
		Source:   "b.go",
		Passages: []NewPassage{{Text: "gamma", Tokens: "gamma"}},
	})
	if err != nil || len(auto) != 1 || auto[0] != 201 {
		t.Fatalf("auto id = %v, %v; want [201]", auto, err)
	}
}

func TestLexicalSearchANDFirst(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	ids := applyTexts(t, s, "a.txt", []string{
		"apple banana cherry",
		"apple zebra",
		"banana zebra",
		"date fig",
	})

	// Case 1: AND returns enough hits (k=1)
	q1 := LexQuery{
		Text:     "apple banana",
		Analyzer: analysis.New(""),
		Options:  LexQueryOptions{ANDFirst: true},
	}
	hits1, err := s.Search(ctx, q1, 1)
	if err != nil {
		t.Fatalf("Search k=1: %v", err)
	}
	if len(hits1) != 1 || hits1[0].ID != ids[0] {
		t.Fatalf("Search k=1 = %v, want [%d] (only the passage matching both terms)", hits1, ids[0])
	}

	// Case 2: AND returns 1 hit, k=3 -> fills up with OR hits (deduped, AND hit first)
	hits3, err := s.Search(ctx, q1, 3)
	if err != nil {
		t.Fatalf("Search k=3: %v", err)
	}
	if len(hits3) != 3 {
		t.Fatalf("Search k=3 got %d hits, want 3", len(hits3))
	}
	if hits3[0].ID != ids[0] {
		t.Fatalf("first hit = %d, want %d (AND hit must stay first)", hits3[0].ID, ids[0])
	}
	// The other two hits must be ids[1] and ids[2] (in some order based on bm25), and ids[0] not repeated.
	remaining := map[int64]bool{hits3[1].ID: true, hits3[2].ID: true}
	if !remaining[ids[1]] || !remaining[ids[2]] {
		t.Fatalf("remaining hits = %v, want %d and %d", remaining, ids[1], ids[2])
	}

	// Case 3: Zero results when query terms don't match anything
	qNone := LexQuery{
		Text:     "nonexistent1 nonexistent2",
		Analyzer: analysis.New(""),
		Options:  LexQueryOptions{ANDFirst: true},
	}
	hitsNone, err := s.Search(ctx, qNone, 10)
	if err != nil || len(hitsNone) != 0 {
		t.Fatalf("Search nonexistent = %v, %v; want 0 hits", hitsNone, err)
	}
}

// TestLexicalSearchANDFirstScoresKeepOrder: the OR fill must score below the
// AND hits even when its own bm25 is higher, so a caller that re-sorts by
// score (format-2 SearchBM25) keeps the AND-first order.
func TestLexicalSearchANDFirstScoresKeepOrder(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	ids := applyTexts(t, s, "a.txt", []string{
		// Matches both terms but is long, so its bm25 is weak.
		"apple banana one two three four five six seven eight nine ten eleven twelve thirteen fourteen",
		// Matches one term only, short and repeated: a strong OR score.
		"banana banana banana",
		// Filler so both terms have a positive IDF.
		"cherry date", "elder fig", "grape kiwi", "lemon mango", "nectar olive", "peach pear",
		"apple quince rose sage thyme ulmo vine wheat yarrow zinnia acorn birch cedar dill",
	})
	q := LexQuery{Text: "apple banana", Analyzer: analysis.New(""), Options: LexQueryOptions{ANDFirst: true}}
	hits, err := s.Search(ctx, q, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 || hits[0].ID != ids[0] || hits[1].ID != ids[1] {
		t.Fatalf("hits = %v, want [%d %d]", hits, ids[0], ids[1])
	}
	if hits[1].Score >= hits[0].Score {
		t.Fatalf("OR fill score %v >= AND hit score %v; re-sorting by score would put the OR hit first", hits[1].Score, hits[0].Score)
	}
}

func TestLexicalSearchDocFreqCut(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// 10 passages:
	// "common": 8 passages (df = 0.8)
	// "rare": 2 passages (df = 0.2)
	// "superrare": 1 passage (df = 0.1)
	texts := []string{
		"common rare superrare", // 0
		"common rare",           // 1
		"common alpha",          // 2
		"common beta",           // 3
		"common gamma",          // 4
		"common delta",          // 5
		"common epsilon",        // 6
		"common zeta",           // 7
		"eta theta",             // 8
		"iota kappa",            // 9
	}
	ids := applyTexts(t, s, "df.txt", texts)

	// Cut at 0.5 drops "common" (0.8 > 0.5), keeps "rare" (0.2 <= 0.5).
	// So "common rare" matches only passages with "rare" (ids[0] and ids[1]).
	qCut := LexQuery{
		Text:     "common rare",
		Analyzer: analysis.New(""),
		Options:  LexQueryOptions{DocFreqCut: 0.5},
	}
	hitsCut, err := s.Search(ctx, qCut, 10)
	if err != nil {
		t.Fatalf("Search with cut 0.5: %v", err)
	}
	if len(hitsCut) != 2 {
		t.Fatalf("hits with cut 0.5 = %d, want 2 (only the rare passages)", len(hitsCut))
	}
	if (hitsCut[0].ID != ids[0] && hitsCut[0].ID != ids[1]) || (hitsCut[1].ID != ids[0] && hitsCut[1].ID != ids[1]) {
		t.Fatalf("hits = %v, want [%d %d]", hitsCut, ids[0], ids[1])
	}

	// Cut at 0.15: both "common" (0.8) and "rare" (0.2) exceed 0.15.
	// Must not drop all terms: keeps the rarest ("rare").
	qAllExceed := LexQuery{
		Text:     "common rare",
		Analyzer: analysis.New(""),
		Options:  LexQueryOptions{DocFreqCut: 0.15},
	}
	hitsAllExceed, err := s.Search(ctx, qAllExceed, 10)
	if err != nil {
		t.Fatalf("Search with cut 0.15: %v", err)
	}
	if len(hitsAllExceed) != 2 {
		t.Fatalf("hits with cut 0.15 = %d, want 2 (kept rarest term 'rare')", len(hitsAllExceed))
	}

	// Cut with ANDFirst combined:
	// "common" is cut, so only "rare" is searched.
	qCombined := LexQuery{
		Text:     "common rare",
		Analyzer: analysis.New(""),
		Options:  LexQueryOptions{ANDFirst: true, DocFreqCut: 0.5},
	}
	hitsCombined, err := s.Search(ctx, qCombined, 10)
	if err != nil {
		t.Fatalf("Search combined: %v", err)
	}
	if len(hitsCombined) != 2 {
		t.Fatalf("hits combined = %d, want 2", len(hitsCombined))
	}
}
