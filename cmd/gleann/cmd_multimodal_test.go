package main

import (
	"strings"
	"testing"
)

// The multimodal usage must be a single clean text: no duplicated or mangled
// fragments, and it names the flags analyze actually reads.
func TestMultimodalUsageIsClean(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"analyze", "--help"}} {
		out := captureHelp(t, func() { cmdMultimodal(args) })
		if n := strings.Count(out, "Usage: gleann multimodal analyze <file|dir>"); n != 1 {
			t.Errorf("multimodal %v: usage line appears %d times, want 1:\n%s", args, n, out)
		}
		for _, frag := range []string{"]|usage:", "]|dir>"} {
			if strings.Contains(out, frag) {
				t.Errorf("multimodal %v: garbled fragment %q in usage:\n%s", args, frag, out)
			}
		}
		for _, f := range []string{"--model", "--host", "--ollama-api-key"} {
			if !strings.Contains(out, f) {
				t.Errorf("multimodal %v: usage does not mention %s:\n%s", args, f, out)
			}
		}
	}
}
