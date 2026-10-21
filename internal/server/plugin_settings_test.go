package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// settingsPlugin is a stand-in plugin implementing GET/PUT /settings.
func settingsPlugin(t *testing.T, withSettings bool) (url string, got *string) {
	t.Helper()
	var last string
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"ok"}`) })
	if withSettings {
		mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				b, _ := io.ReadAll(r.Body)
				last = r.Header.Get("Content-Type") + "|" + string(b)
				if strings.Contains(string(b), "bad") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnprocessableEntity)
					fmt.Fprint(w, `{"error":"invalid","errors":{"x":"nope"}}`)
					return
				}
				fmt.Fprint(w, `{"saved":["language"],"restart_required":true}`)
				return
			}
			fmt.Fprint(w, `{"schema_version":1,"fields":[{"key":"language","type":"text","value":"tr"}]}`)
		})
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL, &last
}

func pluginAPI(t *testing.T, plugins map[string]string) *httptest.Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	var entries []string
	for name, url := range plugins {
		entries = append(entries, fmt.Sprintf(`{"name":%q,"url":%q}`, name, url))
	}
	os.MkdirAll(filepath.Join(home, ".gleann"), 0o755)
	os.WriteFile(filepath.Join(home, ".gleann", "plugins.json"), []byte(`{"plugins":[`+strings.Join(entries, ",")+`]}`), 0o644)

	s := &Server{}
	t.Cleanup(s.closePluginManager)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugins/{name}/settings", s.handlePluginSettings)
	mux.HandleFunc("PUT /api/plugins/{name}/settings", s.handlePluginSettings)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestPluginSettingsProxyGetAndPut(t *testing.T) {
	url, got := settingsPlugin(t, true)
	api := pluginAPI(t, map[string]string{"gleann-plugin-sound": url})

	resp, err := http.Get(api.URL + "/api/plugins/gleann-plugin-sound/settings")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET: %v %v", resp, err)
	}
	var form struct{ Fields []struct{ Key string } }
	json.NewDecoder(resp.Body).Decode(&form)
	if len(form.Fields) != 1 || form.Fields[0].Key != "language" {
		t.Errorf("form = %+v", form)
	}

	req, _ := http.NewRequest(http.MethodPut, api.URL+"/api/plugins/gleann-plugin-sound/settings", strings.NewReader(`{"values":{"language":"en"}}`))
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("PUT: %v %v", resp, err)
	}
	if *got != `application/json|{"values":{"language":"en"}}` {
		t.Errorf("plugin received %q", *got)
	}
}

func TestPluginSettingsProxyPassesValidationErrorsThrough(t *testing.T) {
	url, _ := settingsPlugin(t, true)
	api := pluginAPI(t, map[string]string{"p": url})
	req, _ := http.NewRequest(http.MethodPut, api.URL+"/api/plugins/p/settings", strings.NewReader(`{"values":{"x":"bad"}}`))
	resp, _ := http.DefaultClient.Do(req)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(b), `"x":"nope"`) {
		t.Errorf("status %d body %s", resp.StatusCode, b)
	}
}

func TestPluginSettingsProxyErrors(t *testing.T) {
	url, _ := settingsPlugin(t, false) // healthy plugin without /settings
	api := pluginAPI(t, map[string]string{"plain": url, "down": "http://127.0.0.1:1"})
	for path, want := range map[string]int{
		"/api/plugins/plain/settings":   http.StatusNotImplemented,
		"/api/plugins/missing/settings": http.StatusNotFound,
		"/api/plugins/down/settings":    http.StatusBadGateway,
		"/api/plugins/..%2Fx/settings":  http.StatusBadRequest,
	} {
		resp, err := http.Get(api.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want && !(strings.Contains(path, "..") && resp.StatusCode == http.StatusNotFound) {
			t.Errorf("%s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}
