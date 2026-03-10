package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestHandleMetrics(t *testing.T) {
	// Reset metrics for clean test.
	serverMetrics = &metrics{startTime: time.Now()}

	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	// Record some metrics.
	serverMetrics.RecordSearch(100*time.Millisecond, false)
	serverMetrics.RecordSearch(200*time.Millisecond, true)
	serverMetrics.RecordBuild(5*time.Second, false)
	serverMetrics.RecordAsk()
	serverMetrics.RecordDelete()
	serverMetrics.RecordMultiSearch()
	serverMetrics.RecordWebhook()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()

	s.handleMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain content type, got %s", ct)
	}

	body := w.Body.String()

	// Verify key metric lines exist.
	checks := []string{
		"gleann_up 1",
		"gleann_search_requests_total 2",
		"gleann_search_errors_total 1",
		"gleann_multi_search_requests_total 1",
		"gleann_build_requests_total 1",
		"gleann_build_errors_total 0",
		"gleann_ask_requests_total 1",
		"gleann_delete_requests_total 1",
		"gleann_webhooks_fired_total 1",
		"gleann_cached_searchers 0",
	}

	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("metrics output missing %q", check)
		}
	}
}

func TestHandleMetricsEmpty(t *testing.T) {
	serverMetrics = &metrics{startTime: time.Now()}

	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()

	s.handleMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "gleann_search_requests_total 0") {
		t.Error("expected 0 search requests in fresh metrics")
	}
}

func TestMetricsRecordSearch(t *testing.T) {
	serverMetrics = &metrics{startTime: time.Now()}

	serverMetrics.RecordSearch(50*time.Millisecond, false)
	serverMetrics.RecordSearch(150*time.Millisecond, false)

	if serverMetrics.searchRequests.Load() != 2 {
		t.Errorf("expected 2 searches, got %d", serverMetrics.searchRequests.Load())
	}
	if serverMetrics.searchErrors.Load() != 0 {
		t.Errorf("expected 0 errors, got %d", serverMetrics.searchErrors.Load())
	}
}

func TestMetricsRecordBuildError(t *testing.T) {
	serverMetrics = &metrics{startTime: time.Now()}

	serverMetrics.RecordBuild(1*time.Second, true)

	if serverMetrics.buildRequests.Load() != 1 {
		t.Errorf("expected 1 build, got %d", serverMetrics.buildRequests.Load())
	}
	if serverMetrics.buildErrors.Load() != 1 {
		t.Errorf("expected 1 build error, got %d", serverMetrics.buildErrors.Load())
	}
}
