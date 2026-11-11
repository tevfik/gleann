package gleann

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// QueryExpander rewrites a natural-language query into extra retrieval text.
type QueryExpander interface {
	// Expand returns text to search together with the query ("" = none).
	Expand(ctx context.Context, query, language string) (string, error)
}

// LLMQueryExpander implements HyDE (hypothetical document embeddings) for
// code search: an LLM writes the code that would answer the question, and
// the question plus that code is embedded and matched lexically. The code
// carries the identifiers and vocabulary of an implementation, which a
// natural-language question lacks.
//
// On the gleann benchmark (121 natural-language questions, `gleann bench`)
// this raised MRR from 0.485 to 0.629 and R@10 from 0.71 to 0.81 with
// qwen3.5:9b (~1.5-3 s per query on a GPU); llama3.2:3b gained only 0.03.
// Queries phrased like doc comments lose a little (-0.05), which the
// reranker recovers. Results are cached per query.
type LLMQueryExpander struct {
	Chat *LeannChat

	mu    sync.Mutex
	cache map[string]string
}

// NewLLMQueryExpander creates a HyDE expander using cfg (an LLM chat
// configuration; MaxTokens defaults to 200, temperature to 0).
func NewLLMQueryExpander(searcher Searcher, cfg ChatConfig) *LLMQueryExpander {
	if cfg.MaxTokens <= 0 || cfg.MaxTokens > 400 {
		cfg.MaxTokens = 200
	}
	cfg.Temperature = 0
	off := false
	if cfg.Think == nil {
		cfg.Think = &off
	}
	return &LLMQueryExpander{Chat: NewChat(searcher, cfg), cache: map[string]string{}}
}

// HyDEPrompt builds the instruction for a hypothetical code snippet.
func HyDEPrompt(query, language string) string {
	if language != "" {
		language += " "
	}
	return "Write a short " + language + "code snippet (function signatures, key identifiers and comments, " +
		"max 15 lines, no explanation) that would implement: " + query
}

// Expand returns a hypothetical code snippet for query.
func (e *LLMQueryExpander) Expand(ctx context.Context, query, language string) (string, error) {
	key := language + "\x00" + query
	e.mu.Lock()
	if v, ok := e.cache[key]; ok {
		e.mu.Unlock()
		return v, nil
	}
	e.mu.Unlock()
	out, err := e.Chat.Complete(ctx, []ChatMessage{{Role: "user", Content: HyDEPrompt(query, language)}})
	if err != nil {
		return "", fmt.Errorf("query expansion: %w", err)
	}
	out = strings.TrimSpace(stripCodeFences(out))
	e.mu.Lock()
	if len(e.cache) > 1024 {
		e.cache = map[string]string{}
	}
	e.cache[key] = out
	e.mu.Unlock()
	return out, nil
}

func stripCodeFences(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// languageByExt names the programming language of common source extensions.
var languageByExt = map[string]string{
	".go": "Go", ".py": "Python", ".ts": "TypeScript", ".tsx": "TypeScript", ".js": "JavaScript",
	".jsx": "JavaScript", ".java": "Java", ".kt": "Kotlin", ".rs": "Rust", ".c": "C", ".h": "C",
	".cc": "C++", ".cpp": "C++", ".hpp": "C++", ".cs": "C#", ".rb": "Ruby", ".php": "PHP",
	".swift": "Swift", ".scala": "Scala", ".m": "Objective-C", ".lua": "Lua", ".sh": "shell",
}

// vendorDirs mark third-party code that should not decide the language.
var vendorDirs = []string{"vendor/", "deps/", "third_party/", "node_modules/", "external/", "_deps/"}

// DominantLanguage returns the most common programming language among the
// first-party passages of the loaded index ("" when there is no source
// code). All passages are counted: indexes often store vendored code first,
// so a prefix sample can pick the wrong language. maxPassages > 0 caps the
// scan for very large indexes.
func (s *LeannSearcher) DominantLanguage(maxPassages int) string {
	s.langOnce.Do(func() {
		if s.passages == nil {
			return
		}
		count := map[string]int{}
		n := 0
		_ = s.passages.ForEachPassage(func(p Passage) error {
			if maxPassages > 0 && n >= maxPassages {
				return errStopIteration
			}
			n++
			src, _ := p.Metadata["source"].(string)
			if src == "" {
				src, _ = p.Metadata["file"].(string)
			}
			for _, v := range vendorDirs {
				if strings.Contains(src, v) {
					return nil
				}
			}
			if lang, ok := languageByExt[strings.ToLower(filepath.Ext(src))]; ok {
				count[lang]++
			}
			return nil
		})
		langs := make([]string, 0, len(count))
		for l := range count {
			langs = append(langs, l)
		}
		sort.Slice(langs, func(i, j int) bool {
			if count[langs[i]] != count[langs[j]] {
				return count[langs[i]] > count[langs[j]]
			}
			return langs[i] < langs[j]
		})
		if len(langs) > 0 {
			s.language = langs[0]
		}
	})
	return s.language
}

var errStopIteration = fmt.Errorf("stop iteration")

// SetQueryExpander sets the expander used when a search asks for expansion.
func (s *LeannSearcher) SetQueryExpander(e QueryExpander) {
	s.expander = e
}

// WithQueryExpansion enables HyDE-style expansion for natural-language
// queries (identifier-like queries are never expanded). Needs an expander
// set with SetQueryExpander or passed in SearchConfig.Expander.
func WithQueryExpansion(enabled bool) SearchOption {
	return func(c *SearchConfig) {
		c.Expand = enabled
	}
}

// WithQueryExpander sets a per-request expander and enables expansion.
func WithQueryExpander(e QueryExpander) SearchOption {
	return func(c *SearchConfig) {
		c.Expander = e
		c.Expand = e != nil
	}
}

// retrievalQuery returns the text used for embedding and BM25: the query
// plus its expansion when expansion is enabled and applicable.
func (s *LeannSearcher) retrievalQuery(ctx context.Context, query string, opts SearchConfig) string {
	if !opts.Expand || IsIdentifierQuery(query) {
		return query
	}
	e := opts.Expander
	if e == nil {
		e = s.expander
	}
	if e == nil {
		return query
	}
	extra, err := e.Expand(ctx, query, s.DominantLanguage(200000))
	if err != nil || strings.TrimSpace(extra) == "" {
		if err != nil {
			fmt.Fprintf(os.Stderr, "query expansion warning: %v (searching the query only)\n", err)
		}
		return query
	}
	return query + "\n" + extra
}
