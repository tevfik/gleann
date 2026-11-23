package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/a2a"
)

// servedAgentCardURL mounts the A2A server of s (bound to 127.0.0.1:8081 by
// a2aScopeFixture) and returns supportedInterfaces[0].url of the served card.
func servedAgentCardURL(t *testing.T, s *Server) string {
	t.Helper()
	h, _, _ := a2aScopeFixture(t, s)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	r.Host = "127.0.0.1:8081"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET agent card: status %d: %s", w.Code, w.Body.String())
	}
	var card a2a.AgentCard
	if err := json.Unmarshal(w.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode agent card: %v", err)
	}
	if len(card.SupportedInterfaces) == 0 {
		t.Fatal("agent card has no supportedInterfaces")
	}
	return card.SupportedInterfaces[0].URL
}

// TestA2AAgentCardBaseURLBoundIP checks that the served agent card advertises
// the bound IP verbatim. The card used to say http://localhost127.0.0.1:8081.
func TestA2AAgentCardBaseURLBoundIP(t *testing.T) {
	t.Setenv("GLEANN_A2A_BASE_URL", "")
	const want = "http://127.0.0.1:8081/a2a/v1"
	if got := servedAgentCardURL(t, newTestServer()); got != want {
		t.Errorf("supportedInterfaces[0].url = %q, want %q", got, want)
	}
}

// TestA2AAgentCardBaseURLPrecedence pins the documented precedence of the
// agent card base URL: GLEANN_A2A_BASE_URL > config a2a_base_url > the URL
// derived from the bound address. The config key used to be ignored.
func TestA2AAgentCardBaseURLPrecedence(t *testing.T) {
	cases := []struct {
		name   string
		env    string
		config string
		want   string
	}{
		{"config used when env empty", "", "https://cfg.example/gleann", "https://cfg.example/gleann/a2a/v1"},
		{"env wins over config", "https://env.example", "https://cfg.example/gleann", "https://env.example/a2a/v1"},
		{"neither gives derived URL", "", "", "http://127.0.0.1:8081/a2a/v1"},
		// Env and config are normalised the same way: no "//a2a/v1", no
		// doubled "/a2a/v1/a2a/v1".
		{"config trailing slash", "", "https://cfg.example/", "https://cfg.example/a2a/v1"},
		{"env trailing slash", "https://env.example/", "", "https://env.example/a2a/v1"},
		{"config ending in /a2a/v1", "", "https://cfg.example/gleann/a2a/v1", "https://cfg.example/gleann/a2a/v1"},
		{"env ending in /a2a/v1/", "https://env.example/a2a/v1/", "", "https://env.example/a2a/v1"},
		// An invalid value is ignored (logged), the next source is used.
		{"config without scheme ignored", "", "host:8081", "http://127.0.0.1:8081/a2a/v1"},
		{"config not a URL ignored", "", "foo", "http://127.0.0.1:8081/a2a/v1"},
		{"invalid env falls back to config", "ftp://env.example", "https://cfg.example", "https://cfg.example/a2a/v1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GLEANN_A2A_BASE_URL", c.env)
			s := newTestServer()
			s.config.A2ABaseURL = c.config
			if got := servedAgentCardURL(t, s); got != c.want {
				t.Errorf("supportedInterfaces[0].url = %q, want %q", got, c.want)
			}
		})
	}
}

// TestA2ABaseURLWarningNamesSourceAndFallback: an ignored value is logged
// with its source and the base URL used instead.
func TestA2ABaseURLWarningNamesSourceAndFallback(t *testing.T) {
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(flags) })

	t.Setenv("GLEANN_A2A_BASE_URL", "ftp://env.example")
	s := newTestServer()
	s.addr = "127.0.0.1:8081"
	s.config.A2ABaseURL = "foo"
	if got := s.a2aBaseURL(); got != "http://127.0.0.1:8081" {
		t.Fatalf("a2aBaseURL = %q", got)
	}
	out := buf.String()
	for _, want := range []string{
		`ignoring GLEANN_A2A_BASE_URL "ftp://env.example"`,
		`ignoring config a2a_base_url "foo"`,
		"using http://127.0.0.1:8081 (derived from the server address 127.0.0.1:8081)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}

	buf.Reset()
	s.config.A2ABaseURL = "https://cfg.example/"
	if got := s.a2aBaseURL(); got != "https://cfg.example" {
		t.Fatalf("a2aBaseURL = %q", got)
	}
	if out := buf.String(); !strings.Contains(out, "using https://cfg.example (config a2a_base_url)") {
		t.Errorf("env warning must name the config value used instead:\n%s", out)
	}
	// A rejected URL with userinfo is logged without its password.
	buf.Reset()
	t.Setenv("GLEANN_A2A_BASE_URL", "https://user:s3cret@env.example")
	if got := s.a2aBaseURL(); got != "https://cfg.example" {
		t.Fatalf("userinfo must be refused, got %q", got)
	}
	if out := buf.String(); strings.Contains(out, "s3cret") || !strings.Contains(out, "ignoring GLEANN_A2A_BASE_URL") {
		t.Errorf("warning must name the source and redact the password:\n%s", out)
	}
}
