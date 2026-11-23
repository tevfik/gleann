package analysis

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// TokenString returns the document-side tokens of text joined by single
// spaces: exactly strings.Join(a.Tokens(text, kind), " "), the form the
// lexical index stores (FTS5 receives pre-tokenised text and splits on
// spaces).
//
// It exists for the write path, where every passage of a build goes through
// the analyzer. Tokens allocates a string per lowercased token, a rune slice
// per word and the slice of all tokens; TokenString writes the tokens of
// ASCII words (nearly all of source code and English prose) straight into one
// buffer. Words with a non-ASCII letter or digit, and every word of Turkish
// prose, go through the same functions Tokens uses, so the rules live in one
// place for them. The ASCII path below restates the base rules of
// appendDocWord and splitCamelWords on bytes; TestTokenStringMatchesTokens and
// FuzzTokenString hold the two implementations together.
func (a Analyzer) TokenString(text string, kind Kind) string {
	if a.Language == "tr" && kind == KindProse {
		// Turkish prose: apostrophe suffixes, dotless-i lowercasing and
		// ASCII-folded variants apply to ASCII words too.
		return strings.Join(a.Tokens(text, kind), " ")
	}
	w := tokenWriter{buf: make([]byte, 0, len(text))}
	n := len(text)
	for i := 0; i < n; {
		// Skip separators: everything that is not a letter or a digit, as in
		// Tokens (an invalid UTF-8 byte decodes to U+FFFD, a separator).
		if c := text[i]; c < utf8.RuneSelf {
			if !isASCIIAlnum(c) {
				i++
				continue
			}
		} else {
			r, size := utf8.DecodeRuneInString(text[i:])
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				i += size
				continue
			}
		}
		// A word starts here; it ends at the next separator.
		start, ascii := i, true
		for i < n {
			if c := text[i]; c < utf8.RuneSelf {
				if !isASCIIAlnum(c) {
					break
				}
				i++
				continue
			}
			r, size := utf8.DecodeRuneInString(text[i:])
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				break
			}
			ascii = false
			i += size
		}
		if ascii {
			w.asciiWord(text[start:i])
		} else {
			w.word(text[start:i])
		}
	}
	return string(w.buf)
}

// tokenWriter accumulates tokens separated by single spaces.
type tokenWriter struct {
	buf []byte
	n   int // tokens written
	// scratch is reused for the tokens of non-ASCII words.
	scratch []string
}

// word appends the tokens of one word that holds a non-ASCII letter or digit,
// through the functions Tokens uses (base rules: never Turkish here).
func (w *tokenWriter) word(word string) {
	toks := w.scratch[:0]
	for _, seg := range splitSegments(word) {
		if seg.cjk {
			toks = append(toks, cjkTokens(seg.text)...)
		} else {
			toks = appendDocWord(toks, seg.text, false)
		}
	}
	for _, t := range toks {
		if w.n > 0 {
			w.buf = append(w.buf, ' ')
		}
		w.buf = append(w.buf, t...)
		w.n++
	}
	w.scratch = toks
}

// asciiWord appends the tokens of one word of ASCII letters and digits: the
// base rules of appendDocWord. The whole word lowercased, then, when the word
// splits at a camelCase or letter-digit boundary (splitCamelWords), each
// sub-word lowercased; a token is kept when it is longer than one byte and
// not a stop word. A sub-word is always shorter than the word, so
// appendDocWord's "differs from the whole word" test is always true here.
func (w *tokenWriter) asciiWord(word string) {
	w.lowerToken(word)
	start := 0
	for i := 1; i < len(word); i++ {
		prev, curr := word[i-1], word[i]
		switch {
		// Lowercase -> Uppercase ("openStore" -> "open", "Store").
		case isASCIILower(prev) && isASCIIUpper(curr):
		// Upper -> Upper followed by Lower ("ASTChunker" -> "AST", "Chunker").
		case i+1 < len(word) && isASCIIUpper(prev) && isASCIIUpper(curr) && isASCIILower(word[i+1]):
		// Letter <-> Digit ("sha256" -> "sha", "256").
		case isASCIIDigit(prev) != isASCIIDigit(curr):
		default:
			continue
		}
		w.lowerToken(word[start:i])
		start = i
	}
	if start > 0 {
		w.lowerToken(word[start:])
	}
}

// lowerToken appends tok lowercased unless it is one byte long or a stop word.
func (w *tokenWriter) lowerToken(tok string) {
	if len(tok) <= 1 {
		return
	}
	mark := len(w.buf)
	if w.n > 0 {
		w.buf = append(w.buf, ' ')
	}
	at := len(w.buf)
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if isASCIIUpper(c) {
			c += 'a' - 'A'
		}
		w.buf = append(w.buf, c)
	}
	// The conversion of so short a slice, for a call that does not keep it,
	// stays on the stack.
	if len(tok) <= maxStopWordLen && isStopWord(string(w.buf[at:])) {
		w.buf = w.buf[:mark]
		return
	}
	w.n++
}

func isASCIIUpper(c byte) bool { return c-'A' < 26 }
func isASCIILower(c byte) bool { return c-'a' < 26 }
func isASCIIDigit(c byte) bool { return c-'0' < 10 }
func isASCIIAlnum(c byte) bool { return c-'0' < 10 || (c|0x20)-'a' < 26 }
