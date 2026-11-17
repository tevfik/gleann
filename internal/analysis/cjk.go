package analysis

import (
	"strings"
	"unicode"
)

// isCJK reports whether r belongs to a CJK run: Han, Hiragana, Katakana or
// Hangul (RFC 003 section 4.1 item 4).
func isCJK(r rune) bool {
	// U+30FC (ー, the prolonged sound mark) has Unicode script Common, not
	// Katakana, but it is part of Katakana words ("サーバー"). Without this
	// explicit case the run would break at every lengthened vowel and the
	// bigrams across it would be lost.
	return r == 'ー' ||
		unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// segment is a maximal run of CJK or non-CJK characters inside one word.
type segment struct {
	text string
	cjk  bool
}

// splitSegments splits a word into CJK runs and the non-CJK runs between
// them, so each part gets its own rules: CJK runs become unigrams and
// bigrams, the rest goes through the base word handling. Words without CJK
// (the common case) come back as a single segment.
func splitSegments(w string) []segment {
	if !strings.ContainsFunc(w, isCJK) {
		return []segment{{text: w}}
	}
	runes := []rune(w)
	var segs []segment
	start := 0
	for i := 1; i < len(runes); i++ {
		if isCJK(runes[i]) != isCJK(runes[start]) {
			segs = append(segs, segment{text: string(runes[start:i]), cjk: isCJK(runes[start])})
			start = i
		}
	}
	segs = append(segs, segment{text: string(runes[start:]), cjk: isCJK(runes[start])})
	return segs
}

// cjkTokens emits a CJK run as unigrams followed by bigrams, not as one
// token, so a query for any one- or two-character substring can match
// (RFC 003 section 4.1 item 4; bigrams come from the analyzer because FTS5
// trigram measured 3.4x slower to build and 2.5x larger). Unigrams are
// emitted unconditionally: the one-character drop rule of the base
// tokenizer must not drop them.
func cjkTokens(run string) []string {
	runes := []rune(run)
	out := make([]string, 0, 2*len(runes))
	for _, r := range runes {
		out = append(out, string(r))
	}
	for i := 0; i+1 < len(runes); i++ {
		out = append(out, string(runes[i:i+2]))
	}
	return out
}

// cjkQueryTokens is the query side of cjkTokens: the unigram for a
// one-character run, otherwise only the bigrams. Documents carry both, so
// either form matches.
func cjkQueryTokens(run string) []string {
	runes := []rune(run)
	if len(runes) == 1 {
		return []string{run}
	}
	out := make([]string, 0, len(runes)-1)
	for i := 0; i+1 < len(runes); i++ {
		out = append(out, string(runes[i:i+2]))
	}
	return out
}
