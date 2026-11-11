package gleann

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// explainTestEmbedder is a fake EmbeddingComputer that embeds texts as bag-of-keywords
// vectors over a fixed vocabulary.
type explainTestEmbedder struct {
	vocab        []string
	err          error
	returnWrongN int
}

var _ EmbeddingComputer = (*explainTestEmbedder)(nil)

func explainTestNewEmbedder() *explainTestEmbedder {
	return &explainTestEmbedder{
		vocab: []string{"webhook", "ssrf", "network", "banana", "apple"},
	}
}

func (e *explainTestEmbedder) embedText(text string) []float32 {
	lower := strings.ToLower(text)
	vec := make([]float32, len(e.vocab))
	for i, word := range e.vocab {
		vec[i] = float32(strings.Count(lower, word))
	}
	return vec
}

func (e *explainTestEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	if e.returnWrongN != 0 {
		return make([][]float32, e.returnWrongN), nil
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.embedText(t)
	}
	return out, nil
}

func (e *explainTestEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	return e.embedText(text), nil
}

func (e *explainTestEmbedder) Dimensions() int {
	return len(e.vocab)
}

func (e *explainTestEmbedder) ModelName() string {
	return "explain-test-embedder"
}

// 1. splitSegments tests

func TestSplitSegments_MaxAndDefaults(t *testing.T) {
	lines := make([]string, 25)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d content", i+1)
	}
	text := strings.Join(lines, "\n")

	// Never returns more than max segments
	for _, max := range []int{1, 3, 5, 8, 12} {
		segs := splitSegments(text, max)
		if len(segs) > max {
			t.Errorf("splitSegments(text, %d) returned %d segments, want <= %d", max, len(segs), max)
		}
	}

	// Default max (<= 0) is 8
	for _, max := range []int{0, -1, -100} {
		segs := splitSegments(text, max)
		if len(segs) != 8 {
			t.Errorf("splitSegments(text, %d) returned %d segments, want default 8", max, len(segs))
		}
	}
}

func TestSplitSegments_ContiguousAndCoverage(t *testing.T) {
	testCases := []struct {
		name string
		text string
		max  int
	}{
		{
			name: "uniform lines",
			text: "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10",
			max:  4,
		},
		{
			name: "interspersed blank lines",
			text: "line 1\n\nline 3\nline 4\n\n\nline 7\nline 8\n\nline 10",
			max:  5,
		},
		{
			name: "leading and trailing blank lines",
			text: "\n\nline 3\nline 4\nline 5\n\n",
			max:  3,
		},
		{
			name: "single line",
			text: "single line without newline",
			max:  8,
		},
		{
			name: "two lines with max 1",
			text: "first line\nsecond line",
			max:  1,
		},
		{
			name: "many lines default max",
			text: strings.Repeat("content line\n", 50) + "final line",
			max:  0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			segs := splitSegments(tc.text, tc.max)
			if len(segs) == 0 {
				t.Fatalf("expected segments, got none")
			}

			lines := strings.Split(tc.text, "\n")
			totalLines := len(lines)

			// First segment starts at 1
			if segs[0].StartLine != 1 {
				t.Errorf("first segment StartLine = %d, want 1", segs[0].StartLine)
			}

			// Last segment ends at line count
			if segs[len(segs)-1].EndLine != totalLines {
				t.Errorf("last segment EndLine = %d, want %d", segs[len(segs)-1].EndLine, totalLines)
			}

			// Check StartLine <= EndLine and contiguous coverage
			for i, seg := range segs {
				if seg.StartLine < 1 || seg.EndLine > totalLines || seg.StartLine > seg.EndLine {
					t.Errorf("segment %d invalid range [%d, %d] for totalLines=%d", i, seg.StartLine, seg.EndLine, totalLines)
				}
				if i > 0 {
					if seg.StartLine != segs[i-1].EndLine+1 {
						t.Errorf("gap between segment %d (EndLine=%d) and segment %d (StartLine=%d)",
							i-1, segs[i-1].EndLine, i, seg.StartLine)
					}
				}
			}

			// Concatenating seg.Text with "\n" reproduces the original text exactly
			segTexts := make([]string, len(segs))
			for i, s := range segs {
				segTexts[i] = s.Text
			}
			reproduced := strings.Join(segTexts, "\n")
			if reproduced != tc.text {
				t.Errorf("concatenating segments with \\n did not reproduce original text:\ngot:  %q\nwant: %q", reproduced, tc.text)
			}
		})
	}
}

func TestSplitSegments_FewerNonBlankAndBlanks(t *testing.T) {
	// Fewer non-blank lines than max gives one segment per non-blank line group
	text := "first line\n\nsecond line\n\n\nthird line"
	segs := splitSegments(text, 8)
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments for 3 non-blank lines with max 8, got %d", len(segs))
	}

	segTexts := make([]string, len(segs))
	for i, s := range segs {
		segTexts[i] = s.Text
	}
	if strings.Join(segTexts, "\n") != text {
		t.Errorf("segments do not reproduce text")
	}

	// Blank-only text returns nil
	blankCases := []string{
		"",
		"   ",
		"\t\t",
		"\n\n\n",
		"  \n\t\n   \n",
	}
	for _, b := range blankCases {
		if res := splitSegments(b, 8); res != nil {
			t.Errorf("splitSegments(%q, 8) = %v, want nil", b, res)
		}
	}

	// No segment consists only of whitespace
	mixed := "\n\nalpha\n\n\n\nbeta\n\n\ngamma\n\n"
	for _, s := range splitSegments(mixed, 8) {
		if strings.TrimSpace(s.Text) == "" {
			t.Errorf("segment [%d, %d] consists only of whitespace: %q", s.StartLine, s.EndLine, s.Text)
		}
	}
}

// 2. maskSegment tests

func TestMaskSegment(t *testing.T) {
	text := "line 1\nline 2\nline 3\nline 4\nline 5"

	tests := []struct {
		name  string
		start int
		end   int
		want  string
	}{
		{
			name:  "mask first line",
			start: 1,
			end:   1,
			want:  "line 2\nline 3\nline 4\nline 5",
		},
		{
			name:  "mask middle range",
			start: 2,
			end:   4,
			want:  "line 1\nline 5",
		},
		{
			name:  "mask last line",
			start: 5,
			end:   5,
			want:  "line 1\nline 2\nline 3\nline 4",
		},
		{
			name:  "mask entire document",
			start: 1,
			end:   5,
			want:  "",
		},
		{
			name:  "mask single line in middle",
			start: 3,
			end:   3,
			want:  "line 1\nline 2\nline 4\nline 5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskSegment(text, tt.start, tt.end)
			if got != tt.want {
				t.Errorf("maskSegment(%d, %d):\ngot:  %q\nwant: %q", tt.start, tt.end, got, tt.want)
			}
		})
	}
}

// 3. ExplainResult tests

func TestExplainResult_TopSegmentAndRanking(t *testing.T) {
	ctx := context.Background()

	// 8 lines: lines 1, 2, 3 have "banana apple", line 4 (segment 3) has "webhook ssrf",
	// lines 5, 6, 7, 8 have "banana apple".
	passageLines := []string{
		"banana apple 1",
		"banana apple 2",
		"banana apple 3",
		"webhook ssrf target",
		"banana apple 5",
		"banana apple 6",
		"banana apple 7",
		"banana apple 8",
	}
	passage := strings.Join(passageLines, "\n")

	embedder := explainTestNewEmbedder()
	searcher := &LeannSearcher{
		embedder: embedder,
	}

	result := SearchResult{
		ID:   42,
		Text: passage,
	}

	exp, err := searcher.ExplainResult(ctx, "webhook ssrf", result, ExplainOptions{MaxSegments: 8})
	if err != nil {
		t.Fatalf("ExplainResult failed: %v", err)
	}

	if len(exp.Segments) != 8 {
		t.Fatalf("expected 8 segments, got %d", len(exp.Segments))
	}

	// The top segment (Segments[0]) must be the one with "webhook ssrf" and have Delta > 0.
	top := exp.Segments[0]
	if !strings.Contains(top.Text, "webhook ssrf") {
		t.Errorf("expected top segment to contain 'webhook ssrf', got %q", top.Text)
	}
	if top.Delta <= 0 {
		t.Errorf("expected top segment Delta > 0, got %f", top.Delta)
	}
	if top.StartLine != 4 || top.EndLine != 4 {
		t.Errorf("expected top segment lines [4, 4], got [%d, %d]", top.StartLine, top.EndLine)
	}

	// Segments must be sorted by Delta descending.
	for i := 0; i < len(exp.Segments)-1; i++ {
		if exp.Segments[i].Delta < exp.Segments[i+1].Delta {
			t.Errorf("segments not sorted descending: segment %d Delta=%f < segment %d Delta=%f",
				i, exp.Segments[i].Delta, i+1, exp.Segments[i+1].Delta)
		}
	}
}

func TestExplainResult_EmbedCalls(t *testing.T) {
	ctx := context.Background()
	embedder := explainTestNewEmbedder()
	searcher := &LeannSearcher{
		embedder: embedder,
	}

	// Multi-segment passage: EmbedCalls == len(segments) + 2
	multiLinePassage := "line 1 banana\nline 2 apple\nline 3 network\nline 4 webhook"
	resMulti := SearchResult{ID: 1, Text: multiLinePassage}
	expMulti, err := searcher.ExplainResult(ctx, "webhook", resMulti, ExplainOptions{MaxSegments: 4})
	if err != nil {
		t.Fatalf("ExplainResult failed: %v", err)
	}
	expectedCalls := len(expMulti.Segments) + 2
	if expMulti.EmbedCalls != expectedCalls {
		t.Errorf("multi-segment EmbedCalls = %d, want len(segments)+2 = %d", expMulti.EmbedCalls, expectedCalls)
	}

	// One-line passage: EmbedCalls == 2, Delta == Similarity
	oneLinePassage := "webhook ssrf network"
	resOne := SearchResult{ID: 2, Text: oneLinePassage}
	expOne, err := searcher.ExplainResult(ctx, "webhook ssrf", resOne, ExplainOptions{})
	if err != nil {
		t.Fatalf("ExplainResult failed on one-line passage: %v", err)
	}
	if len(expOne.Segments) != 1 {
		t.Fatalf("expected 1 segment for one-line passage, got %d", len(expOne.Segments))
	}
	if expOne.EmbedCalls != 2 {
		t.Errorf("one-line passage EmbedCalls = %d, want 2", expOne.EmbedCalls)
	}
	if expOne.Segments[0].Delta != expOne.Similarity {
		t.Errorf("one-line passage Delta (%f) != Similarity (%f)", expOne.Segments[0].Delta, expOne.Similarity)
	}
	if expOne.Similarity <= 0 {
		t.Errorf("expected positive similarity for matching query, got %f", expOne.Similarity)
	}
}

func TestExplainResult_Errors(t *testing.T) {
	ctx := context.Background()
	result := SearchResult{ID: 1, Text: "sample webhook text\nsecond line"}

	// 1. Nil embedder produces an error
	sNil := &LeannSearcher{embedder: nil}
	if _, err := sNil.ExplainResult(ctx, "query", result, ExplainOptions{}); err == nil {
		t.Error("expected error for nil embedder, got nil")
	}

	// 2. Embedder returning an error produces an error
	errEmbedder := explainTestNewEmbedder()
	errEmbedder.err = errors.New("simulated embedding error")
	sErr := &LeannSearcher{embedder: errEmbedder}
	if _, err := sErr.ExplainResult(ctx, "query", result, ExplainOptions{}); err == nil {
		t.Error("expected error when embedder returns error, got nil")
	}

	// 3. Embedder returning wrong number of vectors produces an error
	wrongCountEmbedder := explainTestNewEmbedder()
	wrongCountEmbedder.returnWrongN = 1 // texts length is at least 3
	sWrong := &LeannSearcher{embedder: wrongCountEmbedder}
	if _, err := sWrong.ExplainResult(ctx, "query", result, ExplainOptions{}); err == nil {
		t.Error("expected error when embedder returns wrong number of vectors, got nil")
	}
}

func TestExplainResult_ScorerBM25AndNil(t *testing.T) {
	ctx := context.Background()
	passageID := int64(1001)
	passageText := "webhook ssrf network banana apple"
	result := SearchResult{ID: passageID, Text: passageText}

	// When s.scorer is a *BM25Adapter with the passage indexed:
	adapter := NewBM25Adapter()
	adapter.AddDocument(Passage{ID: passageID, Text: passageText})

	searcher := &LeannSearcher{
		embedder: explainTestNewEmbedder(),
		scorer:   adapter,
	}

	exp, err := searcher.ExplainResult(ctx, "webhook ssrf absentterm", result, ExplainOptions{})
	if err != nil {
		t.Fatalf("ExplainResult failed: %v", err)
	}
	if len(exp.Terms) == 0 {
		t.Fatalf("expected non-empty Terms when BM25 scorer is configured")
	}
	// Matching term comes first with TF > 0
	firstTerm := exp.Terms[0]
	if firstTerm.TF <= 0 {
		t.Errorf("expected first term TF > 0, got TF=%d for term %q", firstTerm.TF, firstTerm.Term)
	}
	if firstTerm.Score <= 0 {
		t.Errorf("expected first term Score > 0, got %f for term %q", firstTerm.Score, firstTerm.Term)
	}
	if firstTerm.Term != "webhook" && firstTerm.Term != "ssrf" {
		t.Errorf("expected first term to be 'webhook' or 'ssrf', got %q", firstTerm.Term)
	}

	// When s.scorer is nil: Terms is empty and no panic
	searcherNilScorer := &LeannSearcher{
		embedder: explainTestNewEmbedder(),
		scorer:   nil,
	}
	expNil, err := searcherNilScorer.ExplainResult(ctx, "webhook ssrf", result, ExplainOptions{})
	if err != nil {
		t.Fatalf("ExplainResult failed with nil scorer: %v", err)
	}
	if len(expNil.Terms) != 0 {
		t.Errorf("expected empty Terms with nil scorer, got %v", expNil.Terms)
	}
}

// 4. Explanation.Snippet tests

func TestExplanation_Snippet(t *testing.T) {
	// k <= 0 or nil explanation returns ""
	var nilExp *Explanation
	if got := nilExp.Snippet(3); got != "" {
		t.Errorf("nil explanation Snippet(3) = %q, want \"\"", got)
	}

	emptyExp := &Explanation{}
	if got := emptyExp.Snippet(3); got != "" {
		t.Errorf("empty explanation Snippet(3) = %q, want \"\"", got)
	}

	sampleExp := &Explanation{
		Segments: []SegmentImportance{
			{StartLine: 1, EndLine: 2, Text: "line 1\nline 2", Delta: 0.9},
			{StartLine: 3, EndLine: 4, Text: "line 3\nline 4", Delta: 0.8},
			{StartLine: 7, EndLine: 8, Text: "line 7\nline 8", Delta: 0.5},
		},
	}
	if got := sampleExp.Snippet(0); got != "" {
		t.Errorf("Snippet(0) = %q, want \"\"", got)
	}
	if got := sampleExp.Snippet(-2); got != "" {
		t.Errorf("Snippet(-2) = %q, want \"\"", got)
	}

	// Adjacent segments: plain "\n"
	adjExp := &Explanation{
		Segments: []SegmentImportance{
			{StartLine: 1, EndLine: 2, Text: "line 1\nline 2", Delta: 0.9},
			{StartLine: 3, EndLine: 4, Text: "line 3\nline 4", Delta: 0.8},
		},
	}
	wantAdj := "line 1\nline 2\nline 3\nline 4"
	if got := adjExp.Snippet(2); got != wantAdj {
		t.Errorf("Snippet(2) adjacent mismatch:\ngot:  %q\nwant: %q", got, wantAdj)
	}

	// Non-adjacent segments: "\n…\n"
	// Segments ordered by Delta descending: first is lines 7-8, second is lines 1-2.
	// Snippet must return them in passage (line) order: lines 1-2 then lines 7-8.
	nonAdjExp := &Explanation{
		Segments: []SegmentImportance{
			{StartLine: 7, EndLine: 8, Text: "line 7\nline 8", Delta: 0.9},
			{StartLine: 1, EndLine: 2, Text: "line 1\nline 2", Delta: 0.8},
		},
	}
	wantNonAdj := "line 1\nline 2\n…\nline 7\nline 8"
	if got := nonAdjExp.Snippet(2); got != wantNonAdj {
		t.Errorf("Snippet(2) non-adjacent mismatch:\ngot:  %q\nwant: %q", got, wantNonAdj)
	}

	// Mixed adjacent and non-adjacent segments
	mixedExp := &Explanation{
		Segments: []SegmentImportance{
			{StartLine: 10, EndLine: 11, Text: "line 10\nline 11", Delta: 0.95},
			{StartLine: 1, EndLine: 2, Text: "line 1\nline 2", Delta: 0.85},
			{StartLine: 3, EndLine: 4, Text: "line 3\nline 4", Delta: 0.75},
			{StartLine: 50, EndLine: 51, Text: "line 50\nline 51", Delta: 0.10},
		},
	}
	// top-3 picks (10-11), (1-2), (3-4).
	// Sorted by line order: (1-2), (3-4) [adjacent -> \n], (10-11) [non-adjacent -> \n…\n].
	wantMixed := "line 1\nline 2\nline 3\nline 4\n…\nline 10\nline 11"
	if got := mixedExp.Snippet(3); got != wantMixed {
		t.Errorf("Snippet(3) mixed mismatch:\ngot:  %q\nwant: %q", got, wantMixed)
	}

	// k > len(Segments) returns all segments formatted
	if got := adjExp.Snippet(10); got != wantAdj {
		t.Errorf("Snippet(10) with 2 segments mismatch:\ngot:  %q\nwant: %q", got, wantAdj)
	}
}

// 5. BenchmarkSplitSegments on a 200-line passage

func BenchmarkSplitSegments(b *testing.B) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %03d: func handleRequest(ctx context.Context, id int64) error { return nil }", i+1)
	}
	passage := strings.Join(lines, "\n")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = splitSegments(passage, 8)
	}
}
