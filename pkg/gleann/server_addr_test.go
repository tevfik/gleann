package gleann

import "testing"

// TestAdvertisedBaseURL pins the base URL derived from the bound address:
// a bare ":port" (or nothing) advertises localhost, a bound host:port is
// used verbatim.
func TestAdvertisedBaseURL(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{":8081", "http://localhost:8081"},
		{"127.0.0.1:8081", "http://127.0.0.1:8081"},
		{"localhost:8081", "http://localhost:8081"},
		{"[::1]:8081", "http://[::1]:8081"},
		{"", "http://localhost"},
		// A colon-less port is a port (cmd_serve turns it into ":PORT").
		{"8081", "http://localhost:8081"},
		// Wildcard binds are not routable: advertise localhost, like the
		// UI URL gleann serve prints.
		{"0.0.0.0:8081", "http://localhost:8081"},
		{"[::]:8081", "http://localhost:8081"},
		// A trailing slash would give "//health" in the install hint.
		{"127.0.0.1:8081/", "http://127.0.0.1:8081"},
		// Already a URL: returned unchanged.
		{"http://x:1", "http://x:1"},
		{"https://gleann.example", "https://gleann.example"},
		{"http://x:1/", "http://x:1"},
		// Colon-less and not all digits: a host, not a port.
		{"myhost", "http://myhost"},
		{"myhost/", "http://myhost"},
		{"0.0.0.0", "http://localhost"},
		// A bare IPv6 literal is bracketed; the bare wildcard is localhost.
		{"::1", "http://[::1]"},
		{"[::1]", "http://[::1]"},
		{"::", "http://localhost"},
	}
	for _, c := range cases {
		t.Run(c.addr, func(t *testing.T) {
			if got := AdvertisedBaseURL(c.addr); got != c.want {
				t.Errorf("AdvertisedBaseURL(%q) = %q, want %q", c.addr, got, c.want)
			}
		})
	}
}

func TestNormalizeA2ABaseURL(t *testing.T) {
	ok := map[string]string{
		"":                                 "",
		"  ":                               "",
		"https://x.example":                "https://x.example",
		"https://x.example/":               "https://x.example",
		"https://x.example/gleann/":        "https://x.example/gleann",
		"https://x.example/a2a/v1":         "https://x.example",
		"https://x.example/gleann/a2a/v1/": "https://x.example/gleann",
		"http://10.0.0.5:8081":             "http://10.0.0.5:8081",
		" http://[::1]:8081/ ":             "http://[::1]:8081",
	}
	for in, want := range ok {
		got, err := NormalizeA2ABaseURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeA2ABaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"foo", "host:8081", "/a2a/v1", "ftp://x.example", "https://", "http:///path", "https://x.example/?a=1", "https://x.example/#f", "://x",
		// Userinfo would be published in the public agent card.
		"https://user:pw@x.example", "https://user@x.example"} {
		if got, err := NormalizeA2ABaseURL(bad); err == nil {
			t.Errorf("NormalizeA2ABaseURL(%q) = %q, want an error", bad, got)
		}
	}
}
