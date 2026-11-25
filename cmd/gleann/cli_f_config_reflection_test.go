package main

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

// The saved config (tui.OnboardResult, written by the setup wizard, the TUI
// settings screen and POST /api/settings) is copied into gleann.Config by
// hand in applySavedConfig. These tests keep that copy honest:
//
//   - TestFApplySavedConfigReflectionCoverage walks every persisted field of
//     OnboardResult, sets it alone and checks that applySavedConfig changed
//     gleann.Config. A field that reaches nothing is listed as unmapped and
//     fails the test; a field that must stay outside gleann.Config is
//     allowlisted here with a reason.
//   - TestFApplySavedConfigSentinelsReachConfig populates every field at once
//     with a sentinel and asserts the exact gleann.Config field each one
//     lands in, so a mapping that silently writes the wrong key fails too.
//
// Adding a key to OnboardResult therefore fails these tests until it is either
// mapped in applySavedConfig or justified in the allowlist.

// savedConfigFieldAllowlist lists the persisted OnboardResult fields that
// deliberately do not reach gleann.Config, with the reason. Every other
// persisted field must be mapped by applySavedConfig.
var savedConfigFieldAllowlist = map[string]string{
	"AnthropicKey":       "gleann.Config holds no Anthropic key by design (pkg/gleann/endpoint.go); chat, ask and memory read the saved value directly",
	"Completed":          "wizard progress marker, not a runtime setting",
	"SystemPrompt":       "chat-client setting read from the saved config by cmd_query/chat, not an index setting",
	"Temperature":        "chat-client setting read from the saved config, not an index setting",
	"RepeatPenalty":      "chat-client setting read from the saved config, not an index setting",
	"MaxTokens":          "chat-client setting read from the saved config, not an index setting",
	"ServerEnabled":      "setup/TUI server toggle; gleann.Config has no field",
	"ServerAddr":         "read straight from the saved config by cmd_serve.go:77; gleann.Config has no field",
	"MCPEnabled":         "MCP server toggle consumed by the MCP command/TUI; gleann.Config has no field",
	"InstallPath":        "installer-only value (cmd_install)",
	"InstallCompletions": "installer-only value (cmd_install)",
	"Quiet":              "CLI output formatting",
	"WordWrap":           "CLI output formatting (cmd_query)",
	"Roles":              "custom agent prompts read by the agent registry, not an index setting",
	"FormatText":         "output-format templates read by chat, not an index setting",
	"SetupNotice":        "first-run guidance persisted when Ollama was absent (autosetup.NoOllamaNotice); informational text for the user, not a runtime setting",
}

// isPersistedSavedField reports whether f is an exported OnboardResult field
// that config.json carries (unexported helpers and json:"-" are skipped).
func isPersistedSavedField(f reflect.StructField) bool {
	if f.PkgPath != "" {
		return false
	}
	return savedJSONKey(f) != "-"
}

// savedJSONKey returns the config.json key of a persisted OnboardResult field.
// An exported field without a json name is serialized under its Go field name.
func savedJSONKey(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" {
		return f.Name
	}
	return name
}

// writeSavedConfigJSON writes saved as the saved config.json of home.
func writeSavedConfigJSON(t *testing.T, home string, saved tui.OnboardResult) {
	t.Helper()
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatalf("marshalling the saved config: %v", err)
	}
	fSaveConfig(t, home, string(data))
}

// fillSavedSentinel sets v to a non-zero sentinel derived from path. It
// recurses into structs (LlamaCPPConfig), slices (CORSOrigins) and maps
// (Roles), so a new field of any of those shapes is populated automatically.
func fillSavedSentinel(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString("sentinel-" + path)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(40 + len(path)%60))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(40 + len(path)%60))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(0.5 * float64(1+len(path)%5))
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fillSavedSentinel(t, p.Elem(), path)
		v.Set(p)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fillSavedSentinel(t, s.Index(0), path+"[0]")
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k := reflect.New(v.Type().Key()).Elem()
		fillSavedSentinel(t, k, path+".key")
		val := reflect.New(v.Type().Elem()).Elem()
		fillSavedSentinel(t, val, path+".value")
		m.SetMapIndex(k, val)
		v.Set(m)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.PkgPath != "" {
				continue
			}
			fillSavedSentinel(t, v.Field(i), path+"."+f.Name)
		}
	default:
		t.Fatalf("fillSavedSentinel: no sentinel for %s (%s); teach the helper this kind", path, v.Kind())
	}
}

// TestFApplySavedConfigReflectionCoverage is the measurement the roadmap item
// asks for: every persisted OnboardResult key must change gleann.Config when
// set alone, unless it is allowlisted with a reason.
func TestFApplySavedConfigReflectionCoverage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_INDEX_DIR", "")
	t.Setenv("OLLAMA_HOST", "")

	rt := reflect.TypeOf(tui.OnboardResult{})
	var unmapped []string
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if !isPersistedSavedField(field) {
			continue
		}
		var saved tui.OnboardResult
		fillSavedSentinel(t, reflect.ValueOf(&saved).Elem().Field(i), field.Name)
		writeSavedConfigJSON(t, home, saved)

		cfg := gleann.Config{}
		applySavedConfig(&cfg, nil)
		reached := !reflect.DeepEqual(cfg, gleann.Config{})

		if reason, ok := savedConfigFieldAllowlist[field.Name]; ok {
			if reached {
				t.Errorf("%s (%s) is allowlisted (%q) but applySavedConfig now writes gleann.Config; drop the allowlist entry and assert the mapping instead",
					field.Name, savedJSONKey(field), reason)
			}
			continue
		}
		if !reached {
			unmapped = append(unmapped, field.Name+" ("+savedJSONKey(field)+")")
		}
	}

	// An allowlist entry for a field that no longer exists (or is no longer
	// persisted) hides nothing and must be cleaned up.
	for name := range savedConfigFieldAllowlist {
		f, ok := rt.FieldByName(name)
		if !ok || !isPersistedSavedField(f) {
			t.Errorf("savedConfigFieldAllowlist names %q, which is not a persisted field of tui.OnboardResult", name)
		}
	}

	sort.Strings(unmapped)
	if len(unmapped) > 0 {
		t.Fatalf("%d saved-config keys never reach gleann.Config: %s\n"+
			"Map each one in applySavedConfig (cmd/gleann/config.go) or allowlist it here with a reason.",
			len(unmapped), strings.Join(unmapped, ", "))
	}
}

// savedConfigSentinelChecks maps an OnboardResult field to the assertion that
// its sentinel value landed in the intended gleann.Config field.
func savedConfigSentinelChecks() map[string]func(*testing.T, tui.OnboardResult, gleann.Config) {
	eq := func(t *testing.T, name string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: gleann.Config holds %#v, want the saved value %#v", name, got, want)
		}
	}
	return map[string]func(*testing.T, tui.OnboardResult, gleann.Config){
		"EmbeddingProvider": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "EmbeddingProvider", c.EmbeddingProvider, s.EmbeddingProvider)
		},
		"EmbeddingModel": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "EmbeddingModel", c.EmbeddingModel, s.EmbeddingModel)
		},
		"CodeEmbeddingModel": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "CodeEmbeddingModel", c.CodeEmbeddingModel, s.CodeEmbeddingModel)
		},
		"DocEmbeddingModel": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "DocEmbeddingModel", c.DocEmbeddingModel, s.DocEmbeddingModel)
		},
		"OllamaHost": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "OllamaHost", c.OllamaHost, "http://"+s.OllamaHost)
		},
		"OllamaAPIKey": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "OllamaAPIKey", c.OllamaAPIKey, s.OllamaAPIKey)
		},
		"OpenAIKey": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "OpenAIKey→OpenAIAPIKey", c.OpenAIAPIKey, s.OpenAIKey)
		},
		"OpenAIBaseURL": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "OpenAIBaseURL", c.OpenAIBaseURL, strings.TrimRight(s.OpenAIBaseURL, "/"))
		},
		"LLMProvider": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "LLMProvider", c.LLMProvider, s.LLMProvider)
		},
		"LLMModel": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "LLMModel", c.LLMModel, s.LLMModel)
		},
		"RerankEnabled": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "RerankEnabled→SearchConfig.UseReranker", c.SearchConfig.UseReranker, s.RerankEnabled)
		},
		"RerankModel": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "RerankModel→SearchConfig.RerankerConfig.Model", c.SearchConfig.RerankerConfig.Model, s.RerankModel)
		},
		"RerankProvider": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "RerankProvider→SearchConfig.RerankerConfig.Provider", string(c.SearchConfig.RerankerConfig.Provider), s.RerankProvider)
		},
		"QueryExpand": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "QueryExpand→SearchConfig.Expand", c.SearchConfig.Expand, s.QueryExpand)
		},
		"ExpandModel": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "ExpandModel→SearchConfig.ExpandModel", c.SearchConfig.ExpandModel, s.ExpandModel)
		},
		"IndexDir": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "IndexDir", c.IndexDir, tui.ExpandPath(s.IndexDir))
		},
		"LlamaCPPConfig": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "LlamaCPPConfig", c.LlamaCPPConfig, s.LlamaCPPConfig)
		},
		"TopK": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "TopK→SearchConfig.TopK", c.SearchConfig.TopK, s.TopK)
		},
		"Backend": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "Backend", c.Backend, s.Backend)
		},
		"StorageMode": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "StorageMode", c.StorageMode, s.StorageMode)
			eq(t, "StorageMode→HNSWConfig.PruneEmbeddings", c.HNSWConfig.PruneEmbeddings, s.StorageMode == gleann.StorageRecompute)
		},
		"MultimodalModel": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "MultimodalModel", c.MultimodalModel, s.MultimodalModel)
		},
		"A2AEnabled": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "A2AEnabled", c.A2AEnabled, s.A2AEnabled)
		},
		"A2ABaseURL": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "A2ABaseURL", c.A2ABaseURL, s.A2ABaseURL)
		},
		"Auth": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "Auth", c.Auth, s.Auth)
		},
		"CORSOrigins": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "CORSOrigins", c.CORSOrigins, s.CORSOrigins)
		},
		"AllowedRoots": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "AllowedRoots", c.AllowedRoots, s.AllowedRoots)
		},
		"RateLimit": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "RateLimit", c.RateLimit, s.RateLimit)
		},
		"RateBurst": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "RateBurst", c.RateBurst, s.RateBurst)
		},
		"ProxyMode": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "ProxyMode", c.ProxyMode, s.ProxyMode)
		},
		"ProxyContextIntro": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "ProxyContextIntro", c.ProxyContextIntro, s.ProxyContextIntro)
		},
		"ProxyCover": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "ProxyCover", c.ProxyCover, s.ProxyCover)
		},
		"ProxyCoverMargin": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "ProxyCoverMargin", c.ProxyCoverMargin, float32(s.ProxyCoverMargin))
		},
		"ProxyCoverShare": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "ProxyCoverShare", c.ProxyCoverShare, float32(s.ProxyCoverShare))
		},
		"ProxyCoverFloor": func(t *testing.T, s tui.OnboardResult, c gleann.Config) {
			eq(t, "ProxyCoverFloor", c.ProxyCoverFloor, float32(s.ProxyCoverFloor))
		},
	}
}

// TestFApplySavedConfigSentinelsReachConfig populates every OnboardResult
// field with a sentinel and checks the exact Config destination of each
// mapping. It also insists that every persisted field has either a check or
// an allowlist entry, so a new key cannot slip in unasserted.
func TestFApplySavedConfigSentinelsReachConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_INDEX_DIR", "")
	t.Setenv("OLLAMA_HOST", "")

	var saved tui.OnboardResult
	fillSavedSentinel(t, reflect.ValueOf(&saved).Elem(), "")
	// Values whose normalisation is part of the mapping contract.
	saved.OllamaHost = "sentinel-host:4321"
	saved.OpenAIBaseURL = "http://sentinel-openai.example/v1/"
	saved.StorageMode = gleann.StorageRecompute
	writeSavedConfigJSON(t, home, saved)

	cfg := gleann.Config{}
	applySavedConfig(&cfg, nil)

	checks := savedConfigSentinelChecks()
	rt := reflect.TypeOf(tui.OnboardResult{})

	var missing []string
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if !isPersistedSavedField(field) {
			continue
		}
		if _, ok := checks[field.Name]; ok {
			continue
		}
		if _, ok := savedConfigFieldAllowlist[field.Name]; ok {
			continue
		}
		missing = append(missing, field.Name+" ("+savedJSONKey(field)+")")
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d mapped saved-config keys have no sentinel assertion: %s", len(missing), strings.Join(missing, ", "))
	}
	for name := range checks {
		f, ok := rt.FieldByName(name)
		if !ok || !isPersistedSavedField(f) {
			t.Errorf("savedConfigSentinelChecks names %q, which is not a persisted field of tui.OnboardResult", name)
		}
	}

	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		checks[name](t, saved, cfg)
	}
}
