package netutil

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsConnectionRefusedRealDial(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("request to a closed server succeeded")
	}
	if !IsConnectionRefused(err) {
		t.Fatalf("IsConnectionRefused(%v) = false", err)
	}
	// Wrapped with %w keeps the chain; flattened keeps the message.
	if !IsConnectionRefused(fmt.Errorf("embedding: %w", err)) {
		t.Fatal("wrapped error not recognised")
	}
	if !IsConnectionRefused(errors.New(err.Error())) {
		t.Fatal("flattened error not recognised")
	}
}

func TestIsConnectionRefusedMessages(t *testing.T) {
	for _, msg := range []string{
		"dial tcp 127.0.0.1:1: connect: connection refused",
		"dial tcp 127.0.0.1:1: connectex: No connection could be made because the target machine actively refused it.",
	} {
		if !IsConnectionRefused(errors.New(msg)) {
			t.Errorf("not recognised: %q", msg)
		}
	}
	for _, err := range []error{nil, errors.New("timeout"), &net.OpError{Op: "dial", Err: errors.New("network is unreachable")}} {
		if IsConnectionRefused(err) {
			t.Errorf("recognised: %v", err)
		}
	}
}
