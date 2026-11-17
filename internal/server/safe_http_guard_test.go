package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(k, "")
	}
}

func TestEnvProxyAddrs(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"none", nil, nil},
		{"scheme-less gets http and port 80", map[string]string{"HTTP_PROXY": "proxy.local"}, []string{"proxy.local:80"}},
		{"explicit port, host lowercased", map[string]string{"http_proxy": "http://Proxy.Local:3128"}, []string{"proxy.local:3128"}},
		{"https default port", map[string]string{"HTTPS_PROXY": "https://secure.local"}, []string{"secure.local:443"}},
		{"socks5 default port", map[string]string{"ALL_PROXY": "socks5://s.local"}, []string{"s.local:1080"}},
		{"socks5h default port", map[string]string{"all_proxy": "socks5h://s2.local"}, []string{"s2.local:1080"}},
		{"unparsable value ignored", map[string]string{"HTTP_PROXY": "http://[::1"}, nil},
		{"empty host ignored", map[string]string{"HTTP_PROXY": "http://:8080"}, nil},
		{"several", map[string]string{"HTTP_PROXY": "a.local:1", "HTTPS_PROXY": "b.local:2"}, []string{"a.local:1", "b.local:2"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearProxyEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got := envProxyAddrs()
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for _, w := range tc.want {
				if !got[w] {
					t.Errorf("missing %q in %v", w, got)
				}
			}
		})
	}
}

func TestGuardedDialContext(t *testing.T) {
	clearProxyEnv(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	addr := ln.Addr().String()

	deny := func() bool { return false }
	allow := func() bool { return true }

	t.Run("allowPrivate dials loopback", func(t *testing.T) {
		c, err := guardedDialContext(allow)(context.Background(), "tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
	})
	t.Run("restricted address refused", func(t *testing.T) {
		_, err := guardedDialContext(deny)(context.Background(), "tcp", addr)
		if err == nil || !strings.Contains(err.Error(), "restricted address") {
			t.Fatalf("want restricted-address error, got %v", err)
		}
	})
	t.Run("operator proxy may be private", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", "http://"+addr)
		c, err := guardedDialContext(deny)(context.Background(), "tcp", addr)
		if err != nil {
			t.Fatalf("configured proxy must be reachable: %v", err)
		}
		c.Close()
	})
	t.Run("address without port", func(t *testing.T) {
		if _, err := guardedDialContext(deny)(context.Background(), "tcp", "no-port"); err == nil {
			t.Fatal("want split error")
		}
	})
	t.Run("unresolvable host", func(t *testing.T) {
		if _, err := guardedDialContext(deny)(context.Background(), "tcp", "bad host name:80"); err == nil {
			t.Fatal("want lookup error")
		}
	})
	t.Run("public literal but dial fails", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := guardedDialContext(deny)(ctx, "tcp", "192.0.2.1:80"); err == nil {
			t.Fatal("want dial error from cancelled context")
		}
	})
}

func TestNewGuardedTransport_BlocksPrivateTargets(t *testing.T) {
	clearProxyEnv(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	blocked := &http.Client{Transport: newGuardedTransport(func() bool { return false }, time.Second), Timeout: 5 * time.Second}
	if _, err := blocked.Get(backend.URL); err == nil || !strings.Contains(err.Error(), "restricted address") {
		t.Errorf("loopback backend must be refused, got %v", err)
	}
	open := &http.Client{Transport: newGuardedTransport(func() bool { return true }, time.Second), Timeout: 5 * time.Second}
	resp, err := open.Get(backend.URL)
	if err != nil {
		t.Fatalf("allowPrivate transport: %v", err)
	}
	resp.Body.Close()
}

func TestIsLoopbackHostAndRestrictedIP(t *testing.T) {
	for host, want := range map[string]bool{"localhost": true, "LOCALHOST": true, "127.0.0.1": true, "::1": true, "example.com": false, "10.0.0.1": false, "": false} {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
	if !isRestrictedIP(nil) {
		t.Error("nil IP must be restricted")
	}
	for ip, want := range map[string]bool{"8.8.8.8": false, "10.1.2.3": true, "169.254.169.254": true, "224.0.0.1": true, "0.0.0.0": true, "::1": true} {
		if got := isRestrictedIP(net.ParseIP(ip)); got != want {
			t.Errorf("isRestrictedIP(%s) = %v, want %v", ip, got, want)
		}
	}
}
