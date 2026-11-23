package analysis

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tokenStringCases are texts chosen for the rules TokenString restates on
// bytes (word boundaries, lowercasing, camelCase and letter-digit splits, the
// one-byte drop, stop words) and for the hand-over to the shared functions
// (non-ASCII words, CJK runs, Turkish prose, invalid UTF-8).
var tokenStringCases = []string{
	"",
	" ",
	"a",
	"I",
	"x1",
	"ab",
	"The THE the tHe",
	"is IS If iF then THEN Should should SHOULD shoulds",
	"which Which whichever would could might being",
	"isIt ifThen toDo notNo doDoes theThe aAn",
	"openStore ASTChunker sha256 256bit HTTPServer2X A1b2C3 ABC ABc aB Ab aBc",
	"XMLHttpRequest getHTTPResponseCode parseURL2 URL2Parse v2 V2 x86_64 utf8",
	"snake_case_name __init__ kebab-case-name dotted.name path/to/file.go",
	"func (s *Store) ApplySourceTx(ctx context.Context, tx *sql.Tx, d SourceDelta) ([]PassageID, error) {",
	"   1: package main\n   2: \n   3: import \"fmt\"\n",
	"0 00 007 1a a1 1A A1 a1b 1a1 12ab34cd",
	"Größe naïve café ÉCOLE Ångström straße STRASSE",
	"ΑΒΓ αβγ ΑβΓ Привет привет ПРИВЕТ приветМир",
	"ǅ ǅa aǅ ǲ ſ ß ı İ",
	"١٢٣ a١ ١a x٣y ２０２４ ａｂｃ ＡＢＣ",
	"日本語テキスト サーバー fooバー123 漢字kanji漢字 한국어 텍스트 ー",
	"ışık IŞIK İstanbul ISI ıSı Kanunu'na Anayasa’ya AB'ye 2023'te O'Brien",
	"\xff\xfeabc a\xffb abc\xff \xe6\x97 x\xe6\x97y \xc3(",
	"tab\tsep\nnew\rline\x00nul\x7fdel",
	"a'b a\"b a`b a@b a[b a{b a/b a:b a0b A-Z a-z 0-9",
	"emoji 😀 mixed😀Word café😀 ok",
	"é äb combining x\u200dy zero\u200bwidth",
	"i̇ İ İ ı dotless",
}

func checkTokenString(t *testing.T, text string) {
	t.Helper()
	for _, lang := range []string{"", "en", "tr", "de"} {
		for _, kind := range []Kind{KindCode, KindProse} {
			an := New(lang)
			want := strings.Join(an.Tokens(text, kind), " ")
			if got := an.TokenString(text, kind); got != want {
				t.Fatalf("TokenString(%q) with language %q, kind %q\n got  %q\n want %q (strings.Join of Tokens)", text, lang, kind, got, want)
			}
		}
	}
}

// TestTokenStringMatchesTokens pins TokenString to strings.Join(Tokens, " "):
// the lexical index must hold the same terms whichever of the two wrote them.
func TestTokenStringMatchesTokens(t *testing.T) {
	for _, text := range tokenStringCases {
		checkTokenString(t, text)
	}
	checkTokenString(t, strings.Join(tokenStringCases, "\n"))

	// The package's own sources and tests: real code and prose.
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("listing the package sources: %v (%d files)", err, len(files))
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		checkTokenString(t, string(data))
	}

	// Random texts over an alphabet that mixes every class the rules look at.
	alphabet := []string{
		"a", "b", "z", "A", "B", "Z", "0", "9", "i", "I", "s", "S",
		" ", " ", "_", ".", "'", "’", "\n", "(", "-",
		"the", "The", "IS", "should", "No", "if",
		"ı", "İ", "ş", "Ğ", "ö", "é", "É", "ß", "я", "Я", "ǅ", "٣",
		"日", "本", "テ", "ー", "한", "😀", "\xff", "\xe6\x97", "́",
	}
	rng := rand.New(rand.NewSource(1))
	for n := 0; n < 20000; n++ {
		var sb strings.Builder
		for k := rng.Intn(24); k > 0; k-- {
			sb.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		checkTokenString(t, sb.String())
	}
}

// TestTokenStringStopWords guards the length shortcut of
// tokenWriter.lowerToken, which asks isStopWord only about tokens of at most
// maxStopWordLen bytes: the longest stop words must still be dropped.
func TestTokenStringStopWords(t *testing.T) {
	for _, w := range []string{"the", "should", "which", "would", "could", "might", "being", "then"} {
		if !isStopWord(w) {
			t.Errorf("isStopWord(%q) = false", w)
		}
		if len(w) > maxStopWordLen {
			t.Errorf("stop word %q is longer than maxStopWordLen (%d)", w, maxStopWordLen)
		}
		if got := New("").TokenString("x "+w+" "+strings.ToUpper(w)+" yy", KindCode); got != "yy" {
			t.Errorf("TokenString kept the stop word %q: %q", w, got)
		}
	}
}

func FuzzTokenString(f *testing.F) {
	for _, text := range tokenStringCases {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		checkTokenString(t, text)
	})
}

// benchTexts returns the package's Go sources cut into passage-sized pieces.
func benchTexts(b *testing.B) (texts []string, bytes int64) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		b.Fatalf("listing the package sources: %v (%d files)", err, len(files))
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			b.Fatalf("reading %s: %v", f, err)
		}
		s := string(data)
		bytes += int64(len(s))
		for len(s) > 1500 {
			texts = append(texts, s[:1500])
			s = s[1500:]
		}
		texts = append(texts, s)
	}
	return texts, bytes
}

func BenchmarkTokensJoin(b *testing.B) {
	texts, n := benchTexts(b)
	an := New("")
	b.SetBytes(n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, t := range texts {
			_ = strings.Join(an.Tokens(t, KindCode), " ")
		}
	}
}

func BenchmarkTokenString(b *testing.B) {
	texts, n := benchTexts(b)
	an := New("")
	b.SetBytes(n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, t := range texts {
			_ = an.TokenString(t, KindCode)
		}
	}
}
