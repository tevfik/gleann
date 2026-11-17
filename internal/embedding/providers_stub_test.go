//go:build !eif && !native

package embedding

import (
	"context"
	"strings"
	"testing"
)

// Without the eif / native build tags those providers fail with a message that
// names the tag to build with, and the failure is permanent (not retried).
func TestStubProvidersNameTheBuildTag(t *testing.T) {
	tests := []struct {
		provider Provider
		tag      string
	}{
		{ProviderEIF, "-tags eif"},
		{ProviderNative, "-tags native"},
	}
	for _, tc := range tests {
		t.Run(string(tc.provider), func(t *testing.T) {
			c := NewComputer(Options{Provider: tc.provider, Model: "m", BatchSize: 4})
			// Batch of one fails directly; batch of two takes the one-by-one fallback too.
			for _, texts := range [][]string{{"a"}, {"a", "b"}} {
				_, err := c.Compute(context.Background(), texts)
				if err == nil || !strings.Contains(err.Error(), tc.tag) {
					t.Fatalf("%d texts: err = %v, want containing %q", len(texts), err, tc.tag)
				}
			}
			if d := c.Dimensions(); d != 0 {
				t.Errorf("Dimensions = %d, want 0 for an unusable provider", d)
			}
		})
	}
}
