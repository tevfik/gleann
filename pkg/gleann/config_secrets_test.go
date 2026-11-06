package gleann

import "testing"

func TestRestoreRedactedSecrets_ClearedWhenEndpointChanges(t *testing.T) {
	prev := Config{OpenAIAPIKey: "sk-real", OpenAIBaseURL: "https://api.openai.com/v1", OllamaAPIKey: "ok-real", OllamaHost: "http://localhost:11434"}
	next := prev.Redacted()
	next.OpenAIBaseURL = "http://attacker.example/v1"
	next.RestoreRedactedSecrets(prev)
	if next.OpenAIAPIKey != "" {
		t.Fatalf("secret restored for a changed endpoint: %q", next.OpenAIAPIKey)
	}
	if next.OllamaAPIKey != "ok-real" {
		t.Fatalf("unchanged endpoint must keep secret, got %q", next.OllamaAPIKey)
	}
}
