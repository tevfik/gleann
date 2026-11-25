package main

import (
	"os"
	"testing"
)

// setup --auto pulls models only after an explicit yes: a typed y/yes or
// --yes. EOF (</dev/null), an empty line or a closed pipe mean no.
func TestSetupAutoPullNeedsExplicitYes(t *testing.T) {
	e := newCLIEnv(t)
	e.Ollama.mu.Lock()
	e.Ollama.models = []string{"llama3.2:latest"} // the default models are missing
	e.Ollama.mu.Unlock()
	empty := t.TempDir()

	for name, stdin := range map[string]string{"EOF": "", "empty line": "\n", "blank": "  \n"} {
		r := e.runIn(stdin, "setup", "--auto", "--host", e.Ollama.URL, "--docs", empty)
		wantExit(t, r, 1)
		mustContain(t, name, r.Out, "nothing was saved or downloaded", "--yes")
		if n := e.Ollama.count("/api/pull"); n != 0 {
			t.Fatalf("%s: %d /api/pull requests without a yes", name, n)
		}
		if _, err := os.Stat(d2ConfigPath(e)); !os.IsNotExist(err) {
			t.Errorf("%s: a config was saved without a yes", name)
		}
	}

	// Anything but an affirmative answer cancels without a pull.
	wantExit(t, e.runIn("maybe\n", "setup", "--auto", "--host", e.Ollama.URL, "--docs", empty), 0)
	if n := e.Ollama.count("/api/pull"); n != 0 {
		t.Fatalf("%d /api/pull requests after a non-yes answer", n)
	}

	// An explicit yes pulls (the empty docs dir then ends the run with 1).
	for _, ans := range []string{"y\n", "YES\n"} {
		before := e.Ollama.count("/api/pull")
		r := e.runIn(ans, "setup", "--auto", "--host", e.Ollama.URL, "--docs", empty)
		wantExit(t, r, 1)
		if e.Ollama.count("/api/pull") == before {
			t.Errorf("answer %q did not pull", ans)
		}
	}
}
