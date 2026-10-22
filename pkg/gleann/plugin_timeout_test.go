package gleann

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestTimeoutPrefersLargerHealthTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","timeout":900}`))
	}))
	defer srv.Close()
	m := &PluginManager{}

	if got := m.requestTimeout(&Plugin{Name: "a", URL: srv.URL, Timeout: 120}); got != 900 {
		t.Errorf("stale registry timeout not overridden: %d", got)
	}
	if got := m.requestTimeout(&Plugin{Name: "b", URL: srv.URL, Timeout: 1800}); got != 1800 {
		t.Errorf("larger registry timeout must be kept: %d", got)
	}
	if got := m.requestTimeout(&Plugin{Name: "c", URL: "http://127.0.0.1:1"}); got != DefaultPluginTimeout {
		t.Errorf("unreachable plugin should use the default: %d", got)
	}
}
