package chunking

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func checkBounded(t *testing.T, name string, chunks []string, size int) {
	t.Helper()
	for i, c := range chunks {
		if !utf8.ValidString(c) {
			t.Errorf("%s: chunk %d is not valid UTF-8", name, i)
		}
		// Allow the overlap on top of the limit.
		if n := countWords(c); n > size*2 {
			t.Errorf("%s: chunk %d has %d tokens, limit %d", name, i, n, size)
		}
		if n := utf8.RuneCountInString(c); n > size*maxRunesPerWord*2 {
			t.Errorf("%s: chunk %d has %d runes", name, i, n)
		}
	}
}

func TestChunkLongLogLine(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 3000; i++ {
		sb.WriteString("2026-01-01T00:00:00Z worker-3 processed item without punctuation\n")
	}
	chunks := NewSentenceSplitter(256, 10).Chunk(sb.String())
	if len(chunks) < 20 {
		t.Fatalf("log text produced only %d chunks", len(chunks))
	}
	checkBounded(t, "log", chunks, 256)
	// Newlines are the preferred break: no chunk but the first may start mid-line.
	for i, c := range chunks[1:] {
		if !strings.HasPrefix(c, "2026-") && !strings.HasPrefix(c, "worker") && !strings.HasPrefix(c, "item") && !strings.HasPrefix(c, "processed") && !strings.HasPrefix(c, "without") && !strings.HasPrefix(c, "punctuation") && !strings.HasPrefix(c, "Z") {
			t.Errorf("chunk %d starts oddly: %.40q", i+1, c)
		}
	}
}

func TestChunkNoSpaces(t *testing.T) {
	text := strings.Repeat("abcdefghij", 20000) // 200k runes, no space at all
	chunks := NewSentenceSplitter(128, 5).Chunk(text)
	if len(chunks) < 50 {
		t.Fatalf("minified text produced %d chunks", len(chunks))
	}
	checkBounded(t, "nospace", chunks, 128)
}

func TestChunkCJK(t *testing.T) {
	text := strings.Repeat("日本語のテキストは空白を含みません", 3000) // no spaces, no ASCII punctuation
	chunks := NewSentenceSplitter(256, 10).Chunk(text)
	if len(chunks) < 20 {
		t.Fatalf("CJK text produced %d chunks", len(chunks))
	}
	checkBounded(t, "cjk", chunks, 256)

	// With full-width stops the text splits at sentences.
	text2 := strings.Repeat("これは一文です。", 500)
	chunks2 := NewSentenceSplitter(64, 4).Chunk(text2)
	if len(chunks2) < 5 {
		t.Fatalf("CJK sentences produced %d chunks", len(chunks2))
	}
	checkBounded(t, "cjk2", chunks2, 64)
}

func TestChunkLongParagraphAmongOthers(t *testing.T) {
	long := strings.Repeat("word ", 2000)
	text := "First paragraph stays.\n\n" + long + "\n\nLast paragraph stays."
	chunks := NewSentenceSplitter(100, 5).Chunk(text)
	checkBounded(t, "para", chunks, 100)
	if chunks[0] != "First paragraph stays." || chunks[len(chunks)-1] != "Last paragraph stays." {
		t.Errorf("short paragraphs changed: first=%q last=%q", chunks[0], chunks[len(chunks)-1])
	}
}

func TestSplitOversizeBreakPreference(t *testing.T) {
	// Newline wins over a later space.
	got := splitOversize("aaa bbb\nccc ddd eee", 4, 1000)
	want := []string{"aaa bbb", "ccc ddd eee"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("newline preference: got %q want %q", got, want)
	}
	// Space wins over a hard cut.
	got = splitOversize("aaa bbb ccc ddd", 2, 1000)
	want = []string{"aaa bbb", "ccc ddd"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("space preference: got %q want %q", got, want)
	}
	// Within limits: unchanged.
	if got := splitOversize("a b c", 5, 100); !reflect.DeepEqual(got, []string{"a b c"}) {
		t.Errorf("within limit changed: %q", got)
	}
	// Hard cut never splits a multi-byte rune.
	for _, c := range splitOversize(strings.Repeat("é", 100), 1, 7) {
		if !utf8.ValidString(c) || utf8.RuneCountInString(c) > 7 {
			t.Errorf("bad hard cut: %q", c)
		}
	}
}

// Normal prose within the limits must chunk exactly as before the oversize fix.
func TestChunkProseGolden(t *testing.T) {
	prose := "Gleann indexes source code and documents. It builds two indexes. Searches combine both.\n\nThe chunker splits text into pieces. Each piece holds a limited number of words! Is that enough?\n\nShort paragraph here.\n\nAnother paragraph follows with several sentences. The first one is plain. The second asks a question? The third exclaims!\n\nLast one closes the text."
	want := []string{
		"Gleann indexes source code and documents. It builds two indexes. Searches combine both.",
		"Searches combine both.\n\n\n\nThe chunker splits text into pieces. Each piece holds a limited number of words! Is that enough?",
		"Is that enough?\n\n\n\nShort paragraph here.",
		"Short paragraph here.\n\n\n\nAnother paragraph follows with several sentences. The first one is plain. The second asks a question? The third exclaims!",
		"The third exclaims!\n\n\n\nLast one closes the text.",
	}
	if got := NewSentenceSplitter(20, 3).Chunk(prose); !reflect.DeepEqual(got, want) {
		t.Errorf("paragraph prose changed:\n got %q\nwant %q", got, want)
	}

	single := "Gleann indexes source code and documents. It builds a vector index and a keyword index. Searches combine both. The chunker splits text into pieces. Each piece holds a limited number of words! Is that enough? It overlaps a little."
	want = []string{
		"Gleann indexes source code and documents.",
		"and documents.  It builds a vector index and a keyword index.",
		"keyword index.  Searches combine both.",
		"combine both.  The chunker splits text into pieces.",
		"into pieces.  Each piece holds a limited number of words!",
		"of words!  Is that enough? It overlaps a little.",
	}
	if got := NewSentenceSplitter(10, 2).Chunk(single); !reflect.DeepEqual(got, want) {
		t.Errorf("sentence prose changed:\n got %q\nwant %q", got, want)
	}
}
