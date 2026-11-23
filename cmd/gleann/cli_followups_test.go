package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// "--" ends the flags of ask: what follows is question text, verbatim, so a
// dash-prefixed word is not a flag, a value or an attached file.
func TestAskDoubleDashIsQuestionText(t *testing.T) {
	pos, att := extractPositionalArgs([]string{"idx", "--", "--graph", "why", "--image", "x.png", "--"})
	if got := strings.Join(pos, "|"); got != "idx|--graph|why|--image|x.png|--" {
		t.Errorf("positional = %q, want idx|--graph|why|--image|x.png|--", got)
	}
	if len(att) != 0 {
		t.Errorf("attach = %q, want none", att)
	}
	// Flags before "--" still work.
	pos, att = extractPositionalArgs([]string{"--image", "a.png", "idx", "--cite", "--", "--raw"})
	if got := strings.Join(pos, "|"); got != "idx|--raw" || len(att) != 1 || att[0] != "a.png" {
		t.Errorf("positional = %q attach = %q, want idx|--raw and [a.png]", got, att)
	}
}

// ask --rerank-pool N reaches the config the searcher is built from: deleting
// the applyAskRerankPool call in createAskSearcher fails this test.
func TestAskRerankPoolReachesSearcher(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")
	e.Ollama.setReply("ok")

	var got []gleann.Config
	orig := newAskSearcher
	t.Cleanup(func() { newAskSearcher = orig })
	newAskSearcher = func(c gleann.Config, emb gleann.EmbeddingComputer) *gleann.LeannSearcher {
		got = append(got, c)
		return orig(c, emb)
	}
	r := e.run("ask", "demo", "how", "are", "tokens", "validated?", "--rerank-pool", "37", "--no-cache")
	wantExit(t, r, 0)
	if len(got) != 1 {
		t.Fatalf("searchers built = %d, want 1", len(got))
	}
	if got[0].SearchConfig.RerankPool != 37 {
		t.Errorf("RerankPool reaching the searcher = %d, want 37", got[0].SearchConfig.RerankPool)
	}
}

// A help flag that is the value of a value-taking flag is not a help request.
func TestWantsHelpSkipsFlagValues(t *testing.T) {
	known := flagSet{"--query": true, "--json": false}
	for args, want := range map[string]bool{
		"--query -h":           false,
		"--query --help":       false,
		"--query=-h x":         false,
		"--json -h":            true,
		"--query x -h":         true,
		"--query -h --help":    true,
		"--bogus -h":           true,
		"--query -h -- --help": false,
	} {
		if got := wantsHelpIn(strings.Fields(args), known); got != want {
			t.Errorf("wantsHelpIn(%q) = %v, want %v", args, got, want)
		}
	}
	// Without a spec the old behaviour stays.
	if !wantsHelp([]string{"--query", "-h"}) {
		t.Error("wantsHelp without a flag spec must see -h")
	}
	// A command uses its spec: `ask --model -h` runs, it does not print usage.
	if wantsHelpIn([]string{"--model", "--help", "q"}, askFlags) {
		t.Error("ask: --help as the value of --model was taken for a help request")
	}
	if !wantsHelpIn([]string{"--model", "m", "--help"}, askFlags) {
		t.Error("ask: --help after a complete --model m is not a help request")
	}
}

// The ask usage does not advertise --classic as a working flag: it is a chat
// flag that ask only tolerates.
func TestAskUsageClassicIsNoEffect(t *testing.T) {
	out := helpOut(t, "ask", "--help")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "--classic") && !strings.Contains(line, "no effect") {
			t.Errorf("ask usage line promises --classic: %q", line)
		}
	}
	if !strings.Contains(out, "--classic") {
		t.Error("ask usage no longer lists --classic")
	}
}

// graph hook --help prints the hook usage, not the general graph usage.
func TestGraphHookHelp(t *testing.T) {
	for _, h := range []string{"--help", "-h"} {
		out := helpOut(t, "graph", "hook", h)
		if !strings.Contains(out, "Usage: gleann graph hook <install|uninstall|status>") || strings.Contains(out, "gleann graph deps") {
			t.Errorf("graph hook %s printed:\n%s", h, out)
		}
	}
	// The other subcommands keep the general usage.
	if out := helpOut(t, "graph", "deps", "--help"); !strings.Contains(out, "gleann graph deps") {
		t.Errorf("graph deps --help lost the general usage:\n%s", out)
	}
}

// index watch --quiet silences the status lines (stdout stays empty), as
// index sync --quiet does, and --help lists the flag.
func TestWatchQuiet(t *testing.T) {
	if out := helpOut(t, "index", "watch", "--help"); !strings.Contains(out, "--quiet") {
		t.Errorf("index watch usage does not list --quiet:\n%s", out)
	}
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("w")
	orig := watchNotify
	t.Cleanup(func() { watchNotify = orig })
	watchNotify = func(stop chan<- os.Signal) { stop <- os.Interrupt }
	r := e.run("index", "watch", "w", "--docs", e.Docs, "--quiet")
	wantExit(t, r, 0)
	if strings.TrimSpace(r.Out) != "" {
		t.Errorf("index watch --quiet wrote to stdout:\n%s", r.Out)
	}
	r = e.run("index", "watch", "w", "--docs", e.Docs)
	wantExit(t, r, 0)
	mustContain(t, "without --quiet", r.Out, "Watching", "Stopping watcher...")
}

// getConfig reads no flag after "--": that text belongs to the question.
func TestGetConfigStopsAtDoubleDash(t *testing.T) {
	newCLIEnv(t)
	base := getConfig([]string{"demo"})
	c := getConfig([]string{"demo", "--", "--top-k", "99", "--model", "zzz", "--index-dir", "/nonexistent-x"})
	if c.SearchConfig.TopK != base.SearchConfig.TopK || c.EmbeddingModel != base.EmbeddingModel || c.IndexDir != base.IndexDir {
		t.Errorf("flags after -- changed the config: topk %d model %q dir %q", c.SearchConfig.TopK, c.EmbeddingModel, c.IndexDir)
	}
	c = getConfig([]string{"demo", "--top-k", "7", "--", "--top-k", "99"})
	if c.SearchConfig.TopK != 7 {
		t.Errorf("TopK = %d, want 7 (the flag before --)", c.SearchConfig.TopK)
	}
}

// A dash-prefixed question word after "--" must not change where ask and
// search look for the index.
func TestDoubleDashTextDoesNotChangeIndexDir(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("demo")
	e.Ollama.setReply("ok")

	var dirs []string
	orig := newAskSearcher
	t.Cleanup(func() { newAskSearcher = orig })
	newAskSearcher = func(c gleann.Config, emb gleann.EmbeddingComputer) *gleann.LeannSearcher {
		dirs = append(dirs, c.IndexDir)
		return orig(c, emb)
	}
	r := e.run("ask", "demo", "--no-cache", "--", "why", "--index-dir", "/nonexistent-x")
	wantExit(t, r, 0)
	if len(dirs) != 1 || dirs[0] != e.IndexDir {
		t.Errorf("ask IndexDir = %q, want %q", dirs, e.IndexDir)
	}

	r = e.run("search", "demo", "--", "validate", "--index-dir", "/nonexistent-x")
	wantExit(t, r, 0)
}

// The installed post-commit hook is silent while GLEANN_GRAPH_INDEX is unset,
// as `graph hook --help` says, and announces the sync once it is set.
func TestGraphHookSilentWithoutIndex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs the hook with sh")
	}
	e := newCLIEnv(t)
	wd, _ := os.Getwd()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = wd
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(wd, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "x")
	// diff-tree of a root commit lists nothing: the hook needs a second one.
	if err := os.WriteFile(filepath.Join(wd, "b.go"), []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "y")
	wantExit(t, e.run("graph", "hook", "install"), 0)
	hook := filepath.Join(wd, ".git", "hooks", "post-commit")
	// A user line after the gleann block must run whatever the block does.
	f, err := os.OpenFile(hook, os.O_APPEND|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("echo user-after\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if out, err := exec.Command("sh", "-n", hook).CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v %s", err, out)
	}

	runHook := func(env ...string) string {
		cmd := exec.Command("sh", hook)
		cmd.Dir = wd
		cmd.Env = append(os.Environ(), env...)
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	if out := runHook("GLEANN_GRAPH_INDEX="); strings.TrimSpace(out) != "user-after" {
		t.Errorf("hook without GLEANN_GRAPH_INDEX printed %q, want only the user line", out)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gleann"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := runHook("GLEANN_GRAPH_INDEX=demo", "PATH="+bin+":/usr/bin:/bin"); !strings.Contains(out, "syncing index demo") || !strings.Contains(out, "user-after") {
		t.Errorf("hook with GLEANN_GRAPH_INDEX printed:\n%s", out)
	}
	// No gleann binary: the notice is printed and the user line still runs.
	if out := runHook("GLEANN_GRAPH_INDEX=demo", "PATH=/usr/bin:/bin"); !strings.Contains(out, "binary not found") || !strings.Contains(out, "user-after") {
		t.Errorf("hook without a gleann binary printed:\n%s", out)
	}
}
