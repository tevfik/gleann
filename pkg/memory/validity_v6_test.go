package memory

import "testing"

// v6 repair tests (fifth independent review, memory-fixes-v5.gemini.md):
// hasEnglishFunctionWord omitted the English 1st-person singular subject
// pronoun "i", the object pronouns "me", "us", "him", "them" and the
// indefinite article "a" — all present in isContradictionStopWord. English
// statements such as "I hired Al" / "I hired Alma" therefore bypassed the
// English context guard, were misread as Turkish imperative negations of
// the clause-final stem "al", and produced false-positive contradictions.

// isolateV6Env points HOME and the XDG/Gleann config locations at a temporary
// directory so no test can touch the real home directory. The v6 tests only
// exercise the pure ContradictionCheck heuristic (no store is opened); the
// isolation is belt-and-braces per the repair rules.
func isolateV6Env(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("GLEANN_CONFIG_DIR", dir)
}

// TestDefect_EnglishPronounAlAlmaFalsePositives: all 8 fifth-review pairs
// (subject pronoun "i", object pronouns "me"/"us"/"him"/"them") plus the
// indefinite-article pair ("a") are independent English statements about
// different persons, not Turkish imperative negations of "al". Both
// directions must evaluate to false.
func TestDefect_EnglishPronounAlAlmaFalsePositives(t *testing.T) {
	isolateV6Env(t)
	pairs := []struct{ old, new string }{
		{"I hired Al", "I hired Alma"},
		{"I know Al", "I know Alma"},
		{"I like Al", "I like Alma"},
		{"I met Al", "I met Alma"},
		{"Contact me Al", "Contact me Alma"},
		{"Tell us Al", "Tell us Alma"},
		{"Introduce him Al", "Introduce him Alma"},
		{"Introduce them Al", "Introduce them Alma"},
		// Indefinite article "a" (already in isContradictionStopWord).
		{"Recommend a mentor Al", "Recommend a mentor Alma"},
	}
	for _, p := range pairs {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if b.ContradictionCheck(dir[1]) {
				t.Errorf("English pronoun/article context flagged as Turkish negation: ContradictionCheck(%q -> %q) = true, want false", dir[0], dir[1])
			}
		}
	}
}

// TestDefect_TurkishImperativesSurvivePronounGuard: the six mandatory
// Turkish imperative pairs (plus "paketi al/alma" and bare "al/alma") must
// stay green in both directions: none of their context words collides with
// the newly listed English function words i/me/us/him/them/a. This test is
// green before AND after the fix (regression guard, not a defect test).
func TestDefect_TurkishImperativesSurvivePronounGuard(t *testing.T) {
	isolateV6Env(t)
	pairs := []struct{ old, new string }{
		{"bu paketi at", "bu paketi atma"},
		{"kapiyi it", "kapiyi itme"},
		{"asagi in", "asagi inme"},
		{"resmi duvara as", "resmi duvara asma"},
		{"kapiyi ac", "kapiyi acma"},
		{"kitabi al", "kitabi alma"},
		{"paketi al", "paketi alma"},
		{"al", "alma"},
	}
	for _, p := range pairs {
		for _, dir := range [][2]string{{p.old, p.new}, {p.new, p.old}} {
			b := &Block{Content: dir[0]}
			if !b.ContradictionCheck(dir[1]) {
				t.Errorf("Turkish imperative rejected by pronoun guard: ContradictionCheck(%q -> %q) = false, want true", dir[0], dir[1])
			}
		}
	}
}

// TestHasEnglishFunctionWord_V6Pronouns: the guard must fire on the newly
// listed pronouns and article, still skip the two mechanism tokens, stay
// quiet on Turkish context words, and leave the o/on/her behavior unchanged
// ("o" is not an English guard word; "on" and "her" were already listed).
func TestHasEnglishFunctionWord_V6Pronouns(t *testing.T) {
	isolateV6Env(t)
	cases := []struct {
		tokens             []string
		excludeA, excludeB string
		want               bool
	}{
		// New pronouns/article fire as English context evidence.
		{[]string{"i", "hired", "al"}, "al", "alma", true},
		{[]string{"i", "know", "al"}, "al", "alma", true},
		{[]string{"contact", "me", "al"}, "al", "alma", true},
		{[]string{"tell", "us", "al"}, "al", "alma", true},
		{[]string{"introduce", "him", "al"}, "al", "alma", true},
		{[]string{"introduce", "them", "al"}, "al", "alma", true},
		{[]string{"recommend", "a", "mentor", "al"}, "al", "alma", true},
		// Mechanism-token exclusion still applies to the new words.
		{[]string{"i"}, "i", "x", false},
		{[]string{"me"}, "x", "me", false},
		{[]string{"a"}, "a", "b", false},
		// Turkish context stays quiet (no collision with the new words).
		{[]string{"bu", "paketi", "at"}, "at", "atma", false},
		{[]string{"kapiyi", "it"}, "it", "itme", false},
		{[]string{"asagi", "in"}, "in", "inme", false},
		{[]string{"resmi", "duvara", "as"}, "as", "asma", false},
		{[]string{"kapiyi", "ac"}, "ac", "acma", false},
		{[]string{"kitabi", "al"}, "al", "alma", false},
		// o/on/her behavior unchanged from v5.
		{[]string{"o", "paketi", "al"}, "al", "alma", false},
		{[]string{"on", "paketi", "al"}, "al", "alma", true},
		{[]string{"her", "paketi", "al"}, "al", "alma", true},
	}
	for _, c := range cases {
		if got := hasEnglishFunctionWord(c.tokens, c.excludeA, c.excludeB); got != c.want {
			t.Errorf("hasEnglishFunctionWord(%v, %q, %q) = %v, want %v", c.tokens, c.excludeA, c.excludeB, got, c.want)
		}
	}
}
