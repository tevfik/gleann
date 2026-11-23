package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// helpOut runs `gleann <args...>` in-process and returns stdout+stderr. It
// fails the test unless the command exits 0: asking for help is not an error.
func helpOut(t *testing.T, args ...string) string {
	t.Helper()
	r := newCLIEnv(t).run(args...)
	if r.Code != 0 {
		t.Fatalf("gleann %s: exit %d, want 0\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.Code, r.Out, r.Err)
	}
	return r.combined()
}

var helpFlagRe = regexp.MustCompile(`(?m)(?:^|[\s,\[(])(--[a-z][a-z0-9-]*)`)

// usageFlagsIn returns every --flag named in a usage text.
func usageFlagsIn(out string) map[string]bool {
	m := map[string]bool{}
	for _, g := range helpFlagRe.FindAllStringSubmatch(out, -1) {
		m[g[1]] = true
	}
	return m
}

// ownFlags returns the flags of set that are not shared config flags.
func ownFlags(set flagSet) []string {
	var out []string
	for f := range set {
		if _, shared := configFlags[f]; !shared {
			out = append(out, f)
		}
	}
	return out
}

// Items 1 and 3: ask --help prints usage, exits 0 and lists every flag ask accepts.
func TestHelpAsk(t *testing.T) {
	for _, h := range []string{"--help", "-h"} {
		out := helpOut(t, "ask", h)
		if !strings.Contains(out, "usage: gleann ask") {
			t.Errorf("ask %s: no usage text:\n%s", h, out)
		}
		if strings.Contains(out, "no question provided") {
			t.Errorf("ask %s: printed the missing-question error:\n%s", h, out)
		}
	}
	out := helpOut(t, "ask", "--help")
	// A first-run hint may precede the usage; only the usage is checked.
	if i := strings.Index(out, "usage: gleann ask"); i >= 0 {
		out = out[i:]
	}
	for _, f := range ownFlags(askFlags) {
		if f != "--help" && !strings.Contains(out, f) {
			t.Errorf("ask usage does not list %s", f)
		}
	}
	for f := range usageFlagsIn(out) {
		if _, ok := askFlags[f]; !ok {
			t.Errorf("ask usage lists %s, which ask does not accept", f)
		}
	}
}

// Item 2: chat --help prints usage (no TUI) and lists exactly chat's own flags.
func TestHelpChat(t *testing.T) {
	for _, h := range []string{"--help", "-h", "--help=1"} {
		out := helpOut(t, "chat", h)
		if !strings.Contains(out, "Usage: gleann chat") {
			t.Errorf("chat %s: no usage text:\n%s", h, out)
		}
	}
	out := helpOut(t, "chat", "--help")
	for _, f := range ownFlags(chatFlags) {
		if f != "--help" && !strings.Contains(out, f) {
			t.Errorf("chat usage does not list %s", f)
		}
	}
	for f := range usageFlagsIn(out) {
		if _, ok := chatFlags[f]; !ok {
			t.Errorf("chat usage lists %s, which chat does not accept", f)
		}
	}
}

// Item 4: setup --help prints usage instead of starting the wizard.
func TestHelpSetup(t *testing.T) {
	for _, h := range []string{"--help", "-h", "--help=true", "-h=1"} {
		out := helpOut(t, "setup", h)
		if !strings.Contains(out, "Usage: gleann setup") {
			t.Errorf("setup %s: no usage text:\n%s", h, out)
		}
		if strings.Contains(out, "zero to working") || strings.Contains(out, "Setup cancelled") {
			t.Errorf("setup %s: started the wizard:\n%s", h, out)
		}
	}
	out := helpOut(t, "setup", "--help")
	for _, f := range ownFlags(setupFlags) {
		if f != "--help" && !strings.Contains(out, f) {
			t.Errorf("setup usage does not list %s", f)
		}
	}
	for f := range usageFlagsIn(out) {
		if _, ok := setupFlags[f]; !ok {
			t.Errorf("setup usage lists %s, which setup does not accept", f)
		}
	}
}

// Item 5: every graph subcommand honours --help without needing --index.
func TestHelpGraphSubcommands(t *testing.T) {
	for _, args := range [][]string{
		{"graph", "deps", "--help"},
		{"graph", "deps", "main.main", "-h"},
		{"graph", "query", "--help"},
		{"graph", "toc", "--help"},
		{"graph", "report", "--help"},
		{"graph", "viz", "--help"},
		{"graph", "communities", "-h"},
		{"graph", "export", "--help"},
		{"graph", "build", "--help"},
		{"graph", "-h"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out := helpOut(t, args...)
			if !strings.Contains(out, "gleann graph") {
				t.Errorf("no graph usage text:\n%s", out)
			}
			if strings.Contains(out, "flag required") {
				t.Errorf("complained about a missing flag:\n%s", out)
			}
		})
	}
	if out := helpOut(t, "graph", "--help"); !strings.Contains(out, "[--quiet]") {
		t.Errorf("graph build usage line does not list --quiet:\n%s", out)
	}
}

// Item 6: --prune is the HNSW keep fraction, not "prune unchanged files".
func TestHelpIndexPrune(t *testing.T) {
	out := helpOut(t, "index", "--help")
	if strings.Contains(out, "Prune unchanged files") {
		t.Errorf("index usage still documents --prune as pruning unchanged files:\n%s", out)
	}
	if !strings.Contains(out, "--prune <fraction>") || !strings.Contains(out, "HNSW") {
		t.Errorf("index usage does not document --prune as the HNSW keep fraction:\n%s", out)
	}
}

// Item 7: memory --help lists compact.
func TestHelpMemoryCompact(t *testing.T) {
	out := helpOut(t, "memory", "--help")
	if !strings.Contains(out, "memory compact") || !strings.Contains(out, "--min-validity <0-1>") {
		t.Errorf("memory usage does not list compact [--min-validity <0-1>]:\n%s", out)
	}
}

// Item 8: tokens and completion treat --help as a request for usage.
func TestHelpTokensAndCompletion(t *testing.T) {
	for _, h := range []string{"--help", "-h"} {
		if out := helpOut(t, "tokens", h); !strings.Contains(out, "gleann tokens") || strings.Contains(out, "stat ") {
			t.Errorf("tokens %s: wrong output:\n%s", h, out)
		}
		out := helpOut(t, "completion", h)
		if !strings.Contains(out, "Usage: gleann completion") || strings.Contains(out, "unsupported shell") {
			t.Errorf("completion %s: wrong output:\n%s", h, out)
		}
	}
	// No argument still fails: the usage is an error there.
	if r := newCLIEnv(t).run("completion"); r.Code != 1 {
		t.Errorf("completion without a shell: exit %d, want 1", r.Code)
	}
}

// Item 9: service usage documents --port; --help never runs a subcommand.
func TestHelpServicePort(t *testing.T) {
	origInstall, origUninstall, origStart := serviceInstall, serviceUninstall, serviceStart
	t.Cleanup(func() { serviceInstall, serviceUninstall, serviceStart = origInstall, origUninstall, origStart })
	serviceInstall = func(string, string) error { t.Fatal("serviceInstall must not run on --help"); return nil }
	serviceUninstall = func() error { t.Fatal("serviceUninstall must not run on --help"); return nil }
	serviceStart = func(string, string) error { t.Fatal("serviceStart must not run on --help"); return nil }

	for _, args := range [][]string{
		{"service", "--help"}, {"service", "-h"}, {"service", "install", "--help"},
		{"service", "install", "--help=true"}, {"service", "start", "--help=1"},
		{"service", "restart", "-h=1"}, {"service", "uninstall", "-h"},
	} {
		out := helpOut(t, args...)
		if !strings.Contains(out, "Usage: gleann service") || !strings.Contains(out, "--port") {
			t.Errorf("%v: usage text missing or without --port:\n%s", args, out)
		}
	}
}

// --help is checked before unknown flags in every command that has a usage.
func TestHelpBeforeUnknownFlag(t *testing.T) {
	for _, c := range []string{"setup", "tokens", "completion", "ask", "chat"} {
		helpOut(t, c, "--bogus", "--help")
	}
}

// Flag values of ask (--base-url, --index-dir, ...) are not question words.
func TestAskPositionalSkipsFlagValues(t *testing.T) {
	pos, att := extractPositionalArgs([]string{"idx", "why", "--base-url", "http://x:1", "--index-dir", "/d",
		"--rerank-pool", "5", "--hybrid", "0.5", "--image", "a.png", "--cite", "--title=t"})
	if strings.Join(pos, " ") != "idx why" {
		t.Errorf("positional = %q, want [idx why]", pos)
	}
	if len(att) != 1 || att[0] != "a.png" {
		t.Errorf("attach = %q, want [a.png]", att)
	}
}

// ask --rerank-pool reaches the search config like it does for gleann search.
func TestAskRerankPool(t *testing.T) {
	var c gleann.Config
	applyAskRerankPool(&c, []string{"q", "--rerank-pool", "37"})
	if c.SearchConfig.RerankPool != 37 {
		t.Errorf("RerankPool = %d, want 37", c.SearchConfig.RerankPool)
	}
	c = gleann.Config{}
	applyAskRerankPool(&c, []string{"q", "--rerank-pool", "x"})
	applyAskRerankPool(&c, []string{"q"})
	if c.SearchConfig.RerankPool != 0 {
		t.Errorf("invalid or missing pool changed RerankPool to %d", c.SearchConfig.RerankPool)
	}
}

func TestWantsHelp(t *testing.T) {
	for args, want := range map[string]bool{
		"--help": true, "x -h": true, "--help=true": true, "-h=1": true, "x --help=false": true, "--helpful": false, "x": false, "-- --help": false, "": false,
	} {
		if got := wantsHelp(strings.Fields(args)); got != want {
			t.Errorf("wantsHelp(%q) = %v, want %v", args, got, want)
		}
	}
}
