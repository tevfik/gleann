package gleann

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true,
	"if": true, "then": true, "else": true, "when": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "to": true, "of": true,
	"in": true, "for": true, "with": true, "on": true, "at": true, "by": true,
	"from": true, "up": true, "about": true, "into": true, "over": true, "after": true,
	"it": true, "this": true, "that": true, "these": true, "those": true, "which": true,
	"can": true, "could": true, "will": true, "would": true, "should": true, "do": true,
	"does": true, "did": true, "has": true, "have": true, "had": true, "not": true,
}

// ExtractSummary generates a zero-config, extractive summary from plain text/markdown.
// It removes markdown noise, scores sentences using TF-IDF term frequency,
// and returns the top 3 most information-dense sentences.
func ExtractSummary(text string) string {
	// 1. Clean Markdown Noise
	reCode := regexp.MustCompile("(?s)```.*?```")
	text = reCode.ReplaceAllString(text, "")

	reInline := regexp.MustCompile("`[^`]+`")
	text = reInline.ReplaceAllString(text, "")

	reLink := regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	text = reLink.ReplaceAllString(text, "$1")

	reLists := regexp.MustCompile(`(?m)^[\s]*[#\*\-\+>]+\s+.*$`)
	text = reLists.ReplaceAllString(text, "")

	reHTML := regexp.MustCompile(`(?s)<.*?>`)
	text = reHTML.ReplaceAllString(text, "")

	// 2. Sentence Extraction
	sentRe := regexp.MustCompile(`([A-Z][^\.!\?]+[\.!\?])`)
	rawSentences := sentRe.FindAllString(text, -1)

	var sentences []string
	for _, s := range rawSentences {
		s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
		if len(s) > 30 && len(s) < 400 {
			sentences = append(sentences, s)
		}
	}

	if len(sentences) == 0 {
		return ""
	}

	// 3. Word Frequency (TF)
	wordFreq := make(map[string]int)
	wordTokens := make([][]string, len(sentences))
	wordRe := regexp.MustCompile(`[a-z0-9]+`)

	for i, s := range sentences {
		words := wordRe.FindAllString(strings.ToLower(s), -1)
		var validWords []string
		for _, w := range words {
			if len(w) > 2 && !stopWords[w] {
				wordFreq[w]++
				validWords = append(validWords, w)
			}
		}
		wordTokens[i] = validWords
	}

	maxFreq := 0
	for _, f := range wordFreq {
		if f > maxFreq {
			maxFreq = f
		}
	}

	if maxFreq == 0 {
		return ""
	}

	// 4. Score Sentences
	type SentScore struct {
		Index    int
		Sentence string
		Score    float64
	}

	var scores []SentScore
	for i, s := range sentences {
		var score float64
		for _, w := range wordTokens[i] {
			tf := float64(wordFreq[w]) / float64(maxFreq)
			score += tf
		}
		if len(wordTokens[i]) > 0 {
			score = score / math.Sqrt(float64(len(wordTokens[i])))
		}
		scores = append(scores, SentScore{Index: i, Sentence: s, Score: score})
	}

	// 5. Select Top N
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].Score > scores[j].Score
	})

	topN := 3
	if len(scores) < topN {
		topN = len(scores)
	}

	topSents := scores[:topN]
	sort.Slice(topSents, func(i, j int) bool {
		return topSents[i].Index < topSents[j].Index
	})

	var result []string
	for _, s := range topSents {
		result = append(result, s.Sentence)
	}

	return strings.Join(result, " ")
}
