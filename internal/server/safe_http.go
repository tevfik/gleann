package server

// Outbound HTTP to user-supplied URLs (webhooks, model downloads).
//
// URL validation at registration/request time is not enough: a hostname can
// resolve to a public address when checked and to 127.0.0.1 or the cloud
// metadata service when actually dialed (DNS rebinding), and a public server
// can redirect to an internal one. guardedDialContext therefore resolves the
// host itself at dial time, rejects restricted addresses (isRestrictedIP) and
// connects to the vetted IP literal so no second lookup can be raced.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// guardedDialContext returns a DialContext func that refuses to connect to
// restricted addresses unless allowPrivate() reports true at dial time.
//
// Connections to an HTTP(S) proxy configured via the environment are
// allowed (the proxy is operator-chosen and commonly on a private network);
// the proxy is then responsible for egress policy.
func guardedDialContext(allowPrivate func() bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	proxies := envProxyAddrs()
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if allowPrivate() || proxies[strings.ToLower(addr)] {
			return dialer.DialContext(ctx, network, addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no addresses for host %q", host)
		}
		for _, ip := range ips {
			if isRestrictedIP(ip.IP) {
				return nil, fmt.Errorf("refusing to connect to %q: resolves to restricted address %s", host, ip.IP)
			}
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// envProxyAddrs returns the host:port of proxies configured through the
// standard environment variables.
func envProxyAddrs() map[string]bool {
	out := make(map[string]bool)
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		v := os.Getenv(k)
		if v == "" {
			continue
		}
		if !strings.Contains(v, "://") {
			v = "http://" + v
		}
		u, err := url.Parse(v)
		if err != nil || u.Hostname() == "" {
			continue
		}
		port := u.Port()
		if port == "" {
			switch u.Scheme {
			case "https":
				port = "443"
			case "socks5", "socks5h":
				port = "1080"
			default:
				port = "80"
			}
		}
		out[strings.ToLower(net.JoinHostPort(u.Hostname(), port))] = true
	}
	return out
}

// newGuardedTransport returns an http.Transport with sane timeouts whose
// dialer enforces guardedDialContext. responseHeaderTimeout bounds the wait
// for response headers only, so long body transfers are unaffected.
func newGuardedTransport(allowPrivate func() bool, responseHeaderTimeout time.Duration) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           guardedDialContext(allowPrivate),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
}

// isLoopbackHost reports whether host is "localhost" or a loopback IP literal.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
