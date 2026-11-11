package gleann

import (
	"path/filepath"
	"strings"
)

// EmbeddingPrefixes returns the text put before passages and before queries when they are embedded: the configured
// build and query prompt templates, else "passage: " and "query: " for the E5 family (e5-*, multilingual-e5-*),
// which is trained with them and ranks noticeably worse without, else nothing. A build records them in the index's
// meta (PassagePrefix, QueryPrefix), and searches and updates of that index use the recorded ones, so indexes built
// before prefixes existed keep working unchanged.
func EmbeddingPrefixes(cfg Config, model string) (passage, query string) {
	if cfg.BuildPromptTemplate != "" || cfg.QueryPromptTemplate != "" {
		return cfg.BuildPromptTemplate, cfg.QueryPromptTemplate
	}
	base := strings.ToLower(filepath.Base(model))
	if strings.HasPrefix(base, "e5-") || strings.Contains(base, "multilingual-e5") {
		return "passage: ", "query: "
	}
	return "", ""
}

// withPrefix returns texts with prefix in front of each (texts itself when prefix is empty).
func withPrefix(prefix string, texts []string) []string {
	if prefix == "" {
		return texts
	}
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = prefix + t
	}
	return out
}

// recordedPassagePrefix is the passage prefix an existing index was built with ("" when unknown).
func recordedPassagePrefix(indexDir, name string) string {
	if meta, err := GetIndexMeta(indexDir, name); err == nil {
		return meta.PassagePrefix
	}
	return ""
}

// EmbeddingPrefixes returns the passage and query prefixes the loaded index
// was built with ("" for indexes without prefixes).
func (s *LeannSearcher) EmbeddingPrefixes() (passage, query string) {
	return s.meta.PassagePrefix, s.meta.QueryPrefix
}
