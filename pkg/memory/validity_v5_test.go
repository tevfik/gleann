package memory

import "testing"

// v5 repair tests (fourth independent review, memory-fixes-v4.gemini.md):
//  1. Standard Turkish orthography with diacritics (aç/açma, seç/seçme,
//     çık/çıkma, gör/görme, böl/bölme, çöz/çözme) must negate exactly like
//     the ASCII transliterations; the ASCII-only lexicon, UTF-8 byte-length
//     stem checks and ASCII-only case suffixes made them all miss.
//  2. English acronyms and proper names colliding with lexicon roots in
//     clause-final position (AC/acme, Al/Alma) must not be flagged as
//     Turkish imperative negations.

// isolateV5Env points HOME and the XDG/Gleann config locations at a temporary
// directory so no test can touch the real home directory. The v5 tests only
// exercise the pure ContradictionCheck heuristic (no store is opened); the
// isolation is belt-and-braces per the repair rules.
func isolateV5Env(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("GLEANN_CONFIG_DIR", dir)
}

// TestDefect_TurkishDiacriticShortStemNegation: genuine 2-3 letter Turkish
// imperatives written in standard orthography (with ç/ğ/ı/ö/ş/ü) must be
// detected symmetrically, bare and with an object, exactly like their ASCII
// transliterations.
func TestDefect_TurkishDiacriticShortStemNegation(t *testing.T) {
	isolateV5Env(t)
	pairs := []struct{ old, new string }{
		{"kapiyi aç", "kapiyi açma"},
		{"kapıyı aç", "kapıyı açma"},
		{"dosyayı seç", "dosyayı seçme"},
		{"seç", "seçme"},
		{"disari çık", "disari çıkma"},
		{"dışarı çık", "dışarı çıkma"},
		{"gör", "görme"},
		{"böl", "bölme"},
		{"çöz", "çözme"},
		// Further 3-letter diacritic roots from the closed lexicon.
		{"çek", "çekme"},
		{"dök", "dökme"},
		{"sür", "sürme"},
		{"yık", "yıkma"},
	}
	for _, p := range pairs {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if !b.ContradictionCheck(dir[1]) {
				t.Errorf("Turkish diacritic short-stem negation missed: ContradictionCheck(%q -> %q) = false, want true", dir[0], dir[1])
			}
		}
	}
}

// TestDefect_TurkishDiacriticLongStemNegation: long stems with diacritics
// negate when a case-marked argument is present, and the verbal-noun
// (nominal) default must survive the diacritic folding: a bare adjective
// modifier + -ma/-me is still a noun phrase, not a negative imperative.
func TestDefect_TurkishDiacriticLongStemNegation(t *testing.T) {
	isolateV5Env(t)
	positive := []struct{ old, new string }{
		{"testleri çalıştır", "testleri çalıştırma"},
		{"dosyayı gönder", "dosyayı gönderme"},
		{"dosyayı oluştur", "dosyayı oluşturma"},
	}
	for _, p := range positive {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if !b.ContradictionCheck(dir[1]) {
				t.Errorf("Turkish diacritic long-stem negation missed: ContradictionCheck(%q -> %q) = false, want true", dir[0], dir[1])
			}
		}
	}
	nominal := []struct{ old, new string }{
		{"Otomatik guncelle", "Otomatik guncelleme"},
		{"Temiz derle", "Temiz derleme"},
		{"Otomatik güncelle", "Otomatik güncelleme"},
	}
	for _, p := range nominal {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if b.ContradictionCheck(dir[1]) {
				t.Errorf("agreeing Turkish verbal noun flagged as contradiction: ContradictionCheck(%q -> %q) = true, want false", dir[0], dir[1])
			}
		}
	}
}

// TestDefect_EnglishAcronymShortStemFalsePositives: English software
// acronyms and proper names that collide with Turkish lexicon roots
// (ac/acme, al/alma) are not Turkish imperative negations, even when
// clause-final on both sides.
func TestDefect_EnglishAcronymShortStemFalsePositives(t *testing.T) {
	isolateV5Env(t)
	pairs := []struct{ old, new string }{
		{"Turn on the AC", "Turn on the acme"},
		{"We enabled AC", "We enabled ACME"},
		{"Enable AC", "Enable ACME"},
		{"We hired Al", "We hired Alma"},
	}
	for _, p := range pairs {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if b.ContradictionCheck(dir[1]) {
				t.Errorf("English acronym/name flagged as Turkish negation: ContradictionCheck(%q -> %q) = true, want false", dir[0], dir[1])
			}
		}
	}
}

// TestDefect_TurkishVowelHarmonyGuard: the -ma/-me suffix must agree with
// the stem's last vowel under Turkish back/front vowel harmony. Back-
// unambiguous a/ı take only -ma, front-unambiguous e/ö/ü take only -me;
// ASCII-ambiguous i/o/u allow either so transliterations keep working.
func TestDefect_TurkishVowelHarmonyGuard(t *testing.T) {
	isolateV5Env(t)
	disharmonic := []struct{ old, new string }{
		{"kapiyi ac", "kapiyi acme"},     // a + me: "acme" is not Turkish negation
		{"dosyayi sec", "dosyayi secma"}, // e + ma
		{"gör", "görma"},                 // ö + ma
		{"aç", "açme"},                   // a + me
	}
	for _, p := range disharmonic {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if b.ContradictionCheck(dir[1]) {
				t.Errorf("disharmonic -ma/-me flagged as Turkish negation: ContradictionCheck(%q -> %q) = true, want false", dir[0], dir[1])
			}
		}
	}
	// ASCII transliterations with rounded/neutral vowels keep both suffix
	// readings (o may stand for ö, u for ü, i for ı or i).
	translit := []struct{ old, new string }{
		{"gor", "gorme"},
		{"bol", "bolme"},
		{"coz", "cozme"},
		{"dok", "dokme"},
		{"sur", "surme"},
	}
	for _, p := range translit {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if !b.ContradictionCheck(dir[1]) {
				t.Errorf("ASCII transliteration negation broken by vowel harmony: ContradictionCheck(%q -> %q) = false, want true", dir[0], dir[1])
			}
		}
	}
	// Full harmony sweep: every short-stem pair listed in the repair task
	// must stay green (bare clause-final imperatives, both directions).
	sweep := []struct{ old, new string }{
		{"bul", "bulma"},
		{"oku", "okuma"},
		{"ol", "olma"},
		{"uy", "uyma"},
		{"kur", "kurma"},
		{"tut", "tutma"},
		{"koy", "koyma"},
		{"dur", "durma"},
		{"duy", "duyma"},
		{"gir", "girme"},
		{"git", "gitme"},
		{"sil", "silme"},
		{"in", "inme"},
		{"it", "itme"},
		{"yak", "yakma"},
		{"yik", "yikma"},
		{"cik", "cikma"},
		{"ac", "acma"},
		{"aç", "açma"},
		{"seç", "seçme"},
		{"gör", "görme"},
		{"böl", "bölme"},
		{"çöz", "çözme"},
	}
	for _, p := range sweep {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if !b.ContradictionCheck(dir[1]) {
				t.Errorf("Turkish pair broken by vowel harmony: ContradictionCheck(%q -> %q) = false, want true", dir[0], dir[1])
			}
		}
	}
}

// TestDefect_TurkishImperativeEnglishContextGuard: the English function-word
// guard must only reject English context; genuine Turkish imperatives with
// Turkish arguments (and bare diacritic imperatives) stay green.
func TestDefect_TurkishImperativeEnglishContextGuard(t *testing.T) {
	isolateV5Env(t)
	pairs := []struct{ old, new string }{
		{"Bu paketi at", "Bu paketi atma"},
		{"Kapiyi it", "Kapiyi itme"},
		{"Asagi in", "Asagi inme"},
		{"Resmi duvara as", "Resmi duvara asma"},
		{"kapiyi ac", "kapiyi acma"},
		{"kitabi al", "kitabi alma"},
		{"gör", "görme"},
		{"seç", "seçme"},
	}
	for _, p := range pairs {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if !b.ContradictionCheck(dir[1]) {
				t.Errorf("Turkish imperative rejected by English context guard: ContradictionCheck(%q -> %q) = false, want true", dir[0], dir[1])
			}
		}
	}
}

// TestFoldTurkish: the diacritic fold must map the full Turkish alphabet to
// ASCII, handle the dotted/dotless I family without leaving a combining dot
// (strings.ToLower("İ") alone yields i + U+0307), and leave ASCII untouched.
func TestFoldTurkish(t *testing.T) {
	isolateV5Env(t)
	cases := []struct{ in, want string }{
		{"çÇğĞıIİöÖşŞüÜ", "ccggiiioossuu"},
		{"AÇMA", "acma"},
		{"Seçme", "secme"},
		{"KAPIYI", "kapiyi"},
		{"kapıyı", "kapiyi"},
		{"İSTANBUL", "istanbul"},
		{"IŞIK", "isik"},
		{"çalıştırma", "calistirma"},
		{"gönderme", "gonderme"},
		{"oluşturma", "olusturma"},
		{"hello world", "hello world"},
		{"", ""},
	}
	for _, c := range cases {
		if got := foldTurkish(c.in); got != c.want {
			t.Errorf("foldTurkish(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTurkishNegationHarmonic: back-unambiguous a/ı take only -ma,
// front-unambiguous e/ö/ü take only -me, ASCII-ambiguous i/o/u take either,
// and the check must use the ORIGINAL (unfolded) stem.
func TestTurkishNegationHarmonic(t *testing.T) {
	isolateV5Env(t)
	cases := []struct {
		stem, suffix string
		want         bool
	}{
		{"ac", "ma", true},
		{"ac", "me", false}, // acme is not Turkish negation
		{"aç", "ma", true},
		{"aç", "me", false},
		{"sec", "me", true},
		{"sec", "ma", false},
		{"seç", "me", true},
		{"gör", "me", true},
		{"gör", "ma", false}, // folded gör->gor would wrongly allow -ma
		{"böl", "me", true},
		{"çöz", "me", true},
		{"çık", "ma", true},
		{"çık", "me", false},
		{"yık", "ma", true},
		{"sür", "me", true},
		{"sür", "ma", false},
		// ASCII-ambiguous vowels allow either suffix.
		{"gor", "me", true},
		{"gor", "ma", true},
		{"bol", "me", true},
		{"coz", "me", true},
		{"dok", "me", true},
		{"sur", "me", true},
		{"sur", "ma", true},
		{"gir", "me", true},
		{"yik", "ma", true},
		{"bul", "ma", true},
		{"oku", "ma", true},
		// Long stems follow the same rule.
		{"çalıştır", "ma", true},
		{"çalıştır", "me", false},
		{"gönder", "me", true},
		{"gönder", "ma", false},
		{"oluştur", "ma", true},
		{"guncelle", "me", true},
	}
	for _, c := range cases {
		if got := turkishNegationHarmonic(c.stem, c.suffix); got != c.want {
			t.Errorf("turkishNegationHarmonic(%q, %q) = %v, want %v", c.stem, c.suffix, got, c.want)
		}
	}
}

// TestHasEnglishFunctionWord: the guard fires on unmistakable English
// function words, skips the two mechanism tokens, and stays quiet on
// Turkish context words.
func TestHasEnglishFunctionWord(t *testing.T) {
	isolateV5Env(t)
	cases := []struct {
		tokens             []string
		excludeA, excludeB string
		want               bool
	}{
		{[]string{"we", "enabled", "ac"}, "ac", "acme", true},
		{[]string{"turn", "on", "the", "ac"}, "ac", "acme", true},
		{[]string{"we", "hired", "al"}, "al", "alma", true},
		{[]string{"bu", "paketi", "at"}, "at", "atma", false},
		{[]string{"kapiyi", "ac"}, "ac", "acma", false},
		{[]string{"kitabi", "al"}, "al", "alma", false},
		{[]string{"testleri", "yaz"}, "yaz", "yazma", false},
		{[]string{"dosyayi", "sec"}, "sec", "secme", false},
		{[]string{"gör"}, "gör", "görme", false},
		// Mechanism tokens never count as context evidence.
		{[]string{"the"}, "the", "x", false},
		{[]string{"we"}, "x", "we", false},
		{nil, "a", "b", false},
	}
	for _, c := range cases {
		if got := hasEnglishFunctionWord(c.tokens, c.excludeA, c.excludeB); got != c.want {
			t.Errorf("hasEnglishFunctionWord(%v, %q, %q) = %v, want %v", c.tokens, c.excludeA, c.excludeB, got, c.want)
		}
	}
}
