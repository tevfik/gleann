package quality

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

const embedDims = 256

// hashEmbedder is a deterministic stand-in for a real embedding model: a
// hashed bag of words over lower-cased identifier parts. camelCase and
// snake_case identifiers are split, and each whole identifier is also kept,
// so a query word and a code identifier that share parts land near each
// other. It needs no model, network or CGo, and gives the same vectors on
// every run.
type hashEmbedder struct{}

func (hashEmbedder) Dimensions() int   { return embedDims }
func (hashEmbedder) ModelName() string { return "test-hashed-bow-256" }

func (e hashEmbedder) ComputeSingle(_ context.Context, text string) ([]float32, error) {
	return embedText(text), nil
}

func (e hashEmbedder) Compute(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = embedText(t)
	}
	return out, nil
}

func embedText(text string) []float32 {
	v := make([]float32, embedDims)
	for _, tok := range tokens(text) {
		h := fnv.New64a()
		_, _ = h.Write([]byte(tok))
		sum := h.Sum64()
		sign := float32(1)
		if sum>>63 == 1 {
			sign = -1
		}
		v[sum%embedDims] += sign
	}
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		v[0] = 1
		return v
	}
	inv := float32(1 / math.Sqrt(norm))
	for i := range v {
		v[i] *= inv
	}
	return v
}

// tokens splits text into words, then each word into camelCase and
// snake_case parts. Words with several parts are emitted whole as well.
func tokens(text string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		var parts []string
		for _, s := range strings.Split(w, "_") {
			parts = append(parts, splitCamel(s)...)
		}
		for _, p := range parts {
			if len(p) > 1 {
				out = append(out, strings.ToLower(p))
			}
		}
		if len(parts) > 1 {
			out = append(out, strings.ToLower(w))
		}
	}
	return out
}

func splitCamel(s string) []string {
	rs := []rune(s)
	var parts []string
	start := 0
	for i := 1; i < len(rs); i++ {
		prev, cur := rs[i-1], rs[i]
		lowerToUpper := unicode.IsLower(prev) && unicode.IsUpper(cur)
		acronymEnd := i+1 < len(rs) && unicode.IsUpper(prev) && unicode.IsUpper(cur) && unicode.IsLower(rs[i+1])
		if lowerToUpper || acronymEnd {
			parts = append(parts, string(rs[start:i]))
			start = i
		}
	}
	return append(parts, string(rs[start:]))
}

// ── Exported fixture surface (RFC 008 step 0a) ───────────────────────────
//
// Moved here from embedder_test.go so the parity fixtures of RFC 008
// (internal/paritytest, decision D12) can reuse the exact scheme the
// quality gate measures with. The unexported names above are unchanged;
// the exports below are additive.

// HashEmbedder is the exported name of hashEmbedder, the deterministic
// hashed bag-of-words embedder the quality gate builds its corpus with.
// Indexes built through the paritytest HTTP fake and searches run with a
// direct HashEmbedder produce identical vectors, because the fake serves
// EmbedText.
type HashEmbedder = hashEmbedder

// EmbedDims is the vector length HashEmbedder produces.
const EmbedDims = embedDims

// EmbedText returns the deterministic unit vector HashEmbedder computes
// for text: a signed, hashed bag of lower-cased identifier parts (see
// hashEmbedder). It needs no model, network or CGo, and two calls with
// the same text return equal vectors.
func EmbedText(text string) []float32 { return embedText(text) }
