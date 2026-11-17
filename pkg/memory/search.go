package memory

import (
	"sort"
	"strings"

	"github.com/tevfik/gleann/modules/bm25"
)

// searchText is the text a block is indexed under for lexical retrieval:
// label, content, tags, file paths and symbol names. Paths and symbols are
// tokenised by the bm25 tokenizer, so "RateLimiter" is findable as
// "rate limiter" and "snake_case" as "snake" or "case".
func searchText(b *Block) string {
	var sb strings.Builder
	sb.WriteString(b.Label)
	sb.WriteByte('\n')
	sb.WriteString(b.Content)
	for _, group := range [][]string{b.Tags, b.Paths, b.Symbols} {
		for _, s := range group {
			sb.WriteByte('\n')
			sb.WriteString(s)
		}
	}
	return sb.String()
}

// matchesPhrase reports whether the lowercased phrase occurs verbatim in the
// block's content, label or tags (the pre-ranking Search semantics).
func matchesPhrase(b *Block, phrase string) bool {
	return strings.Contains(strings.ToLower(b.Content), phrase) ||
		strings.Contains(strings.ToLower(b.Label), phrase) ||
		containsTag(b.Tags, phrase)
}

// phraseMatches returns the blocks that contain the query verbatim
// (case-insensitive), in their input order. An empty query matches all.
// Destructive callers such as Forget use this instead of ranked search so a
// loose multi-word query can never delete unrelated blocks.
func phraseMatches(blocks []Block, query string) []Block {
	q := strings.ToLower(query)
	var out []Block
	for i := range blocks {
		if matchesPhrase(&blocks[i], q) {
			out = append(out, blocks[i])
		}
	}
	return out
}

// rankBlocks performs ranked lexical retrieval over candidates, which must
// already be scope/tier filtered and ordered newest first.
//
//   - A block matches when it contains at least one query token, or contains
//     the whole query as a verbatim phrase.
//   - Verbatim phrase matches rank before all others, so precise queries keep
//     their previous top hit.
//   - Within each group blocks are ordered by BM25 score (computed over the
//     candidate set), ties broken by recency (input order).
//   - An empty query returns every candidate, as before. A query with no
//     indexable tokens (e.g. only stop words) falls back to phrase matching.
//
// A dense (embedding) leg would plug in here: score candidates by vector
// similarity and fuse with the BM25 score (e.g. reciprocal rank fusion)
// before the phrase-first ordering. That needs the store decision of RFC 003.
func rankBlocks(candidates []Block, query string) []Block {
	if query == "" {
		return candidates
	}
	phrase := strings.ToLower(query)

	var scores map[int64]float32
	if len(bm25.Tokenize(query)) > 0 {
		sc := bm25.NewScorer()
		for i := range candidates {
			sc.AddDocument(int64(i), searchText(&candidates[i]))
		}
		scores = sc.Score(query)
	}

	type hit struct {
		idx    int
		phrase bool
		score  float32
	}
	var hits []hit
	for i := range candidates {
		p := matchesPhrase(&candidates[i], phrase)
		s := scores[int64(i)]
		if p || s > 0 {
			hits = append(hits, hit{i, p, s})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].phrase != hits[b].phrase {
			return hits[a].phrase
		}
		return hits[a].score > hits[b].score
	})

	var out []Block
	for _, h := range hits {
		out = append(out, candidates[h.idx])
	}
	return out
}
