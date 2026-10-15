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
func TestUpdateReportsAndCountsHowItWasApplied(t *testing.T) {
	_, h, _ := newIndexedServer(t)
	inPlace := `gleann_update_requests_total{mode="in-place"}`
	compact := `gleann_update_requests_total{mode="compact"}`
	embedded := "gleann_update_embedded_texts_total"
	inPlace0, compact0, embedded0 := metricValue(t, h, inPlace), metricValue(t, h, compact), metricValue(t, h, embedded)

	item := func(text string) []gleann.Item {
		return []gleann.Item{{Text: text, Metadata: map[string]any{"source": "n.md"}}}
	}

	resp := postUpdate(t, h, updateIndexRequest{Items: item("a note about caching")})
	if resp["mode"] != "in-place" || resp["passages"] != float64(21) || resp["embedded_texts"] != float64(1) {
		t.Errorf("append response: %v", resp)
	}

	resp = postUpdate(t, h, updateIndexRequest{Items: item("a revised note about caching"), Sources: []string{"n.md"}})
	if resp["mode"] != "compact" || resp["removed"] != float64(1) || resp["embedded_texts"] != float64(21) {
		t.Errorf("replace response: %v", resp)
	}

	if got := metricValue(t, h, inPlace) - inPlace0; got != 1 {
		t.Errorf("in-place updates counted: %v, want 1", got)
	}
	if got := metricValue(t, h, compact) - compact0; got != 1 {
		t.Errorf("compactions counted: %v, want 1", got)
	}
	if got := metricValue(t, h, embedded) - embedded0; got != 22 {
		t.Errorf("embedded texts counted: %v, want 22 (1 append + 21 re-embedded)", got)
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

	// Remove the first note's passage in place, as older versions did: a gap.
	pm := gleann.NewPassageManager(filepath.Join(cfg.IndexDir, "idx", "idx"))
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := pm.RemoveBySource([]string{"n.md"}); err != nil {
		t.Fatal(err)
	}
	_ = pm.Close()

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
