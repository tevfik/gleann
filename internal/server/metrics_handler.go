package server

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// metrics collects server-level metrics in Prometheus exposition format.
// This is a lightweight implementation that avoids pulling in the full
// OpenTelemetry / Prometheus SDK. It provides the same /metrics endpoint
// that OpenTelemetry collectors or Prometheus scrapers expect.
type metrics struct {
	searchRequests      atomic.Int64
	searchErrors        atomic.Int64
	searchLatencyNs     atomic.Int64 // cumulative nanoseconds
	multiSearchRequests atomic.Int64
	buildRequests       atomic.Int64
	buildErrors         atomic.Int64
	buildLatencyNs      atomic.Int64
	askRequests         atomic.Int64
	deleteRequests      atomic.Int64
	webhooksFired       atomic.Int64

	mu        sync.RWMutex
	startTime time.Time
}

var serverMetrics = &metrics{startTime: time.Now()}

// RecordSearch records a search request.
func (m *metrics) RecordSearch(latency time.Duration, err bool) {
	m.searchRequests.Add(1)
	m.searchLatencyNs.Add(int64(latency))
	if err {
		m.searchErrors.Add(1)
	}
}

// RecordMultiSearch records a multi-index search request.
func (m *metrics) RecordMultiSearch() {
	m.multiSearchRequests.Add(1)
}

// RecordBuild records a build request.
func (m *metrics) RecordBuild(latency time.Duration, err bool) {
	m.buildRequests.Add(1)
	m.buildLatencyNs.Add(int64(latency))
	if err {
		m.buildErrors.Add(1)
	}
}

// RecordAsk records an ask request.
func (m *metrics) RecordAsk() {
	m.askRequests.Add(1)
}

// RecordDelete records an index deletion.
func (m *metrics) RecordDelete() {
	m.deleteRequests.Add(1)
}

// RecordWebhook records a webhook delivery.
func (m *metrics) RecordWebhook() {
	m.webhooksFired.Add(1)
}

// handleMetrics serves metrics in Prometheus text exposition format.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	m := serverMetrics
	uptime := time.Since(m.startTime).Seconds()

	s.mu.RLock()
	cachedSearchers := len(s.searchers)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	// HELP and TYPE comments follow Prometheus conventions.
	fmt.Fprintf(w, "# HELP gleann_up Whether the gleann server is running.\n")
	fmt.Fprintf(w, "# TYPE gleann_up gauge\n")
	fmt.Fprintf(w, "gleann_up 1\n\n")

	fmt.Fprintf(w, "# HELP gleann_uptime_seconds Time since server start.\n")
	fmt.Fprintf(w, "# TYPE gleann_uptime_seconds gauge\n")
	fmt.Fprintf(w, "gleann_uptime_seconds %.2f\n\n", uptime)

	fmt.Fprintf(w, "# HELP gleann_search_requests_total Total search requests.\n")
	fmt.Fprintf(w, "# TYPE gleann_search_requests_total counter\n")
	fmt.Fprintf(w, "gleann_search_requests_total %d\n\n", m.searchRequests.Load())

	fmt.Fprintf(w, "# HELP gleann_search_errors_total Total failed search requests.\n")
	fmt.Fprintf(w, "# TYPE gleann_search_errors_total counter\n")
	fmt.Fprintf(w, "gleann_search_errors_total %d\n\n", m.searchErrors.Load())

	searchCount := m.searchRequests.Load()
	avgSearchMs := float64(0)
	if searchCount > 0 {
		avgSearchMs = float64(m.searchLatencyNs.Load()) / float64(searchCount) / 1e6
	}
	fmt.Fprintf(w, "# HELP gleann_search_latency_avg_ms Average search latency in milliseconds.\n")
	fmt.Fprintf(w, "# TYPE gleann_search_latency_avg_ms gauge\n")
	fmt.Fprintf(w, "gleann_search_latency_avg_ms %.2f\n\n", avgSearchMs)

	fmt.Fprintf(w, "# HELP gleann_multi_search_requests_total Total multi-index search requests.\n")
	fmt.Fprintf(w, "# TYPE gleann_multi_search_requests_total counter\n")
	fmt.Fprintf(w, "gleann_multi_search_requests_total %d\n\n", m.multiSearchRequests.Load())

	fmt.Fprintf(w, "# HELP gleann_build_requests_total Total build requests.\n")
	fmt.Fprintf(w, "# TYPE gleann_build_requests_total counter\n")
	fmt.Fprintf(w, "gleann_build_requests_total %d\n\n", m.buildRequests.Load())

	fmt.Fprintf(w, "# HELP gleann_build_errors_total Total failed build requests.\n")
	fmt.Fprintf(w, "# TYPE gleann_build_errors_total counter\n")
	fmt.Fprintf(w, "gleann_build_errors_total %d\n\n", m.buildErrors.Load())

	buildCount := m.buildRequests.Load()
	avgBuildMs := float64(0)
	if buildCount > 0 {
		avgBuildMs = float64(m.buildLatencyNs.Load()) / float64(buildCount) / 1e6
	}
	fmt.Fprintf(w, "# HELP gleann_build_latency_avg_ms Average build latency in milliseconds.\n")
	fmt.Fprintf(w, "# TYPE gleann_build_latency_avg_ms gauge\n")
	fmt.Fprintf(w, "gleann_build_latency_avg_ms %.2f\n\n", avgBuildMs)

	fmt.Fprintf(w, "# HELP gleann_ask_requests_total Total ask (RAG) requests.\n")
	fmt.Fprintf(w, "# TYPE gleann_ask_requests_total counter\n")
	fmt.Fprintf(w, "gleann_ask_requests_total %d\n\n", m.askRequests.Load())

	fmt.Fprintf(w, "# HELP gleann_delete_requests_total Total index delete requests.\n")
	fmt.Fprintf(w, "# TYPE gleann_delete_requests_total counter\n")
	fmt.Fprintf(w, "gleann_delete_requests_total %d\n\n", m.deleteRequests.Load())

	fmt.Fprintf(w, "# HELP gleann_webhooks_fired_total Total webhook notifications sent.\n")
	fmt.Fprintf(w, "# TYPE gleann_webhooks_fired_total counter\n")
	fmt.Fprintf(w, "gleann_webhooks_fired_total %d\n\n", m.webhooksFired.Load())

	fmt.Fprintf(w, "# HELP gleann_cached_searchers Number of cached searcher instances.\n")
	fmt.Fprintf(w, "# TYPE gleann_cached_searchers gauge\n")
	fmt.Fprintf(w, "gleann_cached_searchers %d\n", cachedSearchers)
}
