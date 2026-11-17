package analysis

import "strings"

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
