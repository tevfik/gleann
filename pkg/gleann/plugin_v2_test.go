package gleann

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestFindByCapability_PriorityWins(t *testing.T) {
	mgr := &PluginManager{Registry: &PluginRegistry{Plugins: []Plugin{
		{Name: "a", URL: "http://a", Capabilities: []string{CapDocumentExtraction}, Extensions: []string{".pdf"}, Priority: 0},
		{Name: "b", URL: "http://b", Capabilities: []string{CapDocumentExtraction}, Extensions: []string{".pdf"}, Priority: 10},
		{Name: "c", URL: "http://c", Capabilities: []string{CapDocumentExtraction}, Extensions: []string{".pdf"}, Priority: 5},
	}}}
	got := mgr.FindByCapability(CapDocumentExtraction, ".pdf")
	if got == nil || got.Name != "b" {
		t.Fatalf("expected b (highest priority), got %+v", got)
	}
}

func TestFindByCapability_RejectsFutureSchema(t *testing.T) {
	mgr := &PluginManager{Registry: &PluginRegistry{Plugins: []Plugin{
		{Name: "future", URL: "http://x", Capabilities: []string{CapAudioTranscription}, SchemaVersion: MaxSupportedSchemaVersion + 1},
		{Name: "current", URL: "http://y", Capabilities: []string{CapAudioTranscription}, SchemaVersion: MaxSupportedSchemaVersion},
	}}}
	got := mgr.FindByCapability(CapAudioTranscription, "")
	if got == nil || got.Name != "current" {
		t.Fatalf("expected current (compatible schema), got %+v", got)
	}
}

func TestResolveMultimodalPluginEnv_PopulatesFromRegistry(t *testing.T) {
	os.Unsetenv("GLEANN_AUDIO_PLUGIN_URL")
	os.Unsetenv("GLEANN_VISION_PLUGIN_URL")
	t.Cleanup(func() {
		os.Unsetenv("GLEANN_AUDIO_PLUGIN_URL")
		os.Unsetenv("GLEANN_VISION_PLUGIN_URL")
	})

	mgr := &PluginManager{Registry: &PluginRegistry{Plugins: []Plugin{
		{Name: "sound", URL: "http://localhost:8766", Capabilities: []string{CapAudioTranscription}},
		{Name: "vision", URL: "http://localhost:8767", Capabilities: []string{CapVisionExtraction}},
	}}}
	a, v := mgr.ResolveMultimodalPluginEnv()
	if a != "http://localhost:8766" {
		t.Fatalf("audio URL not resolved: %q", a)
	}
	if v != "http://localhost:8767" {
		t.Fatalf("vision URL not resolved: %q", v)
	}
	if os.Getenv("GLEANN_AUDIO_PLUGIN_URL") != a {
		t.Fatalf("env not exported")
	}
}

func TestResolveMultimodalPluginEnv_RespectsExistingEnv(t *testing.T) {
	os.Setenv("GLEANN_AUDIO_PLUGIN_URL", "http://user-chosen")
	t.Cleanup(func() { os.Unsetenv("GLEANN_AUDIO_PLUGIN_URL") })

	mgr := &PluginManager{Registry: &PluginRegistry{Plugins: []Plugin{
		{Name: "sound", URL: "http://registry", Capabilities: []string{CapAudioTranscription}},
	}}}
	a, _ := mgr.ResolveMultimodalPluginEnv()
	if a != "http://user-chosen" {
		t.Fatalf("user env should win, got %q", a)
	}
}

func TestFetchPluginInfoAndPing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": "stub", "version": "1.0.0", "schema_version": 2,
				"capabilities": []string{CapAudioTranscription},
				"extensions":   []string{".wav"},
			})
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ok", "ready": true, "schema_version": 2, "version": "1.0.0",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	mgr := &PluginManager{Registry: &PluginRegistry{}}
	p := &Plugin{Name: "stub", URL: ts.URL}

	info, err := mgr.FetchPluginInfo(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "1.0.0" || info.SchemaVersion != 2 {
		t.Fatalf("unexpected info: %+v", info)
	}

	h, err := mgr.PingPlugin(p)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Ready || h.SchemaVersion != 2 {
		t.Fatalf("unexpected health: %+v", h)
	}
}

func TestPlugin_BackwardCompatJSON(t *testing.T) {
	// Old registries (v1) didn't include version/schema_version fields.
	// Decoding must still work and produce zero values.
	raw := []byte(`{"plugins":[{"name":"old","url":"http://o","capabilities":["document-extraction"],"extensions":[".pdf"]}]}`)
	var reg PluginRegistry
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	if len(reg.Plugins) != 1 || reg.Plugins[0].SchemaVersion != 0 {
		t.Fatalf("expected zero SchemaVersion for legacy entry, got %+v", reg.Plugins[0])
	}
	// And re-encoding must not emit empty version fields (omitempty).
	out, _ := json.Marshal(reg.Plugins[0])
	if got := string(out); strContains(got, "schema_version") || strContains(got, "version") {
		t.Fatalf("legacy plugin must serialise without version fields: %s", got)
	}
	_ = fmt.Sprintf // keep import
}

func strContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
