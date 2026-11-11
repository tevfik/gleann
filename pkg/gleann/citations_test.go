package gleann

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCitation_BracketFormatsAndDeduplication(t *testing.T) {
	// Tests:
	// - single [n]
	// - adjacent [1][2]
	// - comma list [1, 3]
	// - hyphen range [2-4]
	// - en-dash range [2–4]
	// - duplicate citations within a sentence counted once
	// - Used sorted and de-duplicated across sentences
	answer := "Single [1]. Adjacent [1][2]. Comma list [1, 3]. Hyphen range [2-4]. EnDash range [2–4]. Dups [1] and [1][1] and [1, 1]."
	c := ParseCitations(answer, 5)

	expectedSentences := []struct {
		refs []int
	}{
		{refs: []int{1}},
		{refs: []int{1, 2}},
		{refs: []int{1, 3}},
		{refs: []int{2, 3, 4}},
		{refs: []int{2, 3, 4}},
		{refs: []int{1}}, // duplicate refs within a single sentence must be counted once
	}

	if len(c.Sentences) != len(expectedSentences) {
		t.Fatalf("expected %d sentences, got %d", len(expectedSentences), len(c.Sentences))
	}

	for i, exp := range expectedSentences {
		if !reflect.DeepEqual(c.Sentences[i].Refs, exp.refs) {
			t.Errorf("sentence %d (%q): expected refs %v, got %v", i, c.Sentences[i].Text, exp.refs, c.Sentences[i].Refs)
		}
	}

	// Used must be sorted ascending and de-duplicated across all sentences
	expectedUsed := []int{1, 2, 3, 4}
	if !reflect.DeepEqual(c.Used, expectedUsed) {
		t.Errorf("expected Used %v, got %v", expectedUsed, c.Used)
	}
	if len(c.Invalid) != 0 {
		t.Errorf("expected no Invalid citations, got %v", c.Invalid)
	}
}

func TestCitation_InvalidAndOutOfBounds(t *testing.T) {
	// Tests:
	// - numbers outside 1..n go to Invalid (sorted, de-duplicated)
	// - [0] is invalid
	// - invalid numbers are not in Refs or Used
	answer := "First statement [0] and [5]. Second statement [0][6]. Third statement [2] and [5]."
	// n = 4, so valid citations are 1..4. [0], [5], and [6] are invalid.
	c := ParseCitations(answer, 4)

	// Invalid must be sorted ascending and de-duplicated
	expectedInvalid := []int{0, 5, 6}
	if !reflect.DeepEqual(c.Invalid, expectedInvalid) {
		t.Errorf("expected Invalid %v, got %v", expectedInvalid, c.Invalid)
	}

	// Sentence 0: [0] and [5] are invalid -> refs empty
	if len(c.Sentences[0].Refs) != 0 {
		t.Errorf("sentence 0 should have no valid refs, got %v", c.Sentences[0].Refs)
	}
	// Sentence 1: [0] and [6] are invalid -> refs empty
	if len(c.Sentences[1].Refs) != 0 {
		t.Errorf("sentence 1 should have no valid refs, got %v", c.Sentences[1].Refs)
	}
	// Sentence 2: [2] is valid, [5] is invalid -> refs [2]
	expectedRefs2 := []int{2}
	if !reflect.DeepEqual(c.Sentences[2].Refs, expectedRefs2) {
		t.Errorf("sentence 2: expected refs %v, got %v", expectedRefs2, c.Sentences[2].Refs)
	}

	// Used should only contain valid citations (sorted, unique)
	expectedUsed := []int{2}
	if !reflect.DeepEqual(c.Used, expectedUsed) {
		t.Errorf("expected Used %v, got %v", expectedUsed, c.Used)
	}
}

func TestCitation_SentenceSplit_BeforePeriod_FooBracket1Dot(t *testing.T) {
	// Standard punctuation: "Foo [1]. Bar [2]."
	// Citation marker is inside the sentence before the period.
	answer := "Foo [1]. Bar [2]."
	c := ParseCitations(answer, 3)

	if len(c.Sentences) != 2 {
		t.Fatalf("expected 2 sentences, got %d", len(c.Sentences))
	}
	if c.Sentences[0].Text != "Foo [1]." || !reflect.DeepEqual(c.Sentences[0].Refs, []int{1}) {
		t.Errorf("sentence 0: expected 'Foo [1].' with refs [1], got text %q, refs %v", c.Sentences[0].Text, c.Sentences[0].Refs)
	}
	if c.Sentences[1].Text != "Bar [2]." || !reflect.DeepEqual(c.Sentences[1].Refs, []int{2}) {
		t.Errorf("sentence 1: expected 'Bar [2].' with refs [2], got text %q, refs %v", c.Sentences[1].Text, c.Sentences[1].Refs)
	}
}

func TestCitation_SentenceSplit_AfterPeriodNoSpace_FooDotBracket1(t *testing.T) {
	// Citation marker immediately after the period without space: "Foo.[1] Bar."
	// splitSentences keeps .[1] with the first sentence because sentenceSplit regex
	// matches `[.!?](?:\s*\[[\d,\-–\s]+\])*` including the trailing bracket.
	answer := "Foo.[1] Bar."
	c := ParseCitations(answer, 3)

	if len(c.Sentences) != 2 {
		t.Fatalf("expected 2 sentences, got %d", len(c.Sentences))
	}
	if c.Sentences[0].Text != "Foo.[1]" || !reflect.DeepEqual(c.Sentences[0].Refs, []int{1}) {
		t.Errorf("sentence 0: expected 'Foo.[1]' with refs [1], got text %q, refs %v", c.Sentences[0].Text, c.Sentences[0].Refs)
	}
	if c.Sentences[1].Text != "Bar." || len(c.Sentences[1].Refs) != 0 {
		t.Errorf("sentence 1: expected 'Bar.' with no refs, got text %q, refs %v", c.Sentences[1].Text, c.Sentences[1].Refs)
	}
}

func TestCitation_SentenceSplit_AfterPeriodWithSpace_FooDotSpaceBracket2(t *testing.T) {
	// Citation marker after period with space: "Foo. [2] Bar."
	// In splitSentences, group 1 captures `. [2]` because `\s*\[[\d,\-–\s]+\]` allows
	// leading whitespace. Therefore, ref 2 stays attached to the first sentence ("Foo. [2]"),
	// and "Bar." becomes the second sentence with no citations.
	answer := "Foo. [2] Bar."
	c := ParseCitations(answer, 3)

	if len(c.Sentences) != 2 {
		t.Fatalf("expected 2 sentences, got %d", len(c.Sentences))
	}
	if c.Sentences[0].Text != "Foo. [2]" || !reflect.DeepEqual(c.Sentences[0].Refs, []int{2}) {
		t.Errorf("sentence 0: expected 'Foo. [2]' with refs [2], got text %q, refs %v", c.Sentences[0].Text, c.Sentences[0].Refs)
	}
	if c.Sentences[1].Text != "Bar." || len(c.Sentences[1].Refs) != 0 {
		t.Errorf("sentence 1: expected 'Bar.' with no refs, got text %q, refs %v", c.Sentences[1].Text, c.Sentences[1].Refs)
	}
}

func TestCitation_SentenceSplit_BulletListsAndBlankLines(t *testing.T) {
	// Bullet list splitting:
	// sentenceSplit matches `\n\s*[-*•]\s+` to split bullet list items.
	bulletText := "- a [1]\n- b"
	cBullet := ParseCitations(bulletText, 2)
	if len(cBullet.Sentences) != 2 {
		t.Fatalf("expected 2 sentences for bullet list, got %d", len(cBullet.Sentences))
	}
	// The list marker is trimmed from every item, including the first.
	if cBullet.Sentences[0].Text != "a [1]" || !reflect.DeepEqual(cBullet.Sentences[0].Refs, []int{1}) {
		t.Errorf("bullet sentence 0: expected 'a [1]' with refs [1], got text %q, refs %v", cBullet.Sentences[0].Text, cBullet.Sentences[0].Refs)
	}
	if cBullet.Sentences[1].Text != "b" || len(cBullet.Sentences[1].Refs) != 0 {
		t.Errorf("bullet sentence 1: expected 'b' with no refs, got text %q, refs %v", cBullet.Sentences[1].Text, cBullet.Sentences[1].Refs)
	}

	// Blank-line paragraphs splitting:
	// sentenceSplit matches `\n\s*\n` to split paragraphs.
	paraText := "Paragraph one [1]\n\nParagraph two [2]"
	cPara := ParseCitations(paraText, 2)
	if len(cPara.Sentences) != 2 {
		t.Fatalf("expected 2 sentences for blank-line paragraphs, got %d", len(cPara.Sentences))
	}
	if cPara.Sentences[0].Text != "Paragraph one [1]" || !reflect.DeepEqual(cPara.Sentences[0].Refs, []int{1}) {
		t.Errorf("paragraph 0: expected 'Paragraph one [1]' with refs [1], got text %q, refs %v", cPara.Sentences[0].Text, cPara.Sentences[0].Refs)
	}
	if cPara.Sentences[1].Text != "Paragraph two [2]" || !reflect.DeepEqual(cPara.Sentences[1].Refs, []int{2}) {
		t.Errorf("paragraph 1: expected 'Paragraph two [2]' with refs [2], got text %q, refs %v", cPara.Sentences[1].Text, cPara.Sentences[1].Refs)
	}
}

func TestCitation_CoverageAndEmptyAnswer(t *testing.T) {
	// Empty answer: no sentences, Coverage = 0
	empty := ParseCitations("", 5)
	if len(empty.Sentences) != 0 {
		t.Errorf("expected 0 sentences for empty answer, got %d", len(empty.Sentences))
	}
	if empty.Coverage != 0.0 {
		t.Errorf("expected 0.0 coverage for empty answer, got %f", empty.Coverage)
	}
	if len(empty.Used) != 0 {
		t.Errorf("expected empty Used, got %v", empty.Used)
	}
	if len(empty.Invalid) != 0 {
		t.Errorf("expected empty Invalid, got %v", empty.Invalid)
	}

	// Whitespace-only answer
	whitespaceOnly := ParseCitations("   \n\t  ", 5)
	if len(whitespaceOnly.Sentences) != 0 {
		t.Errorf("expected 0 sentences for whitespace answer, got %d", len(whitespaceOnly.Sentences))
	}
	if whitespaceOnly.Coverage != 0.0 {
		t.Errorf("expected 0.0 coverage for whitespace answer, got %f", whitespaceOnly.Coverage)
	}

	// Coverage calculation: sentences with >= 1 valid ref / total sentences
	// 4 sentences: 2 have valid refs, 1 has invalid ref only, 1 has no refs.
	mixedAnswer := "Valid citation [1]. Another valid [2][3]. Only invalid [99]. No citation here."
	cMixed := ParseCitations(mixedAnswer, 5)
	if len(cMixed.Sentences) != 4 {
		t.Fatalf("expected 4 sentences, got %d", len(cMixed.Sentences))
	}
	expectedCoverage := 2.0 / 4.0 // 0.50
	if cMixed.Coverage != expectedCoverage {
		t.Errorf("expected coverage %f, got %f", expectedCoverage, cMixed.Coverage)
	}
}

func TestCitation_LargeRangesAndNonNumericBrackets(t *testing.T) {
	// Ranges longer than 50 (b - a >= 50) are ignored
	// e.g. [1-51] -> 51 - 1 = 50, which is NOT < 50, so parseRefs ignores it.
	// [1-50] -> 50 - 1 = 49 < 50, which is valid and expanded.
	answer := "Ignored range [1-51]. Valid range [1-50]. Small range [2-4]."
	c := ParseCitations(answer, 100)

	if len(c.Sentences) != 3 {
		t.Fatalf("expected 3 sentences, got %d", len(c.Sentences))
	}

	// Sentence 0: [1-51] is ignored because span >= 50
	if len(c.Sentences[0].Refs) != 0 {
		t.Errorf("sentence 0: expected range >= 50 to be ignored, got refs %v", c.Sentences[0].Refs)
	}

	// Sentence 1: [1-50] has span 49 < 50, so 50 numbers are parsed
	if len(c.Sentences[1].Refs) != 50 {
		t.Errorf("sentence 1: expected 50 refs for [1-50], got %d", len(c.Sentences[1].Refs))
	}

	// Sentence 2: [2-4] has span 2 < 50
	expectedRefs2 := []int{2, 3, 4}
	if !reflect.DeepEqual(c.Sentences[2].Refs, expectedRefs2) {
		t.Errorf("sentence 2: expected refs %v, got %v", expectedRefs2, c.Sentences[2].Refs)
	}

	// Non-numeric brackets like [a] or [link](url) or empty [] are ignored by citeRe
	textNonNumeric := "Check [a] and markdown [link](https://example.com) and empty [] brackets but valid [1]."
	cNonNumeric := ParseCitations(textNonNumeric, 5)
	if len(cNonNumeric.Sentences) != 1 {
		t.Fatalf("expected 1 sentence, got %d", len(cNonNumeric.Sentences))
	}
	expectedRefs := []int{1}
	if !reflect.DeepEqual(cNonNumeric.Sentences[0].Refs, expectedRefs) {
		t.Errorf("expected only valid [1] to be parsed, got %v", cNonNumeric.Sentences[0].Refs)
	}
	if len(cNonNumeric.Invalid) != 0 {
		t.Errorf("non-numeric brackets should not be treated as invalid passage citations: %v", cNonNumeric.Invalid)
	}
}

func TestCitation_FormatReferences(t *testing.T) {
	// 1. Empty when nothing used
	emptyC := Citations{Used: nil}
	if res := FormatReferences(emptyC, nil); res != "" {
		t.Errorf("expected empty string when c.Used is empty, got %q", res)
	}

	// 2. Lists "[n] source" in ascending order using Metadata["source"],
	//    falling back to Metadata["file"], appends ":<line>" for start_line (int or float64),
	//    mentions ignored invalid citations, and skips numbers beyond len(results) without panicking.
	results := []SearchResult{
		{
			Metadata: map[string]any{
				"source":     "internal/auth/jwt.go",
				"start_line": 40, // int line number
			},
		},
		{
			Metadata: map[string]any{
				"file":       "pkg/api/handler.go", // fallback to "file"
				"start_line": float64(125),         // float64 line number
			},
		},
		{
			Metadata: map[string]any{
				"source": "docs/guide.md", // no start_line
			},
		},
	}

	citations := Citations{
		Used:    []int{1, 2, 3, 99}, // 99 is beyond len(results) -> should be skipped without panic
		Invalid: []int{0, 105},
	}

	formatted := FormatReferences(citations, results)

	expectedLines := []string{
		"References:",
		"  [1] internal/auth/jwt.go:40",
		"  [2] pkg/api/handler.go:125",
		"  [3] docs/guide.md",
		"  (ignored citations to missing passages: [0 105])",
	}
	expectedOutput := strings.Join(expectedLines, "\n") + "\n"

	if formatted != expectedOutput {
		t.Errorf("FormatReferences output mismatch.\nExpected:\n%s\nGot:\n%s", expectedOutput, formatted)
	}
}

func TestCitation_ParseRefs_Helper(t *testing.T) {
	// Direct unit tests for parseRefs helper
	tests := []struct {
		input    string
		expected []int
	}{
		{"1", []int{1}},
		{"1, 3, 5", []int{1, 3, 5}},
		{"2-4", []int{2, 3, 4}},
		{"2–4", []int{2, 3, 4}}, // en-dash
		{"1, 3-5, 8", []int{1, 3, 4, 5, 8}},
		{"1-51", nil}, // span >= 50 ignored
		{"invalid", nil},
	}

	for _, tt := range tests {
		got := parseRefs(tt.input)
		if !reflect.DeepEqual(got, tt.expected) {
			t.Errorf("parseRefs(%q): expected %v, got %v", tt.input, tt.expected, got)
		}
	}
}

func TestCitation_SplitSentences_Helper(t *testing.T) {
	// Direct unit tests for splitSentences helper
	text := "First sentence. Second sentence! Third sentence? Fourth sentence."
	sentences := splitSentences(text)
	expected := []string{
		"First sentence.",
		"Second sentence!",
		"Third sentence?",
		"Fourth sentence.",
	}
	if !reflect.DeepEqual(sentences, expected) {
		t.Errorf("splitSentences mismatch.\nExpected: %v\nGot:      %v", expected, sentences)
	}
}

// attributeTestEmbedder embeds text as counts of a few keywords.
type attributeTestEmbedder struct{ fail bool }

func (e attributeTestEmbedder) Compute(_ context.Context, texts []string) ([][]float32, error) {
	if e.fail {
		return nil, fmt.Errorf("boom")
	}
	vocab := []string{"webhook", "ratelimit", "token", "banana"}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, len(vocab))
		for j, w := range vocab {
			v[j] = float32(strings.Count(strings.ToLower(t), w))
		}
		out[i] = v
	}
	return out, nil
}
func (e attributeTestEmbedder) ComputeSingle(ctx context.Context, t string) ([]float32, error) {
	v, err := e.Compute(ctx, []string{t})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}
func (attributeTestEmbedder) Dimensions() int   { return 4 }
func (attributeTestEmbedder) ModelName() string { return "test" }

func TestAttribute(t *testing.T) {
	results := []SearchResult{
		{Text: "webhook validation rejects private IPs", Metadata: map[string]any{"source": "webhook.go"}},
		{Text: "ratelimit token bucket per client", Metadata: map[string]any{"source": "ratelimit.go"}},
	}
	answer := "Webhook URLs are checked [9]. The ratelimit uses a token bucket. I like banana."
	c, err := Attribute(context.Background(), attributeTestEmbedder{}, answer, results, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sentences) != 3 {
		t.Fatalf("sentences = %d, want 3", len(c.Sentences))
	}
	if !reflect.DeepEqual(c.Sentences[0].Refs, []int{1}) || !reflect.DeepEqual(c.Sentences[1].Refs, []int{2}) || len(c.Sentences[2].Refs) != 0 {
		t.Fatalf("refs = %v %v %v", c.Sentences[0].Refs, c.Sentences[1].Refs, c.Sentences[2].Refs)
	}
	if !reflect.DeepEqual(c.Used, []int{1, 2}) || len(c.Invalid) != 0 {
		t.Fatalf("used=%v invalid=%v (inline markers must be ignored)", c.Used, c.Invalid)
	}
	if c.Coverage < 0.66 || c.Coverage > 0.67 {
		t.Fatalf("coverage = %v", c.Coverage)
	}
	// An embedder error is returned.
	if _, err := Attribute(context.Background(), attributeTestEmbedder{fail: true}, answer, results, 0); err == nil {
		t.Fatal("expected embedder error")
	}
	// No results or nil embedder: sentences are returned without refs.
	if c, err := Attribute(context.Background(), nil, answer, results, 0); err != nil || len(c.Used) != 0 || len(c.Sentences) != 3 {
		t.Fatalf("nil embedder: %+v %v", c, err)
	}
}
