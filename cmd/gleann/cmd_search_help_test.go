package main

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"
)

// sortedFlags returns the keys of fs in a stable order, so a failing coverage
// test names the missing flags in a predictable order.
func sortedFlags(fs flagSet) []string {
	out := make([]string, 0, len(fs))
	for f := range fs {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// helpEntryFlags returns the flag spellings an option line of a help text
// documents, i.e. the spellings before the value placeholder and the
// description column. A continuation line of a wrapped description yields
// none. "--code-model, --code-embedding-model <m>  ..." yields both models.
func helpEntryFlags(line string) []string {
	if !strings.HasPrefix(line, "  -") {
		return nil
	}
	spec := line[2:]
	if i := strings.Index(spec, "  "); i >= 0 {
		spec = spec[:i]
	}
	var out []string
	for _, part := range strings.Split(spec, ",") {
		if fields := strings.Fields(part); len(fields) > 0 && strings.HasPrefix(fields[0], "-") {
			out = append(out, fields[0])
		}
	}
	return out
}

// TestSearchHelpCoversAllFlags checks that `gleann search --help` documents
// every flag searchFlags accepts, as its own option entry (aliases share the
// entry of their primary flag). A flag a user cannot discover is as good as
// unsupported: the parser would accept it, but nothing would tell the user it
// exists. TestHelpFlagsAreAccepted checks the other direction, so the help and
// the parser cannot drift apart.
func TestSearchHelpCoversAllFlags(t *testing.T) {
	help := captureHelp(t, func() { cmdSearch([]string{"--help"}) })
	if !strings.Contains(help, "Usage: gleann search") {
		t.Fatalf("search --help printed no usage text: %q", help)
	}
	documented := map[string]bool{}
	for _, line := range strings.Split(help, "\n") {
		for _, flag := range helpEntryFlags(line) {
			documented[flag] = true
		}
	}
	if len(documented) == 0 {
		t.Fatalf("found no option entries in the help text; the parser broke:\n%s", help)
	}
	for _, flag := range sortedFlags(searchFlags) {
		t.Run(flag, func(t *testing.T) {
			if !documented[flag] {
				t.Errorf("gleann search --help has no option entry for %s, although searchFlags accepts it", flag)
			}
		})
	}
}

// helpEntryFor returns the option entry documenting flag (the line whose flag
// column names it), or "" when the help does not document the flag.
func helpEntryFor(help, flag string) string {
	for _, line := range strings.Split(help, "\n") {
		for _, f := range helpEntryFlags(line) {
			if f == flag {
				return line
			}
		}
	}
	return ""
}

// helpMentionsWord reports whether line names word as a whole token, so
// "openai" does not also count as the word "ai".
func helpMentionsWord(line, word string) bool {
	re := regexp.MustCompile(`(?:^|[^a-z0-9_-])` + regexp.QuoteMeta(word) + `(?:[^a-z0-9_-]|$)`)
	return re.MatchString(line)
}

// TestSearchHelpListsSupportedProviders guards the provider lists of the
// search help against the implementations: a provider that works but is not
// listed is undiscoverable. --rerank-provider omitted auto, --provider
// omitted gemini and native, and --llm-provider omitted eif.
func TestSearchHelpListsSupportedProviders(t *testing.T) {
	help := captureHelp(t, func() { cmdSearch([]string{"--help"}) })
	cases := []struct {
		flag string
		want []string
	}{
		// Every non-test embedding provider; mock is test-only.
		{"--provider", []string{
			string(embedding.ProviderOllama), string(embedding.ProviderOpenAI),
			string(embedding.ProviderLlamaCPP), string(embedding.ProviderEIF),
			string(embedding.ProviderGemini), string(embedding.ProviderNative),
		}},
		// Every non-test LLM provider; mock is test-only.
		{"--llm-provider", []string{
			string(gleann.LLMOllama), string(gleann.LLMOpenAI),
			string(gleann.LLMAnthropic), string(gleann.LLMEIF),
		}},
		// Every reranker engine, including the auto picker.
		{"--rerank-provider", []string{
			string(gleann.RerankerAuto), string(gleann.RerankerEIF),
			string(gleann.RerankerLlamacpp), string(gleann.RerankerOllama),
			string(gleann.RerankerJina), string(gleann.RerankerCohere),
			string(gleann.RerankerVoyage),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.flag, func(t *testing.T) {
			line := helpEntryFor(help, tc.flag)
			if line == "" {
				t.Fatalf("gleann search --help has no option entry for %s", tc.flag)
			}
			for _, want := range tc.want {
				if !helpMentionsWord(line, want) {
					t.Errorf("gleann search --help entry %s does not list provider %q:\n%s", tc.flag, want, line)
				}
			}
		})
	}
}

// TestSearchHelpNotesBuildTagCaveats: `eif` embeddings, `native` embeddings
// and the `eif` LLM only exist in builds with -tags eif / -tags native. The
// default pure-Go build advertises them without the caveat, so a user who
// follows the help gets a BM25-only search (exit 0) or a "compiled without
// EIF" warning instead of what the help promises. The caveat belongs on the
// same option line as the provider token so the two cannot drift apart.
func TestSearchHelpNotesBuildTagCaveats(t *testing.T) {
	help := captureHelp(t, func() { cmdSearch([]string{"--help"}) })
	cases := []struct {
		flag string
		want []string
	}{
		{"--provider", []string{"-tags eif", "-tags native"}},
		{"--llm-provider", []string{"-tags eif"}},
	}
	for _, tc := range cases {
		t.Run(tc.flag, func(t *testing.T) {
			line := helpEntryFor(help, tc.flag)
			if line == "" {
				t.Fatalf("gleann search --help has no option entry for %s", tc.flag)
			}
			for _, want := range tc.want {
				if !strings.Contains(line, want) {
					t.Errorf("gleann search --help entry %s does not note the build-tag requirement %q:\n%s", tc.flag, want, line)
				}
			}
		})
	}
}
