// Package server — network-boundary security: bearer-token auth, Host
// allow-list (DNS rebinding), CORS allow-list and cookie CSRF protection.
package server

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/tevfik/gleann/internal/auth"
)

const authCookie = "gleann_token"

// securityConfig is resolved once at Start.
type securityConfig struct {
	store        *auth.Store // nil = auth disabled
	loopbackBind bool
	allowedHosts map[string]bool
	corsOrigins  map[string]bool
	envAdmin     string // GLEANN_API_TOKEN, accepted as an admin credential (Docker/CI)
}

func (sc *securityConfig) verify(tok string) (auth.Scope, bool) {
	if sc.envAdmin != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(sc.envAdmin)) == 1 {
		return auth.ScopeAdmin, true
	}
	return sc.store.Verify(tok)
}

// bindIsLoopback reports whether addr only listens on a loopback interface.
func bindIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return isLoopbackHost(host)
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func newSecurityConfig(addr string, store *auth.Store, extraCORS []string) *securityConfig {
	sc := &securityConfig{store: store, loopbackBind: bindIsLoopback(addr),
		allowedHosts: map[string]bool{}, corsOrigins: map[string]bool{}}
	if t := strings.TrimSpace(os.Getenv(auth.EnvToken)); len(t) >= 16 {
		sc.envAdmin = t
	}
	for _, h := range splitList(os.Getenv("GLEANN_ALLOWED_HOSTS")) {
		sc.allowedHosts[strings.ToLower(h)] = true
	}
	for _, o := range append(splitList(os.Getenv("GLEANN_CORS_ORIGINS")), extraCORS...) {
		sc.corsOrigins[strings.TrimRight(o, "/")] = true
	}
	return sc
}

// requiredScope classifies a request. Public routes return ("", true).
func requiredScope(method, path string) (auth.Scope, bool) {
	isGet := method == http.MethodGet || method == http.MethodHead
	switch {
	case path == "/health" && isGet,
		path == "/.well-known/agent-card.json" && isGet,
		path == "/api/openapi.json" && isGet,
		path == "/api/docs" && isGet:
		return "", true
	case !strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/v1/") &&
		!strings.HasPrefix(path, "/a2a/") && path != "/metrics" &&
		!strings.HasPrefix(path, "/.well-known/") && isGet:
		return "", true // static web UI
	case strings.HasPrefix(path, "/v1/"), strings.HasPrefix(path, "/a2a/"):
		return auth.ScopeQuery, false
	case method == http.MethodPost && (strings.HasSuffix(path, "/search") || strings.HasSuffix(path, "/ask") || path == "/api/search"):
		return auth.ScopeQuery, false
	case isGet && (strings.HasPrefix(path, "/api/indexes") || strings.HasPrefix(path, "/api/graph/") ||
		strings.HasPrefix(path, "/api/blocks") || strings.HasPrefix(path, "/api/conversations")):
		return auth.ScopeQuery, false
	}
	return auth.ScopeAdmin, false
}

func scopeAllows(have, need auth.Scope) bool {
	return have == auth.ScopeAdmin || have == need
}

func bearerOrCookie(r *http.Request) (tok string, fromCookie bool) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:]), false
	}
	if c, err := r.Cookie(authCookie); err == nil {
		return c.Value, true
	}
	return "", false
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true // not a browser cross-site request
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func deny(w http.ResponseWriter, code int, msg string) {
	writeError(w, code, msg)
}

// securityMiddleware enforces Host, CORS and authentication in front of h.
func securityMiddleware(sc *securityConfig, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// DNS rebinding: on a loopback bind only loopback/allow-listed Host names.
		if sc.loopbackBind {
			host := r.Host
			if hh, _, err := net.SplitHostPort(host); err == nil {
				host = hh
			}
			host = strings.Trim(host, "[]")
			if !isLoopbackHost(host) && !sc.allowedHosts[strings.ToLower(host)] {
				deny(w, http.StatusForbidden, "host not allowed (set GLEANN_ALLOWED_HOSTS)")
				return
			}
		}

		// CORS: only allow-listed origins; same-origin needs no headers.
		if o := strings.TrimRight(r.Header.Get("Origin"), "/"); o != "" && sc.corsOrigins[o] {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Gleann-Target, X-Gleann-Index, X-Gleann-Top-K, X-Gleann-Min-Score")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if sc.store == nil {
			h.ServeHTTP(w, r)
			return
		}

		// Web UI bootstrap: ?token= → HttpOnly cookie, then redirect to a clean URL.
		if t := r.URL.Query().Get("token"); t != "" && r.Method == http.MethodGet {
			if _, ok := sc.verify(t); ok {
				http.SetCookie(w, &http.Cookie{Name: authCookie, Value: t, Path: "/",
					HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 30 * 24 * 3600})
				q := r.URL.Query()
				q.Del("token")
				u := *r.URL
				u.RawQuery = q.Encode()
				http.Redirect(w, r, u.RequestURI(), http.StatusSeeOther)
				return
			}
		}

		need, public := requiredScope(r.Method, r.URL.Path)
		if public {
			h.ServeHTTP(w, r)
			return
		}
		tok, fromCookie := bearerOrCookie(r)
		have, ok := sc.verify(tok)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gleann"`)
			deny(w, http.StatusUnauthorized, "authentication required: send 'Authorization: Bearer <token>' (create one with `gleann token create`)")
			return
		}
		if !scopeAllows(have, need) {
			deny(w, http.StatusForbidden, "token scope "+string(have)+" is not sufficient; need "+string(need))
			return
		}
		if fromCookie && r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			deny(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		h.ServeHTTP(w, r)
	})
}
