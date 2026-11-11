package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/tevfik/gleann/internal/auth"
)

func authFixture(t *testing.T, addr string, cors []string) (http.Handler, string, string) {
	t.Helper()
	st, err := auth.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	admin, _, _ := st.EnsureAdmin()
	q, _ := st.Create("q", auth.ScopeQuery)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	return securityMiddleware(newSecurityConfig(addr, st, cors), inner), admin, q
}

func do(h http.Handler, method, path, host string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Host = host
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAuth_RequiredAndScopes(t *testing.T) {
	h, admin, q := authFixture(t, "127.0.0.1:8081", nil)
	bearer := func(t string) map[string]string { return map[string]string{"Authorization": "Bearer " + t} }
	cases := []struct {
		name, method, path string
		hdr                map[string]string
		want               int
	}{
		{"health public", "GET", "/health", nil, 200},
		{"ui public", "GET", "/", nil, 200},
		{"agent card public", "GET", "/.well-known/agent-card.json", nil, 200},
		{"no token", "GET", "/api/config", nil, 401},
		{"bad token", "GET", "/api/config", bearer("gl_x"), 401},
		{"query cannot read config", "GET", "/api/config", bearer(q), 403},
		{"query can search", "POST", "/api/indexes/x/search", bearer(q), 200},
		{"query can list", "GET", "/api/indexes", bearer(q), 200},
		{"query cannot delete", "DELETE", "/api/indexes/x", bearer(q), 403},
		{"query cannot read storage", "GET", "/api/storage", bearer(q), 403},
		{"admin can read storage", "GET", "/api/storage", bearer(admin), 200},
		{"query cannot write config", "POST", "/api/config", bearer(q), 403},
		{"admin can write config", "POST", "/api/config", bearer(admin), 200},
		{"v1 needs token", "POST", "/v1/chat/completions", nil, 401},
		{"v1 with query", "POST", "/v1/chat/completions", bearer(q), 200},
		{"a2a needs token", "POST", "/a2a/v1/message:send", nil, 401},
	}
	for _, c := range cases {
		if got := do(h, c.method, c.path, "127.0.0.1:8081", c.hdr).Code; got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestAuth_CrossOriginNoWildcardAndNoAccess(t *testing.T) {
	h, _, _ := authFixture(t, "127.0.0.1:8081", nil)
	w := do(h, "POST", "/api/indexes/x/index-path", "127.0.0.1:8081", map[string]string{"Origin": "http://evil.example", "Content-Type": "text/plain"})
	if w.Code != 401 {
		t.Fatalf("cross-origin without token: %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("ACAO leaked to foreign origin")
	}
}

func TestAuth_CORSAllowList(t *testing.T) {
	h, _, _ := authFixture(t, "127.0.0.1:8081", []string{"http://app.example"})
	w := do(h, "OPTIONS", "/api/indexes", "127.0.0.1:8081", map[string]string{"Origin": "http://app.example"})
	if w.Header().Get("Access-Control-Allow-Origin") != "http://app.example" {
		t.Fatalf("allow-listed origin not echoed: %v", w.Header())
	}
	w = do(h, "OPTIONS", "/api/indexes", "127.0.0.1:8081", map[string]string{"Origin": "http://evil.example"})
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("foreign origin echoed")
	}
}

func TestAuth_HostRebinding(t *testing.T) {
	h, admin, _ := authFixture(t, "127.0.0.1:8081", nil)
	hdr := map[string]string{"Authorization": "Bearer " + admin}
	if got := do(h, "GET", "/api/config", "attacker.example:8081", hdr).Code; got != 403 {
		t.Fatalf("foreign Host on loopback bind: %d", got)
	}
	if got := do(h, "GET", "/api/config", "localhost:8081", hdr).Code; got != 200 {
		t.Fatalf("localhost Host: %d", got)
	}
	// Non-loopback bind: Host check is off (auth still on).
	h2, admin2, _ := authFixture(t, ":8081", nil)
	if got := do(h2, "GET", "/api/config", "myhost:8081", map[string]string{"Authorization": "Bearer " + admin2}).Code; got != 200 {
		t.Fatalf("non-loopback bind: %d", got)
	}
}

func TestAuth_CookieExchangeAndCSRF(t *testing.T) {
	h, admin, _ := authFixture(t, "127.0.0.1:8081", nil)
	w := do(h, "GET", "/?token="+admin, "127.0.0.1:8081", nil)
	if w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Fatalf("exchange: %d %q", w.Code, w.Header().Get("Location"))
	}
	ck := w.Result().Cookies()
	if len(ck) != 1 || !ck[0].HttpOnly || ck[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie flags: %+v", ck)
	}
	cookie := map[string]string{"Cookie": ck[0].String()}
	if got := do(h, "GET", "/api/config", "127.0.0.1:8081", cookie).Code; got != 200 {
		t.Fatalf("cookie GET: %d", got)
	}
	cookie["Origin"] = "http://evil.example"
	if got := do(h, "POST", "/api/config", "127.0.0.1:8081", cookie).Code; got != 403 {
		t.Fatalf("cookie cross-origin POST: %d", got)
	}
	cookie["Origin"] = "http://127.0.0.1:8081"
	if got := do(h, "POST", "/api/config", "127.0.0.1:8081", cookie).Code; got != 200 {
		t.Fatalf("cookie same-origin POST: %d", got)
	}
	// Invalid token must not set a cookie.
	if w := do(h, "GET", "/?token=gl_bad", "127.0.0.1:8081", nil); len(w.Result().Cookies()) != 0 {
		t.Fatal("cookie set for invalid token")
	}
}

func TestPrepareAuth_RefusesOffOnPublicBind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME")) // Windows
	s := &Server{addr: ":8081"}
	s.config.Auth = "off"
	if _, err := s.PrepareAuth(); err == nil {
		t.Fatal("auth=off on non-loopback bind must be refused")
	}
	s2 := &Server{addr: "127.0.0.1:8081"}
	s2.config.Auth = "off"
	if tok, err := s2.PrepareAuth(); err != nil || tok != "" {
		t.Fatalf("auth=off on loopback: %q %v", tok, err)
	}
	s3 := &Server{addr: ":8081"}
	tok, err := s3.PrepareAuth()
	if err != nil || tok == "" {
		t.Fatalf("auth on: %q %v", tok, err)
	}
}

func TestValidateProxyHost(t *testing.T) {
	for _, ok := range []string{"", "http://localhost:11434", "https://api.example.com/v1"} {
		if err := validateProxyHost(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://127.0.0.1:1/admin?x=", "http://h/#f", "ftp://h", "http://u:p@h", "notaurl"} {
		if err := validateProxyHost(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestWebhookRedaction(t *testing.T) {
	if got := (Webhook{URL: "u", Secret: "s"}).redacted().Secret; got != "********" {
		t.Fatalf("secret not redacted: %q", got)
	}
}
