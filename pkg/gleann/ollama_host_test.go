package gleann

import "testing"

func TestResolveOllamaHostPrecedence(t *testing.T) {
	cases := []struct {
		name, flag, env, cfg, want string
	}{
		{"default", "", "", "", DefaultOllamaHost},
		{"config", "", "", "cfg:2", "http://cfg:2"},
		{"env beats config", "", "env:3", "cfg:2", "http://env:3"},
		{"flag beats env", "flag:1", "env:3", "cfg:2", "http://flag:1"},
		{"port only", "", "11500", "", "http://localhost:11500"},
		{"trailing slash", "http://box:1/", "", "", "http://box:1"},
		{"auto-scan placeholder ignored", "", "", "x (auto-scan)", DefaultOllamaHost},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("OLLAMA_HOST", c.env)
			if got := ResolveOllamaHost(c.flag, c.cfg); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// NormalizeOllamaHost mirrors Ollama's own client: http by default, port
// 11434 by default, bind-all addresses connect to loopback.
func TestNormalizeOllamaHost(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"0.0.0.0":                "http://127.0.0.1:11434",
		"0.0.0.0:11434":          "http://127.0.0.1:11434",
		"0.0.0.0:9999":           "http://127.0.0.1:9999",
		"http://0.0.0.0":         "http://127.0.0.1",
		"http://0.0.0.0:8080":    "http://127.0.0.1:8080",
		":11434":                 "http://127.0.0.1:11434",
		"localhost":              "http://localhost:11434",
		"myhost:1234":            "http://myhost:1234",
		"https://x":              "https://x",
		"https://x:8443/":        "https://x:8443",
		"http://box":             "http://box",
		"http://ollama.lan/":     "http://ollama.lan",
		"https://box":            "https://box",
		"box":                    "http://box:11434",
		"11500":                  "http://localhost:11500",
		"::":                     "http://[::1]:11434",
		"[::]:11434":             "http://[::1]:11434",
		"::1":                    "http://[::1]:11434",
		"[::1]:5000":             "http://[::1]:5000",
		"[2001:db8::2]":          "http://[2001:db8::2]:11434",
		"http://gpu:11434/":      "http://gpu:11434",
		"  http://h:1/prefix/  ": "http://h:1/prefix",
	} {
		if got := NormalizeOllamaHost(in); got != want {
			t.Errorf("NormalizeOllamaHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// A bind-all OLLAMA_HOST (Ollama's documented server setting) still reaches a
// usable client URL and beats the saved host.
func TestResolveOllamaHostBindAllEnv(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "0.0.0.0")
	if got := ResolveOllamaHost("", "http://gpu:11434"); got != "http://127.0.0.1:11434" {
		t.Errorf("got %q", got)
	}
}
