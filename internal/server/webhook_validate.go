// Package server — webhook URL validation (SSRF defense).
//
// Webhooks are user-registered POST targets fired by background events.
// Without validation, an attacker with API access could register
// `http://169.254.169.254/...` (AWS metadata) or any internal-network
// URL and use gleann as an SSRF proxy.
//
// validateWebhookURL enforces:
//   - scheme must be http or https
//   - hostname must resolve to a non-loopback, non-link-local,
//     non-private address (unless GLEANN_WEBHOOK_ALLOW_PRIVATE=1)
//
// The DNS resolution check is best-effort; webhooks that resolve only
// at fire time are still subject to host-level egress controls.
package server

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// errInvalidWebhookURL is returned for malformed or disallowed targets.
var errInvalidWebhookURL = errors.New("invalid webhook URL")

// validateWebhookURL parses and validates a user-supplied webhook URL.
// Returns nil for safe URLs, a descriptive error otherwise.
func validateWebhookURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return fmt.Errorf("%w: empty url", errInvalidWebhookURL)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %v", errInvalidWebhookURL, err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		// ok
	default:
		return fmt.Errorf("%w: scheme %q not allowed (use http or https)", errInvalidWebhookURL, u.Scheme)
	}

	if u.Host == "" {
		return fmt.Errorf("%w: missing host", errInvalidWebhookURL)
	}

	// Opt-out for trusted internal deployments.
	if os.Getenv("GLEANN_WEBHOOK_ALLOW_PRIVATE") == "1" {
		return nil
	}

	host := u.Hostname()
	// Resolve and reject if any resolved IP is in a restricted range.
	ips, err := net.LookupIP(host)
	if err != nil {
		// Unresolvable hosts are rejected to be safe; operators that need
		// late-binding targets can set GLEANN_WEBHOOK_ALLOW_PRIVATE=1.
		return fmt.Errorf("%w: cannot resolve host %q: %v", errInvalidWebhookURL, host, err)
	}
	for _, ip := range ips {
		if isRestrictedIP(ip) {
			return fmt.Errorf("%w: host %q resolves to restricted address %s (set GLEANN_WEBHOOK_ALLOW_PRIVATE=1 to override)", errInvalidWebhookURL, host, ip)
		}
	}
	return nil
}

// isRestrictedIP returns true for loopback, link-local, private,
// unspecified, or multicast addresses — i.e. anything that should not
// be reachable from a public webhook target.
func isRestrictedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() ||
		ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	// Block AWS/GCP/Azure metadata service explicitly (defense in depth;
	// 169.254.169.254 is already caught by IsLinkLocalUnicast).
	return false
}
