package main

import (
	"strings"
	"testing"
)

func TestCheckFlagsSearch(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string // substring; "" means accepted
	}{
		{"plain", []string{"corp", "zebra"}, ""},
		{"unknown after query", []string{"corp", "zebra", "--definitely-not-a-flag"}, "unknown flag --definitely-not-a-flag"},
		{"unknown before query", []string{"corp", "--nope", "zebra"}, "unknown flag --nope"},
		{"unknown with value form", []string{"corp", "--nope=1", "zebra"}, "unknown flag --nope"},
		{"bool flag before and after", []string{"corp", "--json", "zebra", "--rerank"}, ""},
		{"value flag before query", []string{"corp", "--top-k", "3", "--json", "zebra migration"}, ""},
		{"value flag after query", []string{"corp", "zebra migration", "--json", "--top-k", "3"}, ""},
		{"equals form", []string{"corp", "--top-k=3", "zebra"}, ""},
		{"value that looks like a flag is a value", []string{"corp", "--kind", "--weird", "zebra"}, ""},
		{"double dash ends flags", []string{"corp", "--", "--not-a-flag", "x"}, ""},
		{"flag before double dash still checked", []string{"corp", "--bad", "--", "x"}, "unknown flag --bad"},
		{"suggestion", []string{"corp", "q", "--top-kk", "3"}, "did you mean --top-k?"},
		{"config flag", []string{"corp", "q", "--index-dir", "/tmp/x", "--no-mmap"}, ""},
		{"short help", []string{"corp", "-h"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkFlags(tc.args, searchFlags)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestCheckFlagsAsk(t *testing.T) {
	cases := []struct {
		args    []string
		wantErr bool
	}{
		{[]string{"how does it work", "--raw"}, false},
		{[]string{"--continue", "abc", "--role", "code", "question"}, false},
		{[]string{"--attach", "a.png", "--image", "b.png", "q"}, false},
		{[]string{"q", "--expand", "--cite"}, false},
		{[]string{"q", "--bogus"}, true},
		{[]string{"q", "--", "--bogus"}, false},
	}
	for _, tc := range cases {
		err := checkFlags(tc.args, askFlags)
		if (err != nil) != tc.wantErr {
			t.Errorf("checkFlags(%v) = %v, wantErr %v", tc.args, err, tc.wantErr)
		}
	}
}

// A flag shared by the search and ask sets must take a value in both or in neither.
func TestFlagSetsAgree(t *testing.T) {
	for k, v := range searchFlags {
		if w, ok := askFlags[k]; ok && w != v {
			t.Errorf("%s: takes value %v in search but %v in ask", k, v, w)
		}
	}
}

func TestEditDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"", "", 0}, {"abc", "abc", 0}, {"abc", "abd", 1}, {"--top-kk", "--top-k", 1}, {"a", "", 1}} {
		if got := editDistance(c.a, c.b); got != c.want {
			t.Errorf("editDistance(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestConfigExemptCommands(t *testing.T) {
	for _, c := range []string{"config", "setup", "doctor", "version", "help"} {
		if !configExemptCommands[c] {
			t.Errorf("%s must run with a malformed config", c)
		}
	}
	for _, c := range []string{"search", "ask", "index", "serve", "memory"} {
		if configExemptCommands[c] {
			t.Errorf("%s must not be exempt from the config check", c)
		}
	}
}
