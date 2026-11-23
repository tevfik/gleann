package gleann

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// AdvertisedBaseURL returns the http:// base URL under which a gleann server
// bound to addr is reachable (OpenAPI servers[0].url, A2A agent card, the
// install health-check hint). Prefixing "localhost" unconditionally produced
// http://localhost127.0.0.1:8081 for a bound IP. The rules:
//
//   - a trailing "/" is dropped;
//   - a value that already carries a scheme ("http://...") is otherwise
//     returned as is;
//   - an all-digit value is a port ("8081" means ":8081", as in gleann serve);
//   - an empty or wildcard host (":8081", "0.0.0.0", "[::]") is not routable,
//     so localhost is advertised, like the UI URL gleann serve prints;
//   - a value without a port is a host ("myhost"); a bare IPv6 literal is
//     bracketed ("::1" gives http://[::1]);
//   - any other host:port is used verbatim.
func AdvertisedBaseURL(addr string) string {
	if i := strings.Index(addr, "://"); i >= 0 {
		return addr[:i+3] + strings.TrimRight(addr[i+3:], "/")
	}
	addr = strings.TrimRight(addr, "/")
	if addr == "" {
		return "http://localhost"
	}
	if isAllDigits(addr) {
		addr = ":" + addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// No port: a bare host name or IP literal.
		host = strings.TrimSuffix(strings.TrimPrefix(addr, "["), "]")
		if isWildcardHost(host) {
			return "http://localhost"
		}
		if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
			return "http://[" + host + "]"
		}
		return "http://" + addr
	}
	if isWildcardHost(host) {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// isWildcardHost reports whether host binds every interface (not routable).
func isWildcardHost(host string) bool {
	return host == "" || host == "0.0.0.0" || host == "::"
}

// isAllDigits reports whether s is a non-empty run of ASCII digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// a2aInterfacePath is what the A2A agent card appends to its base URL.
const a2aInterfacePath = "/a2a/v1"

// NormalizeA2ABaseURL validates and normalises an A2A agent card base URL
// (GLEANN_A2A_BASE_URL or the a2a_base_url config key). It must be an http or
// https URL with a host and no userinfo (the card is public, so credentials
// would be published); a trailing "/" and a trailing "/a2a/v1" are removed,
// because the card appends "/a2a/v1" itself. An empty value is returned as
// "" without error (unset).
func NormalizeA2ABaseURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("parsing A2A base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("A2A base URL: want an http:// or https:// URL")
	}
	if u.Host == "" {
		return "", errors.New("A2A base URL: no host")
	}
	if u.User != nil {
		return "", errors.New("A2A base URL: no user or password allowed (the agent card is public)")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("A2A base URL: no query or fragment allowed")
	}
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, a2aInterfacePath)
	return strings.TrimRight(s, "/"), nil
}
