package gleann

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTokenForNilPlugin(t *testing.T) {
	if got := (&PluginManager{}).TokenFor(nil); got != "" {
		t.Errorf("TokenFor(nil) = %q", got)
	}
}

func TestProcessRawTransportAndBodyErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var mode atomic.Value
	mode.Store("drop")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		switch mode.Load() {
		case "drop": // connection dies before any response
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		case "short-error": // 500 whose declared body never arrives
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("par"))
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		}
	}))
	defer srv.Close()
	m := &PluginManager{Registry: &PluginRegistry{}}
	p := &Plugin{Name: "net", URL: srv.URL, Timeout: 5}

	if _, err := m.processRaw(p, file); err == nil || !strings.Contains(err.Error(), "plugin request failed") {
		t.Fatalf("dropped connection: err = %v", err)
	}
	mode.Store("short-error")
	if _, err := m.processRaw(p, file); err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("truncated error body: err = %v", err)
	}
}

func TestGetInstalledPluginVersions(t *testing.T) {
	home := fxHome(t)
	if got := GetInstalledPluginVersions(); got != nil {
		t.Errorf("no registry = %v, want nil", got)
	}
	reg := filepath.Join(home, ".gleann", "plugins.json")
	fileAt(t, reg)
	if err := os.WriteFile(reg, []byte(`{"plugins":[{"name":"a","version":"1.2.0"},{"name":"b"},{"name":""}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := GetInstalledPluginVersions()
	if len(got) != 2 || got["a"] != "1.2.0" || got["b"] != "v1-legacy" {
		t.Errorf("versions = %v", got)
	}
	if err := os.WriteFile(reg, []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := GetInstalledPluginVersions(); got != nil {
		t.Errorf("corrupt registry = %v, want nil", got)
	}
}
