package analysis

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tevfik/gleann/modules/bm25"
)

// TestTokensGolden pins the base behaviour copied from modules/bm25:
// camelCase/PascalCase and letter-digit sub-words, stop words, punctuation
// splitting and the one-character drop (RFC 003 section 7.1, analysis row).
func TestTokensGolden(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"camelCase", "openStore", []string{"openstore", "open", "store"}},
		{"PascalCase", "ASTChunker", []string{"astchunker", "ast", "chunker"}},
		{"acronymThenWordThenDigit", "ParseHTTPResponse2", []string{"parsehttpresponse2", "parse", "http", "response"}},
		{"letterDigit", "sha256 256bit", []string{"sha256", "sha", "256", "256bit", "256", "bit"}},
		{"stopWords", "the quick and the dead", []string{"quick", "dead"}},
		{"punctuation", "hello, world! foo-bar (baz)", []string{"hello", "world", "foo", "bar", "baz"}},
		{"oneCharDrop", "a I x 9 ab", []string{"ab"}},
		{"identifiers", "ID INFO INIT", []string{"id", "info", "init"}},
		{"repeatsKept", "foo foo", []string{"foo", "foo"}},
		{"empty", "", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, lang := range []string{"", "en"} {
				for _, kind := range []Kind{KindCode, KindProse} {
					got := New(lang).Tokens(tc.text, kind)
					if !reflect.DeepEqual(got, tc.want) {
						t.Errorf("New(%q).Tokens(%q, %s) = %q, want %q", lang, tc.text, kind, got, tc.want)
					}
				}
			}
		})
	}
}

// TestTokensBaseEquivalenceBM25 reads every *.go and *.md file under docs/
// and pkg/gleann/ at test time and requires the base analyzer to produce
// exactly bm25.Tokenize's output, so the gate 1 quality numbers stay valid
// (RFC 003 sections 4.1 item 2 and 7.1). The bm25 import is test-only.
func TestTokensBaseEquivalenceBM25(t *testing.T) {
	files := corpusFiles(t)
	base := New("")
	cjkFiles := 0
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		text := string(raw)

		// The CJK rule (RFC 003 section 4.1 item 4) intentionally changes
		// the output for CJK runs: unigrams and bigrams instead of one
		// token. One corpus file (pkg/gleann/token_budget_fuzz_test.go)
		// carries CJK fixtures, so equivalence is asserted on the CJK-free
		// text of every file; for files without CJK that is the full text,
		// byte for byte. CJK expansion itself is pinned by TestCJK.
		compare := text
		if strings.ContainsFunc(text, isCJK) {
			cjkFiles++
			compare = stripCJK(text)
		}

		want := bm25.Tokenize(compare)
		if got := base.Tokens(compare, KindCode); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: Tokens(code) differs from bm25.Tokenize\n got: %q\nwant: %q", path, got, want)
		}
		if got := base.Tokens(compare, KindProse); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: Tokens(prose) differs from bm25.Tokenize\n got: %q\nwant: %q", path, got, want)
		}
	}
	t.Logf("compared %d corpus files (%d containing CJK fixtures)", len(files), cjkFiles)
}

func corpusFiles(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	var files []string
	for _, dir := range []string{"docs", filepath.Join("pkg", "gleann")} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if ext := filepath.Ext(path); ext == ".go" || ext == ".md" {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	if len(files) == 0 {
		t.Fatal("no corpus files found under docs/ and pkg/gleann/")
	}
	return files
}

// stripCJK replaces every CJK run with spaces so both tokenizers see the
// same CJK-free text.
func stripCJK(text string) string {
	return strings.Map(func(r rune) rune {
		if isCJK(r) {
			return ' '
		}
		return r
	}, text)
}

// TestTurkishProseProbeQueries is the unit level of IDX-07: with tr+prose,
// the document "İstanbul'un ışıkları nöbetçi" must share at least one token
// with each of the six probe queries' QueryTokens groups.
func TestTurkishProseProbeQueries(t *testing.T) {
	a := New("tr")
	doc := "İstanbul'un ışıkları nöbetçi"
	// The case suffix after the apostrophe ("'un") is not a token
	// (TestTurkishApostropheSuffix).
	want := []string{"istanbul", "ışıkları", "isiklari", "nöbetçi", "nobetci"}
	tokens := a.Tokens(doc, KindProse)
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("Tokens(%q, prose) = %q, want %q", doc, tokens, want)
	}
	inDoc := make(map[string]bool, len(tokens))
	for _, tok := range tokens {
		inDoc[tok] = true
	}

	probes := []string{"İSTANBUL", "istanbul", "ISTANBUL", "IŞIKLARI", "isiklari", "NÖBETÇİ"}
	for _, probe := range probes {
		groups := a.QueryTokens(probe)
		if len(groups) == 0 {
			t.Errorf("probe %q: QueryTokens returned no groups", probe)
			continue
		}
		matched := false
		for _, group := range groups {
			for _, form := range group {
				if inDoc[form] {
					matched = true
				}
			}
		}
		if !matched {
			t.Errorf("probe %q: groups %q share no token with document tokens %q", probe, groups, tokens)
		}
	}
}

// TestTurkishCodeStaysBase: code passages of a tr analyzer use the base
// rules, so ID/INFO/INIT stay id/info/init (RFC 003 section 4.1 item 3).
func TestTurkishCodeStaysBase(t *testing.T) {
	a := New("tr")
	if got, want := a.Tokens("ID INFO INIT", KindCode), []string{"id", "info", "init"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tokens(code) = %q, want %q", got, want)
	}
	for _, text := range []string{"ID INFO INIT", "userID userID2", "ParseHTTPResponse2 sha256"} {
		if got, want := a.Tokens(text, KindCode), bm25.Tokenize(text); !reflect.DeepEqual(got, want) {
			t.Errorf("tr code Tokens(%q) = %q, want bm25 %q", text, got, want)
		}
	}
	// The same word in prose does fold (and the folded form carries ı, so
	// it also gets its ASCII variant), and the query emits both forms.
	if got, want := a.Tokens("ID", KindProse), []string{"ıd", "id"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tokens(ID, prose) = %q, want %q", got, want)
	}
	if got, want := a.QueryTokens("ID"), [][]string{{"id", "ıd"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("QueryTokens(ID) = %q, want %q", got, want)
	}
}

// TestTurkishFoldingGolden pins the Turkish rules: İ→i and I→ı before
// lowercasing, ASCII variants for tokens with ı ş ğ ç ö ü, sub-word variants,
// and that the rules apply only for tr+prose.
func TestTurkishFoldingGolden(t *testing.T) {
	cases := []struct {
		lang string
		kind Kind
		text string
		want []string
	}{
		{"tr", KindProse, "İSTANBUL", []string{"istanbul"}},
		{"tr", KindProse, "ISTANBUL", []string{"ıstanbul", "istanbul"}},
		{"tr", KindProse, "IŞIKLARI", []string{"ışıkları", "isiklari"}},
		{"tr", KindProse, "ışıkları", []string{"ışıkları", "isiklari"}},
		{"tr", KindProse, "NÖBETÇİ", []string{"nöbetçi", "nobetci"}},
		{"tr", KindProse, "ŞĞÇÖÜ", []string{"şğçöü", "sgcou"}},
		{"tr", KindProse, "IşıkLarı", []string{"ışıkları", "isiklari", "ışık", "isik", "ları", "lari"}},
		{"tr", KindProse, "hello world", []string{"hello", "world"}},
		{"tr", KindCode, "IŞIKLARI", []string{"işiklari"}},
		{"en", KindProse, "IŞIKLARI", []string{"işiklari"}},
		{"", KindProse, "IŞIKLARI", []string{"işiklari"}},
	}
	for _, tc := range cases {
		got := New(tc.lang).Tokens(tc.text, tc.kind)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("New(%q).Tokens(%q, %s) = %q, want %q", tc.lang, tc.text, tc.kind, got, tc.want)
		}
	}
}

// TestQueryTokens pins the query-side emission: base groups for non-tr,
// two-form groups for tr (base first, folded second when it differs), and
// empty output for queries without terms.
func TestQueryTokens(t *testing.T) {
	cases := []struct {
		name string
		lang string
		text string
		want [][]string
	}{
		{"base", "", "openStore the sha256", [][]string{{"openstore"}, {"open"}, {"store"}, {"sha256"}, {"sha"}, {"256"}}},
		{"trTwoForm", "tr", "ID IŞIKLARI isiklari", [][]string{{"id", "ıd"}, {"işiklari", "ışıkları"}, {"isiklari"}}},
		{"trCamel", "tr", "InfoID", [][]string{{"infoid", "ınfoıd"}, {"info", "ınfo"}, {"id", "ıd"}}},
		{"trFoldedOnlySurvives", "tr", "IS", [][]string{{"ıs"}}},
		{"stopWordsOnly", "", "the and of", nil},
		{"punctuationOnly", "tr", "!!! ---", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := New(tc.lang).QueryTokens(tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("New(%q).QueryTokens(%q) = %q, want %q", tc.lang, tc.text, got, tc.want)
			}
		})
	}

	// For the base language, flattening the groups must equal the document
	// tokens exactly.
	for _, text := range []string{"openStore sha256 the", "ID INFO INIT", "hello, world!", ""} {
		var flat []string
		for _, group := range New("").QueryTokens(text) {
			flat = append(flat, group...)
		}
		want := New("").Tokens(text, KindCode)
		if len(flat) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(flat, want) {
			t.Errorf("flattened QueryTokens(%q) = %q, want Tokens %q", text, flat, want)
		}
	}
}

// TestCJK is the unit level of IDX-08: CJK runs become unigrams and bigrams
// in every language and kind, and the one-character drop rule does not drop
// CJK unigrams.
func TestCJK(t *testing.T) {
	tokyo := []string{
		"東", "京", "の", "サ", "ー", "バ", "ー",
		"東京", "京の", "のサ", "サー", "ーバ", "バー",
	}
	for _, lang := range []string{"", "en", "tr"} {
		for _, kind := range []Kind{KindCode, KindProse} {
			if got := New(lang).Tokens("東京のサーバー", kind); !reflect.DeepEqual(got, tokyo) {
				t.Errorf("New(%q).Tokens(東京のサーバー, %s) = %q, want %q", lang, kind, got, tokyo)
			}
		}
	}

	cases := []struct {
		name string
		text string
		want []string
	}{
		{"singleCharNotDropped", "東", []string{"東"}},
		{"hangul", "한국어", []string{"한", "국", "어", "한국", "국어"}},
		{"mixedWord", "Go東京", []string{"go", "東", "京", "東京"}},
		{"punctuationBetweenRuns", "東京、日本", []string{"東", "京", "東京", "日", "本", "日本"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := New("").Tokens(tc.text, KindCode); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Tokens(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}

	// A multi-character CJK query uses bigrams only; OR-ing its unigrams
	// would match every passage with any common character of it.
	if got, want := New("").QueryTokens("東京"), [][]string{{"東京"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("QueryTokens(東京) = %q, want %q", got, want)
	}
	if got, want := New("").QueryTokens("東"), [][]string{{"東"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("QueryTokens(東) = %q, want %q", got, want)
	}
}

// TestMatchExpr pins FTS5 MATCH construction: OR within and across groups,
// double-quoting with embedded quotes doubled, deduplication, and the empty
// result that tells the caller not to run SQL (RFC 003 section 4.2 step 1).
func TestMatchExpr(t *testing.T) {
	cases := []struct {
		name   string
		groups [][]string
		want   string
	}{
		{"groupsOrForms", [][]string{{"id", "ıd"}, {"info"}}, `"id" OR "ıd" OR "info"`},
		{"dedup", [][]string{{"id"}, {"id", "ıd"}}, `"id" OR "ıd"`},
		{"embeddedQuote", [][]string{{`a"b`}}, `"a""b"`},
		{"loneQuote", [][]string{{`"`}}, `""""`},
		{"nil", nil, ""},
		{"noGroups", [][]string{}, ""},
		{"emptyGroup", [][]string{{}}, ""},
		{"emptyForm", [][]string{{""}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchExpr(tc.groups); got != tc.want {
				t.Errorf("MatchExpr(%q) = %q, want %q", tc.groups, got, tc.want)
			}
		})
	}

	if got := MatchExpr(New("tr").QueryTokens("the and of")); got != "" {
		t.Errorf("stop-word query expression = %q, want empty", got)
	}
	if got, want := MatchExpr(New("tr").QueryTokens("ID")), `"id" OR "ıd"`; got != want {
		t.Errorf("MatchExpr(QueryTokens(ID)) = %q, want %q", got, want)
	}
}

// TestMatchExprAND pins AND-first expression construction: term groups AND-ed,
// alternative forms within a group OR-ed with parentheses when >1, quoting
// rules identical to MatchExpr, deduplication within and across groups, and
// empty result for token-less queries (RFC 003 section 4.2 step 2).
func TestMatchExprAND(t *testing.T) {
	cases := []struct {
		name   string
		groups [][]string
		want   string
	}{
		{"groupsAndForms", [][]string{{"id", "ıd"}, {"info"}}, `("id" OR "ıd") AND "info"`},
		{"singleGroupMultipleForms", [][]string{{"a", "a2"}}, `("a" OR "a2")`},
		{"singleGroupSingleForm", [][]string{{"b"}}, `"b"`},
		{"multipleGroupsSingleForms", [][]string{{"a"}, {"b"}}, `"a" AND "b"`},
		{"rfcExample", [][]string{{"a", "a2"}, {"b"}}, `("a" OR "a2") AND "b"`},
		{"dedupWithinGroup", [][]string{{"id", "id", "ıd"}}, `("id" OR "ıd")`},
		{"dedupAcrossGroups", [][]string{{"id"}, {"id"}}, `"id"`},
		{"embeddedQuote", [][]string{{`a"b`}, {`c`}}, `"a""b" AND "c"`},
		{"loneQuote", [][]string{{`"`}}, `""""`},
		{"nil", nil, ""},
		{"noGroups", [][]string{}, ""},
		{"emptyGroup", [][]string{{}}, ""},
		{"emptyForm", [][]string{{""}}, ""},
		{"mixedEmptyAndValid", [][]string{{""}, {"valid"}}, `"valid"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchExprAND(tc.groups); got != tc.want {
				t.Errorf("MatchExprAND(%q) = %q, want %q", tc.groups, got, tc.want)
			}
		})
	}

	if got := MatchExprAND(New("tr").QueryTokens("the and of")); got != "" {
		t.Errorf("stop-word query expression = %q, want empty", got)
	}
	if got, want := MatchExprAND(New("tr").QueryTokens("ID")), `("id" OR "ıd")`; got != want {
		t.Errorf("MatchExprAND(QueryTokens(ID)) = %q, want %q", got, want)
	}
	if got, want := MatchExprAND(New("tr").QueryTokens("ID search")), `("id" OR "ıd") AND "search"`; got != want {
		t.Errorf("MatchExprAND(QueryTokens(ID search)) = %q, want %q", got, want)
	}
}

// The one-character drop counts characters in Turkish prose: "I" (which
// becomes the two-byte "ı") and "ö" are dropped like "i" and "o". The base
// rules keep counting bytes, as modules/bm25 does.
func TestTurkishOneCharacterDrop(t *testing.T) {
	tr := New("tr")
	if got := tr.Tokens("a I x 9 ab ö ı İ", KindProse); !reflect.DeepEqual(got, []string{"ab"}) {
		t.Errorf("tr prose one-char drop = %q, want [ab]", got)
	}
	if got := tr.QueryTokens("a I 9 i"); got != nil {
		t.Errorf("tr QueryTokens of one-char words = %q, want nil", got)
	}
	if got := MatchExpr(tr.QueryTokens("a I 9 i")); got != "" {
		t.Errorf("MatchExpr of one-char query = %q, want empty", got)
	}
	// Code in a Turkish index and the base rules are unchanged.
	if got := New("").Tokens("ö ab", KindProse); !reflect.DeepEqual(got, []string{"ö", "ab"}) {
		t.Errorf("base rules changed: %q", got)
	}
}

// Turkish writes a case suffix after an apostrophe on proper nouns,
// abbreviations and numbers. It used to become a term of its own ("na",
// "ya"), so "İş Kanunu'na göre" searched for "na" and matched unrelated
// passages. Turkish prose and Turkish queries now drop it; English keeps
// the base rules.
func TestTurkishApostropheSuffix(t *testing.T) {
	for in, want := range map[string]string{
		"Kanunu'na":            "Kanunu",
		"Anayasa’ya göre":      "Anayasa göre",
		"AB'ye ve 2023'te":     "AB ve 2023",
		"Türkiye'nin başkenti": "Türkiye başkenti",
		"O'Brien":              "O'Brien", // uppercase after the apostrophe: a name, kept
		"'alıntı' bitti":       "'alıntı' bitti",
		"kelime' ":             "kelime' ",
		"yok":                  "yok",
	} {
		if got := dropApostropheSuffixes(in); got != want {
			t.Errorf("dropApostropheSuffixes(%q) = %q, want %q", in, got, want)
		}
	}

	tr := New("tr")
	if got := tr.Tokens("İş Kanunu'na göre", KindProse); !reflect.DeepEqual(got, []string{"iş", "is", "kanunu", "göre", "gore"}) {
		t.Errorf("tr prose tokens = %q", got)
	}
	if got := tr.QueryTokens("Kanunu'na göre"); !reflect.DeepEqual(got, [][]string{{"kanunu"}, {"göre"}}) {
		t.Errorf("tr query = %q", got)
	}
	// Code passages and other languages keep the base rules.
	if got := tr.Tokens("it's x'y", KindCode); !reflect.DeepEqual(got, New("").Tokens("it's x'y", KindCode)) {
		t.Errorf("tr code tokens = %q", got)
	}
	if got := New("").QueryTokens("Kanunu'na"); !reflect.DeepEqual(got, [][]string{{"kanunu"}, {"na"}}) {
		t.Errorf("base query = %q", got)
	}
}

// Only Turkish indexes change analyzer identity, so only they rebuild their
// lexical table after the upgrade.
func TestAnalyzerIdentity(t *testing.T) {
	if New("tr").Identity() != IDTurkish || New("").Identity() != ID || New("en").Identity() != ID {
		t.Errorf("identities: tr %q, base %q, en %q", New("tr").Identity(), New("").Identity(), New("en").Identity())
	}
	if ID == IDTurkish {
		t.Error("the Turkish rules changed; their identity must differ from the base")
	}
}
