package gleann

import (
	"encoding/json"
	"reflect"
	"sort"
)

// RedactedSecret is the placeholder that replaces non-empty secret values
// when a Config is exposed to clients (e.g. GET /api/config). Clients that
// round-trip the redacted value back (POST /api/config) signal "keep the
// existing secret"; see RestoreRedactedSecrets.
const RedactedSecret = "********"

// secretFields returns pointers to every secret-bearing field of c.
//
// This is the single place that enumerates Config secrets; Redacted and
// RestoreRedactedSecrets both iterate over it. When adding a new API key,
// token or password to Config (or to a struct nested in it), add it here.
//
// Current secrets:
//   - OpenAIAPIKey                             (openai_api_key)
//   - OllamaAPIKey                             (ollama_api_key)
//   - SearchConfig.RerankerConfig.APIKey       (search_config.reranker_config.api_key)
func (c *Config) secretFields() []*string {
	return []*string{
		&c.OpenAIAPIKey,
		&c.OllamaAPIKey,
		&c.SearchConfig.RerankerConfig.APIKey,
	}
}

// Redacted returns a copy of c with every non-empty secret replaced by
// RedactedSecret. The receiver is not modified.
//
// All secret fields are plain strings held by value (including the nested
// SearchConfig/RerankerConfig structs), so the value copy made by the
// receiver is sufficient and the live config is never mutated. If a secret is
// ever added behind a pointer, map or slice, deep-copy it here first.
func (c Config) Redacted() Config {
	for _, f := range c.secretFields() {
		if *f != "" {
			*f = RedactedSecret
		}
	}
	return c
}

// RestoreRedactedSecrets replaces every secret in c that equals
// RedactedSecret with the corresponding value from prev. Empty values are
// left untouched (an empty string means "clear the secret").
//
// A secret is only restored when the endpoint it authenticates against is
// unchanged. Otherwise a client could keep the "********" placeholder, point
// the base URL at a server it controls and have gleann send the stored key
// there; in that case the secret is cleared and must be re-entered.
func (c *Config) RestoreRedactedSecrets(prev Config) {
	cur := c.secretFields()
	old := prev.secretFields()
	curEP := c.secretEndpoints()
	oldEP := prev.secretEndpoints()
	for i := range cur {
		if *cur[i] != RedactedSecret {
			continue
		}
		if *curEP[i] == *oldEP[i] {
			*cur[i] = *old[i]
		} else {
			*cur[i] = ""
		}
	}
}

// secretEndpoints returns, index-aligned with secretFields, the endpoint each
// secret is sent to.
func (c *Config) secretEndpoints() []*string {
	return []*string{
		&c.OpenAIBaseURL,
		&c.OllamaHost,
		&c.SearchConfig.RerankerConfig.BaseURL,
	}
}

// ChangedKeys returns the JSON keys whose values differ between a and b.
// It is used to track runtime-only overrides (embedded llama-server URL,
// fallback models) so they are never persisted by accident.
func ChangedKeys(a, b Config) []string {
	ma, mb := configAsMap(a), configAsMap(b)
	seen := map[string]bool{}
	var out []string
	for k, va := range ma {
		seen[k] = true
		if vb, ok := mb[k]; !ok || !reflect.DeepEqual(va, vb) {
			out = append(out, k)
		}
	}
	for k := range mb {
		if !seen[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func configAsMap(c Config) map[string]any {
	raw, _ := json.Marshal(c)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m
}
