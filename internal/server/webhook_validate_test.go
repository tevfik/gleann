package server

import (
	"os"
	"strings"
	"testing"
)

func TestValidateWebhookURL_Valid(t *testing.T) {
	// Use a public hostname; in test environments this may fail if there's
	// no DNS, in which case we set GLEANN_WEBHOOK_ALLOW_PRIVATE to skip
	// the resolution check.
	t.Setenv("GLEANN_WEBHOOK_ALLOW_PRIVATE", "1")
	cases := []string{
		"http://example.com/webhook",
		"https://hooks.example.org/path?x=1",
		"http://10.0.0.5:8080/cb", // private allowed because of env
	}
	for _, c := range cases {
		if err := validateWebhookURL(c); err != nil {
			t.Errorf("expected %q to validate, got %v", c, err)
		}
	}
}

func TestValidateWebhookURL_BadScheme(t *testing.T) {
	cases := []string{
		"file:///etc/passwd",
		"ftp://example.com/x",
		"gopher://example.com/",
		"",
		"   ",
	}
	for _, c := range cases {
		if err := validateWebhookURL(c); err == nil {
			t.Errorf("expected %q to be rejected", c)
		}
	}
}

func TestValidateWebhookURL_BlocksPrivate(t *testing.T) {
	// Make sure the env var is NOT set for this test (t.Setenv handles cleanup).
	_ = os.Unsetenv("GLEANN_WEBHOOK_ALLOW_PRIVATE")
	cases := []string{
		"http://127.0.0.1/",
		"http://localhost/",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"http://[::1]/",
	}
	for _, c := range cases {
		err := validateWebhookURL(c)
		if err == nil {
			t.Errorf("expected %q to be rejected as restricted address", c)
			continue
		}
		if !strings.Contains(err.Error(), "restricted") && !strings.Contains(err.Error(), "cannot resolve") {
			t.Errorf("unexpected error for %q: %v", c, err)
		}
	}
}

func TestValidateWebhookURL_MalformedHost(t *testing.T) {
	t.Setenv("GLEANN_WEBHOOK_ALLOW_PRIVATE", "1")
	if err := validateWebhookURL("http:///nopath"); err == nil {
		t.Error("expected missing-host error")
	}
}
