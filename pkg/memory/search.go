package memory

import (
	"math"
	"os"
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

// searchFloorEnv relaxes the retrieval floor: "off", "0" or "false" restores
// the previous behaviour (any block sharing a single query token matches).
const searchFloorEnv = "GLEANN_MEMORY_SEARCH_FLOOR"

// floorOptions configures the retrieval floor of rankBlocks.
type floorOptions struct {
	// enabled turns the floor on. Off, a block matches on any single token.
	enabled bool
	// coverage is the fraction of distinct query tokens a block must contain
	// (capped at maxMatch tokens) for queries with 2+ indexable tokens.
	coverage float64
	maxMatch int
	// relative drops non-phrase hits scoring below this fraction of the best
	// non-phrase score. 0 disables the cut.
	relative float64
}

// defaultFloor is the shipped rule, chosen with TestSearchFloorEvaluation.
func defaultFloor() floorOptions {
	return floorOptions{enabled: true, coverage: 0.35, maxMatch: 2, relative: 0.25}
}

// floorFromEnv returns the floor configured by GLEANN_MEMORY_SEARCH_FLOOR.
func floorFromEnv() floorOptions {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(searchFloorEnv))) {
	case "off", "0", "false", "no":
		return floorOptions{}
	}
	return defaultFloor()
}

// requiredMatches is the number of distinct query tokens a block must contain.
// A one-token query needs that token; longer queries need
// min(maxMatch, ceil(coverage*n)), at least 1.
func (f floorOptions) requiredMatches(n int) int {
	if !f.enabled || n < 2 {
		return 1
	}
	r := int(math.Ceil(f.coverage * float64(n)))
	if r > f.maxMatch {
		r = f.maxMatch
	}
	if r < 1 {
		r = 1
	}
	return r
}

// isQueryFiller reports question and chat words that carry no topic. They are
// ignored when counting how many query tokens a block covers, so "where does
// ollama listen" is a two-token query (ollama, listen), not four. A switch,
// not a map, keeps the list immutable (AGENTS.md section 4).
func isQueryFiller(w string) bool {
	switch w {
	case "how", "what", "where", "when", "why", "who", "which",
		"can", "we", "you", "our", "your", "my", "me", "us",
		"there", "here", "any", "about", "tell", "show", "please",
		"need", "want", "get", "go":
		return true
	}
	return false
}

// stem folds the most common English inflections (plural, -ed, -ing, -ion) so a
// query word matches its inflected form in a memory ("listen" and "listens").
// Suffixes are stripped until none applies, so the result is a fixed point and
// every inflection of a word reaches the same stem: "connection",
// "connections" and "connect" all give "connect"; "version" and "versions"
// both give "vers". It is only used for floor coverage counting and duplicate
// detection; BM25 scoring is unchanged.
func stem(w string) string {
	for {
		next := stemOnce(w)
		if next == w {
			return w
		}
		w = next
	}
}

func stemOnce(w string) string {
	for _, suf := range [...]string{"ing", "ion", "es", "ed", "s"} {
		if len(w) >= len(suf)+4 && strings.HasSuffix(w, suf) {
			return w[:len(w)-len(suf)]
		}
	}
	return w
}

// coverageTokens returns the distinct stemmed topic tokens of text; with
// query set, filler words are dropped.
func coverageTokens(text string, query bool) map[string]struct{} {
	toks := bm25.Tokenize(text)
	set := make(map[string]struct{}, len(toks))
	for _, t := range toks {
		if query {
			if isQueryFiller(t) {
				continue
			}
		}
		set[stem(t)] = struct{}{}
	}
	return set
}

// rankBlocks performs ranked lexical retrieval over candidates, which must
// already be scope/tier filtered and ordered newest first.
//
//   - A block matches when it contains the whole query as a verbatim phrase,
//     or when it contains enough distinct query tokens: one for a one-token
//     query, otherwise min(2, ceil(0.35*n)) of the n distinct query tokens.
//     Sharing one common word with a long query is not a match, so a small or
//     unrelated store returns nothing instead of weak hits. The floor is
//     counted in tokens, not BM25 score, because scores are not comparable
//     across queries and store sizes. GLEANN_MEMORY_SEARCH_FLOOR=off restores
//     the old any-token rule.
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
	return rankBlocksWith(candidates, query, floorFromEnv())
}

// isGenericLabel reports the labels the write paths assign by default; they
// say nothing about a block's topic.
func isGenericLabel(l string) bool {
	switch l {
	case "user_memory", "agent_memory", "api_memory":
		return true
	}
	return false
}

// coverage counts the distinct query tokens present in the block.
func coverage(qTokens map[string]struct{}, b *Block) int {
	text := searchText(b)
	if isGenericLabel(b.Label) {
		// The write path stamps these labels on every block; counting them
		// would let the query word "memory" or "user" match everything.
		text = strings.TrimPrefix(text, b.Label)
	}
	dt := coverageTokens(text, false)
	n := 0
	for t := range qTokens {
		if _, ok := dt[t]; ok {
			n++
		}
	}
	return n
}

func rankBlocksWith(candidates []Block, query string, floor floorOptions) []Block {
	if query == "" {
		return candidates
	}
	phrase := strings.ToLower(query)

	var scores map[int64]float32
	qTokens := coverageTokens(query, true)
	if len(bm25.Tokenize(query)) > 0 {
		sc := bm25.NewScorer()
		for i := range candidates {
			sc.AddDocument(int64(i), searchText(&candidates[i]))
		}
		scores = sc.Score(query)
	}
	need := floor.requiredMatches(len(qTokens))

	type hit struct {
		idx    int
		phrase bool
		score  float32
	}
	var hits []hit
	for i := range candidates {
		p := matchesPhrase(&candidates[i], phrase)
		s := scores[int64(i)]
		if !p {
			if !floor.enabled {
				if s <= 0 {
					continue
				}
			} else if coverage(qTokens, &candidates[i]) < need {
				continue
			}
		}
		hits = append(hits, hit{i, p, s})
	}
	if floor.enabled && floor.relative > 0 {
		var top float32
		for _, h := range hits {
			if !h.phrase && h.score > top {
				top = h.score
			}
		}
		kept := hits[:0]
		for _, h := range hits {
			if h.phrase || float64(h.score) >= floor.relative*float64(top) {
				kept = append(kept, h)
			}
		}
		hits = kept
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
