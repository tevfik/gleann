package gleann

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// passageList is a PassageStore that only knows how to iterate a fixed list.
type passageList struct {
	PassageStore
	list []Passage
}

func (c passageList) ForEachPassage(fn func(Passage) error) error {
	for _, p := range c.list {
		if err := fn(p); err != nil {
			return err
		}
	}
	return nil
}

func fxPassage(key, val string) Passage {
	return Passage{Metadata: map[string]any{key: val}}
}

func TestNewLLMQueryExpanderDefaults(t *testing.T) {
	yes := true
	cases := []struct {
		name          string
		in            ChatConfig
		wantMax       int
		wantThinkTrue bool
	}{
		{"zero max tokens", ChatConfig{Provider: LLMMock}, 200, false},
		{"oversized max tokens", ChatConfig{Provider: LLMMock, MaxTokens: 9000}, 200, false},
		{"reasonable max tokens kept", ChatConfig{Provider: LLMMock, MaxTokens: 300}, 300, false},
		{"explicit think kept", ChatConfig{Provider: LLMMock, Think: &yes, Temperature: 0.9}, 200, true},
	}
	for _, c := range cases {
		e := NewLLMQueryExpander(nil, c.in)
		cfg := e.Chat.Config()
		if cfg.MaxTokens != c.wantMax || cfg.Temperature != 0 || cfg.Think == nil || *cfg.Think != c.wantThinkTrue {
			t.Errorf("%s: max=%d temp=%v think=%v", c.name, cfg.MaxTokens, cfg.Temperature, cfg.Think)
		}
	}
}

func TestExpandFailureIsWrappedAndNotCached(t *testing.T) {
	e := NewLLMQueryExpander(nil, ChatConfig{Provider: "no-such-provider"})
	_, err := e.Expand(context.Background(), "how does indexing work", "Go")
	if err == nil || !strings.Contains(err.Error(), "query expansion:") {
		t.Fatalf("err = %v", err)
	}
	if len(e.cache) != 0 {
		t.Error("a failed expansion must not be cached")
	}
}

func TestExpandCacheIsBounded(t *testing.T) {
	e := NewLLMQueryExpander(nil, ChatConfig{Provider: LLMMock})
	ctx := context.Background()
	for i := 0; i <= 1030; i++ {
		if _, err := e.Expand(ctx, fmt.Sprintf("query number %d", i), "Go"); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(e.cache); n > 1030 || n == 0 {
		t.Errorf("cache size = %d, want it reset once it passed 1024 entries", n)
	}
	// A repeated query is answered from the cache (the mock would give the same text,
	// so check the entry exists under the language-qualified key).
	if _, ok := e.cache["Go\x00query number 1030"]; !ok {
		t.Error("latest expansion missing from the cache")
	}
}

func TestQueryExpansionOptions(t *testing.T) {
	var cfg SearchConfig
	WithQueryExpansion(true)(&cfg)
	if !cfg.Expand {
		t.Error("WithQueryExpansion(true) did not enable expansion")
	}
	WithQueryExpansion(false)(&cfg)
	if cfg.Expand {
		t.Error("WithQueryExpansion(false) did not disable expansion")
	}

	exp := &fakeExpander{}
	WithQueryExpander(exp)(&cfg)
	if !cfg.Expand || cfg.Expander != exp {
		t.Errorf("WithQueryExpander: %+v", cfg)
	}
	WithQueryExpander(nil)(&cfg)
	if cfg.Expand || cfg.Expander != nil {
		t.Errorf("WithQueryExpander(nil) must switch expansion off: %+v", cfg)
	}
}

func TestDominantLanguageFaults(t *testing.T) {
	cases := []struct {
		name string
		list []Passage
		max  int
		want string
	}{
		{"majority wins", []Passage{fxPassage("source", "a.py"), fxPassage("source", "b.go"), fxPassage("source", "c.go")}, 0, "Go"},
		{"tie goes to the alphabetically first language", []Passage{fxPassage("source", "a.py"), fxPassage("source", "b.go")}, 0, "Go"},
		{"file key is the fallback", []Passage{fxPassage("file", "x/main.RS")}, 0, "Rust"},
		{"vendored code is ignored", []Passage{fxPassage("source", "vendor/lib/a.py"), fxPassage("source", "node_modules/x/b.js"), fxPassage("source", "app.ts")}, 0, "TypeScript"},
		{"scan cap stops early", []Passage{fxPassage("source", "a.py"), fxPassage("source", "b.go"), fxPassage("source", "c.go")}, 1, "Python"},
		{"docs only", []Passage{fxPassage("source", "README.md"), {}}, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &LeannSearcher{passages: passageList{list: c.list}}
			if got := s.DominantLanguage(c.max); got != c.want {
				t.Errorf("DominantLanguage = %q, want %q", got, c.want)
			}
			// The result is computed once.
			if got := s.DominantLanguage(c.max); got != c.want {
				t.Errorf("second call = %q", got)
			}
		})
	}

	// Before a Load nothing is consumed, so a later call still scans.
	s := &LeannSearcher{}
	if got := s.DominantLanguage(0); got != "" {
		t.Errorf("unloaded = %q", got)
	}
	s.passages = passageList{list: []Passage{fxPassage("source", "a.c")}}
	if got := s.DominantLanguage(0); got != "C" {
		t.Errorf("after load = %q, want C", got)
	}
}
