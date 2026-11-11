package main

import (
	"github.com/tevfik/gleann/pkg/gleann"
)

// wantExpand reports whether searches use HyDE query expansion: --expand,
// or the saved "expand" search setting unless --no-expand is given.
func wantExpand(config gleann.Config, args []string) bool {
	if hasFlag(args, "--no-expand") {
		return false
	}
	return hasFlag(args, "--expand") || config.SearchConfig.Expand
}

// newQueryExpander builds the HyDE expander from the chat settings, with the
// model from --expand-model or the expand_model setting when given.
func newQueryExpander(s gleann.Searcher, args []string, config gleann.Config) *gleann.LLMQueryExpander {
	cc := buildChatConfig(args, config)
	if m := getFlag(args, "--expand-model"); m != "" {
		cc.Model = m
	} else if config.SearchConfig.ExpandModel != "" {
		cc.Model = config.SearchConfig.ExpandModel
	}
	cc.MaxTokens = 120
	return gleann.NewLLMQueryExpander(s, cc)
}
