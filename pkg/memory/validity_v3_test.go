package memory

import "testing"

// v3 repair tests (second independent review, memory-fixes-v2.gemini.md):
// empty-residual subset matching, Turkish short-stem negation, Turkish
// verbal-noun agreement, single-quoted tokens and negative numbers.

// TestDefect_EmptyResidualMatchesUnrelated: a statement whose residual is
// empty (only stop words and a polarity mechanism token) must not match an
// unrelated statement as an "elaboration". The subset tolerance needs at
// least one shared residual topic word.
func TestDefect_EmptyResidualMatchesUnrelated(t *testing.T) {
	pairs := []struct{ old, new string }{
		{"It is enabled", "Docker containers are disabled"},
		{"This is recommended", "Database access is deprecated"},
	}
	for _, p := range pairs {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if b.ContradictionCheck(dir[1]) {
				t.Errorf("unrelated statements with an empty residual flagged as contradiction: %q vs %q", dir[0], dir[1])
			}
		}
	}
}

// TestDefect_TurkishShortStemNegation: 2- and 3-letter Turkish imperative
// stems (yaz, oku, yap, sil, al) must be inspected for -ma/-me negation;
// the old len(stem) < 4 guard skipped them.
func TestDefect_TurkishShortStemNegation(t *testing.T) {
	pairs := []struct{ old, new string }{
		{"kodu buraya yaz", "kodu buraya yazma"},
		{"bu dosyayi oku", "bu dosyayi okuma"},
		{"servise istek yap", "servise istek yapma"},
		{"dosyayi sil", "dosyayi silme"},
		{"paketi al", "paketi alma"},
	}
	for _, p := range pairs {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if !b.ContradictionCheck(dir[1]) {
				t.Errorf("Turkish short-stem negation missed: ContradictionCheck(%q -> %q) = false, want true", dir[0], dir[1])
			}
		}
	}
}

// TestDefect_TurkishVerbalNounAgreement: -ma/-me also forms verbal nouns.
// "Otomatik guncelle" (update automatically) and "Otomatik guncelleme"
// (automatic update) agree; a bare adjective modifier + -ma/-me is a noun
// phrase, not a negative imperative.
func TestDefect_TurkishVerbalNounAgreement(t *testing.T) {
	pairs := []struct{ old, new string }{
		{"Otomatik guncelle", "Otomatik guncelleme"},
		{"Temiz derle", "Temiz derleme"},
	}
	for _, p := range pairs {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if b.ContradictionCheck(dir[1]) {
				t.Errorf("agreeing Turkish verbal noun flagged as contradiction: ContradictionCheck(%q -> %q) = true, want false", dir[0], dir[1])
			}
		}
	}
}

// TestDefect_SingleQuotedAntonyms: tokens wrapped in single quotes must
// normalize to their bare form so the antonym table sees true/false, while
// internal apostrophes (contractions like don't) stay intact.
func TestDefect_SingleQuotedAntonyms(t *testing.T) {
	b := &Block{Content: "Set auth to 'true'"}
	if !b.ContradictionCheck("Set auth to 'false'") {
		t.Error("single-quoted antonyms not recognized: ContradictionCheck(\"Set auth to 'true'\" -> \"Set auth to 'false'\") = false, want true")
	}
	b2 := &Block{Content: "Set auth to 'false'"}
	if !b2.ContradictionCheck("Set auth to 'true'") {
		t.Error("single-quoted antonyms not recognized in reverse direction")
	}
	// Internal apostrophes must survive: contractions still negate.
	b3 := &Block{Content: "We do use MySQL"}
	if !b3.ContradictionCheck("We don't use MySQL") {
		t.Error("contraction negation broken by quote trimming")
	}
}

// TestDefect_NegativeNumberValueFlip: a minus directly prefixed to a digit
// is a numeric sign, not a hyphen; -5 vs 5 must be a valueFlip contradiction.
// Hyphenated words (read-only, utf-8) must still split cleanly.
func TestDefect_NegativeNumberValueFlip(t *testing.T) {
	pairs := []struct{ old, new string }{
		{"Temperature threshold is -5", "Temperature threshold is 5"},
		{"Min balance is -100", "Min balance is 100"},
	}
	for _, p := range pairs {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if !b.ContradictionCheck(dir[1]) {
				t.Errorf("negative number sign stripped: ContradictionCheck(%q -> %q) = false, want true", dir[0], dir[1])
			}
		}
	}
	// Hyphenated words keep splitting cleanly: no sign invented mid-word,
	// and a non-numeric word swap is still not a value flip.
	b := &Block{Content: "The mode is read-only"}
	if b.ContradictionCheck("The mode is read-write") {
		t.Error("hyphenated word swap flagged as contradiction: read-only vs read-write")
	}
	// A digit-bearing hyphenated token still flips on its numeric part.
	b2 := &Block{Content: "Encoding is utf-8"}
	if !b2.ContradictionCheck("Encoding is utf-16") {
		t.Error("utf-8 vs utf-16 value flip missed after tokenizer change")
	}
}
