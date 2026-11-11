package gleann

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// SegmentImportance is the effect of one text segment (a run of lines) on a
// result's similarity to the query.
type SegmentImportance struct {
	StartLine int    `json:"start_line"` // 1-based, within the passage text
	EndLine   int    `json:"end_line"`   // inclusive
	Text      string `json:"text"`
	// Delta is sim(query, full text) - sim(query, text without this segment).
	// Positive values mean the segment pulls the passage towards the query.
	Delta float32 `json:"delta"`
}

// Explanation describes why a passage matched a query.
type Explanation struct {
	ResultID int64 `json:"result_id"`
	// Similarity is the cosine similarity of the full passage to the query.
	Similarity float32 `json:"similarity"`
	// Segments are sorted by Delta, most important first.
	Segments []SegmentImportance `json:"segments"`
	// Terms is the BM25 contribution of each query term (empty when the
	// searcher has no BM25 scorer that supports term explanations).
	Terms []TermExplanation `json:"terms,omitempty"`
	// EmbedCalls is the number of texts embedded to build this explanation.
	EmbedCalls int `json:"embed_calls"`
}

// TermExplanation is one query term's share of a passage's BM25 score.
type TermExplanation struct {
	Term  string  `json:"term"`
	TF    int     `json:"tf"`
	Score float64 `json:"score"`
}

// TermExplainer is implemented by scorers that can break a document score
// down by query term.
type TermExplainer interface {
	ExplainTerms(query string, id int64) []TermExplanation
}

// ExplainOptions controls Explain.
type ExplainOptions struct {
	// MaxSegments caps how many segments a passage is split into, which is
	// also the number of extra embedding calls (default 8; on the gleann
	// benchmark 8 kept 96% of the quality of 16 at ~70% of the latency).
	MaxSegments int
}

// splitSegments groups the lines of text into at most max contiguous
// segments of near-equal line counts. Blank lines stay attached to the
// segment they fall in.
func splitSegments(text string, max int) []SegmentImportance {
	lines := strings.Split(text, "\n")
	if max <= 0 {
		max = 8
	}
	// Count non-blank lines so a segment is never only whitespace.
	var content []int
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			content = append(content, i)
		}
	}
	if len(content) == 0 {
		return nil
	}
	n := min(max, len(content))
	segs := make([]SegmentImportance, 0, n)
	for k := 0; k < n; k++ {
		lo := content[k*len(content)/n]
		var hi int
		if k == n-1 {
			hi = len(lines) - 1
		} else {
			hi = content[(k+1)*len(content)/n] - 1
		}
		if k == 0 {
			lo = 0
		}
		segs = append(segs, SegmentImportance{
			StartLine: lo + 1,
			EndLine:   hi + 1,
			Text:      strings.Join(lines[lo:hi+1], "\n"),
		})
	}
	return segs
}

// maskSegment returns text with lines [start,end] (1-based) removed.
func maskSegment(text string, start, end int) string {
	lines := strings.Split(text, "\n")
	start = max(start, 1)
	end = min(end, len(lines))
	if start > end {
		return text
	}
	out := make([]string, 0, len(lines))
	out = append(out, lines[:start-1]...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n")
}

// ExplainResult explains why result matched query by occlusion: every
// segment of the passage is removed in turn and the drop in cosine
// similarity to the query is recorded (txtai's explain uses the same idea at
// token level; segments keep the cost to MaxSegments+2 embeddings in one
// batch). BM25 term contributions are added when the scorer supports them.
func (s *LeannSearcher) ExplainResult(ctx context.Context, query string, result SearchResult, opts ExplainOptions) (*Explanation, error) {
	if s.embedder == nil {
		return nil, fmt.Errorf("explain needs an embedding model; the index is lexical-only")
	}
	segs := splitSegments(result.Text, opts.MaxSegments)
	exp := &Explanation{ResultID: result.ID}
	if te, ok := s.scorer.(TermExplainer); ok {
		exp.Terms = te.ExplainTerms(query, result.ID)
	}
	if len(segs) == 0 {
		return exp, nil
	}

	texts := make([]string, 0, len(segs)+2)
	texts = append(texts, query, result.Text)
	if len(segs) > 1 {
		for _, sg := range segs {
			texts = append(texts, maskSegment(result.Text, sg.StartLine, sg.EndLine))
		}
	}
	embs, err := s.embedder.Compute(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("explain: embed: %w", err)
	}
	if len(embs) != len(texts) {
		return nil, fmt.Errorf("explain: embedder returned %d vectors for %d texts", len(embs), len(texts))
	}
	exp.EmbedCalls = len(texts)
	q := embs[0]
	full := cosineSimilarity(q, embs[1])
	exp.Similarity = full
	if len(segs) == 1 {
		segs[0].Delta = full
	} else {
		for i := range segs {
			segs[i].Delta = full - cosineSimilarity(q, embs[2+i])
		}
	}
	sort.SliceStable(segs, func(i, j int) bool { return segs[i].Delta > segs[j].Delta })
	exp.Segments = segs
	return exp, nil
}

// Snippet returns the k most important segments in passage order, joined
// with "…" between gaps. It is a compact view of a result for display or for
// an LLM context window.
func (e *Explanation) Snippet(k int) string {
	if e == nil || len(e.Segments) == 0 || k <= 0 {
		return ""
	}
	top := append([]SegmentImportance(nil), e.Segments[:min(k, len(e.Segments))]...)
	sort.Slice(top, func(i, j int) bool { return top[i].StartLine < top[j].StartLine })
	var b strings.Builder
	prevEnd := 0
	for i, sg := range top {
		if i > 0 && sg.StartLine > prevEnd+1 {
			b.WriteString("\n…\n")
		} else if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(sg.Text)
		prevEnd = sg.EndLine
	}
	return b.String()
}
