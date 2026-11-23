package main

import (
	"context"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// TestCLIESearchExplainNoteHonoursQuiet: the multi-index --explain note is a
// status message, not an error, so --quiet suppresses it while the results
// stay. Without --quiet the note explains why no explanations are shown.
func TestCLIESearchExplainNoteHonoursQuiet(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("idx1")
	e.build("idx2")
	e.Ollama.setReply("token bucket rate limiter")

	const note = "--explain works for a single index only"

	r := e.run("search", "idx1,idx2", "token bucket rate limiter", "--explain")
	wantExit(t, r, 0)
	mustContain(t, "note without --quiet", r.Err, note)
	mustContain(t, "results without --quiet", r.Out, "Multi-index results for")

	r = e.run("search", "idx1,idx2", "token bucket rate limiter", "--explain", "--quiet")
	wantExit(t, r, 0)
	mustNotContain(t, "note with --quiet", r.Err, note)
	mustContain(t, "results with --quiet", r.Out, "Multi-index results for")
}

// useFakeLlamaRunner replaces the embedded llama-server with d1Runner and
// restores the package-level seam afterwards.
func useFakeLlamaRunner(t *testing.T) {
	t.Helper()
	origNew, origGlobal := newLlamaRunner, globalLlamaRunner
	t.Cleanup(func() {
		if globalLlamaRunner != origGlobal {
			_ = globalLlamaRunner.Stop()
		}
		newLlamaRunner, globalLlamaRunner = origNew, origGlobal
	})
	globalLlamaRunner = nil
	newLlamaRunner = func(string) llamaRunner { return &d1Runner{} }
}

// TestCLIInitLlamaCPPQuiet checks that the quiet variant suppresses both
// startup status lines, while initLlamaCPP keeps printing them and quiet=false
// behaves exactly like initLlamaCPP.
func TestCLIInitLlamaCPPQuiet(t *testing.T) {
	useFakeLlamaRunner(t)

	run := func(fn func(*gleann.Config) error) cliResult {
		cfg := gleann.Config{EmbeddingProvider: "llamacpp", EmbeddingModel: "emb.gguf"}
		return captureRun(t, "", func() {
			if err := fn(&cfg); err != nil {
				t.Errorf("init: %v", err)
			}
		})
	}

	r := run(func(c *gleann.Config) error { return initLlamaCPP(context.Background(), c) })
	mustContain(t, "initLlamaCPP", r.Err, "Starting embedded llama.cpp server", "Embedded llama-server is ready")

	r = run(func(c *gleann.Config) error { return initLlamaCPPQuiet(context.Background(), c, false) })
	mustContain(t, "quiet=false", r.Err, "Starting embedded llama.cpp server", "Embedded llama-server is ready")

	cfg := gleann.Config{EmbeddingProvider: "llamacpp", EmbeddingModel: "emb.gguf"}
	r = captureRun(t, "", func() {
		if err := initLlamaCPPQuiet(context.Background(), &cfg, true); err != nil {
			t.Errorf("init: %v", err)
		}
	})
	mustNotContain(t, "quiet=true", r.Err, "Starting embedded llama.cpp server", "Embedded llama-server is ready")
	if cfg.EmbeddingProvider != "openai" || cfg.OpenAIBaseURL != "http://127.0.0.1:5555" {
		t.Errorf("quiet init did not rewrite the config: %+v", cfg)
	}
}

// TestCLISearchLlamaCPPStartupHonoursQuiet drives the real command: the
// startup lines are suppressed by --quiet, while a genuine failure (a missing
// index) still reaches stderr.
func TestCLISearchLlamaCPPStartupHonoursQuiet(t *testing.T) {
	useFakeLlamaRunner(t)
	e := newCLIEnv(t)

	args := []string{"search", "missing-index", "q", "--provider", "llamacpp", "--model", "emb.gguf"}

	r := e.run(args...)
	wantExit(t, r, 1)
	mustContain(t, "loud", r.Err, "Starting embedded llama.cpp server", "error loading index")

	quietArgs := append(append([]string{}, args...), "--quiet")
	r = e.run(quietArgs...)
	wantExit(t, r, 1)
	mustNotContain(t, "quiet", r.Err, "Starting embedded llama.cpp server", "Embedded llama-server is ready")
	mustContain(t, "error stays", r.Err, "error loading index")
}

// TestCLISearchShutdownHonoursQuiet: the deferred cleanup is the other half of
// the llama-server lifecycle whose startup lines --quiet gates. It must stop
// the runner either way, but only print the shutdown status line when the
// command is loud.
func TestCLISearchShutdownHonoursQuiet(t *testing.T) {
	useFakeLlamaRunner(t)
	e := newCLIEnv(t)

	args := []string{"search", "missing-index", "q", "--provider", "llamacpp", "--model", "emb.gguf"}

	r := e.run(args...)
	wantExit(t, r, 1)
	mustContain(t, "loud", r.Err, "Shutting down embedded llama-server", "error loading index")

	r = e.run(append(append([]string{}, args...), "--quiet")...)
	wantExit(t, r, 1)
	mustNotContain(t, "quiet", r.Err, "Shutting down embedded llama-server")
	mustContain(t, "error stays", r.Err, "error loading index")
}

// TestCLICleanupLlamaCPPHonoursQuiet drives cleanupLlamaCPP directly after a
// quiet and a loud init: the runner must be stopped in both cases, but the
// shutdown status line belongs to the loud one only.
func TestCLICleanupLlamaCPPHonoursQuiet(t *testing.T) {
	useFakeLlamaRunner(t)

	start := func(quiet bool) (*d1Runner, error) {
		fake := &d1Runner{}
		newLlamaRunner = func(string) llamaRunner { return fake }
		cfg := gleann.Config{EmbeddingProvider: "llamacpp", EmbeddingModel: "emb.gguf"}
		return fake, initLlamaCPPQuiet(context.Background(), &cfg, quiet)
	}

	fake, err := start(true)
	if err != nil {
		t.Fatalf("quiet init: %v", err)
	}
	r := captureRun(t, "", cleanupLlamaCPP)
	if !fake.stopped {
		t.Error("quiet cleanup did not stop the runner")
	}
	mustNotContain(t, "quiet cleanup", r.Err, "Shutting down embedded llama-server")

	fake, err = start(false)
	if err != nil {
		t.Fatalf("loud init: %v", err)
	}
	r = captureRun(t, "", cleanupLlamaCPP)
	if !fake.stopped {
		t.Error("loud cleanup did not stop the runner")
	}
	mustContain(t, "loud cleanup", r.Err, "Shutting down embedded llama-server")
}

// TestCLIIndexSyncStartupHonoursQuiet: `index sync` starts the embedded
// llama-server through internal/indexing, whose status lines go to
// Options.Stdout. --quiet must silence them (scripting output pollution) while
// the genuine start failure stays on stderr and the exit status is unchanged.
func TestCLIIndexSyncStartupHonoursQuiet(t *testing.T) {
	// Each case builds its own index: a failed sync must not leak into the next.
	run := func(t *testing.T, quiet bool) cliResult {
		t.Helper()
		e := newCLIEnv(t)
		e.sampleDocs()
		e.build("idx", "--model", "test-embed")
		e.write("README.md", "# Demo project\n\n## Rate limiting\n\nThe token bucket rate limiter allows 5 requests per second.\n")

		args := []string{"index", "sync", "idx", "--provider", "llamacpp"}
		if quiet {
			args = append(args, "--quiet")
		}
		return e.run(args...)
	}

	t.Run("loud", func(t *testing.T) {
		r := run(t, false)
		wantExit(t, r, 1)
		mustContain(t, "sync status", r.Out, "Starting embedded llama.cpp server")
		mustContain(t, "error", r.Err, "error:")
	})

	t.Run("quiet", func(t *testing.T) {
		r := run(t, true)
		wantExit(t, r, 1)
		mustNotContain(t, "quiet sync stdout", r.Out,
			"Starting embedded llama.cpp server",
			"Synchronizing index",
			"Using embedding model",
		)
		mustContain(t, "error stays", r.Err, "error:")
	})
}

// TestHasFlagLastSpellingWins: repeated spellings of a boolean flag are a
// contradiction; the last one wins (the usual CLI convention), including the
// bare, =true, =false, =1 and =0 forms, and nothing after "--" is a flag.
func TestHasFlagLastSpellingWins(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"--quiet=false", "--quiet"}, true},
		{[]string{"--quiet", "--quiet=false"}, false},
		{[]string{"--quiet=0", "--quiet=1"}, true},
		{[]string{"--quiet=1", "--quiet=0"}, false},
		{[]string{"--quiet=false", "--quiet", "--quiet=off"}, false},
		{[]string{"--quiet=false", "--quiet", "--", "--quiet=false"}, true},
		{[]string{"--", "--quiet"}, false},
		{[]string{"--quiet=on"}, true},
		{[]string{"--quiet=no"}, false},
	}
	for _, tc := range cases {
		if got := hasFlag(tc.args, "--quiet"); got != tc.want {
			t.Errorf("hasFlag(%q, %q) = %v, want %v", tc.args, "--quiet", got, tc.want)
		}
	}
}

// TestCLIExplainNoteHonoursLastQuietSpelling drives the mixed spellings
// through the real command: the bare --quiet after --quiet=false still wins.
func TestCLIExplainNoteHonoursLastQuietSpelling(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.build("idx1")
	e.build("idx2")
	e.Ollama.setReply("token bucket rate limiter")

	const note = "--explain works for a single index only"

	r := e.run("search", "idx1,idx2", "token bucket rate limiter", "--explain", "--quiet=false", "--quiet")
	wantExit(t, r, 0)
	mustNotContain(t, "bare --quiet last", r.Err, note)

	r = e.run("search", "idx1,idx2", "token bucket rate limiter", "--explain", "--quiet", "--quiet=false")
	wantExit(t, r, 0)
	mustContain(t, "--quiet=false last", r.Err, note)
}
