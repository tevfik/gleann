package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// flagSet maps a long flag to whether it consumes the next argument. A flag
// that is not in the set is unknown.
type flagSet map[string]bool

// union returns a new set holding the flags of fs and every set in more.
func (fs flagSet) union(more ...flagSet) flagSet {
	out := make(flagSet, len(fs))
	for k, v := range fs {
		out[k] = v
	}
	for _, m := range more {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// configFlags are the flags read by getConfig and applySavedConfig, which
// every command that builds a gleann.Config shares.
var configFlags = flagSet{
	"--model": true, "--code-model": true, "--code-embedding-model": true,
	"--doc-model": true, "--docs-model": true, "--doc-embedding-model": true,
	"--provider": true, "--index-dir": true, "--top-k": true, "--ef-search": true,
	"--chunk-size": true, "--chunk-overlap": true, "--batch-size": true,
	"--concurrency": true, "--hybrid": true, "--metric": true, "--prune": true,
	"--storage": true, "--backend": true, "--pq-dim": true, "--pq-centroids": true,
	"--search-l": true, "--search-pq-rerank": true, "--diskann-r": true,
	"--diskann-l": true, "--diskann-alpha": true, "--host": true,
	"--ollama-api-key": true, "--openai-base-url": true,
	"--llm-model": true, "--llm-provider": true,
	"--signatures": false, "--signatures-only": false, "--no-mmap": false,
	"--quiet": false, "--help": false,
}

// rerankExpandFlags are shared by search and ask (wantRerank,
// rerankerConfigFromFlags, wantExpand, newQueryExpander).
var rerankExpandFlags = flagSet{
	"--rerank": false, "--no-rerank": false, "--rerank-model": true,
	"--rerank-url": true, "--rerank-pool": true, "--rerank-provider": true,
	"--expand": false, "--no-expand": false, "--expand-model": true,
}

// searchFlags is every flag `gleann search` understands. "--top" and
// "--base-url" are accepted for old scripts although nothing reads them.
var searchFlags = configFlags.union(rerankExpandFlags, flagSet{
	"--all": false, "--json": false, "--no-hybrid": false, "--hybrid-alpha": true,
	"--graph": false, "--one-per-file": false, "--include-tests": false,
	"--explain": false, "--kind": true,
	"--top": true, "--base-url": true, "--api-key": true,
})

// askFlags is every flag `gleann ask` understands.
var askFlags = configFlags.union(rerankExpandFlags, flagSet{
	"--interactive": false, "--agent": false, "--swarm": false,
	"--continue": true, "--continue-last": false, "--title": true, "--role": true,
	"--format": true, "--attach": true, "--image": true, "--raw": false,
	"--cite": false, "--no-cache": false, "--no-limit": false,
	"--word-wrap": true, "--repeat-penalty": true, "--repetition-penalty": true,
	"--classic": false, "--modular": false, "--smart-context": false,
	"--compress-context": false, "--graph": false,
	"--base-url": true, "--api-key": true,
})

// checkFlags returns an error for the first --flag in args that is not in
// known. Values of value-taking flags are skipped, "--flag=value" is one
// token, and a literal "--" ends flag parsing so queries can contain dashes.
// "-h" and "--help" are always accepted, and an argument whose name part
// contains whitespace is free text, not a flag.
func checkFlags(args []string, known flagSet) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return nil
		}
		if !strings.HasPrefix(a, "--") {
			continue
		}
		name, _, hasEq := strings.Cut(a, "=")
		if name == "--help" {
			continue
		}
		// Free text such as "--force is dangerous" is not a flag name.
		if strings.ContainsAny(name, " \t\n") {
			continue
		}
		takesValue, ok := known[name]
		if !ok {
			msg := "unknown flag " + name
			if s := suggestFlag(name, known); s != "" {
				msg += " (did you mean " + s + "?)"
			}
			return fmt.Errorf("%s", msg)
		}
		if takesValue && !hasEq {
			i++
		}
	}
	return nil
}

// suggestFlag returns the known flag closest to name, or "" when none is
// close enough to be a plausible typo.
func suggestFlag(name string, known flagSet) string {
	names := make([]string, 0, len(known))
	for k := range known {
		names = append(names, k)
	}
	sort.Strings(names)
	best, bestDist := "", 3
	for _, k := range names {
		if d := editDistance(name, k); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// rejectUnknownFlags prints the error and exits with status 2 (usage error)
// when args holds a flag that known does not list.
func rejectUnknownFlags(cmd string, args []string, known flagSet) {
	if err := checkFlags(args, known); err != nil {
		fmt.Fprintf(os.Stderr, "gleann %s: %v\n", cmd, err)
		exitFunc(2)
	}
}

// stripDoubleDash returns args without the first "--" so the text after it
// reaches code that does not know the separator.
func stripDoubleDash(args []string) []string {
	for i, a := range args {
		if a == "--" {
			out := make([]string, 0, len(args)-1)
			out = append(out, args[:i]...)
			return append(out, args[i+1:]...)
		}
	}
	return args
}

// freeText joins the arguments that are not flags of known (the words of a
// query or a note). Everything after "--" is text, flags included.
func freeText(args []string, known flagSet) string {
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			words = append(words, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "--") {
			name, _, hasEq := strings.Cut(a, "=")
			if takesValue, ok := known[name]; ok {
				if takesValue && !hasEq {
					i++
				}
				continue
			}
		}
		words = append(words, a)
	}
	return strings.Join(words, " ")
}

// expandFlagValues rewrites "--flag=value" into the two tokens "--flag" and
// "value" for the value-taking flags of known, and "--flag=true" into "--flag"
// for its boolean flags ("--flag=false" is dropped), so code that scans for
// "--flag value" sees both spellings. Arguments after a literal "--" and
// unknown flags are left alone.
func expandFlagValues(args []string, known flagSet) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			return append(out, args[i:]...)
		}
		name, value, hasEq := strings.Cut(a, "=")
		takesValue, ok := known[name]
		if !hasEq || !ok || !strings.HasPrefix(a, "--") {
			out = append(out, a)
			continue
		}
		switch {
		case takesValue:
			out = append(out, name, value)
		case parseBoolFlag(value):
			out = append(out, name)
		}
	}
	return out
}

// parseBoolFlag reads the value of "--flag=value" for a boolean flag.
func parseBoolFlag(v string) bool {
	switch strings.ToLower(v) {
	case "", "1", "t", "true", "yes", "on":
		return true
	}
	return false
}
