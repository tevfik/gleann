package memory

import "testing"

// v4 repair tests (third independent review, memory-fixes-v3.gemini.md):
// the 2-3 letter Turkish stem exemption must be a closed verb lexicon with
// clause-final position on both sides, and the English stop-word filter must
// not suppress the 2-letter Turkish verb stems at/it/in/as.

// isolateV4Env points HOME and the XDG/Gleann config locations at a temporary
// directory so no test can touch the real home directory. The v4 tests only
// exercise the pure ContradictionCheck heuristic (no store is opened); the
// isolation is belt-and-braces per the repair rules.
func isolateV4Env(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("GLEANN_CONFIG_DIR", dir)
}

// TestDefect_TurkishShortStemEnglishFalsePositives: arbitrary 2-3 letter
// English words plus "ma"/"me" are not Turkish verb+negation. The blanket
// len(stem) <= 3 acceptance flagged dog/dogma and car/carma, and a
// non-clause-final "al" is not an imperative either.
func TestDefect_TurkishShortStemEnglishFalsePositives(t *testing.T) {
	isolateV4Env(t)
	pairs := []struct{ old, new string }{
		{"The dog runs", "Avoid religious dogma"},
		{"the dog is big", "the dogma is big"},
		{"the car is fast", "the carma is fast"},
		{"The red car", "The red carma"},
		{"al is ready", "alma is ready"},
	}
	for _, p := range pairs {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if b.ContradictionCheck(dir[1]) {
				t.Errorf("non-verb short stem flagged as Turkish negation: ContradictionCheck(%q -> %q) = true, want false", dir[0], dir[1])
			}
		}
	}
}

// TestDefect_TurkishStopWordVerbStems: at, it, in and as are English stop
// words but also Turkish 2-letter imperative roots (at/atma "throw",
// it/itme "push", in/inme "descend", as/asma "hang"). Dropping them during
// the stem scan made genuine Turkish imperative negations undetectable.
func TestDefect_TurkishStopWordVerbStems(t *testing.T) {
	isolateV4Env(t)
	pairs := []struct{ old, new string }{
		{"Bu paketi at", "Bu paketi atma"},
		{"Kapiyi it", "Kapiyi itme"},
		{"Asagi in", "Asagi inme"},
		{"Resmi duvara as", "Resmi duvara asma"},
	}
	for _, p := range pairs {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if !b.ContradictionCheck(dir[1]) {
				t.Errorf("Turkish stop-word verb stem negation missed: ContradictionCheck(%q -> %q) = false, want true", dir[0], dir[1])
			}
		}
	}
}

// TestDefect_TurkishClauseFinalShortVerbs: clause-final 2-3 letter verb
// roots from the closed lexicon still negate (positive guard for the
// position requirement), while a non-final stem is not an imperative.
func TestDefect_TurkishClauseFinalShortVerbs(t *testing.T) {
	isolateV4Env(t)
	positive := []struct{ old, new string }{
		{"testleri yaz", "testleri yazma"},
		{"kapiyi ac", "kapiyi acma"},
		{"dosyayi kes", "dosyayi kesme"},
		{"ona ver", "ona verme"},
		{"burada dur", "burada durma"},
		{"resmi cek", "resmi cekme"},
	}
	for _, p := range positive {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if !b.ContradictionCheck(dir[1]) {
				t.Errorf("clause-final Turkish short verb negation missed: ContradictionCheck(%q -> %q) = false, want true", dir[0], dir[1])
			}
		}
	}
	// Non-final stems: the -ma/-me form is not a clause-final negative
	// imperative, so no polarity is established.
	nonFinal := []struct{ old, new string }{
		{"yaz kodu", "yazma kodu"},
		{"al is ready", "alma is ready"},
	}
	for _, p := range nonFinal {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if b.ContradictionCheck(dir[1]) {
				t.Errorf("non-final Turkish stem flagged as imperative negation: ContradictionCheck(%q -> %q) = true, want false", dir[0], dir[1])
			}
		}
	}
}

// TestDefect_TurkishVerbalNounGuardsV4: the long-stem verbal-noun guards
// from the second review must survive the v4 short-stem tightening.
func TestDefect_TurkishVerbalNounGuardsV4(t *testing.T) {
	isolateV4Env(t)
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

// TestDefect_EnglishStopWordsStillFiltered: the stop-word exemption for
// Turkish verb roots must not leak into English residual filtering: a
// non-final "at"/"in"/"as"/"it" stays a function word, and prepositions do
// not become topic words in the residual comparison.
func TestDefect_EnglishStopWordsStillFiltered(t *testing.T) {
	isolateV4Env(t)
	// Same claim with the preposition moved: still a confirmation, not a
	// contradiction (stop words carry no topic meaning).
	b := &Block{Content: "The server runs at night"}
	if b.ContradictionCheck("The server runs in the night") {
		t.Error("preposition swap flagged as contradiction: at night vs in the night")
	}
	// A non-final stop-word stem never forms a Turkish mechanism even if
	// the other side ends with the suffixed lookalike.
	b2 := &Block{Content: "Look at the door"}
	if b2.ContradictionCheck("Look atma the door") {
		t.Error("non-final stop-word stem formed a Turkish negation mechanism")
	}
	// Unrelated statements sharing only stop words stay unrelated.
	b3 := &Block{Content: "It is at the office"}
	if b3.ContradictionCheck("The report is in the archive") {
		t.Error("unrelated statements matched through stop words")
	}
}
