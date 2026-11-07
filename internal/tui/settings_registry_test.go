package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/modules/settings"
)

// Every key of config.json that OnboardResult owns must be editable from the
// settings registry (and so from both the web UI and the TUI), or listed as
// internal on purpose. This is what keeps the two screens equal.
func TestSettingsCoverEveryConfigKey(t *testing.T) {
	if missing := Settings().Uncovered(); len(missing) > 0 {
		t.Fatalf("config keys with no setting (add a field or list them in Internal): %v", missing)
	}
}

func settingsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".gleann"), 0o700); err != nil {
		t.Fatal(err)
	}
	ResetModelOptionsCache()
	return home
}

func TestSettingsSaveClearsAndKeepsUnknownKeys(t *testing.T) {
	home := settingsHome(t)
	path := filepath.Join(home, ".gleann", "config.json")
	if err := os.WriteFile(path, []byte(`{"rerank_model":"old.gguf","rerank_enabled":true,"search_config":{"top_k":7},"rate_limit":5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := Settings()
	c, err := reg.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, errs := reg.Update(c, map[string]any{"rerank_model": "", "storage_mode": "recompute"}); len(errs) > 0 {
		t.Fatal(errs)
	}
	reg.BeforeSave(c)
	if err := reg.Save(c); err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["rerank_model"]; ok {
		t.Errorf("a cleared field must be removed, got rerank_model=%v", raw["rerank_model"])
	}
	if raw["storage_mode"] != "recompute" || raw["completed"] != true {
		t.Errorf("storage_mode/completed not saved: %v", raw)
	}
	if raw["rate_limit"] == nil || raw["search_config"] == nil {
		t.Errorf("keys the registry does not own must survive a save: %v", raw)
	}
}

func TestSettingsRefuseInvalidJSON(t *testing.T) {
	home := settingsHome(t)
	if err := os.WriteFile(filepath.Join(home, ".gleann", "config.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Settings().Load(); err == nil {
		t.Fatal("loading an invalid config.json must fail, not start from an empty config")
	}
}

func TestSettingsRerankerRecommendsCatalogModelsFirst(t *testing.T) {
	settingsHome(t)
	reg := Settings()
	c := &OnboardResult{EmbeddingProvider: "eif", RerankEnabled: true, RerankProvider: "eif"}
	f, _ := reg.Field("rerank_model")
	opts, err := f.OptionsFrom(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) < 2 || !strings.HasPrefix(opts[0].Value, "mmarco-mMiniLMv2") || !strings.Contains(opts[0].Label, "recommended") {
		t.Fatalf("the recommended multilingual reranker must come first, got %+v", opts)
	}
	if !strings.Contains(opts[0].Label, "download") {
		t.Errorf("a model that is not on disk must say where to download it: %q", opts[0].Label)
	}
}

func TestSettingsRerankFieldsFollowTheToggle(t *testing.T) {
	reg := Settings()
	c := &OnboardResult{}
	for _, k := range []string{"rerank_provider", "rerank_model"} {
		if reg.Visible(c, k) {
			t.Errorf("%s must be hidden while reranking is off", k)
		}
	}
	c.RerankEnabled = true
	for _, k := range []string{"rerank_provider", "rerank_model"} {
		if !reg.Visible(c, k) {
			t.Errorf("%s must show once reranking is on", k)
		}
	}
	if got := reg.Display(c, "rerank_provider"); !strings.HasPrefix(got, "auto") {
		t.Errorf("an unset reranker engine is auto, got %q", got)
	}
}

func TestSettingsA2ADefaultsOn(t *testing.T) {
	reg := Settings()
	c := &OnboardResult{}
	if reg.Value(c, "a2a_enabled") != true {
		t.Fatal("A2A is on unless turned off")
	}
	if err := reg.Set(c, "a2a_enabled", false); err != nil {
		t.Fatal(err)
	}
	if c.A2AEnabled == nil || *c.A2AEnabled {
		t.Fatalf("turning A2A off must store false, got %v", c.A2AEnabled)
	}
}

func TestSettingsEmbeddingListsHideRerankers(t *testing.T) {
	settingsHome(t)
	opts, _ := modelOptions(&OnboardResult{}, "eif", "embedding")
	for _, o := range opts {
		if isRerankerName(o.Value) {
			t.Errorf("a reranker must not be offered as an embedding model: %s", o.Value)
		}
	}
	var _ settings.Option // keep the import when the list is empty
}

func TestSettingsServerPolicyRoundTrip(t *testing.T) {
	home := settingsHome(t)
	path := filepath.Join(home, ".gleann", "config.json")
	if err := os.WriteFile(path, []byte(`{"cors_origins":["https://a.example"],"rate_limit":5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := Settings()
	c, err := reg.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Display(c, "cors_origins"); got != "https://a.example" {
		t.Fatalf("cors_origins shows %q", got)
	}
	if err := reg.Set(c, "cors_origins", "*"); err == nil {
		t.Error("a wildcard origin must be refused")
	}
	if _, errs := reg.Update(c, map[string]any{"cors_origins": "https://a.example/, https://b.example", "rate_burst": 300.0}); len(errs) > 0 {
		t.Fatal(errs)
	}
	if err := reg.Save(c); err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{}
	data, _ := os.ReadFile(path)
	_ = json.Unmarshal(data, &raw)
	origins, _ := raw["cors_origins"].([]any)
	if len(origins) != 2 || origins[0] != "https://a.example" || raw["rate_limit"] != 5.0 || raw["rate_burst"] != 300.0 {
		t.Fatalf("server policy not saved as gleann reads it: %v", raw)
	}
}

func TestSettingsAuthOffOnlyOnLoopback(t *testing.T) {
	reg := Settings()
	c := &OnboardResult{ServerAddr: "0.0.0.0:8081"}
	if _, errs := reg.Update(c, map[string]any{"auth": "off"}); len(errs) == 0 {
		t.Fatal("auth off must be refused while the server listens on all interfaces")
	}
	c = &OnboardResult{ServerAddr: "127.0.0.1:8081"}
	if _, errs := reg.Update(c, map[string]any{"auth": "off"}); len(errs) > 0 {
		t.Fatalf("auth off is fine on loopback: %v", errs)
	}
}
