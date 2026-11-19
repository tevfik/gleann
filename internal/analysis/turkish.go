package analysis

import (
	"strings"
	"unicode"
)

// turkishLower lowercases with the Turkish dot rules: İ→i and I→ı happen
// before general lowercasing, because plain strings.ToLower maps both I and
// İ to dotted i and loses the distinction Turkish prose needs (RFC 003
// section 4.1 item 3). "IŞIKLARI" becomes "ışıkları", not "isiklari".
func turkishLower(s string) string {
	mapped := strings.Map(func(r rune) rune {
		switch r {
		case 'İ':
			return 'i'
		case 'I':
			return 'ı'
		}
		return r
	}, s)
	return strings.ToLower(mapped)
}

// asciiFold maps the Turkish-specific letters to their ASCII look-alikes
// ("ışıkları" -> "isiklari") so an ASCII-only query can match Turkish prose.
// It is only called on already-lowercased tokens; letters without a mapping
// pass through unchanged, and a token with no Turkish-specific letter comes
// back identical (strings.Map returns the original string when nothing
// changes), which is how the caller detects that no variant is needed.
func asciiFold(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case 'ı':
			return 'i'
		case 'ş':
			return 's'
		case 'ğ':
			return 'g'
		case 'ç':
			return 'c'
		case 'ö':
			return 'o'
		case 'ü':
			return 'u'
		}
		return r
	}, s)
}

// dropApostropheSuffixes removes the case suffix Turkish writes after an
// apostrophe on proper nouns, abbreviations and numbers: "Kanunu'na",
// "Anayasa'ya", "AB'ye", "2023'te" become "Kanunu", "Anayasa", "AB", "2023".
// Without it the suffix became a query term of its own ("na", "ya"), which
// matches unrelated passages. A suffix is the lowercase letters after an
// apostrophe (' or U+2019) that follows a letter or digit; an apostrophe
// followed by an uppercase letter ("O'Brien") is left alone, and so is one at
// a word edge.
func dropApostropheSuffixes(text string) string {
	if !strings.ContainsAny(text, "'\u2019") {
		return text
	}
	runes := []rune(text)
	out := make([]rune, 0, len(runes))
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if (r == '\'' || r == '\u2019') && i > 0 && i+1 < len(runes) &&
			(unicode.IsLetter(runes[i-1]) || unicode.IsDigit(runes[i-1])) && unicode.IsLower(runes[i+1]) {
			j := i + 1
			for j < len(runes) && unicode.IsLower(runes[j]) {
				j++
			}
			i = j - 1 // skip the apostrophe and the suffix
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
