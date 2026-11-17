package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/background"
	"github.com/tevfik/gleann/pkg/gleann"
)

// pluginCatalogServer isolates HOME and points the plugin registry at a local
// server that always fails, so the built-in catalog is used (no network).
func pluginCatalogServer(t *testing.T) (*Server, http.Handler, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(dead.Close)
	old := gleann.RegistryURL
	gleann.RegistryURL = dead.URL
	// The catalog is cached for 10 minutes; a catalog served by an earlier
	// test (or an earlier -count run) must not leak into this one.
	gleann.InvalidatePluginCatalog()
	t.Cleanup(func() {
		gleann.RegistryURL = old
		gleann.InvalidatePluginCatalog()
	})

	bg := background.NewManager(1)
	t.Cleanup(bg.Stop)
	s := &Server{bgManager: bg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugins", s.handleListPlugins)
	mux.HandleFunc("POST /api/plugins/{name}/install", s.handleInstallPlugin)
	mux.HandleFunc("DELETE /api/plugins/{name}", s.handleUninstallPlugin)
	return s, mux, home
}

func TestListPlugins_CatalogWithInstallStatus(t *testing.T) {
	_, h, home := pluginCatalogServer(t)
	if err := os.MkdirAll(filepath.Join(home, ".gleann"), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := `{"plugins":[{"name":"gleann-plugin-sound","url":"http://127.0.0.1:1"}]}`
	if err := os.WriteFile(filepath.Join(home, ".gleann", "plugins.json"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/plugins", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Plugins []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, p := range resp.Plugins {
		status[p.Name] = p.Status
	}
	if status["gleann-plugin-sound"] != "installed" {
		t.Errorf("registered plugin must be 'installed': %v", status)
	}
	if status["gleann-plugin-docs"] != "not_installed" {
		t.Errorf("unregistered catalog plugin must be 'not_installed': %v", status)
	}
}

func TestInstallPlugin_Rejections(t *testing.T) {
	_, h, _ := pluginCatalogServer(t)
	tests := []struct {
		name, path string
		want       int
	}{
		{"unknown plugin", "/api/plugins/no-such-plugin/install", http.StatusNotFound},
		{"invalid name", "/api/plugins/bad%20name/install", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, tt.path, nil))
			if w.Code != tt.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestInstallPlugin_RunsAsBackgroundTaskAndReportsFailure(t *testing.T) {
	s, h, _ := pluginCatalogServer(t)
	// Serve a catalog whose only plugin has an unusable, non-GitHub repo URL:
	// the clone fails offline and the install task must end failed.
	cat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"demo-plugin","repo_url":"file:///nonexistent/demo-plugin"}]`))
	}))
	t.Cleanup(cat.Close)
	gleann.RegistryURL = cat.URL

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/plugins/demo-plugin/install", nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.TaskID == "" {
		t.Fatalf("no task id in %s", w.Body.String())
	}

	deadline := time.Now().Add(20 * time.Second)
	var task *background.Task
	for time.Now().Before(deadline) {
		task = s.bgManager.Get(resp.TaskID)
		if task != nil && (task.Status == background.StatusCompleted || task.Status == background.StatusFailed) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if task == nil || task.Status != background.StatusFailed {
		t.Fatalf("task = %+v, want failed", task)
	}
	if !strings.Contains(task.Error, "git clone") {
		t.Errorf("error should explain the clone failure: %q", task.Error)
	}
}

func TestUninstallPlugin_RemovesDirAndRegistryEntry(t *testing.T) {
	_, h, home := pluginCatalogServer(t)
	dir := filepath.Join(home, ".gleann", "plugins", "gleann-plugin-x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	regPath := filepath.Join(home, ".gleann", "plugins.json")
	reg := `{"plugins":[{"name":"gleann-plugin-x","url":"http://127.0.0.1:1"},{"name":"keep-me","url":"http://127.0.0.1:2"}]}`
	if err := os.WriteFile(regPath, []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/plugins/gleann-plugin-x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("plugin dir still exists: %v", err)
	}
	b, _ := os.ReadFile(regPath)
	if strings.Contains(string(b), "gleann-plugin-x") || !strings.Contains(string(b), "keep-me") {
		t.Errorf("registry after uninstall = %s", b)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/plugins/bad%20name", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid name: status %d", w.Code)
	}
}
