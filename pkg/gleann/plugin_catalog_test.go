package gleann

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestFetchPluginCatalog(t *testing.T) {
	// Test fallback mechanism by giving an invalid URL
	originalURL := RegistryURL
	RegistryURL = "http://localhost:1" // guaranteed connection refused
	defer func() { RegistryURL = originalURL }()

	catalog := FetchPluginCatalog()
	if len(catalog) == 0 {
		t.Fatal("expected fallback catalog, got empty")
	}
	if catalog[0].Name != "gleann-plugin-docs" {
		t.Errorf("expected first fallback plugin to be gleann-plugin-docs, got %s", catalog[0].Name)
	}

	// Test successful fetch from mock server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name": "mock-plugin", "description": "A test plugin"}]`))
	}))
	defer ts.Close()

	RegistryURL = ts.URL
	catalog = FetchPluginCatalog()
	if len(catalog) != 1 {
		t.Fatalf("expected 1 plugin from mock server, got %d", len(catalog))
	}
	if catalog[0].Name != "mock-plugin" {
		t.Errorf("expected mock-plugin, got %s", catalog[0].Name)
	}

	// Test cache hit: even if RegistryURL is broken, it returns cached catalog
	RegistryURL = "http://localhost:1"
	cached := FetchPluginCatalog()
	if len(cached) != 1 || cached[0].Name != "mock-plugin" {
		t.Fatalf("expected cached mock-plugin, got %v", cached)
	}

	// Test invalidation: once cache is cleared, it falls back to defaultCatalog
	InvalidatePluginCatalog()
	afterInvalidate := FetchPluginCatalog()
	if len(afterInvalidate) == 0 || afterInvalidate[0].Name != "gleann-plugin-docs" {
		t.Fatalf("expected fallback after invalidate, got %v", afterInvalidate)
	}
}

// Plugins that ship a setup TUI must advertise it, both in the built-in
// fallback and in plugins-registry.json (the remote catalog wins when online).
func TestCatalogSettingsCmdMatchesRegistry(t *testing.T) {
	data, err := os.ReadFile("../../plugins-registry.json")
	if err != nil {
		t.Fatal(err)
	}
	var registry []PluginMeta
	if err := json.Unmarshal(data, &registry); err != nil {
		t.Fatal(err)
	}
	byName := map[string]PluginMeta{}
	for _, p := range registry {
		byName[p.Name] = p
	}
	for _, name := range []string{"gleann-plugin-docs", "gleann-plugin-sound"} {
		var def PluginMeta
		for _, p := range defaultCatalog {
			if p.Name == name {
				def = p
			}
		}
		reg := byName[name]
		if !def.HasSettings || len(def.SettingsCmd) != 2 || def.SettingsCmd[0] != name || def.SettingsCmd[1] != "tui" {
			t.Errorf("%s: default catalog settings = %v %v", name, def.HasSettings, def.SettingsCmd)
		}
		if reg.HasSettings != def.HasSettings || strings.Join(reg.SettingsCmd, " ") != strings.Join(def.SettingsCmd, " ") {
			t.Errorf("%s: registry (%v %v) differs from default catalog (%v %v)", name, reg.HasSettings, reg.SettingsCmd, def.HasSettings, def.SettingsCmd)
		}
	}
}

// The remote registry (plugins-registry.json) and the built-in fallback
// catalog must describe the same plugins with the same metadata. Plugin
// versions are resolved dynamically from GitHub release tags by the
// installer/updater (CheckPluginUpdate), so neither side may ship a
// placeholder version.
func TestCatalogRegistryParity(t *testing.T) {
	data, err := os.ReadFile("../../plugins-registry.json")
	if err != nil {
		t.Fatal(err)
	}
	var registry []PluginMeta
	if err := json.Unmarshal(data, &registry); err != nil {
		t.Fatalf("plugins-registry.json does not parse as []PluginMeta: %v", err)
	}
	if len(registry) != len(defaultCatalog) {
		t.Fatalf("plugins-registry.json has %d entries, defaultCatalog has %d", len(registry), len(defaultCatalog))
	}
	byName := make(map[string]PluginMeta, len(registry))
	for _, p := range registry {
		byName[p.Name] = p
	}
	for _, def := range defaultCatalog {
		reg, ok := byName[def.Name]
		if !ok {
			t.Errorf("%s: present in defaultCatalog but missing from plugins-registry.json", def.Name)
			continue
		}
		if reg.Icon != def.Icon {
			t.Errorf("%s: Icon registry=%q defaultCatalog=%q", def.Name, reg.Icon, def.Icon)
		}
		if reg.Description != def.Description {
			t.Errorf("%s: Description registry=%q defaultCatalog=%q", def.Name, reg.Description, def.Description)
		}
		if reg.RepoURL != def.RepoURL {
			t.Errorf("%s: RepoURL registry=%q defaultCatalog=%q", def.Name, reg.RepoURL, def.RepoURL)
		}
		if reg.Language != def.Language {
			t.Errorf("%s: Language registry=%q defaultCatalog=%q", def.Name, reg.Language, def.Language)
		}
		if !reflect.DeepEqual(reg.Extensions, def.Extensions) {
			t.Errorf("%s: Extensions registry=%v defaultCatalog=%v", def.Name, reg.Extensions, def.Extensions)
		}
		if reg.RequiresMarkitdown != def.RequiresMarkitdown {
			t.Errorf("%s: RequiresMarkitdown registry=%v defaultCatalog=%v", def.Name, reg.RequiresMarkitdown, def.RequiresMarkitdown)
		}
		if reg.HasSettings != def.HasSettings {
			t.Errorf("%s: HasSettings registry=%v defaultCatalog=%v", def.Name, reg.HasSettings, def.HasSettings)
		}
		if !reflect.DeepEqual(reg.SettingsCmd, def.SettingsCmd) {
			t.Errorf("%s: SettingsCmd registry=%v defaultCatalog=%v", def.Name, reg.SettingsCmd, def.SettingsCmd)
		}
	}
	for _, p := range registry {
		if p.Version == "1.0.0" || p.Version == "v1.0.0" {
			t.Errorf("%s: plugins-registry.json ships placeholder version %q; versions come from live release tags", p.Name, p.Version)
		}
	}
	for _, p := range defaultCatalog {
		if p.Version == "1.0.0" || p.Version == "v1.0.0" {
			t.Errorf("%s: defaultCatalog ships placeholder version %q; versions come from live release tags", p.Name, p.Version)
		}
	}
}
