package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// metricValue reads one sample from the /metrics output, e.g.
// `gleann_update_requests_total{mode="compact"}`.
func metricValue(t *testing.T, h http.Handler, sample string) float64 {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if strings.HasPrefix(line, sample+" ") {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, sample)), 64)
			if err != nil {
				t.Fatalf("bad sample %q: %v", line, err)
			}
			return v
		}
	}
	t.Fatalf("metric %s not exposed:\n%s", sample, w.Body.String())
	return 0
}

func postUpdate(t *testing.T, h http.Handler, req updateIndexRequest) map[string]any {
	t.Helper()
	b, _ := json.Marshal(req)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/indexes/idx/update", bytes.NewReader(b)))
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// An update says how it was applied, and /metrics counts it by mode, so a slow
// update can be told apart: a compaction re-embeds every remaining passage.
// Format 2 has no in-place append, so both updates compact there.
func TestUpdateReportsAndCountsHowItWasApplied(t *testing.T) {
	_, h, cfg := newIndexedServer(t)
	format2 := gleann.StoreFormatOf(filepath.Join(cfg.IndexDir, "idx", "idx")) == 2
	inPlace := `gleann_update_requests_total{mode="in-place"}`
	compact := `gleann_update_requests_total{mode="compact"}`
	embedded := "gleann_update_embedded_texts_total"
	inPlace0, compact0, embedded0 := metricValue(t, h, inPlace), metricValue(t, h, compact), metricValue(t, h, embedded)

	item := func(text string) []gleann.Item {
		return []gleann.Item{{Text: text, Metadata: map[string]any{"source": "n.md"}}}
	}

	resp := postUpdate(t, h, updateIndexRequest{Items: item("a note about caching")})
	if format2 {
		if resp["mode"] != "compact" || resp["passages"] != float64(21) || resp["embedded_texts"] != float64(21) {
			t.Errorf("append response: %v", resp)
		}
	} else if resp["mode"] != "in-place" || resp["passages"] != float64(21) || resp["embedded_texts"] != float64(1) {
		t.Errorf("append response: %v", resp)
	}

	resp = postUpdate(t, h, updateIndexRequest{Items: item("a revised note about caching"), Sources: []string{"n.md"}})
	if resp["mode"] != "compact" || resp["removed"] != float64(1) || resp["embedded_texts"] != float64(21) {
		t.Errorf("replace response: %v", resp)
	}

	wantInPlace, wantCompact, wantEmbedded := float64(1), float64(1), float64(22)
	if format2 {
		wantInPlace, wantCompact, wantEmbedded = 0, 2, 42
	}
	if got := metricValue(t, h, inPlace) - inPlace0; got != wantInPlace {
		t.Errorf("in-place updates counted: %v, want %v", got, wantInPlace)
	}
	if got := metricValue(t, h, compact) - compact0; got != wantCompact {
		t.Errorf("compactions counted: %v, want %v", got, wantCompact)
	}
	if got := metricValue(t, h, embedded) - embedded0; got != wantEmbedded {
		t.Errorf("embedded texts counted: %v, want %v (1 append + 21 re-embedded in format 1, 21 + 21 in format 2)", got, wantEmbedded)
	}
}

// A search against an index damaged by an older version says so in the
// response, so a client does not trust wrong results.
func TestSearchResponseWarnsAboutADamagedIndex(t *testing.T) {
	_, h, cfg := newIndexedServer(t)
	// Two appended notes; removing the first leaves a gap (removing the last
	// passage would not).
	postUpdate(t, h, updateIndexRequest{Items: []gleann.Item{
		{Text: "a note about caching", Metadata: map[string]any{"source": "n.md"}},
		{Text: "a note about queues", Metadata: map[string]any{"source": "m.md"}},
	}})

	// Remove the first note's passage in place, as older versions did: a gap
	// in the passage IDs (format 1) or the vector slots (format 2).
	ps, err := gleann.OpenPassageStore(filepath.Join(cfg.IndexDir, "idx", "idx"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.RemoveBySource([]string{"n.md"}); err != nil {
		t.Fatal(err)
	}
	_ = ps.Close()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/indexes/idx/search", strings.NewReader(`{"query":"alpha","top_k":3}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("search: %d %s", w.Code, w.Body.String())
	}
	var resp searchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "gleann index repair idx") {
		t.Errorf("want a warning naming the repair command, got %v", resp.Warnings)
	}
}
