package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Two real format-2 indexes: one has four passages about the question, the other one weaker passage. The plain
// top-3 holds only the first index; with cover the second keeps its best passage, replacing the lowest extra of
// the first. The raw values cover compares (vector distance, BM25 score) are populated on format-2 results.
func TestPassthroughCoverWithFormat2Indexes(t *testing.T) {
	t.Setenv("GLEANN_PROXY_CONTEXT_INTRO", "")
	up := &fakeLLM{status: http.StatusOK, body: "data: {}\n\n"}
	ts := httptest.NewServer(up)
	t.Cleanup(ts.Close)
	s, _, _ := newBuildServer(t)
	s.config.ProxyMode = "passthrough"
	s.config.LLMProvider = "openai"
	s.config.OpenAIBaseURL = ts.URL + "/v1"
	s.config.LLMModel = "llm"

	buildFormat2DocsIndex(t, s, "main", map[string]string{
		"a.md": "# A\n\nzebra zebra zebra stripes\n",
		"b.md": "# B\n\nzebra zebra stripes herd\n",
		"c.md": "# C\n\nzebra zebra herd migration\n",
		"d.md": "# D\n\nzebra zebra savanna grass\n",
	})
	buildFormat2DocsIndex(t, s, "side", map[string]string{
		"e.md": "# E\n\nOne zebra stands somewhere among a very long list of unrelated words about rivers mountains forests deserts oceans clouds winds rain snow valleys\n",
	})

	// Format-2 results carry the raw values: both legs in a hybrid search.
	sr, release, err := s.acquireSearcher(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	res, err := sr.Search(context.Background(), "zebra", gleann.WithTopK(4), gleann.WithHybridAlpha(0.5))
	release()
	if err != nil || len(res) == 0 {
		t.Fatalf("search: %v (%d results)", err, len(res))
	}
	var withDist, withBM25 int
	for _, r := range res {
		if r.VectorDistance != nil {
			withDist++
		}
		if r.BM25Score != nil {
			withBM25++
		}
	}
	if withDist != len(res) || withBM25 == 0 {
		t.Fatalf("raw values on %d results: VectorDistance %d, BM25Score %d", len(res), withDist, withBM25)
	}

	lexical := map[string]string{"X-Gleann-Hybrid-Alpha": "0", "X-Gleann-Top-K": "3"}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range lexical {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	count := func(src []proxySource, index string) int {
		n := 0
		for _, x := range src {
			if x.Index == index {
				n++
			}
		}
		return n
	}

	plain := passthroughSources(t, s, "gleann/main,side", "zebra", lexical)
	if len(plain) != 3 || count(plain, "side") != 0 {
		t.Fatalf("without cover: %+v, want three passages of main", plain)
	}
	covered := passthroughSources(t, s, "gleann/main,side", "zebra", with(map[string]string{
		"X-Gleann-Cover": "true", "X-Gleann-Cover-Share": "0", "X-Gleann-Cover-Floor": "0"}))
	if len(covered) != 3 || count(covered, "side") != 1 || count(covered, "main") != 2 {
		t.Fatalf("with cover: %+v, want two of main and the best of side", covered)
	}
	// The main passages keep their order; the covering passage comes last (it ranks last).
	if covered[0].Index != "main" || covered[1].Index != "main" || covered[2].Index != "side" {
		t.Errorf("order changed: %+v", covered)
	}
	// A share of 1 means "as good as the best": the weaker index is not covered.
	strict := passthroughSources(t, s, "gleann/main,side", "zebra", with(map[string]string{
		"X-Gleann-Cover": "true", "X-Gleann-Cover-Share": "1", "X-Gleann-Cover-Floor": "0"}))
	if count(strict, "side") != 0 {
		t.Fatalf("share 1: %+v, want no passage of side", strict)
	}
}
