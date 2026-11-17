package analysis

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The base word handling below is copied from modules/bm25/bm25.go
// (tokenize, splitCamelWords, stopWords) so the analyzer output stays
// byte-identical to today's tokenizer for English and code text (RFC 003
// section 4.1 item 2). It is a copy, not an import: modules/bm25 keeps its
// own tokenizer until the lexical leg is wired (RFC 003 section 8, step 2).

// splitCamelWords splits camelCase, PascalCase, or letter-digit compound words into sub-words.
func splitCamelWords(s string) []string {
	var words []string
	runes := []rune(s)
	n := len(runes)
	if n == 0 {
		return nil
	}

	start := 0
	for i := 1; i < n; i++ {
		prev := runes[i-1]
		curr := runes[i]

		// Lowercase -> Uppercase (e.g. "openStore" -> "open", "Store")
		if unicode.IsLower(prev) && unicode.IsUpper(curr) {
			words = append(words, string(runes[start:i]))
			start = i
			continue
		}

		// Upper -> Upper followed by Lower (e.g. "ASTChunker" -> "AST", "Chunker")
		if i+1 < n && unicode.IsUpper(prev) && unicode.IsUpper(curr) && unicode.IsLower(runes[i+1]) {
			words = append(words, string(runes[start:i]))
			start = i
			continue
		}

		// Letter <-> Digit transition (e.g. "sha256" -> "sha", "256", "256bit" -> "256", "bit")
		if (unicode.IsLetter(prev) && unicode.IsDigit(curr)) || (unicode.IsDigit(prev) && unicode.IsLetter(curr)) {
			words = append(words, string(runes[start:i]))
			start = i
			continue
		}
	}
	if start < n {
		words = append(words, string(runes[start:]))
	}
	return words
}

// isStopWord reports whether word is in the stop list copied from
// modules/bm25 (stopWords). A switch keeps the list immutable: the analyzer
// must have no package-level mutable state (AGENTS.md section 4).
func isStopWord(word string) bool {
	switch word {
	case "the", "is", "at", "which", "on",
		"a", "an", "and", "or", "but",
		"in", "to", "for", "of", "with",
		"by", "from", "as", "it", "that",
		"this", "be", "are", "was", "were",
		"been", "being", "have", "has", "had",
		"do", "does", "did", "will", "would",
		"could", "should", "may", "might",
		"not", "no", "so", "if", "then":
		return true
	}
	return false
}

// appendDocWord appends the document tokens of one non-CJK word: the whole
// word lowercased, then the camelCase/letter-digit sub-words, each kept only
// if it is longer than one byte and not a stop word. This is the tokenize
// loop of modules/bm25; with turkish set, lowercasing uses the Turkish dot
// rules and every emitted token containing Turkish-specific letters also
// gets its ASCII-folded variant (RFC 003 section 4.1 item 3).
//
// The length check is on bytes, as in modules/bm25, so the base rules stay
// identical to it (a single Cyrillic or accented letter survives, as today).
// Turkish prose counts characters instead (longEnough): otherwise "I", which
// becomes the two-byte "ı", would survive while "i" is dropped. CJK unigrams
// never reach this function; they are emitted directly.
func appendDocWord(out []string, w string, turkish bool) []string {
	lower := strings.ToLower(w)
	if turkish {
		lower = turkishLower(w)
	}
	if longEnough(lower, turkish) && !isStopWord(lower) {
		out = appendDocToken(out, lower, turkish)
	}

	// If the word contains camelCase/PascalCase or letter-digit transitions, add sub-tokens.
	parts := splitCamelWords(w)
	if len(parts) > 1 {
		for _, part := range parts {
			partLower := strings.ToLower(part)
			if turkish {
				partLower = turkishLower(part)
			}
			if longEnough(partLower, turkish) && !isStopWord(partLower) && partLower != lower {
				out = appendDocToken(out, partLower, turkish)
			}
		}
	}
	return out
}

// appendDocToken appends one token and, for Turkish prose, its ASCII-folded
// variant when the token carries Turkish-specific letters ("ışıkları" also
// yields "isiklari"), so ASCII-only queries match Turkish prose.
func appendDocToken(out []string, tok string, turkish bool) []string {
	out = append(out, tok)
	if turkish {
		if folded := asciiFold(tok); folded != tok {
			out = append(out, folded)
		}
	}
	return out
}

// appendQueryWord appends the query groups of one non-CJK word. It mirrors
// appendDocWord's emission sites (whole word, then sub-words) so each base
// form pairs with the Turkish-folded form of the same source word. Folding
// must start from the original case: the query ID folds to "ıd", which
// lowercasing the base form "id" first could never produce.
func appendQueryWord(groups [][]string, w string, turkish bool) [][]string {
	lower := strings.ToLower(w)
	folded := ""
	if turkish {
		folded = turkishLower(w)
	}
	groups = appendQueryGroup(groups, lower, folded)

	parts := splitCamelWords(w)
	if len(parts) > 1 {
		for _, part := range parts {
			partLower := strings.ToLower(part)
			partFolded := ""
			if turkish {
				partFolded = turkishLower(part)
			}
			if partLower != lower {
				groups = appendQueryGroup(groups, partLower, partFolded)
			}
		}
	}
	return groups
}

// appendQueryGroup adds one query term as a group of alternative forms: the
// base form first, then the Turkish-folded form when it differs. Each form
// passes the same length/stop-word filter as the document side, so a form
// that only exists after folding (the query "IS" folds to "ıs", while the
// base "is" is a stop word) still gets emitted. A group with no surviving
// form is skipped.
func appendQueryGroup(groups [][]string, base, folded string) [][]string {
	var group []string
	if len(base) > 1 && !isStopWord(base) {
		group = append(group, base)
	}
	if folded != "" && folded != base && longEnough(folded, true) && !isStopWord(folded) {
		group = append(group, folded)
	}
	if len(group) > 0 {
		groups = append(groups, group)
	}
	return groups
}

// longEnough applies the one-character drop rule: bytes for the base rules
// (as modules/bm25 does), characters for Turkish prose.
func longEnough(tok string, turkish bool) bool {
	if turkish {
		return utf8.RuneCountInString(tok) > 1
	}
	return len(tok) > 1
}
