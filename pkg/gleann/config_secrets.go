package gleann

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
func (c *Config) RestoreRedactedSecrets(prev Config) {
	cur := c.secretFields()
	old := prev.secretFields()
	for i := range cur {
		if *cur[i] == RedactedSecret {
			*cur[i] = *old[i]
		}
	}
}
