// Package analysis implements gleann's text analyzer: it decides what a term
// is before text reaches the lexical index (RFC 003, section 4). FTS5 receives
// pre-tokenised text and only splits on spaces.
//
// The base behaviour is a copy of the tokenizer in modules/bm25 (RFC 003
// section 4.1 item 2), so the quality gate measured against that tokenizer
// stays valid. On top of the base, Turkish prose folding applies when the
// index language is "tr" and the passage kind is prose, and CJK runs are
// emitted as unigrams and bigrams in every language. Code passages always use
// the base rules, so identifiers such as ID stay "id" even in a Turkish index.
//
// The analyzer is not wired into gleann yet (RFC 003 section 8, step 2); it
// carries no package-level mutable state, so one Analyzer value is safe to
// share across goroutines.
package analysis

import (
	"strings"
	"unicode"
)

// ID identifies this analyzer version. It is stored in meta.analyzer; a
// mismatch with a stored index makes the lexical index rebuild from the
// stored passage text (RFC 003 section 4.1 item 1).
const ID = "gleann-tok/1"

// Kind selects which rules apply to a passage. It mirrors passages.kind in
// the store schema (RFC 003 section 3.4): the indexer sets it from the source
// file type.
type Kind string

const (
	// KindCode is source code; always tokenised with the base rules.
	KindCode Kind = "code"
	// KindProse is documents, markdown and text; Turkish rules apply here
	// when the analyzer language is "tr".
	KindProse Kind = "prose"
)

// Analyzer tokenises text for one index language. "" or "en" selects the base
// rules; "tr" adds the Turkish rules for prose passages. Any other value
// behaves like the base, so a new language setting cannot break indexing
// before its rules exist.
type Analyzer struct {
	Language string
}

// New returns an Analyzer for the given index language.
func New(language string) Analyzer {
	return Analyzer{Language: language}
}

// Tokens returns the document-side tokens of text, in order, with repeats
// kept (the scorer needs term frequencies). Turkish folding applies only when
// the language is "tr" and kind is prose; CJK runs become unigrams and
// bigrams in every language and kind.
func (a Analyzer) Tokens(text string, kind Kind) []string {
	turkish := a.Language == "tr" && kind == KindProse
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	out := make([]string, 0, len(words)*2)
	for _, w := range words {
		for _, seg := range splitSegments(w) {
			if seg.cjk {
				out = append(out, cjkTokens(seg.text)...)
			} else {
				out = appendDocWord(out, seg.text, turkish)
			}
		}
	}
	return out
}

// QueryTokens returns the query-side terms of text. Each query term is a
// group of alternative forms: the base form first, then, when the language
// is "tr", the Turkish-folded form if it differs. A query has no kind, so
// both forms are emitted and OR-ed by MatchExpr: the query ID then matches
// "id" in code and "ıd" in Turkish prose (RFC 003 section 4.1 item 3).
// A CJK run of two or more characters is queried by its bigrams only (a
// single character by its unigram): OR-ing the unigrams of a query would
// match every passage that contains any common character of it.
func (a Analyzer) QueryTokens(text string) [][]string {
	turkish := a.Language == "tr"
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	var groups [][]string
	for _, w := range words {
		for _, seg := range splitSegments(w) {
			if seg.cjk {
				for _, tok := range cjkQueryTokens(seg.text) {
					groups = append(groups, []string{tok})
				}
			} else {
				groups = appendQueryWord(groups, seg.text, turkish)
			}
		}
	}
	return groups
}

// MatchExpr builds the FTS5 MATCH expression for QueryTokens output: forms
// within a group are OR-ed and groups are OR-ed. Each form is double-quoted
// with embedded quotes doubled (FTS5 string syntax); the analyzer splits on
// non-letters so a quote should not occur, but the store must not depend on
// that (RFC 003 section 4.2 step 1). Identical forms are emitted once.
//
// It returns "" when there is no token (a query of only stop words,
// punctuation or one-character words). The caller must then return an empty
// result without running SQL: FTS5 rejects an empty MATCH expression with a
// syntax error.
func MatchExpr(groups [][]string) string {
	seen := make(map[string]bool)
	var parts []string
	for _, group := range groups {
		for _, form := range group {
			if form == "" || seen[form] {
				continue
			}
			seen[form] = true
			parts = append(parts, `"`+strings.ReplaceAll(form, `"`, `""`)+`"`)
		}
	}
	return strings.Join(parts, " OR ")
}

// MatchExprAND builds the FTS5 MATCH expression with all term groups AND-ed
// and forms within each group OR-ed: ("a" OR "a2") AND "b" (RFC 003 section
// 4.2 step 2). Quoting rules are identical to MatchExpr: each form is
// double-quoted with embedded quotes doubled. Forms within a group are
// deduplicated; groups of a single form are emitted as "a" without
// parentheses. Duplicate groups are emitted once.
//
// It returns "" when there is no token (empty groups, or all forms empty).
// The caller must then return an empty result without running SQL.
func MatchExprAND(groups [][]string) string {
	seenGroups := make(map[string]bool)
	var groupParts []string
	for _, group := range groups {
		seenForms := make(map[string]bool)
		var forms []string
		for _, form := range group {
			if form == "" || seenForms[form] {
				continue
			}
			seenForms[form] = true
			forms = append(forms, `"`+strings.ReplaceAll(form, `"`, `""`)+`"`)
		}
		if len(forms) == 0 {
			continue
		}
		var gStr string
		if len(forms) == 1 {
			gStr = forms[0]
		} else {
			gStr = "(" + strings.Join(forms, " OR ") + ")"
		}
		if seenGroups[gStr] {
			continue
		}
		seenGroups[gStr] = true
		groupParts = append(groupParts, gStr)
	}
	return strings.Join(groupParts, " AND ")
}
