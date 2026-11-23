package memory

import (
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ValidityScore computes a Bayesian-inspired confidence score for a memory block.
// Returns a value between 0.0 (unreliable) and 1.0 (highly reliable).
//
// The score combines:
//   - Confirmation ratio: confirms / (confirms + conflicts)
//   - Staleness decay: exponential decay based on age and access recency
//     (short and medium tiers only)
//   - Access frequency: blocks used more often are scored higher
//
// Long-term facts never decay by age: the long tier stays changeable through
// explicit edits, Confirms/Conflicts and deletion, not through silence. An
// unconfirmed TierLong block keeps its 0.5 prior at any age; heavy conflict
// can still drop it below the 0.2 prune threshold.
func (b *Block) ValidityScore() float64 {
	// Prior: start with 50% confidence (uninformative prior).
	const priorAlpha = 1.0 // pseudo-confirm
	const priorBeta = 1.0  // pseudo-conflict

	alpha := priorAlpha + float64(b.Confirms)
	beta := priorBeta + float64(b.Conflicts)

	// Bayesian mean of Beta(alpha, beta).
	confirmScore := alpha / (alpha + beta)

	// Staleness decay: halve confidence every 90 days without access.
	// TierLong is exempt — age alone must never retire a long-term fact.
	decayFactor := 1.0
	if b.Tier != TierLong {
		const halfLifeDays = 90.0
		age := time.Since(b.UpdatedAt)
		if b.LastAccessedAt != nil && b.LastAccessedAt.After(b.UpdatedAt) {
			age = time.Since(*b.LastAccessedAt)
		}
		decayFactor = math.Pow(0.5, age.Hours()/(halfLifeDays*24.0))
	}

	// Access frequency boost (diminishing returns).
	accessBoost := 1.0
	if b.AccessCount > 0 {
		accessBoost = 1.0 + 0.1*math.Log1p(float64(b.AccessCount))
	}

	score := confirmScore * decayFactor * accessBoost
	if score > 1.0 {
		score = 1.0
	}
	return score
}

// ValidityLabel returns a human-readable label for the block's validity score.
func (b *Block) ValidityLabel() string {
	score := b.ValidityScore()
	switch {
	case score >= 0.8:
		return "high"
	case score >= 0.5:
		return "medium"
	case score >= 0.2:
		return "low"
	default:
		return "unreliable"
	}
}

// IsStale returns true if the block hasn't been accessed or updated recently.
// The staleness threshold depends on the tier. Long-term facts never go stale
// by age; they change only through explicit edits, conflicts or deletion.
func (b *Block) IsStale() bool {
	var threshold time.Duration
	switch b.Tier {
	case TierShort:
		threshold = 24 * time.Hour // 1 day
	case TierMedium:
		threshold = 30 * 24 * time.Hour // 30 days
	case TierLong:
		return false // permanent archive: no age-based staleness
	default:
		threshold = 90 * 24 * time.Hour
	}

	ref := b.UpdatedAt
	if b.LastAccessedAt != nil && b.LastAccessedAt.After(ref) {
		ref = *b.LastAccessedAt
	}
	return time.Since(ref) > threshold
}

// Confirm marks this block as confirmed by new evidence.
func (b *Block) Confirm() {
	b.Confirms++
	b.UpdatedAt = time.Now()
}

// Conflict marks this block as conflicting with new information.
func (b *Block) Conflict() {
	b.Conflicts++
	b.UpdatedAt = time.Now()
}

// RecordAccess updates the access tracking for this block.
func (b *Block) RecordAccess() {
	b.AccessCount++
	now := time.Now()
	b.LastAccessedAt = &now
}

// ContradictionCheck performs a simple text-similarity check for potential
// contradictions between this block and new content.
// Returns true if the new content likely contradicts this block.
//
// The heuristic has two paths:
//
//  1. Polarity flip: exactly one side carries a negative mechanism (a
//     negation marker such as "not"/"degil", the negative half of an
//     antonym pair such as "cannot"/"can", or a Turkish -ma/-me negation
//     suffix) while the other side is positive. A token that belongs to a
//     negative pattern never counts as its positive counterpart, so two
//     agreeing negative statements do not fire. After removing the
//     mechanism tokens and stop words, the remaining content must be equal
//     as a multiset: word order is ignored, up to two extra words on one
//     side (a short elaboration such as "today" or "we use PostgreSQL")
//     are tolerated when the other side is fully contained and shares at
//     least one topic word (an empty residual never matches an unrelated
//     statement), and one substituted word is tolerated when at least one
//     significant topic word is shared. Anything more divergent is a
//     different statement, not a contradiction.
//  2. Value flip: with no polarity mechanism present, the texts differ by
//     exactly one substituted token and both tokens contain digits
//     (versions, ports, counts), anchored by a shared topic word.
//
// For LLM-powered contradiction detection, use the manager's CheckContradiction method.
func (b *Block) ContradictionCheck(newContent string) bool {
	// Identical text cannot contradict itself; a rewrite is a confirmation.
	if normalizeContent(b.Content) == normalizeContent(newContent) {
		return false
	}
	oldTokens := contradictionTokens(b.Content)
	newTokens := contradictionTokens(newContent)

	mech, oldNeg, newNeg, matched := polarityMechanisms(oldTokens, newTokens)
	if matched {
		// Exactly one side may be negative. Two agreeing negatives (or a
		// marker on both sides) are not a contradiction.
		if oldNeg != newNeg &&
			residualsMatch(residualTokens(oldTokens, mech), residualTokens(newTokens, mech)) {
			return true
		}
		return false
	}
	return valueFlip(residualTokens(oldTokens, nil), residualTokens(newTokens, nil))
}

// contradictionTokens splits content into lowercase whole words. Internal
// apostrophes are kept so negations like "don't" stay one token, but
// leading and trailing single quotes are trimmed so quoted values
// ('true') normalize to their bare form. A minus directly prefixed to a
// digit at a word boundary is kept as a numeric sign ("-5"); a hyphen
// inside a word ("read-only", "utf-8") still splits.
func contradictionTokens(content string) []string {
	content = strings.ReplaceAll(content, "’", "'")
	runes := []rune(strings.ToLower(content))
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) == 0 {
			return
		}
		t := strings.Trim(string(cur), "'")
		if t != "" {
			out = append(out, t)
		}
		cur = cur[:0]
	}
	for i, r := range runes {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'':
			cur = append(cur, r)
		case r == '-' && i+1 < len(runes) && unicode.IsDigit(runes[i+1]) &&
			(i == 0 || !unicode.IsLetter(runes[i-1]) && !unicode.IsDigit(runes[i-1]) && runes[i-1] != '\''):
			// Numeric sign: a minus at a word boundary directly before a
			// digit ("-5", "(-5)"). Mid-word hyphens ("read-only",
			// "utf-8", "5-3") remain delimiters.
			flush()
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// polarityMechanisms finds the negation mechanisms present in the two
// texts. It returns the set of mechanism tokens to strip from the
// residuals, whether each side is negative, and whether any mechanism
// matched at all. A side that contains both halves of an antonym pair is
// ambiguous and does not establish polarity for that pair.
func polarityMechanisms(oldTokens, newTokens []string) (mech map[string]bool, oldNeg, newNeg, matched bool) {
	mech = make(map[string]bool)
	oldSet := tokenSet(oldTokens)
	newSet := tokenSet(newTokens)

	// Negation markers: the side carrying the marker is negative.
	for _, marker := range []string{"not", "no", "degil"} {
		if oldSet[marker] || newSet[marker] {
			matched = true
			mech[marker] = true
			oldNeg = oldNeg || oldSet[marker]
			newNeg = newNeg || newSet[marker]
		}
	}

	// Antonym pairs: the negative half never counts as the positive half
	// on the same side, so "cannot" does not fire against itself.
	antonyms := [][2]string{
		{"cannot", "can"},
		{"can't", "can"},
		{"never", "always"},
		{"don't", "do"},
		{"doesn't", "does"},
		{"didn't", "did"},
		{"isn't", "is"},
		{"aren't", "are"},
		{"wasn't", "was"},
		{"weren't", "were"},
		{"won't", "will"},
		{"shouldn't", "should"},
		{"mustn't", "must"},
		{"couldn't", "could"},
		{"wouldn't", "would"},
		{"hasn't", "has"},
		{"haven't", "have"},
		{"disable", "enable"},
		{"disabled", "enabled"},
		{"false", "true"},
		{"deprecated", "recommended"},
		{"removed", "added"},
		{"kapali", "acik"},
	}
	for _, pair := range antonyms {
		neg, pos := pair[0], pair[1]
		oldHasNeg := oldSet[neg] && !oldSet[pos]
		oldHasPos := oldSet[pos] && !oldSet[neg]
		newHasNeg := newSet[neg] && !newSet[pos]
		newHasPos := newSet[pos] && !newSet[neg]
		if (oldHasNeg && newHasPos) || (oldHasPos && newHasNeg) {
			matched = true
			mech[neg] = true
			mech[pos] = true
			oldNeg = oldNeg || oldHasNeg
			newNeg = newNeg || newHasNeg
		}
	}

	// Turkish morphological negation: calistir -> calistirma (-ma/-me).
	// The suffixed form is ambiguous between a negative imperative
	// ("testleri calistirma" = do not run the tests) and a verbal noun
	// ("otomatik guncelleme" = automatic update); turkishImperativeNegation
	// decides which reading the context supports. Only the imperative
	// reading establishes negative polarity on the suffixed side.
	for a := range oldSet {
		if utf8.RuneCountInString(a) < 2 {
			continue
		}
		// at/it/in/as are English stop words but also Turkish 2-letter
		// imperative roots; a stop-word stem is only skipped when it is
		// not a short verb root. turkishImperativeNegation still requires
		// clause-final position on both sides, so a non-final English
		// preposition never forms a mechanism.
		if isContradictionStopWord(a) && !isTurkishShortVerbStem(a) {
			continue
		}
		for _, suffix := range []string{"ma", "me"} {
			b := a + suffix
			if newSet[b] && turkishImperativeNegation(a, b, oldTokens, newTokens) {
				matched = true
				mech[a] = true
				mech[b] = true
				newNeg = true
			}
		}
	}
	for a := range newSet {
		if utf8.RuneCountInString(a) < 2 {
			continue
		}
		// Same stop-word exemption as the oldSet loop above, symmetric
		// for a negated form stored in the old block.
		if isContradictionStopWord(a) && !isTurkishShortVerbStem(a) {
			continue
		}
		for _, suffix := range []string{"ma", "me"} {
			b := a + suffix
			if oldSet[b] && turkishImperativeNegation(a, b, oldTokens, newTokens) {
				matched = true
				mech[a] = true
				mech[b] = true
				oldNeg = true
			}
		}
	}
	return mech, oldNeg, newNeg, matched
}

// turkishImperativeNegation reports whether a cross-side pair (stem,
// stem+ma/me) should be read as a negative imperative rather than a verbal
// noun. The two forms are morphologically identical in Turkish, so the
// decision comes from context:
//
//   - The suffix must agree with the stem's last vowel under Turkish
//     back/front vowel harmony (turkishNegationHarmonic): a/ı take -ma,
//     e/ö/ü take -me, ASCII-ambiguous i/o/u take either. This rejects
//     English lookalikes such as ac+me ("acme") and sec+ma outright.
//   - A 2-3 letter stem (measured in runes, so diacritic roots such as
//     aç, seç, çık qualify) qualifies only when it is in the closed lexicon
//     of common Turkish imperative roots (isTurkishShortVerbStem: yaz,
//     oku, yap, sil, al, gel, git, at, it, in, as, ...). Arbitrary short
//     words (dog/dogma, car/carma) are not verbs. The imperative reading
//     additionally requires clause-final position on both sides: the bare
//     stem ends one text and the negated form ends the other, so a
//     non-final "al" in "al is ready" is not an imperative. An unmistakable
//     English function word on either side (hasEnglishFunctionWord) also
//     rejects the short-stem reading: "We hired Al" / "We hired Alma" is
//     English, not a Turkish imperative pair.
//   - A longer stem (guncelle, derle, calistir, çalıştır, gönder, ...)
//     needs clausal evidence: an argument carrying an overt Turkish case
//     suffix somewhere in the two texts (plural+case, buffered -(y)I/-(y)A,
//     or an apostrophe-separated case on a borrowing). "testleri calistirma"
//     has the accusative object testleri; "otomatik guncelleme" and "temiz
//     derleme" have only a bare adjective modifier, so the -ma/-me form is
//     a verbal noun (a noun phrase) and establishes no polarity.
//
// Without POS tagging this is conservative: when no case evidence is
// found the nominal reading wins, preferring a missed contradiction over
// flagging two agreeing statements.
func turkishImperativeNegation(stem, suffixed string, oldTokens, newTokens []string) bool {
	// Vowel harmony inspects the original (unfolded) stem: folding would
	// turn ö/ü into ASCII-ambiguous o/u and lose the front-vowel evidence.
	if !turkishNegationHarmonic(stem, strings.TrimPrefix(suffixed, stem)) {
		return false
	}
	if utf8.RuneCountInString(stem) <= 3 {
		if !isTurkishShortVerbStem(stem) {
			return false
		}
		if hasEnglishFunctionWord(oldTokens, stem, suffixed) ||
			hasEnglishFunctionWord(newTokens, stem, suffixed) {
			return false
		}
		return (clauseFinalToken(oldTokens) == stem && clauseFinalToken(newTokens) == suffixed) ||
			(clauseFinalToken(newTokens) == stem && clauseFinalToken(oldTokens) == suffixed)
	}
	for _, t := range oldTokens {
		if t != stem && t != suffixed && turkishCaseMarked(t) {
			return true
		}
	}
	for _, t := range newTokens {
		if t != stem && t != suffixed && turkishCaseMarked(t) {
			return true
		}
	}
	return false
}

// turkishCaseMarked reports whether t carries an overt Turkish case
// inflection. Turkish diacritics are folded to ASCII first (foldTurkish),
// so both standard orthography (kapıyı, dosyayı, ları, Ankara'yı) and
// ASCII transliteration (kapiyi, dosyayi, lari, Ankara'yi) match the same
// reliable suffixes: plural+case
// (-leri/-lari/-lere/-lara/-lerde/-larda/-lerden/-lardan), the buffered
// accusative/dative -(y)i/-(y)u/-(y)a/-(y)e on vowel-final stems, or an
// apostrophe-separated case suffix on a borrowing or proper noun
// ("cache'i", "AWS'e", "Docker'da"). Bare word-final vowels are NOT
// treated as case markers: too many English words end in a/e/i/u
// ("code", "cache", "data", "node"), and vowel-final adjectives ("yeni",
// "hizli") are lexically ambiguous without a dictionary.
func turkishCaseMarked(t string) bool {
	// Fold diacritics so standard Turkish orthography hits the same
	// suffix checks as ASCII transliteration (kapıyı -> kapiyi -> -yi).
	// ASCII tokens are already lowercase and unchanged by the fold.
	t = foldTurkish(t)
	// Apostrophe-separated case on a borrowing/proper noun. English
	// contractions end in consonants ("don't", "isn't") or "s" ("it's"),
	// never in a bare case vowel.
	if i := strings.LastIndexByte(t, '\''); i >= 0 && i+1 < len(t) {
		switch t[i+1:] {
		case "i", "u", "a", "e", "ya", "ye", "yi", "yu",
			"da", "de", "ta", "te", "dan", "den", "tan", "ten",
			"na", "ne", "nda", "nde":
			return true
		}
	}
	// Plural + case: -leri/-lari/-lere/-lara/-lerde/-larda/-lerden/-lardan.
	for _, s := range []string{"lerden", "lardan", "leri", "lari", "lere", "lara", "lerde", "larda"} {
		if len(t) > len(s) && strings.HasSuffix(t, s) {
			return true
		}
	}
	// Buffered accusative/dative -(y)i/-(y)u/-(y)a/-(y)e; a stem of at
	// least two letters keeps out English "dye"/"bye".
	for _, s := range []string{"yi", "yu", "ya", "ye"} {
		if len(t) > len(s)+1 && strings.HasSuffix(t, s) {
			return true
		}
	}
	return false
}

// isTurkishShortVerbStem reports whether stem is one of the common 2-3
// letter Turkish imperative verb roots. Matching folds Turkish diacritics
// to ASCII (foldTurkish), so both standard orthography (aç, seç, çık, gör,
// böl, çöz, çek, dök, sür, yık) and ASCII transliteration (ac, sec, cik,
// gor, bol, coz, cek, dok, sur, yik) hit the same closed list. The list is
// closed on purpose: -ma/-me negation on short stems is trusted only for
// these roots, so English lookalikes such as dog/dogma or car/carma never
// fire.
func isTurkishShortVerbStem(stem string) bool {
	switch foldTurkish(stem) {
	case "al", "at", "et", "it", "in", "as", "ac", "ek", "ol", "uy",
		"yaz", "oku", "yap", "sil", "gel", "git", "bak", "gor", "kes", "sec",
		"ver", "dur", "kur", "tut", "koy", "cek", "bas", "bul", "kal", "sat",
		"sor", "yak", "yik", "dok", "bol", "coz", "diz", "gez", "gir", "cik",
		"kaz", "say", "sev", "sur", "tak", "duy":
		return true
	}
	return false
}

// foldTurkish maps Turkish diacritics to their ASCII counterparts and
// lowercases the result, so standard Turkish orthography and ASCII
// transliteration compare equal (aç -> ac, seç -> sec, kapıyı -> kapiyi).
// The dotted/dotless I family is mapped explicitly BEFORE ToLower:
// strings.ToLower alone would turn İ (U+0130) into i plus a combining dot
// above (U+0307), and Turkish uppercase I is the uppercase of dotless ı,
// so I, İ and ı all fold to plain i.
func foldTurkish(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 'ç', 'Ç':
			b.WriteRune('c')
		case 'ğ', 'Ğ':
			b.WriteRune('g')
		case 'ı', 'I', 'İ':
			b.WriteRune('i')
		case 'ö', 'Ö':
			b.WriteRune('o')
		case 'ş', 'Ş':
			b.WriteRune('s')
		case 'ü', 'Ü':
			b.WriteRune('u')
		default:
			b.WriteRune(r)
		}
	}
	return strings.ToLower(b.String())
}

// turkishNegationHarmonic reports whether the negative suffix -ma/-me
// agrees with the stem's last vowel under Turkish back/front vowel
// harmony. The stem is inspected UNFOLDED: back-unambiguous a/ı take only
// -ma and front-unambiguous e/ö/ü take only -me, so ac+me ("acme"),
// sec+ma and gör+ma are rejected while aç+ma, seç+me and gör+me pass.
// The ASCII-ambiguous vowels i/o/u allow either suffix because a
// transliteration may stand for ı/i, o/ö or u/ü (gor/gorme, bol/bolme,
// coz/cozme, dok/dokme, sur/surme keep working). A stem without a vowel
// imposes no constraint.
func turkishNegationHarmonic(stem, suffix string) bool {
	var last rune
	for _, r := range stem {
		switch r {
		case 'a', 'e', 'i', 'o', 'u', 'ı', 'ö', 'ü':
			last = r
		}
	}
	switch last {
	case 'a', 'ı':
		return suffix == "ma"
	case 'e', 'ö', 'ü':
		return suffix == "me"
	default:
		return true
	}
}

// hasEnglishFunctionWord reports whether tokens contain an unmistakable
// English function word, ignoring the two mechanism tokens excludeA and
// excludeB. It is the context guard for short-stem Turkish imperative
// negation: "We hired Al" / "We hired Alma" and "Turn on the AC" / "Turn
// on the acme" are English sentences, not Turkish imperatives. The pronoun
// coverage includes the 1st-person singular subject "i" and the object
// pronouns "me", "us", "him", "them", plus the indefinite article "a" (all
// also listed in isContradictionStopWord), so "I hired Al" / "I hired Alma"
// and "Contact me Al" / "Contact me Alma" are rejected as English as well.
// at/it/in/as are deliberately absent (they are Turkish 2-letter roots that
// double as English prepositions). A few listed words also have Turkish
// readings (on "ten", her "every"), so a Turkish sentence using them loses
// the short-stem reading — the conservative direction (a miss, not a false
// positive).
func hasEnglishFunctionWord(tokens []string, excludeA, excludeB string) bool {
	for _, t := range tokens {
		if t == excludeA || t == excludeB {
			continue
		}
		switch t {
		case "the", "we", "our", "my", "your", "his", "her", "their", "they",
			"you", "she", "he", "i", "me", "us", "him", "them",
			"this", "that", "these", "those", "was", "were",
			"have", "has", "had", "been", "is", "are", "to", "from", "with",
			"for", "of", "on", "and", "or", "an", "a":
			return true
		}
	}
	return false
}

// clauseFinalToken returns the last token of a text, or "" when empty.
func clauseFinalToken(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	return tokens[len(tokens)-1]
}

// tokenSet builds a whole-token membership set.
func tokenSet(tokens []string) map[string]bool {
	set := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		set[t] = true
	}
	return set
}

// residualTokens removes mechanism tokens and stop words, leaving the
// content words that carry the topic.
func residualTokens(tokens []string, mech map[string]bool) []string {
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if mech[t] || isContradictionStopWord(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// isContradictionStopWord reports whether a token is a function word that
// carries no topic meaning for the contradiction comparison. Note that
// "at", "it", "in" and "as" double as Turkish 2-letter imperative roots;
// polarityMechanisms exempts them as candidate stems (see
// isTurkishShortVerbStem) but they remain stop words here, so English
// residual filtering is unchanged.
func isContradictionStopWord(t string) bool {
	switch t {
	case "a", "an", "the", "is", "are", "was", "were", "be", "been", "being", "am",
		"i", "we", "you", "he", "she", "it", "they", "me", "him", "her", "us", "them",
		"my", "your", "his", "its", "our", "their", "this", "that", "these", "those",
		"do", "does", "did", "done", "will", "would", "shall", "should", "can", "could",
		"may", "might", "must", "have", "has", "had", "having",
		"of", "to", "in", "on", "at", "by", "for", "with", "from", "as", "into", "over", "under",
		"and", "or", "but", "if", "then", "than", "so", "such", "nor", "too", "very", "just",
		"all", "both", "each", "some", "any", "there", "here", "when", "where", "how", "what", "which", "who",
		"also", "again", "once", "before", "after", "while", "during", "until",
		// Turkish function words (ASCII transliteration, as stored by the CLI).
		"bir", "ve", "ile", "icin", "bu", "su", "o", "da", "de", "daha", "cok",
		"gibi", "kadar", "tum", "mi", "mu", "ise", "veya", "ama", "ancak", "hem":
		return true
	}
	return false
}

// residualsMatch compares two residual token multisets. Equal multisets
// match (word order is ignored); so does one residual being a subset of
// the other with at most two extra words (a short elaboration, e.g. an
// added adverb such as "today" or a trailing clause like "we use
// PostgreSQL") when at least one residual topic word is shared, or one
// substituted word when at least one significant topic word (>4 chars)
// is shared.
func residualsMatch(a, b []string) bool {
	extraA, extraB := multisetDiff(a, b)
	switch {
	case len(extraA) == 0 && len(extraB) == 0:
		return true
	case len(extraA) <= 2 && len(extraB) == 0, len(extraA) == 0 && len(extraB) <= 2:
		// An elaboration must share at least one topic word with its
		// base. One residual is fully contained in the other here, so
		// the shared count is the size of the contained residual: an
		// empty residual (only stop words and mechanism tokens, e.g.
		// "It is enabled") can never match an unrelated statement such
		// as "Docker containers are disabled".
		return len(a) > 0 && len(b) > 0
	case len(extraA) == 1 && len(extraB) == 1:
		return sharedSignificantWords(a, b) >= 1
	}
	return false
}

// multisetDiff returns the tokens of a not covered by b and vice versa.
func multisetDiff(a, b []string) (extraA, extraB []string) {
	counts := make(map[string]int, len(b))
	for _, t := range b {
		counts[t]++
	}
	for _, t := range a {
		if counts[t] > 0 {
			counts[t]--
		} else {
			extraA = append(extraA, t)
		}
	}
	for t, n := range counts {
		for i := 0; i < n; i++ {
			extraB = append(extraB, t)
		}
	}
	return extraA, extraB
}

// valueFlip detects a numeric value change (version, port, count) between
// otherwise identical statements: exactly one substituted token, both
// containing digits, anchored by at least one shared significant word.
func valueFlip(a, b []string) bool {
	extraA, extraB := multisetDiff(a, b)
	if len(extraA) != 1 || len(extraB) != 1 {
		return false
	}
	if !strings.ContainsAny(extraA[0], "0123456789") || !strings.ContainsAny(extraB[0], "0123456789") {
		return false
	}
	return sharedSignificantWords(a, b) >= 1
}

// sharedSignificantWords counts distinct words longer than four characters
// that occur in both token lists.
func sharedSignificantWords(a, b []string) int {
	set := make(map[string]struct{}, len(a))
	for _, w := range a {
		if len(w) > 4 {
			set[w] = struct{}{}
		}
	}
	n := 0
	for _, w := range b {
		if len(w) <= 4 {
			continue
		}
		if _, ok := set[w]; ok {
			n++
			delete(set, w) // count each shared word once
		}
	}
	return n
}
