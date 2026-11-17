package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// captureLog redirects the standard logger into a buffer for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

func TestFireWebhookHTTP_SignsBodyAndSetsHeaders(t *testing.T) {
	t.Setenv("GLEANN_WEBHOOK_ALLOW_PRIVATE", "1")
	type got struct {
		body []byte
		hdr  http.Header
	}
	ch := make(chan got, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- got{b, r.Header.Clone()}
	}))
	defer srv.Close()

	body := []byte(`{"event":"x"}`)
	fireWebhookHTTP(srv.URL, "s3cret", body)
	g := <-ch

	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if g.hdr.Get("X-Gleann-Signature") != want {
		t.Errorf("signature = %q, want %q", g.hdr.Get("X-Gleann-Signature"), want)
	}
	if g.hdr.Get("Content-Type") != "application/json" || g.hdr.Get("User-Agent") != "gleann-webhook/1.0" {
		t.Errorf("headers = %v", g.hdr)
	}
	if string(g.body) != string(body) {
		t.Errorf("body = %q", g.body)
	}

	// Without a secret no signature header is sent.
	fireWebhookHTTP(srv.URL, "", body)
	if g = <-ch; g.hdr.Get("X-Gleann-Signature") != "" {
		t.Errorf("unexpected signature %q", g.hdr.Get("X-Gleann-Signature"))
	}
}

func TestFireWebhookHTTP_LogsFailures(t *testing.T) {
	t.Setenv("GLEANN_WEBHOOK_ALLOW_PRIVATE", "1")
	var status int
	var target string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if target != "" {
			http.Redirect(w, r, target, status)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()

	t.Run("client error status", func(t *testing.T) {
		buf := captureLog(t)
		status, target = http.StatusBadRequest, ""
		fireWebhookHTTP(srv.URL, "", []byte("{}"))
		if !strings.Contains(buf.String(), "returned status 400") {
			t.Errorf("log = %q", buf.String())
		}
	})
	t.Run("redirect is not followed", func(t *testing.T) {
		buf := captureLog(t)
		status, target = http.StatusFound, srv.URL+"/elsewhere"
		fireWebhookHTTP(srv.URL, "", []byte("{}"))
		if !strings.Contains(buf.String(), "redirects are not followed") {
			t.Errorf("log = %q", buf.String())
		}
	})
	t.Run("invalid URL", func(t *testing.T) {
		buf := captureLog(t)
		fireWebhookHTTP("http://bad host/", "", []byte("{}"))
		if !strings.Contains(buf.String(), "failed to create request") {
			t.Errorf("log = %q", buf.String())
		}
	})
	t.Run("connection refused", func(t *testing.T) {
		buf := captureLog(t)
		dead := httptest.NewServer(http.NotFoundHandler())
		url := dead.URL
		dead.Close()
		fireWebhookHTTP(url, "", []byte("{}"))
		if !strings.Contains(buf.String(), "failed to deliver") {
			t.Errorf("log = %q", buf.String())
		}
	})
	t.Run("private target refused without opt-in", func(t *testing.T) {
		t.Setenv("GLEANN_WEBHOOK_ALLOW_PRIVATE", "")
		buf := captureLog(t)
		// A fresh server: the shared client would reuse a pooled connection
		// to srv, which was already dialled while private targets were allowed.
		fresh := httptest.NewServer(http.NotFoundHandler())
		defer fresh.Close()
		fireWebhookHTTP(fresh.URL, "", []byte("{}"))
		if !strings.Contains(buf.String(), "restricted address") {
			t.Errorf("log = %q", buf.String())
		}
	})
}

func TestWebhookHandlers_ErrorsAndRedaction(t *testing.T) {
	resetWebhooks()
	t.Cleanup(resetWebhooks)
	t.Setenv("GLEANN_WEBHOOK_ALLOW_PRIVATE", "")
	s := &Server{config: gleann.DefaultConfig(), searchers: make(map[string]*gleann.LeannSearcher)}

	do := func(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(http.MethodPost, "/api/webhooks", strings.NewReader(body)))
		return w
	}
	tests := []struct {
		name string
		h    http.HandlerFunc
		body string
		want int
	}{
		{"register bad json", s.handleRegisterWebhook, "{", 400},
		{"register private target", s.handleRegisterWebhook, `{"url":"http://127.0.0.1/hook","events":["x"]}`, 400},
		{"register bad scheme", s.handleRegisterWebhook, `{"url":"ftp://example.com/h","events":["x"]}`, 400},
		{"delete bad json", s.handleDeleteWebhook, "[", 400},
		{"delete missing url", s.handleDeleteWebhook, `{}`, 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if w := do(tc.h, tc.body); w.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if n := len(globalWebhooks.webhooks); n != 0 {
		t.Errorf("rejected registrations were stored: %d", n)
	}

	// A registered secret is masked in list output.
	t.Setenv("GLEANN_WEBHOOK_ALLOW_PRIVATE", "1")
	if w := do(s.handleRegisterWebhook, `{"url":"http://127.0.0.1/hook","events":["x"],"secret":"topsecret"}`); w.Code != 201 ||
		strings.Contains(w.Body.String(), "topsecret") {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	w := httptest.NewRecorder()
	s.handleListWebhooks(w, httptest.NewRequest(http.MethodGet, "/api/webhooks", nil))
	if strings.Contains(w.Body.String(), "topsecret") || !strings.Contains(w.Body.String(), "********") {
		t.Errorf("list leaks secret: %s", w.Body.String())
	}
}

func TestNotifyWebhooks_SkipsNonMatchingEvents(t *testing.T) {
	resetWebhooks()
	t.Cleanup(resetWebhooks)
	t.Setenv("GLEANN_WEBHOOK_ALLOW_PRIVATE", "1")
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit <- struct{}{} }))
	defer srv.Close()
	globalWebhooks.mu.Lock()
	globalWebhooks.webhooks = []Webhook{{URL: srv.URL, Events: []string{"index_deleted"}}}
	globalWebhooks.mu.Unlock()

	// No target matches, so nothing is fired and the payload stays untouched.
	payload := map[string]any{"k": "v"}
	notifyWebhooks("build_complete", payload)
	if _, ok := payload["event"]; ok {
		t.Error("payload must not be decorated when no webhook matches")
	}
	select {
	case <-hit:
		t.Error("non-matching webhook was fired")
	default:
	}
}

func TestValidateWebhookURL_Rejections(t *testing.T) {
	t.Setenv("GLEANN_WEBHOOK_ALLOW_PRIVATE", "")
	tests := []struct{ name, url, want string }{
		{"empty", "   ", "empty url"},
		{"unparsable", "http://[::1", "invalid webhook URL"},
		{"missing host", "http:///path", "missing host"},
		{"unresolvable", "http://no-such-host.invalid/x", "cannot resolve host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWebhookURL(tc.url)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want substring %q", err, tc.want)
			}
		})
	}
	// With the opt-out any well-formed http(s) URL is accepted, even loopback.
	t.Setenv("GLEANN_WEBHOOK_ALLOW_PRIVATE", "1")
	if err := validateWebhookURL("http://127.0.0.1:9/hook"); err != nil {
		t.Errorf("opt-out: %v", err)
	}
}
